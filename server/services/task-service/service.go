package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound      = errors.New("task not found")
	ErrNoWork        = errors.New("no task is ready")
	ErrStateConflict = errors.New("task state conflict")
	ErrQuotaExceeded = errors.New("task quota exceeded")
)

var allowedTaskTypes = map[string]struct{}{
	"workflow.execute":     {},
	"profile.sync":         {},
	"proxy.health_check":   {},
	"notification.deliver": {},
	"analytics.rollup":     {},
	"system.healthcheck":   {},
}

type Task struct {
	ID                string                 `json:"id"`
	WorkspaceID       string                 `json:"workspaceId"`
	TaskType          string                 `json:"taskType"`
	WorkflowID        string                 `json:"workflowId,omitempty"`
	WorkflowVersionID string                 `json:"workflowVersionId,omitempty"`
	RequestedBy       string                 `json:"requestedBy,omitempty"`
	IdempotencyKey    string                 `json:"idempotencyKey"`
	Status            string                 `json:"status"`
	Priority          int                    `json:"priority"`
	Payload           map[string]interface{} `json:"payload"`
	RetryLimit        int                    `json:"retryLimit"`
	AvailableAt       time.Time              `json:"availableAt"`
	LeaseOwner        string                 `json:"leaseOwner,omitempty"`
	LeaseExpiresAt    *time.Time             `json:"leaseExpiresAt,omitempty"`
	CreatedAt         time.Time              `json:"createdAt"`
	UpdatedAt         time.Time              `json:"updatedAt"`
	CompletedAt       *time.Time             `json:"completedAt,omitempty"`
	ErrorCode         string                 `json:"errorCode,omitempty"`
	ErrorMessage      string                 `json:"errorMessage,omitempty"`
}

type Lease struct {
	Task      Task      `json:"task"`
	RunID     string    `json:"runId"`
	AttemptID string    `json:"attemptId"`
	Attempt   int       `json:"attempt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Repository interface {
	CreateTask(context.Context, Task) (Task, error)
	FindTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, int) ([]Task, error)
	CancelTask(context.Context, string, string, time.Time) (Task, error)
	ClaimNextTask(context.Context, string, []string, time.Duration, time.Time) (Lease, error)
	StartTask(context.Context, Lease, time.Time) error
	CompleteTask(context.Context, Lease, map[string]interface{}, time.Time) error
	FailTask(context.Context, Lease, string, string, bool, time.Time, time.Time) error
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
}

type EnqueueInput struct {
	TaskType          string                 `json:"taskType"`
	WorkflowID        string                 `json:"workflowId,omitempty"`
	WorkflowVersionID string                 `json:"workflowVersionId,omitempty"`
	Priority          int                    `json:"priority,omitempty"`
	Payload           map[string]interface{} `json:"payload,omitempty"`
	RetryLimit        *int                   `json:"retryLimit,omitempty"`
	AvailableAt       *time.Time             `json:"availableAt,omitempty"`
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now}
}

func (s *Service) Enqueue(ctx context.Context, actorID, workspaceID, idempotencyKey string, input EnqueueInput) (Task, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionTaskOperate); err != nil {
		return Task{}, err
	}
	taskType := strings.ToLower(strings.TrimSpace(input.TaskType))
	if _, ok := allowedTaskTypes[taskType]; !ok {
		return Task{}, errors.New("taskType is invalid")
	}
	if taskType == "workflow.execute" {
		if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceOperate); err != nil {
			return Task{}, err
		}
		if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
			return Task{}, err
		}
		instanceID, ok := input.Payload["instanceId"].(string)
		if !ok || len(input.Payload) != 1 {
			return Task{}, errors.New("workflow payload must contain only instanceId")
		}
		for _, value := range []string{input.WorkflowID, input.WorkflowVersionID, instanceID} {
			if _, err := uuid.Parse(value); err != nil {
				return Task{}, errors.New("workflowId, workflowVersionId and instanceId must be UUIDs")
			}
		}
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		return Task{}, errors.New("Idempotency-Key is required")
	}
	if input.Priority < -100 || input.Priority > 100 {
		return Task{}, errors.New("priority must be between -100 and 100")
	}
	retryLimit := 3
	// Workflow steps can submit forms or otherwise have non-idempotent effects.
	if taskType == "workflow.execute" {
		retryLimit = 0
	}
	if input.RetryLimit != nil {
		retryLimit = *input.RetryLimit
	}
	if retryLimit < 0 || retryLimit > 10 {
		return Task{}, errors.New("retryLimit must be between 0 and 10")
	}
	now := s.now().UTC()
	availableAt := now
	if input.AvailableAt != nil {
		availableAt = input.AvailableAt.UTC()
		if availableAt.After(now.Add(365 * 24 * time.Hour)) {
			return Task{}, errors.New("availableAt must be within one year")
		}
	}
	payload := input.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	task := Task{
		ID: uuid.NewString(), WorkspaceID: workspaceID, TaskType: taskType,
		WorkflowID: strings.TrimSpace(input.WorkflowID), WorkflowVersionID: strings.TrimSpace(input.WorkflowVersionID),
		RequestedBy: actorID, IdempotencyKey: idempotencyKey, Status: "queued",
		Priority: input.Priority, Payload: payload, RetryLimit: retryLimit,
		AvailableAt: availableAt, CreatedAt: now, UpdatedAt: now,
	}
	return s.repository.CreateTask(ctx, task)
}

// SameWorkflowRequest prevents an idempotency key from silently selecting a
// different task request. AvailableAt is excluded because its default is now.
func SameWorkflowRequest(a, b Task) bool {
	left, leftErr := json.Marshal(a.Payload)
	right, rightErr := json.Marshal(b.Payload)
	return leftErr == nil && rightErr == nil &&
		a.TaskType == b.TaskType &&
		a.WorkflowID == b.WorkflowID &&
		a.WorkflowVersionID == b.WorkflowVersionID &&
		a.Priority == b.Priority &&
		a.RetryLimit == b.RetryLimit &&
		string(left) == string(right)
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string, limit int) ([]Task, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.repository.ListTasks(ctx, workspaceID, limit)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, taskID string) (Task, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkflowRead); err != nil {
		return Task{}, err
	}
	return s.repository.FindTask(ctx, workspaceID, taskID)
}

func (s *Service) Cancel(ctx context.Context, actorID, workspaceID, taskID string) (Task, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionTaskOperate); err != nil {
		return Task{}, err
	}
	return s.repository.CancelTask(ctx, workspaceID, taskID, s.now().UTC())
}

func (s *Service) Claim(ctx context.Context, workerID string, supportedTypes []string, leaseTTL time.Duration) (Lease, error) {
	if strings.TrimSpace(workerID) == "" || len(supportedTypes) == 0 || leaseTTL <= 0 {
		return Lease{}, errors.New("worker claim configuration is invalid")
	}
	return s.repository.ClaimNextTask(ctx, workerID, supportedTypes, leaseTTL, s.now().UTC())
}

func (s *Service) Start(ctx context.Context, lease Lease) error {
	return s.repository.StartTask(ctx, lease, s.now().UTC())
}

func (s *Service) Complete(ctx context.Context, lease Lease, result map[string]interface{}) error {
	if result == nil {
		result = map[string]interface{}{}
	}
	return s.repository.CompleteTask(ctx, lease, result, s.now().UTC())
}

func (s *Service) Fail(ctx context.Context, lease Lease, code, message string, retryable bool, retryDelay time.Duration) error {
	now := s.now().UTC()
	if retryDelay < 0 {
		retryDelay = 0
	}
	return s.repository.FailTask(
		ctx, lease, strings.TrimSpace(code), strings.TrimSpace(message), retryable,
		now.Add(retryDelay), now,
	)
}
