package taskservice

import (
	"context"
	"errors"
	"github.com/google/uuid"
	automation "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	members "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	"time"
)

type DeviceRepository interface {
	FindTaskLease(context.Context, string, string) (Lease, error)
	ClaimDeviceWorkflow(context.Context, string, string, time.Duration, time.Time) (Lease, error)
	FindWorkflowVersionByID(context.Context, string, string, string) (automation.WorkflowVersion, error)
}

type DeviceReport struct {
	RunID     string `json:"runId"`
	AttemptID string `json:"attemptId"`
	Status    string `json:"status"`
}

func (s *Service) ReportDevice(ctx context.Context, userID, workspaceID, deviceID, taskID string, report DeviceReport) error {
	for _, permission := range []members.Permission{members.PermissionTaskOperate, members.PermissionInstanceOperate, members.PermissionWorkflowRead} {
		if err := s.authorizer.Require(ctx, workspaceID, userID, permission); err != nil {
			return err
		}
	}
	for _, id := range []string{taskID, report.RunID, report.AttemptID} {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("task, run and attempt IDs must be UUIDs")
		}
	}
	if report.Status != "running" && report.Status != "succeeded" && report.Status != "failed" {
		return errors.New("invalid device task status")
	}
	repository, ok := s.repository.(DeviceRepository)
	if !ok {
		return errors.New("device task repository unavailable")
	}
	lease, err := repository.FindTaskLease(ctx, workspaceID, taskID)
	if err != nil {
		return err
	}
	if lease.Task.TaskType != "workflow.execute" || lease.Task.LeaseOwner != "device:"+deviceID || lease.RunID != report.RunID || lease.AttemptID != report.AttemptID {
		return ErrStateConflict
	}
	if report.Status == "running" && lease.Task.Status == "running" {
		return nil
	}
	if report.Status == "running" {
		return s.Start(ctx, lease)
	}
	if report.Status == lease.Task.Status {
		return nil
	}
	if lease.Task.Status != "running" {
		return ErrStateConflict
	}
	if report.Status == "succeeded" {
		return s.Complete(ctx, lease, map[string]interface{}{"status": "succeeded"})
	}
	// Do not persist arbitrary browser diagnostics or retry side effects automatically.
	return s.Fail(ctx, lease, "workflow_execution_failed", "Workflow execution failed", false, 0)
}

type DeviceExecution struct {
	Lease   Lease                      `json:"lease"`
	Version automation.WorkflowVersion `json:"version"`
}

// Identity parameters must come from authenticated device credentials, never
// from a request body. Recheck current membership before exposing a definition.
func (s *Service) ClaimDevice(ctx context.Context, userID, workspaceID, deviceID string) (DeviceExecution, error) {
	for _, permission := range []members.Permission{members.PermissionTaskOperate, members.PermissionInstanceOperate, members.PermissionWorkflowRead} {
		if err := s.authorizer.Require(ctx, workspaceID, userID, permission); err != nil {
			return DeviceExecution{}, err
		}
	}
	repository, ok := s.repository.(DeviceRepository)
	if !ok {
		return DeviceExecution{}, errors.New("device task repository unavailable")
	}
	lease, err := repository.ClaimDeviceWorkflow(ctx, workspaceID, deviceID, 30*time.Minute, s.now().UTC())
	if err != nil {
		return DeviceExecution{}, err
	}
	version, err := repository.FindWorkflowVersionByID(ctx, workspaceID, lease.Task.WorkflowID, lease.Task.WorkflowVersionID)
	if err != nil {
		return DeviceExecution{}, err
	}
	return DeviceExecution{Lease: lease, Version: version}, nil
}
