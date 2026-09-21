package memory

import (
	"context"
	"testing"
	"time"

	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

func TestClaimDueSchedulesIsSingleClaimAndIdempotent(t *testing.T) {
	store := New()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	item := scheduleservice.Schedule{ID: "schedule-1", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", WorkflowVersionID: "version-1", InstanceID: "instance-1", CronExpression: "* * * * *", Timezone: "UTC", Enabled: true, Status: "active", NextRunAt: &now, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := store.CreateSchedule(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDueSchedules(context.Background(), "worker-a", now, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: len=%d err=%v", len(first), err)
	}
	second, err := store.ClaimDueSchedules(context.Background(), "worker-b", now, 10)
	if err != nil || len(second) != 0 {
		t.Fatalf("second claim: len=%d err=%v", len(second), err)
	}
	if first[0].Task.IdempotencyKey != "schedule:schedule-1:2025-01-01T00:00:00Z" {
		t.Fatalf("unexpected idempotency key %q", first[0].Task.IdempotencyKey)
	}
}
