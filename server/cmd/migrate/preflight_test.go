package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

func TestRunPreflightRejectsUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"-force"}, {"extra"}, {"-fix", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := runPreflight(context.Background(), "postgres://unused.invalid/db", args, &stdout, &stderr); code != preflightUsage {
			t.Errorf("args %q: exit code %d, want %d (stderr %q)", args, code, preflightUsage, stderr.String())
		}
	}
}

func TestWriteReportListsGroupsAndSchedules(t *testing.T) {
	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	report := postgres.PreflightReport{
		Role: "migrator", Superuser: true, LatestMigration: "026_profile_storage_ledger.sql",
		Conflicts: []postgres.PreflightConflict{
			{Table: "browser_instances", WorkspaceID: "ws-1", Renamable: true, Rows: []postgres.PreflightRow{
				{ID: "instance-a", CreatedAt: created, Name: "Shop 1"},
				{ID: "instance-b", CreatedAt: created.Add(time.Hour), Name: "SHOP 1\nrenamed 0 rows"},
			}},
			{Table: "accounts", WorkspaceID: "ws-1", Platform: "amazon", Rows: []postgres.PreflightRow{
				{ID: "account-a", CreatedAt: created, Name: "se***@example.test"},
				{ID: "account-b", CreatedAt: created, Name: "Se***@example.test"},
			}},
			{Table: "workflows", WorkspaceID: "ws-2", Renamable: true, Rows: []postgres.PreflightRow{
				{ID: "workflow-a", CreatedAt: created, Name: "Nightly", Archived: true},
				{ID: "workflow-b", CreatedAt: created, Name: "nightly"},
			}},
		},
		Schedules: []postgres.PreflightSchedule{
			{ID: "schedule-a", WorkspaceID: "ws-1", CronExpression: "5/15 * * * *", Timezone: "UTC", Status: "active", Steps: scheduleservice.NumericStartSteps("5/15 * * * *")},
			{ID: "schedule-b", WorkspaceID: "ws-1", CronExpression: "30/45 * * * *", Timezone: "UTC", Status: "paused", Steps: scheduleservice.NumericStartSteps("30/45 * * * *")},
			{ID: "schedule-c", WorkspaceID: "ws-1", CronExpression: "+5 * * * *", Timezone: "UTC", Status: "active", ParseError: "invalid cron expression: cron field 1: invalid value"},
		},
	}
	var out bytes.Buffer
	writeReport(&out, report, false)
	text := out.String()
	for _, want := range []string{
		"schema at 026_profile_storage_ledger.sql",
		`keep   instance-a  2025-01-02T03:04:05Z  "Shop 1"`,
		`rename instance-b  2025-01-02T04:04:05Z  "SHOP 1\nrenamed 0 rows"`,
		`accounts in workspace ws-1, platform "amazon":`,
		`manual account-b`, "never renames them",
		`"Nightly" (archived)`,
		"preflight -fix",
		`minute "5/15" now means 5-59/15 (previously only 5)`,
		`minute "30/45" still selects only 30`,
		"no longer accepted: invalid cron expression",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\nrenamed 0 rows") {
		t.Fatalf("a name broke the output onto a new line:\n%s", text)
	}

	out.Reset()
	writeReport(&out, postgres.PreflightReport{Role: "migrator", BypassRLS: true, LatestMigration: "031_workflow_name_case_insensitive.sql"}, true)
	if text := out.String(); !strings.Contains(text, "(BYPASSRLS)") || !strings.Contains(text, "migrations 027 and 031 can run") || strings.Contains(text, "schedules to review") {
		t.Fatalf("clean report:\n%s", text)
	}
}
