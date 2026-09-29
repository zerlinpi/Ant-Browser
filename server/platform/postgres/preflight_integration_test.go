package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
)

// preflightDatabase creates an empty database next to ANT_TEST_DATABASE_URL
// and drops it when the test ends. It returns the owner connection URL.
func preflightDatabase(t *testing.T, ctx context.Context) (string, *pgx.Conn) {
	t.Helper()
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	server, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	name := "ant_preflight_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := server.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = server.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		_ = server.Close(context.Background())
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String(), server
}

// migrationsThrough copies the migration files up to and including the given
// prefix into a temporary directory.
func migrationsThrough(t *testing.T, last string) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name()[:3] > last {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, entry.Name()), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

// TestMigrationPreflightAgainstPostgres seeds case-variant duplicates into a
// schema at migration 026, then checks that 027 and 031 abort without
// printing names, that the preflight reports and fixes the rows, and that
// every migration applies afterwards.
func TestMigrationPreflightAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL, server := preflightDatabase(t, ctx)
	allMigrations, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsThrough(t, "026")); err != nil {
		t.Fatal(err)
	}
	owner, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	exec := func(sql string, args ...interface{}) {
		t.Helper()
		if _, err := owner.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(hours int) time.Time { return base.Add(time.Duration(hours) * time.Hour) }

	// Two tenants; the second one reuses names, which is never a conflict.
	userID := uuid.NewString()
	exec(`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, userID, userID+"@preflight.test")
	workspaceID, otherWorkspaceID := uuid.NewString(), uuid.NewString()
	for _, workspace := range []string{workspaceID, otherWorkspaceID} {
		organization := uuid.NewString()
		slug := "preflight-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
		exec(`INSERT INTO organizations (id, name, slug, owner_user_id) VALUES ($1, 'Preflight', $2, $3)`, organization, slug, userID)
		exec(`INSERT INTO workspaces (id, organization_id, name, slug, created_by) VALUES ($1, $2, 'Preflight', $3, $4)`, workspace, organization, slug, userID)
	}
	insertInstance := func(workspace, name string, created time.Time, deleted bool) string {
		id := uuid.NewString()
		var deletedAt *time.Time
		if deleted {
			deletedAt = &created
		}
		exec(`INSERT INTO browser_instances (id, workspace_id, name, created_at, updated_at, deleted_at) VALUES ($1, $2, $3, $4, $4, $5)`, id, workspace, name, created, deletedAt)
		return id
	}
	insertProxy := func(workspace, name string, created time.Time, deleted bool) string {
		id := uuid.NewString()
		var deletedAt *time.Time
		if deleted {
			deletedAt = &created
		}
		exec(`INSERT INTO proxies (id, workspace_id, name, protocol, host, port, connector_type, kernel, created_at, updated_at, deleted_at)
			VALUES ($1, $2, $3, 'direct', '', 0, 'xray', 'direct', $4, $4, $5)`, id, workspace, name, created, deletedAt)
		return id
	}
	insertNamed := func(table, extraColumns, extraValues, workspace, name string, created time.Time) string {
		id := uuid.NewString()
		exec(`INSERT INTO `+table+` (id, workspace_id, name, created_at, updated_at`+extraColumns+`) VALUES ($1, $2, $3, $4, $4`+extraValues+`)`, id, workspace, name, created)
		return id
	}
	insertAccount := func(workspace, platform, identifier string, created time.Time, deleted bool) string {
		id := uuid.NewString()
		var deletedAt *time.Time
		status := "active"
		if deleted {
			deletedAt, status = &created, "deleted"
		}
		exec(`INSERT INTO accounts (id, workspace_id, platform, external_identifier, status, created_at, updated_at, deleted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6, $7)`, id, workspace, platform, identifier, status, created, deletedAt)
		return id
	}

	// The Free plan allows three live instances per organization.
	shop := insertInstance(workspaceID, "Shop 1", at(1), false)
	shopUpper := insertInstance(workspaceID, "SHOP 1", at(2), false)
	shopLower := insertInstance(workspaceID, "shop 1", at(3), false)
	insertInstance(workspaceID, "sHoP 1", at(4), true)
	insertInstance(otherWorkspaceID, "SHOP 1", at(1), false)
	// "residential (2)" is taken ignoring case, "Residential (3)" only by a
	// deleted proxy.
	residential := insertProxy(workspaceID, "Residential", at(1), false)
	residentialLower := insertProxy(workspaceID, "residential", at(2), false)
	residentialUpper := insertProxy(workspaceID, "RESIDENTIAL", at(3), false)
	insertProxy(workspaceID, "Residential (2)", at(4), false)
	insertProxy(workspaceID, "Residential (3)", at(5), true)
	longName := "A" + strings.Repeat("b", 119)
	fingerprint := insertNamed("fingerprint_templates", ", mode, browser_major, platform, seed, locale, timezone", ", 'seeded', 120, 'windows', 1, 'en-US', 'UTC'", workspaceID, longName, at(1))
	fingerprintUpper := insertNamed("fingerprint_templates", ", mode, browser_major, platform, seed, locale, timezone", ", 'seeded', 120, 'windows', 1, 'en-US', 'UTC'", workspaceID, strings.ToUpper(longName), at(2))
	profile := insertNamed("browser_profiles", "", "", workspaceID, "Storefront", at(1))
	profileUpper := insertNamed("browser_profiles", "", "", workspaceID, "STOREFRONT", at(2))
	seller := insertAccount(workspaceID, "amazon", "seller@example.test", at(1), false)
	sellerUpper := insertAccount(workspaceID, "amazon", "Seller@Example.TEST", at(2), false)
	insertAccount(workspaceID, "amazon", "SELLER@example.test", at(3), true)
	insertAccount(workspaceID, "ebay", "seller@example.test", at(1), false)
	nightly := insertNamed("workflows", ", status, archived_at", ", 'archived', $4", workspaceID, "Nightly", at(1))
	nightlyLower := insertNamed("workflows", "", "", workspaceID, "nightly", at(2))
	versionID := uuid.NewString()
	exec(`INSERT INTO workflow_versions (id, workspace_id, workflow_id, version, dsl_schema_version, definition, content_hash)
		VALUES ($1, $2, $3, 1, 'ant-workflow/v1', '{}'::jsonb, '\x00'::bytea)`, versionID, workspaceID, nightlyLower)
	insertSchedule := func(cron string) string {
		id := uuid.NewString()
		exec(`INSERT INTO schedules (id, workspace_id, workflow_id, workflow_version_id, instance_id, cron_expression, timezone, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'UTC', 'active')`, id, workspaceID, nightlyLower, versionID, shop, cron)
		return id
	}
	changedStep := insertSchedule("5/15 * * * *")
	insertSchedule("*/5 * * * *")
	unchangedStep := insertSchedule("30/45 9 * * *")
	signed := insertSchedule("+5 * * * *")

	secretNames := []string{"Shop 1", "Residential", "Storefront", "Nightly", "seller@", "Seller@", longName}
	assertNoNames := func(message string) {
		t.Helper()
		for _, name := range secretNames {
			if strings.Contains(message, name) {
				t.Fatalf("error reveals %q: %s", name, message)
			}
		}
	}
	// 027 aborts on the live duplicates and names only table and workspace.
	err = postgres.Migrate(ctx, databaseURL, allMigrations)
	if err == nil || !strings.Contains(err.Error(), "differ only by letter case") || !strings.Contains(err.Error(), workspaceID) {
		t.Fatalf("migration over duplicates error=%v", err)
	}
	assertNoNames(err.Error())

	// A role that row-level security filters is refused.
	probe := "preflight_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := server.Exec(ctx, `CREATE ROLE `+probe+` LOGIN PASSWORD 'preflight-probe' NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = server.Exec(context.Background(), `DROP ROLE IF EXISTS `+probe) })
	probeURL, _ := url.Parse(databaseURL)
	probeURL.User = url.UserPassword(probe, "preflight-probe")
	if _, err := postgres.Preflight(ctx, probeURL.String()); !errors.Is(err, postgres.ErrPreflightPrivileges) || !strings.Contains(err.Error(), "FORCE ROW LEVEL SECURITY") {
		t.Fatalf("preflight as a filtered role error=%v", err)
	}
	if _, err := postgres.FixPreflight(ctx, probeURL.String()); !errors.Is(err, postgres.ErrPreflightPrivileges) {
		t.Fatalf("fix as a filtered role error=%v", err)
	}

	report, err := postgres.Preflight(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Superuser || report.LatestMigration != "026_profile_storage_ledger.sql" || !report.Blocking() {
		t.Fatalf("report header=%+v", report)
	}
	type groupView struct {
		table, workspace, platform string
		ids                        []string
		names                      []string
		renamable                  bool
	}
	view := func(conflicts []postgres.PreflightConflict) []groupView {
		var views []groupView
		for _, group := range conflicts {
			item := groupView{table: group.Table, workspace: group.WorkspaceID, platform: group.Platform, renamable: group.Renamable}
			for _, row := range group.Rows {
				item.ids = append(item.ids, row.ID)
				item.names = append(item.names, row.Name)
			}
			views = append(views, item)
		}
		return views
	}
	wantGroups := []groupView{
		{"browser_instances", workspaceID, "", []string{shop, shopUpper, shopLower}, []string{"Shop 1", "SHOP 1", "shop 1"}, true},
		{"proxies", workspaceID, "", []string{residential, residentialLower, residentialUpper}, []string{"Residential", "residential", "RESIDENTIAL"}, true},
		{"fingerprint_templates", workspaceID, "", []string{fingerprint, fingerprintUpper}, []string{longName, strings.ToUpper(longName)}, true},
		{"browser_profiles", workspaceID, "", []string{profile, profileUpper}, []string{"Storefront", "STOREFRONT"}, true},
		{"accounts", workspaceID, "amazon", []string{seller, sellerUpper}, []string{"se***@example.test", "Se***@Example.TEST"}, false},
		{"workflows", workspaceID, "", []string{nightly, nightlyLower}, []string{"Nightly", "nightly"}, true},
	}
	if got := view(report.Conflicts); !reflect.DeepEqual(got, wantGroups) {
		t.Fatalf("conflict groups:\n got %+v\nwant %+v", got, wantGroups)
	}
	if !report.Conflicts[5].Rows[0].Archived || report.Conflicts[5].Rows[1].Archived || !report.Conflicts[0].Rows[0].CreatedAt.Equal(at(1)) {
		t.Fatalf("workflow rows=%+v instance rows=%+v", report.Conflicts[5].Rows, report.Conflicts[0].Rows)
	}
	assertSchedules := func(schedules []postgres.PreflightSchedule) {
		t.Helper()
		byID := map[string]postgres.PreflightSchedule{}
		for _, item := range schedules {
			byID[item.ID] = item
		}
		if len(byID) != 3 || len(byID[changedStep].Steps) != 1 || !byID[changedStep].Steps[0].Changed ||
			len(byID[unchangedStep].Steps) != 1 || byID[unchangedStep].Steps[0].Changed || byID[signed].ParseError == "" {
			t.Fatalf("schedules to review=%+v", schedules)
		}
	}
	assertSchedules(report.Schedules)

	versions := func(table string, ids ...string) map[string]int64 {
		t.Helper()
		values := map[string]int64{}
		for _, id := range ids {
			var version int64
			if err := owner.QueryRow(ctx, `SELECT version FROM `+table+` WHERE id = $1::uuid`, id).Scan(&version); err != nil {
				t.Fatal(err)
			}
			values[id] = version
		}
		return values
	}
	instanceVersions := versions("browser_instances", shop, shopUpper, shopLower)
	workflowVersions := versions("workflows", nightly, nightlyLower)

	fix, err := postgres.FixPreflight(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	renames := map[string]string{}
	for _, item := range fix.Renamed {
		renames[item.ID] = item.OldName + " -> " + item.NewName
	}
	wantRenames := map[string]string{
		shopUpper:        "SHOP 1 -> SHOP 1 (2)",
		shopLower:        "shop 1 -> shop 1 (3)",
		residentialLower: "residential -> residential (3)",
		residentialUpper: "RESIDENTIAL -> RESIDENTIAL (4)",
		fingerprintUpper: strings.ToUpper(longName) + " -> " + strings.ToUpper(longName)[:116] + " (2)",
		profileUpper:     "STOREFRONT -> STOREFRONT (2)",
		nightlyLower:     "nightly -> nightly (2)",
	}
	if !reflect.DeepEqual(renames, wantRenames) {
		t.Fatalf("renames:\n got %v\nwant %v", renames, wantRenames)
	}
	if got := view(fix.Report.Conflicts); !reflect.DeepEqual(got, wantGroups[4:5]) {
		t.Fatalf("after the fix only the account group should remain: %+v", got)
	}
	assertSchedules(fix.Report.Schedules)
	after := versions("browser_instances", shop, shopUpper, shopLower)
	afterWorkflows := versions("workflows", nightly, nightlyLower)
	if after[shop] != instanceVersions[shop] || after[shopUpper] != instanceVersions[shopUpper]+1 || after[shopLower] != instanceVersions[shopLower]+1 ||
		afterWorkflows[nightly] != workflowVersions[nightly] || afterWorkflows[nightlyLower] != workflowVersions[nightlyLower]+1 {
		t.Fatalf("versions before %v %v, after %v %v", instanceVersions, workflowVersions, after, afterWorkflows)
	}
	var updatedAt time.Time
	if err := owner.QueryRow(ctx, `SELECT updated_at FROM proxies WHERE id = $1::uuid`, residentialLower).Scan(&updatedAt); err != nil || !updatedAt.After(at(2)) {
		t.Fatalf("renamed proxy updated_at=%v err=%v", updatedAt, err)
	}
	var identifier string
	if err := owner.QueryRow(ctx, `SELECT external_identifier FROM accounts WHERE id = $1::uuid`, sellerUpper).Scan(&identifier); err != nil || identifier != "Seller@Example.TEST" {
		t.Fatalf("account identifier changed to %q err=%v", identifier, err)
	}

	// Running the fix again changes nothing.
	again, err := postgres.FixPreflight(ctx, databaseURL)
	if err != nil || len(again.Renamed) != 0 || len(again.Report.Conflicts) != 1 {
		t.Fatalf("second fix renamed=%+v conflicts=%+v err=%v", again.Renamed, again.Report.Conflicts, err)
	}

	// The operator resolves the account duplicate. A new pair of workflow
	// names then lets 027 pass and stops 031, again without names.
	exec(`UPDATE accounts SET status = 'deleted', deleted_at = now() WHERE id = $1::uuid`, sellerUpper)
	insertNamed("workflows", "", "", workspaceID, "Weekly", at(6))
	weeklyUpper := insertNamed("workflows", "", "", workspaceID, "WEEKLY", at(7))
	secretNames = append(secretNames, "Weekly", "WEEKLY")
	err = postgres.Migrate(ctx, databaseURL, allMigrations)
	if err == nil || !strings.Contains(err.Error(), "workflows in workspaces "+workspaceID) {
		t.Fatalf("migration over workflow duplicates error=%v", err)
	}
	assertNoNames(err.Error())
	var applied027, applied031 bool
	if err := owner.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM schema_migrations WHERE version = '027_live_name_uniqueness.sql'),
		EXISTS (SELECT 1 FROM schema_migrations WHERE version LIKE '031_%')`).Scan(&applied027, &applied031); err != nil || !applied027 || applied031 {
		t.Fatalf("027 applied=%v 031 applied=%v err=%v", applied027, applied031, err)
	}
	fix, err = postgres.FixPreflight(ctx, databaseURL)
	if err != nil || len(fix.Renamed) != 1 || fix.Renamed[0].ID != weeklyUpper || fix.Renamed[0].NewName != "WEEKLY (2)" || fix.Report.Blocking() {
		t.Fatalf("workflow fix renamed=%+v report=%+v err=%v", fix.Renamed, fix.Report.Conflicts, err)
	}

	if err := postgres.Migrate(ctx, databaseURL, allMigrations); err != nil {
		t.Fatalf("migrations after the fix: %v", err)
	}
	final, err := postgres.Preflight(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if final.Blocking() || final.LatestMigration != lastMigration(t, allMigrations) {
		t.Fatalf("final report conflicts=%+v latest=%s", final.Conflicts, final.LatestMigration)
	}
	assertSchedules(final.Schedules)
}

func lastMigration(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names[len(names)-1]
}
