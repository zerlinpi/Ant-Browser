package scheduleservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

var (
	ErrNotFound        = errors.New("schedule not found")
	ErrVersionConflict = errors.New("schedule version conflict")
	ErrStateConflict   = errors.New("schedule state conflict")
)

type Schedule struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspaceId"`
	WorkflowID        string     `json:"workflowId"`
	WorkflowVersionID string     `json:"workflowVersionId"`
	InstanceID        string     `json:"instanceId"`
	CronExpression    string     `json:"cronExpression"`
	Timezone          string     `json:"timezone"`
	Enabled           bool       `json:"enabled"`
	Status            string     `json:"status"`
	NextRunAt         *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt         *time.Time `json:"lastRunAt,omitempty"`
	LastError         string     `json:"lastError,omitempty"`
	LeaseOwner        string     `json:"leaseOwner,omitempty"`
	LeaseExpiresAt    *time.Time `json:"leaseExpiresAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	Version           int64      `json:"version"`
}

type CreateInput struct {
	WorkflowID        string `json:"workflowId"`
	WorkflowVersionID string `json:"workflowVersionId"`
	InstanceID        string `json:"instanceId"`
	CronExpression    string `json:"cronExpression"`
	Timezone          string `json:"timezone"`
}
type UpdateInput struct {
	CronExpression  string `json:"cronExpression"`
	Timezone        string `json:"timezone"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type Repository interface {
	CreateSchedule(context.Context, Schedule) (Schedule, error)
	FindSchedule(context.Context, string, string) (Schedule, error)
	ListSchedules(context.Context, string) ([]Schedule, error)
	UpdateSchedule(context.Context, Schedule, int64, time.Time) (Schedule, error)
	DeleteSchedule(context.Context, string, string) error
	SetScheduleEnabled(context.Context, string, string, bool, time.Time) (Schedule, error)
}
type Dispatch struct {
	Schedule Schedule
	Task     taskservice.Task
}

// SchedulerRepository is implemented by production and memory stores. A
// dispatch claims the schedule and inserts its task in one transaction.
type SchedulerRepository interface {
	ClaimDueSchedules(context.Context, string, time.Time, int) ([]Dispatch, error)
}
type WorkflowLookup interface {
	FindWorkflow(context.Context, string, string) (automationservice.Workflow, error)
	FindWorkflowVersionByID(context.Context, string, string, string) (automationservice.WorkflowVersion, error)
}
type InstanceLookup interface {
	FindInstance(context.Context, string, string) (browserinstanceservice.BrowserInstance, error)
}
type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	workflows  WorkflowLookup
	instances  InstanceLookup
	authorizer Authorizer
	now        func() time.Time
}

func New(repository Repository, workflows WorkflowLookup, instances InstanceLookup, authorizer Authorizer) *Service {
	return &Service{repository: repository, workflows: workflows, instances: instances, authorizer: authorizer, now: time.Now}
}

func (s *Service) validate(ctx context.Context, actor, workspace string, input CreateInput) (CreateInput, *time.Location, error) {
	input.WorkflowID = strings.TrimSpace(input.WorkflowID)
	input.WorkflowVersionID = strings.TrimSpace(input.WorkflowVersionID)
	input.InstanceID = strings.TrimSpace(input.InstanceID)
	for name, value := range map[string]string{"workflowId": input.WorkflowID, "workflowVersionId": input.WorkflowVersionID, "instanceId": input.InstanceID} {
		if _, err := uuid.Parse(value); err != nil {
			return input, nil, errors.New(name + " must be a UUID")
		}
	}
	input.CronExpression = strings.TrimSpace(input.CronExpression)
	if _, err := ParseCron(input.CronExpression); err != nil {
		return input, nil, err
	}
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	location, err := time.LoadLocation(input.Timezone)
	if err != nil {
		return input, nil, errors.New("timezone must be a valid IANA timezone")
	}
	if s.workflows != nil {
		workflow, err := s.workflows.FindWorkflow(ctx, workspace, input.WorkflowID)
		if err != nil {
			return input, nil, err
		}
		version, err := s.workflows.FindWorkflowVersionByID(ctx, workspace, input.WorkflowID, input.WorkflowVersionID)
		if err != nil {
			return input, nil, err
		}
		if workflow.Status != "published" || workflow.PublishedVersionID != version.ID {
			return input, nil, ErrStateConflict
		}
		if version.Definition.Engine != "playwright" && version.Definition.Engine != "cdp" {
			return input, nil, ErrStateConflict
		}
	}
	if s.instances != nil {
		instance, err := s.instances.FindInstance(ctx, workspace, input.InstanceID)
		if err != nil {
			return input, nil, err
		}
		if instance.DeletedAt != nil {
			return input, nil, ErrStateConflict
		}
	}
	return input, location, nil
}
func (s *Service) authorize(ctx context.Context, actor, workspace string, permission memberservice.Permission) error {
	if s.authorizer == nil {
		return nil
	}
	return s.authorizer.Require(ctx, workspace, actor, permission)
}

func (s *Service) Create(ctx context.Context, actor, workspace string, input CreateInput) (Schedule, error) {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowManage); err != nil {
		return Schedule{}, err
	}
	input, loc, err := s.validate(ctx, actor, workspace, input)
	if err != nil {
		return Schedule{}, err
	}
	now := s.now().UTC()
	cron, _ := ParseCron(input.CronExpression)
	next, err := cron.Next(now, loc)
	if err != nil {
		return Schedule{}, err
	}
	item := Schedule{ID: uuid.NewString(), WorkspaceID: workspace, WorkflowID: input.WorkflowID, WorkflowVersionID: input.WorkflowVersionID, InstanceID: input.InstanceID, CronExpression: input.CronExpression, Timezone: input.Timezone, Enabled: true, Status: "active", NextRunAt: &next, CreatedAt: now, UpdatedAt: now, Version: 1}
	return s.repository.CreateSchedule(ctx, item)
}
func (s *Service) List(ctx context.Context, actor, workspace string) ([]Schedule, error) {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowRead); err != nil {
		return nil, err
	}
	return s.repository.ListSchedules(ctx, workspace)
}
func (s *Service) Get(ctx context.Context, actor, workspace, id string) (Schedule, error) {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowRead); err != nil {
		return Schedule{}, err
	}
	return s.repository.FindSchedule(ctx, workspace, id)
}
func (s *Service) Update(ctx context.Context, actor, workspace, id string, input UpdateInput) (Schedule, error) {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowManage); err != nil {
		return Schedule{}, err
	}
	current, err := s.repository.FindSchedule(ctx, workspace, id)
	if err != nil {
		return Schedule{}, err
	}
	if input.ExpectedVersion < 1 {
		return Schedule{}, errors.New("expectedVersion must be positive")
	}
	normalized, loc, err := s.validate(ctx, actor, workspace, CreateInput{WorkflowID: current.WorkflowID, WorkflowVersionID: current.WorkflowVersionID, InstanceID: current.InstanceID, CronExpression: input.CronExpression, Timezone: input.Timezone})
	if err != nil {
		return Schedule{}, err
	}
	now := s.now().UTC()
	cron, _ := ParseCron(normalized.CronExpression)
	next, err := cron.Next(now, loc)
	if err != nil {
		return Schedule{}, err
	}
	current.CronExpression = normalized.CronExpression
	current.Timezone = normalized.Timezone
	current.NextRunAt = &next
	current.UpdatedAt = now
	return s.repository.UpdateSchedule(ctx, current, input.ExpectedVersion, now)
}
func (s *Service) Delete(ctx context.Context, actor, workspace, id string) error {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowManage); err != nil {
		return err
	}
	return s.repository.DeleteSchedule(ctx, workspace, id)
}
func (s *Service) Enable(ctx context.Context, actor, workspace, id string) (Schedule, error) {
	return s.setEnabled(ctx, actor, workspace, id, true)
}
func (s *Service) Disable(ctx context.Context, actor, workspace, id string) (Schedule, error) {
	return s.setEnabled(ctx, actor, workspace, id, false)
}
func (s *Service) setEnabled(ctx context.Context, actor, workspace, id string, enabled bool) (Schedule, error) {
	if err := s.authorize(ctx, actor, workspace, memberservice.PermissionWorkflowManage); err != nil {
		return Schedule{}, err
	}
	return s.repository.SetScheduleEnabled(ctx, workspace, id, enabled, s.now().UTC())
}
