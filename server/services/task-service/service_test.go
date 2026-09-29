package taskservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type allowAuthorizer struct{}

func (allowAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

func TestTaskLeaseRetryAndCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedTaskWorkspace(t, store)
	service := taskservice.New(store, allowAuthorizer{})
	retryLimit := 1
	task, err := service.Enqueue(ctx, "user", "workspace", "healthcheck-once", taskservice.EnqueueInput{
		TaskType: "system.healthcheck", RetryLimit: &retryLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Enqueue(ctx, "user", "workspace", "healthcheck-once", taskservice.EnqueueInput{
		TaskType: "system.healthcheck", RetryLimit: &retryLimit,
	})
	if err != nil || replayed.ID != task.ID {
		t.Fatalf("idempotent enqueue=%+v err=%v", replayed, err)
	}

	first, err := service.Claim(ctx, "worker-a", []string{"system.healthcheck"}, time.Minute)
	if err != nil || first.Attempt != 1 || first.Task.LeaseOwner != "worker-a" {
		t.Fatalf("first lease=%+v err=%v", first, err)
	}
	if err := service.Start(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := service.Fail(ctx, first, "transient", "retry", true, 0); err != nil {
		t.Fatal(err)
	}

	second, err := service.Claim(ctx, "worker-b", []string{"system.healthcheck"}, time.Minute)
	if err != nil || second.Attempt != 2 || second.RunID != first.RunID {
		t.Fatalf("second lease=%+v err=%v", second, err)
	}
	if err := service.Start(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := service.Complete(ctx, second, map[string]interface{}{"ok": true}); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Get(ctx, "user", "workspace", task.ID)
	if err != nil || completed.Status != "succeeded" || completed.CompletedAt == nil {
		t.Fatalf("completed task=%+v err=%v", completed, err)
	}
	if _, err := service.Claim(ctx, "worker-c", []string{"system.healthcheck"}, time.Minute); !errors.Is(err, taskservice.ErrNoWork) {
		t.Fatalf("empty queue error=%v", err)
	}
	if _, err := service.Cancel(ctx, "user", "workspace", task.ID); !errors.Is(err, taskservice.ErrStateConflict) {
		t.Fatalf("terminal task cancel error=%v", err)
	}
}

func TestIdempotencyRejectsDifferentTaskRequests(t *testing.T) {
	base := taskservice.Task{
		TaskType:       "system.healthcheck",
		IdempotencyKey: "same",
		Priority:       1,
		RetryLimit:     1,
		Payload:        map[string]interface{}{"scope": "one"},
	}
	for name, different := range map[string]taskservice.Task{
		"task type":     {TaskType: "proxy.health_check", IdempotencyKey: base.IdempotencyKey, Priority: base.Priority, RetryLimit: base.RetryLimit, Payload: base.Payload},
		"payload":       {TaskType: base.TaskType, IdempotencyKey: base.IdempotencyKey, Priority: base.Priority, RetryLimit: base.RetryLimit, Payload: map[string]interface{}{"scope": "two"}},
		"priority":      {TaskType: base.TaskType, IdempotencyKey: base.IdempotencyKey, Priority: 2, RetryLimit: base.RetryLimit, Payload: base.Payload},
		"retry limit":   {TaskType: base.TaskType, IdempotencyKey: base.IdempotencyKey, Priority: base.Priority, RetryLimit: 2, Payload: base.Payload},
		"workflow type": {TaskType: "workflow.execute", WorkflowID: "flow", WorkflowVersionID: "version", IdempotencyKey: base.IdempotencyKey, Payload: map[string]interface{}{"instanceId": "instance"}},
	} {
		t.Run(name, func(t *testing.T) {
			if taskservice.SameWorkflowRequest(base, different) {
				t.Fatalf("different request was accepted: %+v", different)
			}
		})
	}
	if !taskservice.SameWorkflowRequest(base, base) {
		t.Fatal("identical request was rejected")
	}
}

func TestWorkflowTaskRequiresVersion(t *testing.T) {
	t.Parallel()
	store := memory.New()
	seedTaskWorkspace(t, store)
	service := taskservice.New(store, allowAuthorizer{})
	if _, err := service.Enqueue(context.Background(), "user", "workspace", "workflow", taskservice.EnqueueInput{
		TaskType: "workflow.execute",
	}); err == nil {
		t.Fatal("workflow task without a version was accepted")
	}
}

func seedTaskWorkspace(t *testing.T, store *memory.Store) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateUser(context.Background(), authservice.User{
		ID: "user", Email: "task@example.com", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOrganizationWorkspace(
		context.Background(),
		workspaceservice.Organization{ID: "organization", Name: "Organization", Slug: "organization", Version: 1, CreatedAt: now, UpdatedAt: now},
		workspaceservice.Workspace{ID: "workspace", OrganizationID: "organization", Name: "Workspace", Slug: "workspace", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now},
		workspaceservice.Membership{ID: "membership", WorkspaceID: "workspace", UserID: "user", Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now},
	); err != nil {
		t.Fatal(err)
	}
}
