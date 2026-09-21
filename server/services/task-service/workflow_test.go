package taskservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	automation "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	instance "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	tasks "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

func TestWorkflowEnqueuePinsPublishedVersionAndTenantTarget(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedTaskWorkspace(t, store)
	svc := tasks.New(store, allowAuthorizer{})
	workflows := automation.New(store, allowAuthorizer{})
	targetID := uuid.NewString()
	if err := store.CreateInstance(ctx, instance.BrowserInstance{ID: targetID, WorkspaceID: "workspace", Name: "Target"}); err != nil {
		t.Fatal(err)
	}
	workflow, version, err := workflows.Create(ctx, "user", "workspace", automation.CreateInput{Name: "Flow", Definition: automation.Definition{Engine: "playwright", Steps: []automation.Step{{ID: "wait", Action: "wait", Parameters: map[string]interface{}{"durationMs": 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	input := tasks.EnqueueInput{TaskType: "workflow.execute", WorkflowID: workflow.ID, WorkflowVersionID: version.ID, Payload: map[string]interface{}{"instanceId": targetID}}
	if _, err := svc.Enqueue(ctx, "user", "workspace", "draft", input); !errors.Is(err, tasks.ErrStateConflict) {
		t.Fatalf("draft accepted: %v", err)
	}
	published, err := workflows.Publish(ctx, "user", "workspace", workflow.ID, automation.StateInput{ExpectedVersion: 1, WorkflowVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := svc.Enqueue(ctx, "user", "workspace", "run", input)
	if err != nil || accepted.WorkflowVersionID != version.ID || accepted.RetryLimit != 0 {
		t.Fatalf("invalid accepted execution: %+v %v", accepted, err)
	}
	invalid := input
	invalid.Payload = map[string]interface{}{"instanceId": uuid.NewString()}
	if _, err := svc.Enqueue(ctx, "user", "workspace", "foreign-target", invalid); !errors.Is(err, tasks.ErrNotFound) {
		t.Fatalf("foreign target accepted: %v", err)
	}
	if _, err := svc.Enqueue(ctx, "user", "workspace", "run", invalid); !errors.Is(err, tasks.ErrStateConflict) {
		t.Fatalf("idempotency collision accepted: %v", err)
	}
	invalid = input
	invalid.WorkflowVersionID = uuid.NewString()
	if _, err := svc.Enqueue(ctx, "user", "workspace", "foreign-version", invalid); !errors.Is(err, tasks.ErrStateConflict) {
		t.Fatalf("foreign version accepted: %v", err)
	}
	invalid = input
	invalid.Payload = map[string]interface{}{"instanceId": targetID, "script": "injected"}
	if _, err := svc.Enqueue(ctx, "user", "workspace", "injected", invalid); err == nil {
		t.Fatal("arbitrary workflow payload accepted")
	}
	if _, err := workflows.Archive(ctx, "user", "workspace", workflow.ID, automation.StateInput{ExpectedVersion: published.Version}); err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.Enqueue(ctx, "user", "workspace", "run", input)
	if err != nil || replayed.ID != accepted.ID {
		t.Fatalf("accepted execution replay lost: %+v %v", replayed, err)
	}
	if _, err := svc.Enqueue(ctx, "user", "workspace", "new-after-archive", input); !errors.Is(err, tasks.ErrStateConflict) {
		t.Fatalf("archived workflow accepted: %v", err)
	}
}
