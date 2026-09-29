package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
)

type automationState struct {
	mu        sync.RWMutex
	workflows map[string]automationservice.Workflow
	versions  map[string]map[int]automationservice.WorkflowVersion
}

var automationStates sync.Map // map[*Store]*automationState

func (s *Store) automation() *automationState {
	if value, ok := automationStates.Load(s); ok {
		return value.(*automationState)
	}
	created := &automationState{
		workflows: make(map[string]automationservice.Workflow),
		versions:  make(map[string]map[int]automationservice.WorkflowVersion),
	}
	actual, _ := automationStates.LoadOrStore(s, created)
	return actual.(*automationState)
}

func (s *Store) CreateWorkflow(_ context.Context, workflow automationservice.Workflow, version automationservice.WorkflowVersion) error {
	state := s.automation()
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, current := range state.workflows {
		if current.WorkspaceID == workflow.WorkspaceID && current.Name == workflow.Name {
			return automationservice.ErrNameConflict
		}
	}
	state.workflows[workflow.ID] = workflow
	state.versions[workflow.ID] = map[int]automationservice.WorkflowVersion{version.Version: cloneWorkflowVersion(version)}
	return nil
}

func (s *Store) FindWorkflow(_ context.Context, workspaceID, workflowID string) (automationservice.Workflow, error) {
	state := s.automation()
	state.mu.RLock()
	defer state.mu.RUnlock()
	workflow, ok := state.workflows[workflowID]
	if !ok || workflow.WorkspaceID != workspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	return workflow, nil
}

func (s *Store) ListWorkflows(_ context.Context, workspaceID string) ([]automationservice.Workflow, error) {
	state := s.automation()
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]automationservice.Workflow, 0)
	for _, workflow := range state.workflows {
		if workflow.WorkspaceID == workspaceID {
			items = append(items, workflow)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *Store) AddWorkflowVersion(_ context.Context, version automationservice.WorkflowVersion, expectedVersion int64, now time.Time) (automationservice.Workflow, error) {
	state := s.automation()
	state.mu.Lock()
	defer state.mu.Unlock()
	workflow, ok := state.workflows[version.WorkflowID]
	if !ok || workflow.WorkspaceID != version.WorkspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	if workflow.Version != expectedVersion || version.Version != workflow.LatestVersion+1 {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if workflow.Status == "archived" {
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	state.versions[workflow.ID][version.Version] = cloneWorkflowVersion(version)
	workflow.LatestVersion = version.Version
	workflow.Version++
	workflow.UpdatedAt = now
	state.workflows[workflow.ID] = workflow
	return workflow, nil
}

func (s *Store) FindWorkflowVersion(_ context.Context, workspaceID, workflowID string, version int) (automationservice.WorkflowVersion, error) {
	state := s.automation()
	state.mu.RLock()
	defer state.mu.RUnlock()
	workflow, ok := state.workflows[workflowID]
	result, versionOK := state.versions[workflowID][version]
	if !ok || workflow.WorkspaceID != workspaceID || !versionOK {
		return automationservice.WorkflowVersion{}, automationservice.ErrNotFound
	}
	return cloneWorkflowVersion(result), nil
}

func (s *Store) TransitionWorkflow(_ context.Context, workspaceID, workflowID, status string, workflowVersion int, expectedVersion int64, now time.Time) (automationservice.Workflow, error) {
	state := s.automation()
	state.mu.Lock()
	defer state.mu.Unlock()
	workflow, ok := state.workflows[workflowID]
	if !ok || workflow.WorkspaceID != workspaceID {
		return automationservice.Workflow{}, automationservice.ErrNotFound
	}
	if workflow.Version != expectedVersion {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if workflow.Status == "archived" {
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	switch status {
	case "published":
		selected, exists := state.versions[workflowID][workflowVersion]
		if !exists {
			return automationservice.Workflow{}, automationservice.ErrNotFound
		}
		workflow.PublishedVersionID = selected.ID
	case "archived":
		workflow.ArchivedAt = &now
	default:
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	workflow.Status = status
	workflow.Version++
	workflow.UpdatedAt = now
	state.workflows[workflow.ID] = workflow
	return workflow, nil
}

func cloneWorkflowVersion(value automationservice.WorkflowVersion) automationservice.WorkflowVersion {
	value.Definition.Steps = append([]automationservice.Step(nil), value.Definition.Steps...)
	for index := range value.Definition.Steps {
		value.Definition.Steps[index].Parameters = cloneWorkflowParameters(value.Definition.Steps[index].Parameters)
	}
	return value
}

func (s *Store) FindWorkflowVersionByID(_ context.Context, workspaceID, workflowID, versionID string) (automationservice.WorkflowVersion, error) {
	state := s.automation()
	state.mu.RLock()
	defer state.mu.RUnlock()
	for _, version := range state.versions[workflowID] {
		if version.ID == versionID && version.WorkspaceID == workspaceID {
			return cloneWorkflowVersion(version), nil
		}
	}
	return automationservice.WorkflowVersion{}, automationservice.ErrNotFound
}

func cloneWorkflowParameters(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	result := make(map[string]interface{}, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]interface{}:
			result[key] = cloneWorkflowParameters(typed)
		case []interface{}:
			result[key] = append([]interface{}(nil), typed...)
		default:
			result[key] = typed
		}
	}
	return result
}
