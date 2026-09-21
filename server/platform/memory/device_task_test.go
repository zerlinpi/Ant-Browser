package memory

import (
	"context"
	"errors"
	automation "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	instances "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	devices "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	tasks "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	"testing"
	"time"
)

func TestDeviceClaimRestrictsWorkspaceAssignmentAndEngine(t *testing.T) {
	ctx := context.Background()
	s := New()
	now := time.Now().UTC()
	s.devices["device"] = devices.Device{ID: "device", WorkspaceID: "workspace"}
	s.devices["other-device"] = devices.Device{ID: "other-device", WorkspaceID: "workspace"}
	s.instances["instance"] = instances.BrowserInstance{ID: "instance", WorkspaceID: "workspace", AssignedDeviceID: "device"}
	if err := s.CreateWorkflow(ctx, automation.Workflow{ID: "flow", WorkspaceID: "workspace"}, automation.WorkflowVersion{ID: "version", WorkflowID: "flow", WorkspaceID: "workspace", Version: 1, Definition: automation.Definition{Engine: "playwright"}}); err != nil {
		t.Fatal(err)
	}
	s.tasks["task"] = tasks.Task{ID: "task", WorkspaceID: "workspace", TaskType: "workflow.execute", WorkflowID: "flow", WorkflowVersionID: "version", Payload: map[string]interface{}{"instanceId": "instance"}, Status: "queued", AvailableAt: now}
	if _, err := s.ClaimNextTask(ctx, "cloud-worker", []string{"workflow.execute"}, time.Minute, now); !errors.Is(err, tasks.ErrNoWork) {
		t.Fatalf("cloud worker claimed device execution: %v", err)
	}
	for _, scope := range [][2]string{{"workspace", "other-device"}, {"foreign", "device"}, {"workspace", "missing"}} {
		if _, err := s.ClaimDeviceWorkflow(ctx, scope[0], scope[1], time.Minute, now); !errors.Is(err, tasks.ErrNoWork) {
			t.Fatalf("wrong scope claimed task: %v", err)
		}
	}
	lease, err := s.ClaimDeviceWorkflow(ctx, "workspace", "device", time.Minute, now)
	if err != nil || lease.Task.ID != "task" || lease.Task.LeaseOwner != "device:device" {
		t.Fatalf("assigned device claim: %+v %v", lease, err)
	}
	if _, err := s.ClaimDeviceWorkflow(ctx, "workspace", "device", time.Minute, now); !errors.Is(err, tasks.ErrNoWork) {
		t.Fatalf("live task double claimed: %v", err)
	}
}

func TestDeviceClaimRejectsRevocationDeletionAndUnsupportedEngine(t *testing.T) {
	for _, invalid := range []string{"revoked", "deleted", "engine"} {
		t.Run(invalid, func(t *testing.T) {
			s := New()
			now := time.Now().UTC()
			device := devices.Device{ID: "device", WorkspaceID: "workspace"}
			instance := instances.BrowserInstance{ID: "instance", WorkspaceID: "workspace", AssignedDeviceID: "device"}
			version := automation.WorkflowVersion{ID: "version", WorkflowID: "flow", WorkspaceID: "workspace", Version: 1, Definition: automation.Definition{Engine: "playwright"}}
			switch invalid {
			case "revoked":
				device.RevokedAt = &now
			case "deleted":
				instance.DeletedAt = &now
			case "engine":
				version.Definition.Engine = "puppeteer"
			}
			s.devices["device"] = device
			s.instances["instance"] = instance
			if err := s.CreateWorkflow(context.Background(), automation.Workflow{ID: "flow", WorkspaceID: "workspace"}, version); err != nil {
				t.Fatal(err)
			}
			s.tasks["task"] = tasks.Task{ID: "task", WorkspaceID: "workspace", TaskType: "workflow.execute", WorkflowID: "flow", WorkflowVersionID: "version", Payload: map[string]interface{}{"instanceId": "instance"}, Status: "queued", AvailableAt: now}
			if _, err := s.ClaimDeviceWorkflow(context.Background(), "workspace", "device", time.Minute, now); !errors.Is(err, tasks.ErrNoWork) {
				t.Fatalf("unsafe claim accepted: %v", err)
			}
		})
	}
}

func TestDeviceClaimAcceptsRawCDPEngine(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	s.devices["device"] = devices.Device{ID: "device", WorkspaceID: "workspace"}
	s.instances["instance"] = instances.BrowserInstance{ID: "instance", WorkspaceID: "workspace", AssignedDeviceID: "device"}
	version := automation.WorkflowVersion{
		ID: "version", WorkflowID: "flow", WorkspaceID: "workspace", Version: 1,
		Definition: automation.Definition{Engine: "cdp"},
	}
	if err := s.CreateWorkflow(context.Background(), automation.Workflow{ID: "flow", WorkspaceID: "workspace"}, version); err != nil {
		t.Fatal(err)
	}
	s.tasks["task"] = tasks.Task{
		ID: "task", WorkspaceID: "workspace", TaskType: "workflow.execute",
		WorkflowID: "flow", WorkflowVersionID: "version",
		Payload: map[string]interface{}{"instanceId": "instance"}, Status: "queued", AvailableAt: now,
	}
	lease, err := s.ClaimDeviceWorkflow(context.Background(), "workspace", "device", time.Minute, now)
	if err != nil || lease.Task.ID != "task" {
		t.Fatalf("CDP task claim = %+v, %v", lease, err)
	}
}
