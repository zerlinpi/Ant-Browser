package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
)

// Exit codes of `migrate preflight`.
const (
	preflightClean    = 0 // migrations 027 and 031 can run
	preflightError    = 1
	preflightUsage    = 2
	preflightBlocking = 3 // rows that 027 or 031 reject remain
)

// runPreflight implements `migrate preflight [-fix]`. Without -fix it only
// reads. Schedules listed for review never block.
func runPreflight(ctx context.Context, databaseURL string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("preflight", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fix := flags.Bool("fix", false, `keep the oldest row of each conflicting group and rename the others to "<name> (2)", "<name> (3)", ...`)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return preflightClean
		}
		return preflightUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n%s\n", flags.Arg(0), usage)
		return preflightUsage
	}
	var report postgres.PreflightReport
	if *fix {
		result, err := postgres.FixPreflight(ctx, databaseURL)
		// Groups finished before an error stay renamed; list them either way.
		writeRenames(stdout, result.Renamed)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return preflightError
		}
		report = result.Report
	} else {
		var err error
		if report, err = postgres.Preflight(ctx, databaseURL); err != nil {
			fmt.Fprintln(stderr, err)
			return preflightError
		}
	}
	writeReport(stdout, report, *fix)
	if report.Blocking() {
		return preflightBlocking
	}
	return preflightClean
}

func writeRenames(w io.Writer, renamed []postgres.PreflightRename) {
	if len(renamed) == 0 {
		fmt.Fprintln(w, "nothing to rename")
		return
	}
	fmt.Fprintf(w, "renamed %s (old -> new):\n", count(len(renamed), "row"))
	for _, item := range renamed {
		fmt.Fprintf(w, "  %s %s in workspace %s: %q -> %q\n", item.Table, item.ID, item.WorkspaceID, item.OldName, item.NewName)
	}
	fmt.Fprintln(w)
}

// writeReport prints names quoted (%q) so control characters cannot forge
// output lines. Account identifiers arrive masked.
func writeReport(w io.Writer, report postgres.PreflightReport, fixed bool) {
	attribute := "superuser"
	if !report.Superuser {
		attribute = "BYPASSRLS"
	}
	if report.LatestMigration == "" {
		fmt.Fprintf(w, "preflight as %s (%s): no migrations applied, nothing to check\n", report.Role, attribute)
		return
	}
	fmt.Fprintf(w, "preflight as %s (%s), schema at %s\n", report.Role, attribute, report.LatestMigration)
	writeConflicts(w, report.Conflicts, fixed)
	writeSchedules(w, report.Schedules)
}

func writeConflicts(w io.Writer, conflicts []postgres.PreflightConflict, fixed bool) {
	if len(conflicts) == 0 {
		fmt.Fprintln(w, "\nno names differ only by letter case: migrations 027 and 031 can run")
		return
	}
	fmt.Fprintf(w, "\n%s of names that differ only by letter case (migrations 027 and 031 abort on them), oldest row first:\n", count(len(conflicts), "group"))
	manual := 0
	for _, group := range conflicts {
		fmt.Fprintf(w, "  %s in workspace %s", group.Table, group.WorkspaceID)
		if group.Platform != "" {
			fmt.Fprintf(w, ", platform %q", group.Platform)
		}
		fmt.Fprintln(w, ":")
		for index, row := range group.Rows {
			action := "keep"
			if index > 0 {
				action = "rename"
				if !group.Renamable {
					action = "manual"
				}
			}
			archived := ""
			if row.Archived {
				archived = " (archived)"
			}
			fmt.Fprintf(w, "    %-6s %s  %s  %q%s\n", action, row.ID, row.CreatedAt.UTC().Format(time.RFC3339), row.Name, archived)
		}
		if !group.Renamable {
			manual++
		}
	}
	if manual > 0 {
		fmt.Fprintln(w, "\naccount identifiers (shown masked) are external account IDs such as logins, e-mail addresses or platform user IDs,")
		fmt.Fprintln(w, "so the preflight never renames them: correct or delete the duplicate accounts, then run the preflight again")
	}
	if !fixed && manual < len(conflicts) {
		fmt.Fprintln(w, "\nrun `go run ./cmd/migrate preflight -fix` to rename all but the oldest row of each other group")
	}
}

func writeSchedules(w io.Writer, schedules []postgres.PreflightSchedule) {
	if len(schedules) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s to review after the cron parser change (a step \"N/S\" now runs from N to the field maximum instead of selecting only N; numbers must be unsigned digits):\n", count(len(schedules), "schedule"))
	for _, schedule := range schedules {
		fmt.Fprintf(w, "  schedule %s in workspace %s: %q (timezone %q, %s)\n", schedule.ID, schedule.WorkspaceID, schedule.CronExpression, schedule.Timezone, schedule.Status)
		if schedule.ParseError != "" {
			fmt.Fprintf(w, "    no longer accepted: %s\n", schedule.ParseError)
			continue
		}
		for _, step := range schedule.Steps {
			if step.Changed {
				fmt.Fprintf(w, "    %s %q now means %s (previously only %d)\n", step.FieldName, step.Item, step.Expanded, step.Start)
			} else {
				fmt.Fprintf(w, "    %s %q still selects only %d (the step exceeds the rest of the field)\n", step.FieldName, step.Item, step.Start)
			}
		}
	}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
