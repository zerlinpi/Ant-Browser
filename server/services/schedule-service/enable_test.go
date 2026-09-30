package scheduleservice

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedClock(now time.Time) func() time.Time { return func() time.Time { return now } }

func TestEnableRecomputesNextRunFromNow(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 17, 30, 0, time.UTC)
	stale := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for _, status := range []string{"paused", "error"} {
		t.Run(status, func(t *testing.T) {
			repo := &scheduleRepo{item: Schedule{
				ID: "schedule", WorkspaceID: "workspace", CronExpression: "0 * * * *", Timezone: "Asia/Shanghai",
				Enabled: status == "error", Status: status, NextRunAt: &stale, Version: 4,
			}}
			svc := New(repo, nil, nil, nil)
			svc.now = fixedClock(now)
			enabled, err := svc.Enable(context.Background(), "actor", "workspace", "schedule")
			if err != nil {
				t.Fatal(err)
			}
			want := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
			if !enabled.Enabled || enabled.Status != "active" || enabled.NextRunAt == nil || !enabled.NextRunAt.Equal(want) {
				t.Fatalf("enabled schedule=%+v next=%v, want next %v", enabled, enabled.NextRunAt, want)
			}
		})
	}
}

func TestEnableKeepsPendingRunOfActiveSchedule(t *testing.T) {
	due := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	repo := &scheduleRepo{item: Schedule{
		ID: "schedule", WorkspaceID: "workspace", CronExpression: "0 * * * *", Timezone: "UTC",
		Enabled: true, Status: "active", NextRunAt: &due, Version: 2,
	}}
	svc := New(repo, nil, nil, nil)
	svc.now = fixedClock(due.Add(30 * time.Second))
	enabled, err := svc.Enable(context.Background(), "actor", "workspace", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	if enabled.NextRunAt == nil || !enabled.NextRunAt.Equal(due) {
		t.Fatalf("an active schedule's due run was skipped: next=%v", enabled.NextRunAt)
	}
}

func TestDisableClearsNextRun(t *testing.T) {
	next := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	repo := &scheduleRepo{item: Schedule{
		ID: "schedule", WorkspaceID: "workspace", CronExpression: "0 * * * *", Timezone: "UTC",
		Enabled: true, Status: "active", NextRunAt: &next, Version: 1,
	}}
	disabled, err := New(repo, nil, nil, nil).Disable(context.Background(), "actor", "workspace", "schedule")
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Status != "paused" || disabled.NextRunAt != nil {
		t.Fatalf("disabled schedule=%+v", disabled)
	}
}

// conflictingRepo reports a version conflict until the schedule was re-read
// a given number of times, as when the worker dispatches it concurrently.
type conflictingRepo struct {
	scheduleRepo
	conflicts int
	reads     int
}

func (r *conflictingRepo) FindSchedule(ctx context.Context, workspace, id string) (Schedule, error) {
	r.reads++
	return r.scheduleRepo.FindSchedule(ctx, workspace, id)
}

func (r *conflictingRepo) SetScheduleEnabled(ctx context.Context, workspace, id string, enabled bool, next *time.Time, expected int64, now time.Time) (Schedule, error) {
	if r.conflicts > 0 {
		r.conflicts--
		r.item.Version++
		return Schedule{}, ErrVersionConflict
	}
	return r.scheduleRepo.SetScheduleEnabled(ctx, workspace, id, enabled, next, expected, now)
}

func TestEnableRetriesConcurrentChangesThenGivesUp(t *testing.T) {
	base := Schedule{ID: "schedule", WorkspaceID: "workspace", CronExpression: "*/5 * * * *", Timezone: "UTC", Status: "paused", Version: 1}
	repo := &conflictingRepo{scheduleRepo: scheduleRepo{item: base}, conflicts: enableAttempts - 1}
	if _, err := New(repo, nil, nil, nil).Enable(context.Background(), "actor", "workspace", "schedule"); err != nil {
		t.Fatalf("enable after %d concurrent changes: %v", enableAttempts-1, err)
	}
	if repo.reads != enableAttempts {
		t.Fatalf("schedule read %d times, want %d", repo.reads, enableAttempts)
	}

	repo = &conflictingRepo{scheduleRepo: scheduleRepo{item: base}, conflicts: enableAttempts}
	if _, err := New(repo, nil, nil, nil).Enable(context.Background(), "actor", "workspace", "schedule"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("persistent conflict error=%v", err)
	}
}

func TestEnableRejectsStoredRuleThatCannotRun(t *testing.T) {
	for name, item := range map[string]Schedule{
		"malformed cron":   {CronExpression: "* * *", Timezone: "UTC"},
		"never fires":      {CronExpression: "0 0 30 2 *", Timezone: "UTC"},
		"unknown timezone": {CronExpression: "0 * * * *", Timezone: "Mars/Olympus"},
	} {
		t.Run(name, func(t *testing.T) {
			item.ID, item.WorkspaceID, item.Status, item.Version = "schedule", "workspace", "error", 1
			repo := &scheduleRepo{item: item}
			_, err := New(repo, nil, nil, nil).Enable(context.Background(), "actor", "workspace", "schedule")
			if !errors.Is(err, ErrInvalidCron) && !errors.Is(err, ErrInvalidTimezone) {
				t.Fatalf("error=%v, want a classified validation error", err)
			}
			if repo.item.Status != "error" || repo.item.Version != 1 {
				t.Fatalf("schedule changed after a rejected enable: %+v", repo.item)
			}
		})
	}
}

func TestUpdateOfPausedScheduleKeepsNoNextRun(t *testing.T) {
	repo := &scheduleRepo{item: Schedule{
		ID: "schedule", WorkspaceID: "workspace", WorkflowID: "11111111-1111-4111-8111-111111111111",
		WorkflowVersionID: "22222222-2222-4222-8222-222222222222", InstanceID: "33333333-3333-4333-8333-333333333333",
		CronExpression: "0 * * * *", Timezone: "UTC", Status: "paused", Version: 3,
	}}
	updated, err := New(repo, nil, nil, nil).Update(context.Background(), "actor", "workspace", "schedule", UpdateInput{
		CronExpression: "30 8 * * 1", Timezone: "Europe/Berlin", ExpectedVersion: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt != nil || updated.CronExpression != "30 8 * * 1" || updated.Timezone != "Europe/Berlin" {
		t.Fatalf("paused update=%+v", updated)
	}
}

func TestInvalidRulesAreClassified(t *testing.T) {
	for _, expression := range []string{"* * *", "1,,2 * * * *", "61 * * * *", "*/0 * * * *", "0 0 30 2 *"} {
		_, parseErr := ParseCron(expression)
		err := parseErr
		if err == nil {
			cron, _ := ParseCron(expression)
			_, err = cron.Next(time.Now(), time.UTC)
		}
		if !errors.Is(err, ErrInvalidCron) {
			t.Errorf("%q: error=%v, want ErrInvalidCron", expression, err)
		}
	}
	for _, name := range []string{"", "Local", "Mars/Olympus", "../../etc/passwd"} {
		if _, err := loadTimezone(name); !errors.Is(err, ErrInvalidTimezone) {
			t.Errorf("timezone %q accepted: %v", name, err)
		}
	}
	if _, err := loadTimezone("America/New_York"); err != nil {
		t.Fatal(err)
	}
}
