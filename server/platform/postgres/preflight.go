package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

// Migration preflight. Migration 027 aborts when live resource names (or
// account identifiers per platform) differ only by letter case, and 031
// aborts on such workflow names, archived included. Preflight lists those
// rows before `up` runs; FixPreflight renames all but the oldest row of each
// group. Both also list schedules whose cron expression changed meaning when
// "N/S" steps started to run to the field maximum.

// ErrPreflightPrivileges: the checks must see every tenant's rows, and FORCE
// ROW LEVEL SECURITY filters even the table owner unless it is a superuser
// or has BYPASSRLS.
var ErrPreflightPrivileges = errors.New("preflight requires a superuser or BYPASSRLS role")

// preflightNameLimit bounds renamed names. The services accept at most 120
// characters; the columns allow 255 (length(trim(name)) checks) or are
// unbounded, so a renamed row stays editable through the API.
const preflightNameLimit = 120

// preflightLockTimeout keeps -fix from waiting indefinitely on rows that the
// running API holds locked.
const preflightLockTimeout = "15s"

// PreflightConflict is one group of rows that a case-insensitive unique index
// of migration 027 or 031 would reject.
type PreflightConflict struct {
	Table       string
	WorkspaceID string
	// Platform is set for accounts, whose identifiers are unique per platform.
	Platform string
	// Rows are ordered oldest first (created_at, then id); FixPreflight keeps
	// the first row and renames the others.
	Rows []PreflightRow
	// Renamable is false for accounts: identifiers are external account IDs
	// and must be corrected manually.
	Renamable bool
}

type PreflightRow struct {
	ID        string
	CreatedAt time.Time
	// Name is the resource name. For accounts it is the identifier masked to
	// at most its first two characters and, for e-mail addresses, the domain.
	Name string
	// Archived marks archived workflows, which 031 includes.
	Archived bool
}

// PreflightSchedule is a schedule to review because of the cron step change.
type PreflightSchedule struct {
	ID             string
	WorkspaceID    string
	CronExpression string
	Timezone       string
	// Status is active, paused or error.
	Status string
	// Steps lists the "N/S" items; ParseError is set instead when the
	// current parser rejects the expression (for example a signed number).
	Steps      []scheduleservice.StepChange
	ParseError string
}

type PreflightReport struct {
	Role      string
	Superuser bool
	BypassRLS bool
	// LatestMigration is the last applied migration file, empty for a
	// database without migrations (which has nothing to check).
	LatestMigration string
	Conflicts       []PreflightConflict
	Schedules       []PreflightSchedule
}

// Blocking reports whether migration 027 or 031 would abort.
func (r PreflightReport) Blocking() bool { return len(r.Conflicts) > 0 }

type PreflightRename struct {
	Table       string
	WorkspaceID string
	ID          string
	OldName     string
	NewName     string
}

// PreflightFix lists the renames and the report after them, in which only
// account groups (and schedules to review) can remain.
type PreflightFix struct {
	Renamed []PreflightRename
	Report  PreflightReport
}

// preflightTable is one uniqueness rule of migration 027 or 031. Table and
// column names are constants, never input, so they are spliced into SQL.
type preflightTable struct {
	name   string
	column string
	// live restricts the rule to rows with deleted_at IS NULL (027).
	live bool
	// perPlatform partitions by platform as well (accounts).
	perPlatform bool
	// hasStatus reports archived workflows.
	hasStatus bool
	renamable bool
}

var preflightTables = []preflightTable{
	{name: "browser_instances", column: "name", live: true, renamable: true},
	{name: "proxies", column: "name", live: true, renamable: true},
	{name: "fingerprint_templates", column: "name", live: true, renamable: true},
	{name: "browser_profiles", column: "name", live: true, renamable: true},
	{name: "accounts", column: "external_identifier", live: true, perPlatform: true},
	{name: "workflows", column: "name", hasStatus: true, renamable: true},
}

func (t preflightTable) liveFilter() string {
	if t.live {
		return " AND deleted_at IS NULL"
	}
	return ""
}

// conflictGroup keeps the folded key that FixPreflight needs to re-read the
// group; it is never exported because for accounts it is the identifier.
type conflictGroup struct {
	PreflightConflict
	table  preflightTable
	folded string
}

// Preflight reports what migrations 027 and 031 would reject and the
// schedules to review. It only reads.
func Preflight(ctx context.Context, databaseURL string) (PreflightReport, error) {
	connection, report, err := openPreflight(ctx, databaseURL)
	if err != nil {
		return PreflightReport{}, err
	}
	defer connection.Close(context.Background())
	if report.LatestMigration == "" {
		return report, nil
	}
	if err := collectPreflight(ctx, connection, &report); err != nil {
		return PreflightReport{}, err
	}
	return report, nil
}

// FixPreflight renames every row but the oldest of each renamable conflict
// group to "<name> (2)", "<name> (3)", … and reports what remains. Each
// group is fixed in its own transaction, so an interrupted run keeps the
// groups it finished; running it again is a no-op for fixed groups.
func FixPreflight(ctx context.Context, databaseURL string) (PreflightFix, error) {
	connection, report, err := openPreflight(ctx, databaseURL)
	if err != nil {
		return PreflightFix{}, err
	}
	defer connection.Close(context.Background())
	if report.LatestMigration == "" {
		return PreflightFix{Report: report}, nil
	}
	if _, err := connection.Exec(ctx, `SET lock_timeout = '`+preflightLockTimeout+`'`); err != nil {
		return PreflightFix{}, fmt.Errorf("preflight: set lock timeout: %w", err)
	}
	groups, err := collectConflicts(ctx, connection)
	if err != nil {
		return PreflightFix{}, err
	}
	var fix PreflightFix
	bumps := map[string][]string{}
	for _, group := range groups {
		if !group.table.renamable {
			continue
		}
		columns, ok := bumps[group.table.name]
		if !ok {
			if columns, err = bumpColumns(ctx, connection, group.table.name); err != nil {
				return fix, err
			}
			bumps[group.table.name] = columns
		}
		renamed, err := renameGroup(ctx, connection, group, columns)
		if err != nil {
			return fix, fmt.Errorf("preflight: rename %s in workspace %s: %w", group.table.name, group.WorkspaceID, err)
		}
		fix.Renamed = append(fix.Renamed, renamed...)
	}
	fix.Report = report
	if err := collectPreflight(ctx, connection, &fix.Report); err != nil {
		return fix, err
	}
	return fix, nil
}

// openPreflight connects, refuses roles that row-level security could filter
// and reads the latest applied migration.
func openPreflight(ctx context.Context, databaseURL string) (*pgx.Conn, PreflightReport, error) {
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, PreflightReport{}, fmt.Errorf("preflight: connect: %w", err)
	}
	var report PreflightReport
	if err := connection.QueryRow(ctx, `SELECT r.rolname::text, r.rolsuper, r.rolbypassrls FROM pg_roles r WHERE r.rolname = current_user`).
		Scan(&report.Role, &report.Superuser, &report.BypassRLS); err != nil {
		connection.Close(context.Background())
		return nil, PreflightReport{}, fmt.Errorf("preflight: read role attributes: %w", err)
	}
	if !report.Superuser && !report.BypassRLS {
		connection.Close(context.Background())
		return nil, PreflightReport{}, fmt.Errorf("%w: role %q would see only the rows its row-level security policies allow, "+
			"and FORCE ROW LEVEL SECURITY applies these policies even to the table owner, so conflicts in other workspaces "+
			"would go unreported; connect with the migration role as a superuser or grant it BYPASSRLS", ErrPreflightPrivileges, report.Role)
	}
	// Belt and braces: should a policy still apply, fail instead of
	// returning a silently filtered, falsely clean result.
	if _, err := connection.Exec(ctx, `SET row_security = off`); err != nil {
		connection.Close(context.Background())
		return nil, PreflightReport{}, fmt.Errorf("preflight: disable row security: %w", err)
	}
	var migrated bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&migrated); err != nil {
		connection.Close(context.Background())
		return nil, PreflightReport{}, fmt.Errorf("preflight: inspect schema: %w", err)
	}
	if migrated {
		if err := connection.QueryRow(ctx, `SELECT COALESCE(max(version), '') FROM schema_migrations`).Scan(&report.LatestMigration); err != nil {
			connection.Close(context.Background())
			return nil, PreflightReport{}, fmt.Errorf("preflight: read applied migrations: %w", err)
		}
	}
	return connection, report, nil
}

func collectPreflight(ctx context.Context, connection *pgx.Conn, report *PreflightReport) error {
	groups, err := collectConflicts(ctx, connection)
	if err != nil {
		return err
	}
	report.Conflicts = make([]PreflightConflict, 0, len(groups))
	for _, group := range groups {
		report.Conflicts = append(report.Conflicts, group.PreflightConflict)
	}
	report.Schedules, err = collectSchedules(ctx, connection)
	return err
}

func collectConflicts(ctx context.Context, connection *pgx.Conn) ([]conflictGroup, error) {
	var groups []conflictGroup
	for _, table := range preflightTables {
		tableGroups, err := collectTableConflicts(ctx, connection, table)
		if err != nil {
			return nil, fmt.Errorf("preflight: check %s: %w", table.name, err)
		}
		groups = append(groups, tableGroups...)
	}
	return groups, nil
}

func collectTableConflicts(ctx context.Context, connection *pgx.Conn, table preflightTable) ([]conflictGroup, error) {
	folded := "lower(" + table.column + ")"
	partition, platform, order := "workspace_id, "+folded, "''", "workspace_id, "+folded
	if table.perPlatform {
		partition, platform, order = "workspace_id, platform, "+folded, "platform", "workspace_id, platform, "+folded
	}
	archived := "false"
	if table.hasStatus {
		archived = "status = 'archived'"
	}
	where := ""
	if table.live {
		where = " WHERE deleted_at IS NULL"
	}
	rows, err := connection.Query(ctx, `SELECT workspace_id::text, `+platform+`, `+folded+`, id::text, `+table.column+`, created_at, `+archived+`
		FROM (SELECT *, count(*) OVER (PARTITION BY `+partition+`) AS copies FROM `+table.name+where+`) AS grouped
		WHERE copies > 1
		ORDER BY `+order+`, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []conflictGroup
	for rows.Next() {
		var workspaceID, platformValue, key string
		var row PreflightRow
		if err := rows.Scan(&workspaceID, &platformValue, &key, &row.ID, &row.Name, &row.CreatedAt, &row.Archived); err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		if !table.renamable {
			row.Name = maskIdentifier(row.Name)
		}
		last := len(groups) - 1
		if last < 0 || groups[last].WorkspaceID != workspaceID || groups[last].Platform != platformValue || groups[last].folded != key {
			groups = append(groups, conflictGroup{
				PreflightConflict: PreflightConflict{Table: table.name, WorkspaceID: workspaceID, Platform: platformValue, Renamable: table.renamable},
				table:             table, folded: key,
			})
			last++
		}
		groups[last].Rows = append(groups[last].Rows, row)
	}
	return groups, rows.Err()
}

func collectSchedules(ctx context.Context, connection *pgx.Conn) ([]PreflightSchedule, error) {
	rows, err := connection.Query(ctx, `SELECT id::text, workspace_id::text, cron_expression, timezone, status
		FROM schedules ORDER BY workspace_id, id`)
	if err != nil {
		return nil, fmt.Errorf("preflight: check schedules: %w", err)
	}
	defer rows.Close()
	var schedules []PreflightSchedule
	for rows.Next() {
		var item PreflightSchedule
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.CronExpression, &item.Timezone, &item.Status); err != nil {
			return nil, err
		}
		if _, err := scheduleservice.ParseCron(item.CronExpression); err != nil {
			item.ParseError = err.Error()
		} else {
			item.Steps = scheduleservice.NumericStartSteps(item.CronExpression)
		}
		if item.ParseError == "" && len(item.Steps) == 0 {
			continue
		}
		schedules = append(schedules, item)
	}
	return schedules, rows.Err()
}

// bumpColumns returns which of version and updated_at the table has.
func bumpColumns(ctx context.Context, connection *pgx.Conn, table string) ([]string, error) {
	rows, err := connection.Query(ctx, `SELECT column_name::text FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name IN ('version', 'updated_at')
		ORDER BY column_name`, table)
	if err != nil {
		return nil, fmt.Errorf("preflight: inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

// renameGroup re-reads the group under row locks, keeps its oldest row and
// renames the others in one transaction.
func renameGroup(ctx context.Context, connection *pgx.Conn, group conflictGroup, bumps []string) ([]PreflightRename, error) {
	table := group.table
	tx, err := connection.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	rows, err := tx.Query(ctx, `SELECT id::text, `+table.column+` FROM `+table.name+`
		WHERE workspace_id = $1::uuid AND lower(`+table.column+`) = $2`+table.liveFilter()+`
		ORDER BY created_at, id FOR UPDATE`, group.WorkspaceID, group.folded)
	if err != nil {
		return nil, err
	}
	type member struct{ id, name string }
	var members []member
	for rows.Next() {
		var item member
		if err := rows.Scan(&item.id, &item.name); err != nil {
			rows.Close()
			return nil, err
		}
		members = append(members, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	set := table.column + " = $2"
	for _, column := range bumps {
		switch column {
		case "version":
			set += ", version = version + 1"
		case "updated_at":
			set += ", updated_at = clock_timestamp()"
		}
	}
	var renamed []PreflightRename
	// members[0] is the oldest row and keeps its name. A group that another
	// run resolved meanwhile has a single member and is left alone.
	for index := 1; index < len(members); index++ {
		newName, err := freeName(ctx, tx, table, group.WorkspaceID, members[index].name)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE `+table.name+` SET `+set+` WHERE id = $1::uuid`, members[index].id, newName); err != nil {
			return nil, err
		}
		renamed = append(renamed, PreflightRename{Table: table.name, WorkspaceID: group.WorkspaceID, ID: members[index].id, OldName: members[index].name, NewName: newName})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return renamed, nil
}

// freeName finds the first "<name> (k)", k >= 2, that no row the index
// covers uses, ignoring letter case the way the database's lower() does.
func freeName(ctx context.Context, tx pgx.Tx, table preflightTable, workspaceID, name string) (string, error) {
	for k := 2; k <= 10000; k++ {
		candidate := suffixedName(name, k, preflightNameLimit)
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table.name+`
			WHERE workspace_id = $1::uuid AND lower(`+table.column+`) = lower($2)`+table.liveFilter()+`)`,
			workspaceID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", errors.New("no free suffixed name")
}

// suffixedName appends " (k)" and shortens the name so the result has at
// most limit characters, counted like PostgreSQL's length() on text.
func suffixedName(name string, k, limit int) string {
	suffix := fmt.Sprintf(" (%d)", k)
	room := limit - utf8.RuneCountInString(suffix)
	if room < 1 {
		room = 1
	}
	if runes := []rune(name); len(runes) > room {
		name = strings.TrimRightFunc(string(runes[:room]), unicode.IsSpace)
	}
	return name + suffix
}

// maskIdentifier hides an account identifier, which can be an e-mail
// address: it keeps at most the first two characters, never the whole local
// part, and the domain of an address ("seller@example.test" becomes
// "se***@example.test", "creator42" becomes "cr***").
func maskIdentifier(identifier string) string {
	local, domain := identifier, ""
	if at := strings.LastIndex(identifier, "@"); at > 0 && at < len(identifier)-1 {
		local, domain = identifier[:at], identifier[at:]
	}
	runes := []rune(local)
	keep := len(runes) - 1
	if keep > 2 {
		keep = 2
	}
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + "***" + domain
}
