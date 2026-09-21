package memory

import (
	"context"
	"errors"
	"github.com/google/uuid"
	automation "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	instances "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	devices "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	members "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	tasks "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	"testing"
	"time"
)

type reportAuthorizer struct{}

func (reportAuthorizer) Require(context.Context, string, string, members.Permission) error {
	return nil
}

func TestDeviceReportRequiresAttemptOwnerAndRunningState(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "reassigned"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			s := New()
			now := time.Now().UTC()
			id := uuid.NewString()
			s.devices["device"] = devices.Device{ID: "device", WorkspaceID: "workspace"}
			s.instances["instance"] = instances.BrowserInstance{ID: "instance", WorkspaceID: "workspace", AssignedDeviceID: "device"}
			if err := s.CreateWorkflow(ctx, automation.Workflow{ID: "flow", WorkspaceID: "workspace"}, automation.WorkflowVersion{ID: "version", WorkflowID: "flow", WorkspaceID: "workspace", Version: 1, Definition: automation.Definition{Engine: "playwright"}}); err != nil {
				t.Fatal(err)
			}
			s.tasks[id] = tasks.Task{ID: id, WorkspaceID: "workspace", TaskType: "workflow.execute", WorkflowID: "flow", WorkflowVersionID: "version", Payload: map[string]interface{}{"instanceId": "instance"}, Status: "queued", AvailableAt: now}
			lease, err := s.ClaimDeviceWorkflow(ctx, "workspace", "device", time.Minute, now)
			if err != nil {
				t.Fatal(err)
			}
			svc := tasks.New(s, reportAuthorizer{})
			report := tasks.DeviceReport{RunID: lease.RunID, AttemptID: lease.AttemptID, Status: "succeeded"}
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); !errors.Is(err, tasks.ErrStateConflict) {
				t.Fatalf("completion before start: %v", err)
			}
			report.Status = "running"
			if err := svc.ReportDevice(ctx, "user", "workspace", "other", id, report); !errors.Is(err, tasks.ErrStateConflict) {
				t.Fatalf("wrong device: %v", err)
			}
			forged := report
			forged.AttemptID = uuid.NewString()
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, forged); !errors.Is(err, tasks.ErrStateConflict) {
				t.Fatalf("wrong attempt: %v", err)
			}
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); err != nil {
				t.Fatal(err)
			}
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); err != nil {
				t.Fatalf("running report replay: %v", err)
			}
			if terminal == "reassigned" {
				instance := s.instances["instance"]
				instance.AssignedDeviceID = "other"
				s.instances["instance"] = instance
				report.Status = "succeeded"
				if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); !errors.Is(err, tasks.ErrStateConflict) {
					t.Fatalf("reassigned target: %v", err)
				}
				return
			}
			report.Status = terminal
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); err != nil {
				t.Fatal(err)
			}
			if s.tasks[id].Status != terminal {
				t.Fatalf("terminal status=%s", s.tasks[id].Status)
			}
			if err := svc.ReportDevice(ctx, "user", "workspace", "device", id, report); err != nil {
				t.Fatalf("terminal report replay: %v", err)
			}
		})
	}
}
