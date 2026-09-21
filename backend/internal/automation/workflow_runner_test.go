package automation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ant-chrome/backend/internal/config"
)

func TestWorkflowRunnerAssetsInstalledAndRepaired(t *testing.T) {
	dir := t.TempDir()
	runner := filepath.Join(dir, runnerScriptFileName)
	if err := writeRunnerScript(runner); err != nil {
		t.Fatal(err)
	}
	workflow := filepath.Join(dir, "runner_workflow.cjs")
	got, err := os.ReadFile(workflow)
	if err != nil || string(got) != string(runnerWorkflowContent) {
		t.Fatalf("workflow runner not installed: %v", err)
	}
	if err := os.Remove(workflow); err != nil {
		t.Fatal(err)
	}
	if err := syncRunnerScript(runner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workflow); err != nil {
		t.Fatalf("workflow runner not repaired: %v", err)
	}
}

func TestRunWorkflowAcceptsCDPEngineBeforeRuntimeReadinessCheck(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := NewManager(t.TempDir(), cfg, nil, Options{})
	_, err := manager.RunWorkflowTask(context.Background(), WorkflowTaskRequest{
		Definition: json.RawMessage(`{"schemaVersion":"ant-workflow/v1","engine":"cdp","steps":[{"id":"close","action":"close"}]}`),
	})
	if err == nil {
		t.Fatal("workflow unexpectedly ran without an installed runtime")
	}
	if strings.Contains(err.Error(), "invalid or unsupported workflow definition") {
		t.Fatalf("CDP definition was rejected before runtime readiness: %v", err)
	}
}

func TestRunWorkflowAcceptsPuppeteerEngineBeforeRuntimeReadinessCheck(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := NewManager(t.TempDir(), cfg, nil, Options{})
	_, err := manager.RunWorkflowTask(context.Background(), WorkflowTaskRequest{
		Definition: json.RawMessage(`{"schemaVersion":"ant-workflow/v1","engine":"puppeteer","steps":[{"id":"close","action":"close"}]}`),
	})
	if err == nil {
		t.Fatal("workflow unexpectedly ran without an installed runtime")
	}
	if strings.Contains(err.Error(), "invalid or unsupported workflow definition") {
		t.Fatalf("Puppeteer definition was rejected before runtime readiness: %v", err)
	}
}

func TestRunWorkflowRejectsInvalidDefinitionBeforeRuntimeAccess(t *testing.T) {
	var manager *Manager // Invalid input must be rejected before reading runtime state.
	for _, definition := range []string{
		`null`, `{}`,
		`{"schemaVersion":"ant-workflow/v1","engine":"unknown","steps":[{}]}`,
		`{"schemaVersion":"ant-workflow/v1","engine":"playwright","steps":[]}`,
	} {
		if _, err := manager.RunWorkflowTask(context.Background(), WorkflowTaskRequest{Definition: json.RawMessage(definition)}); err == nil {
			t.Fatalf("invalid workflow accepted: %s", definition)
		}
	}
}
