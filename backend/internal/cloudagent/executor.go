package cloudagent

import (
	"ant-chrome/backend/internal/automation"
	browseragent "ant-chrome/desktop/browser-agent"
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Executor is constructed by the desktop using trusted local configuration.
// Binding resolution must be workspace-scoped and reject unknown cloud IDs.
type Executor struct {
	Runtime          workflowRuntime
	ResolveProfile   func(context.Context, string) (string, error)
	LaunchBaseURL    string
	LaunchAuthHeader string
	LaunchAuthValue  string
	ArtifactRoot     string
}

type workflowRuntime interface {
	RunWorkflowTask(context.Context, automation.WorkflowTaskRequest) (automation.ScriptTaskResult, error)
}

func (e *Executor) Execute(ctx context.Context, input browseragent.Execution) (retErr error) {
	if e.Runtime == nil || e.ResolveProfile == nil || !filepath.IsAbs(e.ArtifactRoot) {
		return errors.New("local workflow executor is not configured")
	}
	profile, err := e.ResolveProfile(ctx, input.InstanceID)
	if err != nil || profile == "" {
		return errors.New("cloud instance has no authorized local binding")
	}
	if err := os.MkdirAll(e.ArtifactRoot, 0700); err != nil {
		return err
	}
	artifactDir, err := os.MkdirTemp(e.ArtifactRoot, "workflow-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(artifactDir); cleanupErr != nil && retErr == nil {
			retErr = errors.New("workflow artifact cleanup failed")
		}
	}()
	result, err := e.Runtime.RunWorkflowTask(ctx, automation.WorkflowTaskRequest{
		ScriptTaskRequest: automation.ScriptTaskRequest{TaskKey: profile, Selector: map[string]any{"profileId": profile}, LaunchBaseURL: e.LaunchBaseURL, LaunchAuthHeader: e.LaunchAuthHeader, LaunchAuthValue: e.LaunchAuthValue, ArtifactDir: artifactDir},
		Definition:        input.Definition,
	})
	if err != nil || !result.OK {
		return errors.New("local workflow execution failed")
	}
	return nil
}
