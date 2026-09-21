package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

func TestProxyHealthRetryExhaustionFinalizesCheck(t *testing.T) {
	for _, expired := range []bool{false, true} {
		s := New()
		ctx := context.Background()
		now := time.Now().UTC()
		s.tasks["task"] = taskservice.Task{ID: "task", WorkspaceID: "workspace", TaskType: "proxy.health_check", Status: "queued", AvailableAt: now, RetryLimit: 0}
		s.proxyHealthChecks["check"] = proxyservice.HealthCheck{ID: "check", WorkspaceID: "workspace", RequestID: "task", Status: "queued"}
		lease, err := s.ClaimNextTask(ctx, "worker", []string{"proxy.health_check"}, time.Second, now)
		if err != nil {
			t.Fatal(err)
		}
		if expired {
			_, err = s.ClaimNextTask(ctx, "worker", []string{"proxy.health_check"}, time.Second, now.Add(2*time.Second))
			if !errors.Is(err, taskservice.ErrNoWork) {
				t.Fatalf("exhausted task was reclaimed: %v", err)
			}
		} else {
			if err := s.FailTask(ctx, lease, "probe_unavailable", "", true, now.Add(time.Second), now); err != nil {
				t.Fatal(err)
			}
		}
		check := s.proxyHealthChecks["check"]
		if check.Status != "failed" || check.ErrorCode != "retry_limit_exhausted" || check.CompletedAt == nil {
			t.Fatalf("expired=%v: check not finalized: %+v", expired, check)
		}
	}
}

func TestProxyHealthTerminalStateIsScopedAndFinal(t *testing.T) {
	for _, status := range []string{"cancelled", "failed", "dead_letter"} {
		t.Run(status, func(t *testing.T) {
			s := New()
			now := time.Now().UTC()
			s.proxyHealthChecks["check"] = proxyservice.HealthCheck{ID: "check", WorkspaceID: "workspace", RequestID: "task", Status: "queued"}
			s.proxyHealthChecks["foreign"] = proxyservice.HealthCheck{ID: "foreign", WorkspaceID: "other", RequestID: "task", Status: "queued"}
			s.syncProxyHealthTerminalLocked(taskservice.Task{ID: "task", WorkspaceID: "workspace", TaskType: "proxy.health_check", Status: status}, now)
			check := s.proxyHealthChecks["check"]
			wantStatus, wantCode := "failed", "task_failed"
			if status == "cancelled" {
				wantStatus, wantCode = "cancelled", "task_cancelled"
			}
			if status == "dead_letter" {
				wantCode = "retry_limit_exhausted"
			}
			if check.Status != wantStatus || check.ErrorCode != wantCode || check.CompletedAt == nil {
				t.Fatalf("terminal state not propagated: %+v", check)
			}
			if s.proxyHealthChecks["foreign"].CompletedAt != nil {
				t.Fatal("cross-workspace check changed")
			}
			late, err := s.CompleteProxyHealthCheck(context.Background(), "workspace", "check", proxyservice.HealthResult{Status: "succeeded", IP: "192.0.2.1"}, now.Add(time.Second))
			if err != nil || late.Status != wantStatus || late.ErrorCode != wantCode || !late.CompletedAt.Equal(now) {
				t.Fatalf("late result overwrote terminal check: %+v, %v", late, err)
			}
		})
	}
}
