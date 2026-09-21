package memory

import (
	"context"
	"errors"
	tasks "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	"testing"
	"time"
)

func TestTaskResultsRequireCurrentUnexpiredAttempt(t *testing.T) {
	ctx := context.Background()
	for _, operation := range []string{"start", "complete", "fail"} {
		t.Run(operation, func(t *testing.T) {
			s := New()
			now := time.Now().UTC()
			s.tasks["task"] = tasks.Task{ID: "task", WorkspaceID: "workspace", TaskType: "system.healthcheck", Status: "queued", AvailableAt: now, RetryLimit: 2}
			first, err := s.ClaimNextTask(ctx, "same-worker", []string{"system.healthcheck"}, time.Second, now)
			if err != nil {
				t.Fatal(err)
			}
			apply := func(lease tasks.Lease, at time.Time) error {
				switch operation {
				case "start":
					return s.StartTask(ctx, lease, at)
				case "complete":
					return s.CompleteTask(ctx, lease, nil, at)
				default:
					return s.FailTask(ctx, lease, "failure", "", true, at, at)
				}
			}
			if err := apply(first, now.Add(time.Second)); !errors.Is(err, tasks.ErrStateConflict) {
				t.Fatalf("expired attempt accepted: %v", err)
			}
			second, err := s.ClaimNextTask(ctx, "same-worker", []string{"system.healthcheck"}, time.Minute, now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			at := now.Add(3 * time.Second)
			if err := apply(first, at); !errors.Is(err, tasks.ErrStateConflict) {
				t.Fatalf("stale attempt accepted: %v", err)
			}
			for _, field := range []string{"workspace", "run", "attempt"} {
				forged := second
				switch field {
				case "workspace":
					forged.Task.WorkspaceID = "other"
				case "run":
					forged.RunID = "other"
				case "attempt":
					forged.Attempt++
				}
				if err := apply(forged, at); !errors.Is(err, tasks.ErrStateConflict) {
					t.Fatalf("forged %s accepted: %v", field, err)
				}
			}
			if err := apply(second, at); err != nil {
				t.Fatalf("current attempt rejected: %v", err)
			}
		})
	}
}

func TestCancelTaskClosesCurrentRunAndAttempt(t *testing.T) {
	ctx := context.Background()
	for _, start := range []bool{false, true} {
		t.Run(map[bool]string{false: "leased", true: "running"}[start], func(t *testing.T) {
			s := New()
			now := time.Now().UTC()
			s.tasks["task"] = tasks.Task{ID: "task", WorkspaceID: "workspace", TaskType: "system.healthcheck", Status: "queued", AvailableAt: now, RetryLimit: 0}
		lease, err := s.ClaimNextTask(ctx, "worker", []string{"system.healthcheck"}, time.Minute, now)
		if err != nil {
			t.Fatal(err)
		}
		if start {
			if err := s.StartTask(ctx, lease, now); err != nil {
				t.Fatal(err)
			}
		}
		cancelled, err := s.CancelTask(ctx, "workspace", "task", now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != "cancelled" {
			t.Fatalf("task status = %q", cancelled.Status)
		}
		if _, ok := s.taskLeases[lease.Task.ID]; ok {
			t.Fatal("task lease was not removed")
		}
		run := s.taskRuns[lease.RunID]
		if run.status != "cancelled" || run.errorCode != "task_cancelled" || run.finishedAt == nil {
			t.Fatalf("run was not closed: %+v", run)
		}
		attempt := s.taskAttemptStates[lease.AttemptID]
		if attempt.status != "expired" || attempt.errorCode != "task_cancelled" || attempt.finishedAt == nil {
			t.Fatalf("attempt was not closed: %+v", attempt)
		}
		if start && run.status != "cancelled" {
			t.Fatalf("running task run was not cancelled: %+v", run)
		}
		if !start && run.status != "cancelled" {
			t.Fatalf("leased task run was not cancelled: %+v", run)
		}
		if _, err := s.ClaimNextTask(ctx, "worker-2", []string{"system.healthcheck"}, time.Minute, now.Add(2*time.Second)); !errors.Is(err, tasks.ErrNoWork) {
			t.Fatalf("cancelled task was claimable: %v", err)
		}
	})
	}
}
