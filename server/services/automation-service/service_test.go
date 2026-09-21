package automationservice_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

type allowAuthorizer struct{}

func (allowAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

type fakeRepository struct {
	mu        sync.Mutex
	workflows map[string]automationservice.Workflow
	versions  map[string]map[int]automationservice.WorkflowVersion
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		workflows: make(map[string]automationservice.Workflow),
		versions:  make(map[string]map[int]automationservice.WorkflowVersion),
	}
}

func (r *fakeRepository) CreateWorkflow(_ context.Context, workflow automationservice.Workflow, version automationservice.WorkflowVersion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.workflows {
		if existing.WorkspaceID == workflow.WorkspaceID && existing.Name == workflow.Name {
			return errors.New("workflow name already exists")
		}
	}
	r.workflows[workflow.ID] = workflow
	r.versions[workflow.ID] = map[int]automationservice.WorkflowVersion{version.Version: version}
	return nil
}

func (r *fakeRepository) FindWorkflow(_ context.Context, workspaceID, workflowID string) (automationservice.Workflow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workflow, ok := r.workflows[workflowID]
	if !ok || workflow.WorkspaceID != workspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	return workflow, nil
}

func (r *fakeRepository) ListWorkflows(_ context.Context, workspaceID string) ([]automationservice.Workflow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]automationservice.Workflow, 0)
	for _, workflow := range r.workflows {
		if workflow.WorkspaceID == workspaceID {
			items = append(items, workflow)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (r *fakeRepository) AddWorkflowVersion(_ context.Context, version automationservice.WorkflowVersion, expected int64, now time.Time) (automationservice.Workflow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workflow, ok := r.workflows[version.WorkflowID]
	if !ok || workflow.WorkspaceID != version.WorkspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	if workflow.Version != expected {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if version.Version != workflow.LatestVersion+1 {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	r.versions[workflow.ID][version.Version] = version
	workflow.LatestVersion = version.Version
	workflow.Version++
	workflow.UpdatedAt = now
	r.workflows[workflow.ID] = workflow
	return workflow, nil
}

func (r *fakeRepository) FindWorkflowVersion(_ context.Context, workspaceID, workflowID string, version int) (automationservice.WorkflowVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workflow, ok := r.workflows[workflowID]
	value, versionOK := r.versions[workflowID][version]
	if !ok || workflow.WorkspaceID != workspaceID || !versionOK {
		return automationservice.WorkflowVersion{}, automationservice.ErrNotFound
	}
	return value, nil
}

func (r *fakeRepository) TransitionWorkflow(_ context.Context, workspaceID, workflowID, status string, version int, expected int64, now time.Time) (automationservice.Workflow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	workflow, ok := r.workflows[workflowID]
	if !ok || workflow.WorkspaceID != workspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	if workflow.Version != expected {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if workflow.Status == "archived" {
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	if status == "published" {
		selected, ok := r.versions[workflowID][version]
		if !ok {
			return automationservice.Workflow{}, automationservice.ErrNotFound
		}
		workflow.PublishedVersionID = selected.ID
	}
	if status == "archived" {
		workflow.ArchivedAt = &now
	}
	workflow.Status = status
	workflow.Version++
	workflow.UpdatedAt = now
	r.workflows[workflow.ID] = workflow
	return workflow, nil
}

func TestWorkflowVersioningAndDeterministicDefinitionHash(t *testing.T) {
	t.Parallel()
	repository := newFakeRepository()
	service := automationservice.New(repository, allowAuthorizer{})
	definition := automationservice.Definition{
		Engine: "Playwright",
		Steps: []automationservice.Step{
			{ID: "open", Action: "navigate", Parameters: map[string]interface{}{"waitUntil": "networkidle", "url": "https://seller.example.test/login"}},
			{ID: "email", Action: "input", Parameters: map[string]interface{}{"selector": "#email", "valueRef": "variable://account-email"}},
			{ID: "password", Action: "input", Parameters: map[string]interface{}{"valueRef": "account-secret://password", "selector": "input[type=password]"}},
			{ID: "submit", Action: "click", Parameters: map[string]interface{}{"selector": "button[type=submit]"}},
			{ID: "capture", Action: "screenshot", Parameters: map[string]interface{}{"format": "png", "name": "post-login"}},
			{ID: "finish", Action: "close"},
		},
	}
	workflow, first, err := service.Create(context.Background(), "actor", "workspace", automationservice.CreateInput{Name: "Seller login", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Status != "draft" || first.Definition.Engine != "playwright" || len(first.ContentHash) != 64 {
		t.Fatalf("unexpected workflow/version: %+v %+v", workflow, first)
	}
	updated, second, err := service.AddVersion(context.Background(), "actor", "workspace", workflow.ID, automationservice.AddVersionInput{
		ExpectedVersion: workflow.Version, Definition: definition,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 || second.ContentHash != first.ContentHash || updated.Version != 2 {
		t.Fatalf("workflow canonical versioning failed: %+v %+v", updated, second)
	}
	published, err := service.Publish(context.Background(), "actor", "workspace", workflow.ID, automationservice.StateInput{
		ExpectedVersion: updated.Version, WorkflowVersion: second.Version,
	})
	if err != nil || published.Status != "published" || published.PublishedVersionID != second.ID {
		t.Fatalf("publish workflow=%+v err=%v", published, err)
	}
	if _, err := service.Archive(context.Background(), "actor", "workspace", workflow.ID, automationservice.StateInput{ExpectedVersion: updated.Version}); !errors.Is(err, automationservice.ErrVersionConflict) {
		t.Fatalf("stale workflow update error=%v", err)
	}
}

func TestWorkflowRejectsEmbeddedPasswordAndInvalidWait(t *testing.T) {
	t.Parallel()
	service := automationservice.New(newFakeRepository(), allowAuthorizer{})
	_, _, err := service.Create(context.Background(), "actor", "workspace", automationservice.CreateInput{
		Name: "Unsafe workflow",
		Definition: automationservice.Definition{Engine: "cdp", Steps: []automationservice.Step{{
			ID: "password", Action: "input",
			Parameters: map[string]interface{}{"selector": "#password", "value": "plaintext-password"},
		}}},
	})
	if err == nil {
		t.Fatal("password literal was accepted")
	}
	_, _, err = service.Create(context.Background(), "actor", "workspace", automationservice.CreateInput{
		Name: "Ambiguous wait",
		Definition: automationservice.Definition{Engine: "puppeteer", Steps: []automationservice.Step{{
			ID: "wait", Action: "wait", Parameters: map[string]interface{}{"selector": "#ready", "durationMs": 1000},
		}}},
	})
	if err == nil {
		t.Fatal("ambiguous wait was accepted")
	}
}
