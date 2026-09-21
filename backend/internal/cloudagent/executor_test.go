package cloudagent

import (
	"ant-chrome/backend/internal/automation"
	browseragent "ant-chrome/desktop/browser-agent"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type workflowRuntimeFunc func(context.Context, automation.WorkflowTaskRequest) (automation.ScriptTaskResult, error)

func (f workflowRuntimeFunc) RunWorkflowTask(ctx context.Context, req automation.WorkflowTaskRequest) (automation.ScriptTaskResult, error) {
	return f(ctx, req)
}

func TestExecutorCleansArtifactsAfterEveryOutcome(t *testing.T) {
	tests := []struct {
		name      string
		cancel    bool
		execErr   error
		resultOK  bool
		wantError bool
	}{
		{name: "success", resultOK: true},
		{name: "runtime failure", execErr: errors.New("runner failed"), wantError: true},
		{name: "workflow failure", wantError: true},
		{name: "cancelled", cancel: true, execErr: context.Canceled, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifactRoot := t.TempDir()
			seenDuringExecution := false
			runtime := workflowRuntimeFunc(func(ctx context.Context, req automation.WorkflowTaskRequest) (automation.ScriptTaskResult, error) {
				info, err := os.Stat(req.ArtifactDir)
				if err != nil {
					t.Fatalf("artifact directory unavailable during execution: %v", err)
				}
				if !info.IsDir() {
					t.Fatal("artifact path is not a directory")
				}
				seenDuringExecution = true
				if err := os.WriteFile(filepath.Join(req.ArtifactDir, "during-run.txt"), []byte("test"), 0600); err != nil {
					t.Fatalf("write artifact during execution: %v", err)
				}
				if test.execErr != nil {
					return automation.ScriptTaskResult{}, test.execErr
				}
				return automation.ScriptTaskResult{OK: test.resultOK}, nil
			})
			executor := &Executor{
				Runtime:      runtime,
				ArtifactRoot: artifactRoot,
				ResolveProfile: func(context.Context, string) (string, error) {
					return "local-profile", nil
				},
			}

			ctx := context.Background()
			var cancel context.CancelFunc
			if test.cancel {
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := executor.Execute(ctx, browseragent.Execution{InstanceID: "cloud-instance"})
			if (err != nil) != test.wantError {
				t.Fatalf("Execute error = %v, wantError=%v", err, test.wantError)
			}
			if !seenDuringExecution {
				t.Fatal("executor did not expose an existing artifact directory during execution")
			}
			entries, err := os.ReadDir(artifactRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("artifact directory leaked after Execute: %v", entries)
			}
		})
	}
}
