// Package batchservice coordinates bounded, tenant-scoped calls to the
// existing domain services. A batch is deliberately not a transaction: every
// item has its own result and an item failure never rolls back successful
// siblings.
package batchservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

const MaxBatchSize = 100

const (
	OperationInstanceCreate  = "instance.create"
	OperationInstanceStart   = "instance.start"
	OperationInstanceStop    = "instance.stop"
	OperationAccountBind     = "account.bind"
	OperationProxyAssign     = "proxy.assign"
	OperationWorkflowExecute = "workflow.execute"
)

var (
	ErrInvalidScope           = errors.New("actorId and workspaceId are required")
	ErrEmptyBatch             = errors.New("batch must contain at least one item")
	ErrBatchTooLarge          = errors.New("batch exceeds maximum size")
	ErrIdempotencyKeyRequired = errors.New("batch idempotency key is required")
	ErrInvalidIdempotencyKey  = errors.New("batch idempotency key is invalid")
	ErrInvalidAction          = errors.New("batch command action must be start or stop")
	ErrDuplicateItem          = errors.New("duplicate batch item")
	ErrDependencyUnavailable  = errors.New("batch service dependency is unavailable")
)

type ItemStatus string

const (
	ItemSucceeded ItemStatus = "succeeded"
	ItemFailed    ItemStatus = "failed"
	ItemCancelled ItemStatus = "cancelled"
)

const (
	ErrorCodeInvalidItem      = "invalid_item"
	ErrorCodeDuplicateItem    = "duplicate_item"
	ErrorCodeNotFound         = "not_found"
	ErrorCodeForbidden        = "forbidden"
	ErrorCodeVersionConflict  = "version_conflict"
	ErrorCodeStateConflict    = "state_conflict"
	ErrorCodeUnsupported      = "unsupported"
	ErrorCodeCancelled        = "cancelled"
	ErrorCodeDeadlineExceeded = "deadline_exceeded"
	ErrorCodeOperationFailed  = "operation_failed"
	// ErrorCodeNameConflict: the item's name (or account identifier) is
	// already used by a live resource, possibly an earlier item of the batch.
	ErrorCodeNameConflict = "name_conflict"
)

// ItemError is safe for structured transport while retaining the original
// error for errors.Is/errors.As and service-side logging.
type ItemError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Cause     error  `json:"-"`
}

func (e *ItemError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ItemError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type Summary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// ItemResult preserves request order. IdempotencyKey is a deterministic
// per-item key derived from workspace, batch key, operation, and normalized
// item semantics. It is passed to downstream services that support keyed
// idempotency (instance commands and task enqueueing).
type ItemResult[T any] struct {
	Index          int        `json:"index"`
	ItemID         string     `json:"itemId,omitempty"`
	IdempotencyKey string     `json:"idempotencyKey,omitempty"`
	Status         ItemStatus `json:"status"`
	Value          *T         `json:"value,omitempty"`
	Error          *ItemError `json:"error,omitempty"`
}

// BatchResult always reports Atomic=false. ReplaySafe is false for instance
// creation because the existing Create contract has no idempotency input; the
// coordinator intentionally does not pretend an in-memory cache is durable
// idempotency. Other operations are protected by downstream keys or natural
// upsert keys.
type BatchResult[T any] struct {
	Operation  string          `json:"operation"`
	Atomic     bool            `json:"atomic"`
	ReplaySafe bool            `json:"replaySafe"`
	Summary    Summary         `json:"summary"`
	Items      []ItemResult[T] `json:"items"`
}

type CommandValue struct {
	Command  browserinstanceservice.Command         `json:"command"`
	Instance browserinstanceservice.BrowserInstance `json:"instance"`
}

// InstanceOperations is the only browser-instance-service surface needed by
// this coordinator.
type InstanceOperations interface {
	Create(context.Context, string, string, browserinstanceservice.CreateInput) (browserinstanceservice.BrowserInstance, error)
	RequestCommand(context.Context, string, string, string, string, browserinstanceservice.CommandInput) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error)
}

// AccountBindings is the only account-service surface needed here.
type AccountBindings interface {
	Bind(context.Context, string, string, string, string, string) (accountservice.AccountBinding, error)
}

// ProxyAssignments is the only proxy-service surface needed here.
type ProxyAssignments interface {
	Assign(context.Context, string, string, string, string, string) (proxyservice.Assignment, error)
}

// TaskEnqueuer is the only task-service surface needed here. Published
// workflow/version and workspace ownership remain enforced by task storage.
type TaskEnqueuer interface {
	Enqueue(context.Context, string, string, string, taskservice.EnqueueInput) (taskservice.Task, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	instances  InstanceOperations
	accounts   AccountBindings
	proxies    ProxyAssignments
	tasks      TaskEnqueuer
	authorizer Authorizer
}

func New(instances InstanceOperations, accounts AccountBindings, proxies ProxyAssignments, tasks TaskEnqueuer, authorizer Authorizer) *Service {
	return &Service{instances: instances, accounts: accounts, proxies: proxies, tasks: tasks, authorizer: authorizer}
}

// Compile-time assertions keep the coordinator interfaces aligned with the
// domain-service contracts without depending on their repositories.
var (
	_ InstanceOperations = (*browserinstanceservice.Service)(nil)
	_ AccountBindings    = (*accountservice.Service)(nil)
	_ ProxyAssignments   = (*proxyservice.Service)(nil)
	_ TaskEnqueuer       = (*taskservice.Service)(nil)
)

type CreateInstanceItem struct {
	// ItemID is a caller-stable identifier. It is required because a new
	// instance does not have a resource ID before creation.
	ItemID string                             `json:"itemId"`
	Input  browserinstanceservice.CreateInput `json:"input"`
}

type CreateInstancesInput struct {
	IdempotencyKey string               `json:"idempotencyKey"`
	Items          []CreateInstanceItem `json:"items"`
}

type InstanceCommandItem struct {
	ItemID          string                 `json:"itemId,omitempty"`
	InstanceID      string                 `json:"instanceId"`
	ExpectedVersion int64                  `json:"expectedVersion"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
}

type InstanceCommandsInput struct {
	IdempotencyKey string                `json:"idempotencyKey"`
	Action         string                `json:"action"`
	Items          []InstanceCommandItem `json:"items"`
}

type AccountBindingItem struct {
	ItemID      string `json:"itemId,omitempty"`
	AccountID   string `json:"accountId"`
	BindingType string `json:"bindingType"`
	TargetID    string `json:"targetId"`
}

type BindAccountsInput struct {
	IdempotencyKey string               `json:"idempotencyKey"`
	Items          []AccountBindingItem `json:"items"`
}

type ProxyAssignmentItem struct {
	ItemID     string `json:"itemId,omitempty"`
	ProxyID    string `json:"proxyId"`
	TargetID   string `json:"targetId"`
	TargetType string `json:"targetType"`
}

type AssignProxiesInput struct {
	IdempotencyKey string                `json:"idempotencyKey"`
	Items          []ProxyAssignmentItem `json:"items"`
}

type WorkflowExecutionItem struct {
	ItemID            string     `json:"itemId,omitempty"`
	WorkflowID        string     `json:"workflowId"`
	WorkflowVersionID string     `json:"workflowVersionId"`
	InstanceID        string     `json:"instanceId"`
	Priority          int        `json:"priority,omitempty"`
	RetryLimit        *int       `json:"retryLimit,omitempty"`
	AvailableAt       *time.Time `json:"availableAt,omitempty"`
}

type ExecuteWorkflowsInput struct {
	IdempotencyKey string                  `json:"idempotencyKey"`
	Items          []WorkflowExecutionItem `json:"items"`
}

func (s *Service) CreateInstances(ctx context.Context, actorID, workspaceID string, input CreateInstancesInput) (BatchResult[browserinstanceservice.BrowserInstance], error) {
	result := newBatchResult[browserinstanceservice.BrowserInstance](OperationInstanceCreate, false, len(input.Items))
	actorID, workspaceID, batchKey, err := normalizeRequest(ctx, actorID, workspaceID, input.IdempotencyKey, len(input.Items))
	if err != nil {
		return result, err
	}
	if s == nil || s.instances == nil {
		return result, unavailable("browser instance operations")
	}
	if err := s.authorize(ctx, actorID, workspaceID, memberservice.PermissionInstanceCreate); err != nil {
		return result, err
	}

	items := make([]workItem[browserinstanceservice.BrowserInstance], len(input.Items))
	for index := range input.Items {
		item := input.Items[index]
		itemID, itemErr := normalizeRequired(item.ItemID, "itemId")
		items[index] = workItem[browserinstanceservice.BrowserInstance]{itemID: itemID, duplicateIdentity: itemID}
		if itemErr != nil {
			items[index].failure = invalidFailure(itemErr)
			continue
		}
		items[index].idempotencyKey = deriveItemKey(workspaceID, batchKey, OperationInstanceCreate, itemID)
		items[index].execute = func(callCtx context.Context) (browserinstanceservice.BrowserInstance, error) {
			return s.instances.Create(callCtx, actorID, workspaceID, item.Input)
		}
	}
	rejectDuplicates(items)
	return executeBatch(ctx, result, items)
}

// StartInstances submits independent instance.start commands.
func (s *Service) StartInstances(ctx context.Context, actorID, workspaceID string, input InstanceCommandsInput) (BatchResult[CommandValue], error) {
	input.Action = OperationInstanceStart
	return s.CommandInstances(ctx, actorID, workspaceID, input)
}

// StopInstances submits independent instance.stop commands.
func (s *Service) StopInstances(ctx context.Context, actorID, workspaceID string, input InstanceCommandsInput) (BatchResult[CommandValue], error) {
	input.Action = OperationInstanceStop
	return s.CommandInstances(ctx, actorID, workspaceID, input)
}

func (s *Service) CommandInstances(ctx context.Context, actorID, workspaceID string, input InstanceCommandsInput) (BatchResult[CommandValue], error) {
	action, actionErr := normalizeCommandAction(input.Action)
	operation := action
	if operation == "" {
		operation = strings.ToLower(strings.TrimSpace(input.Action))
	}
	result := newBatchResult[CommandValue](operation, true, len(input.Items))
	actorID, workspaceID, batchKey, err := normalizeRequest(ctx, actorID, workspaceID, input.IdempotencyKey, len(input.Items))
	if err != nil {
		return result, err
	}
	if actionErr != nil {
		return result, actionErr
	}
	result.Operation = action
	if s == nil || s.instances == nil {
		return result, unavailable("browser instance operations")
	}
	if err := s.authorize(ctx, actorID, workspaceID, memberservice.PermissionInstanceOperate); err != nil {
		return result, err
	}

	items := make([]workItem[CommandValue], len(input.Items))
	for index := range input.Items {
		item := input.Items[index]
		item.InstanceID = strings.TrimSpace(item.InstanceID)
		itemID, itemErr := normalizeOptional(item.ItemID, item.InstanceID)
		items[index] = workItem[CommandValue]{itemID: itemID, duplicateIdentity: item.InstanceID}
		switch {
		case item.InstanceID == "":
			items[index].failure = invalidFailure(errors.New("instanceId is required"))
			continue
		case itemErr != nil:
			items[index].failure = invalidFailure(itemErr)
			continue
		case item.ExpectedVersion < 1:
			items[index].failure = invalidFailure(errors.New("expectedVersion must be positive"))
			continue
		}
		if item.Payload == nil {
			item.Payload = map[string]interface{}{}
		}
		canonical, canonicalErr := canonicalJSON(struct {
			Action          string                 `json:"action"`
			InstanceID      string                 `json:"instanceId"`
			ExpectedVersion int64                  `json:"expectedVersion"`
			Payload         map[string]interface{} `json:"payload"`
		}{action, item.InstanceID, item.ExpectedVersion, item.Payload})
		if canonicalErr != nil {
			items[index].failure = invalidFailure(errors.New("payload must be JSON serializable"))
			continue
		}
		items[index].idempotencyKey = deriveItemKey(workspaceID, batchKey, action, canonical)
		items[index].execute = func(callCtx context.Context) (CommandValue, error) {
			command, instance, callErr := s.instances.RequestCommand(callCtx, actorID, workspaceID, item.InstanceID, items[index].idempotencyKey, browserinstanceservice.CommandInput{
				Action: action, ExpectedVersion: item.ExpectedVersion, Payload: item.Payload,
			})
			return CommandValue{Command: command, Instance: instance}, callErr
		}
	}
	rejectDuplicates(items)
	return executeBatch(ctx, result, items)
}

func (s *Service) BindAccounts(ctx context.Context, actorID, workspaceID string, input BindAccountsInput) (BatchResult[accountservice.AccountBinding], error) {
	result := newBatchResult[accountservice.AccountBinding](OperationAccountBind, true, len(input.Items))
	actorID, workspaceID, batchKey, err := normalizeRequest(ctx, actorID, workspaceID, input.IdempotencyKey, len(input.Items))
	if err != nil {
		return result, err
	}
	if s == nil || s.accounts == nil {
		return result, unavailable("account bindings")
	}
	if err := s.authorize(ctx, actorID, workspaceID, memberservice.PermissionAccountManage); err != nil {
		return result, err
	}

	items := make([]workItem[accountservice.AccountBinding], len(input.Items))
	for index := range input.Items {
		item := input.Items[index]
		item.AccountID = strings.TrimSpace(item.AccountID)
		item.BindingType = strings.ToLower(strings.TrimSpace(item.BindingType))
		item.TargetID = strings.TrimSpace(item.TargetID)
		identity := item.AccountID + "\x00" + item.BindingType
		fallbackID := item.AccountID + ":" + item.BindingType
		itemID, itemErr := normalizeOptional(item.ItemID, fallbackID)
		items[index] = workItem[accountservice.AccountBinding]{itemID: itemID, duplicateIdentity: identity}
		switch {
		case item.AccountID == "" || item.TargetID == "":
			items[index].failure = invalidFailure(errors.New("accountId and targetId are required"))
			continue
		case itemErr != nil:
			items[index].failure = invalidFailure(itemErr)
			continue
		case !validAccountBindingType(item.BindingType):
			items[index].failure = invalidFailure(errors.New("bindingType must be profile, browser_instance, or proxy"))
			continue
		}
		canonical, _ := canonicalJSON([]string{item.AccountID, item.BindingType, item.TargetID})
		items[index].idempotencyKey = deriveItemKey(workspaceID, batchKey, OperationAccountBind, canonical)
		items[index].execute = func(callCtx context.Context) (accountservice.AccountBinding, error) {
			return s.accounts.Bind(callCtx, actorID, workspaceID, item.AccountID, item.BindingType, item.TargetID)
		}
	}
	rejectDuplicates(items)
	return executeBatch(ctx, result, items)
}

func (s *Service) AssignProxies(ctx context.Context, actorID, workspaceID string, input AssignProxiesInput) (BatchResult[proxyservice.Assignment], error) {
	result := newBatchResult[proxyservice.Assignment](OperationProxyAssign, true, len(input.Items))
	actorID, workspaceID, batchKey, err := normalizeRequest(ctx, actorID, workspaceID, input.IdempotencyKey, len(input.Items))
	if err != nil {
		return result, err
	}
	if s == nil || s.proxies == nil {
		return result, unavailable("proxy assignments")
	}
	if err := s.authorize(ctx, actorID, workspaceID, memberservice.PermissionProxyManage); err != nil {
		return result, err
	}

	items := make([]workItem[proxyservice.Assignment], len(input.Items))
	for index := range input.Items {
		item := input.Items[index]
		item.ProxyID = strings.TrimSpace(item.ProxyID)
		item.TargetID = strings.TrimSpace(item.TargetID)
		item.TargetType = strings.ToLower(strings.TrimSpace(item.TargetType))
		identity := item.TargetType + "\x00" + item.TargetID
		fallbackID := item.TargetType + ":" + item.TargetID
		itemID, itemErr := normalizeOptional(item.ItemID, fallbackID)
		items[index] = workItem[proxyservice.Assignment]{itemID: itemID, duplicateIdentity: identity}
		switch {
		case item.ProxyID == "" || item.TargetID == "":
			items[index].failure = invalidFailure(errors.New("proxyId and targetId are required"))
			continue
		case itemErr != nil:
			items[index].failure = invalidFailure(itemErr)
			continue
		case !validProxyTargetType(item.TargetType):
			items[index].failure = invalidFailure(errors.New("targetType must be account, profile, or browser_instance"))
			continue
		}
		canonical, _ := canonicalJSON([]string{item.ProxyID, item.TargetType, item.TargetID})
		items[index].idempotencyKey = deriveItemKey(workspaceID, batchKey, OperationProxyAssign, canonical)
		items[index].execute = func(callCtx context.Context) (proxyservice.Assignment, error) {
			return s.proxies.Assign(callCtx, actorID, workspaceID, item.ProxyID, item.TargetID, item.TargetType)
		}
	}
	rejectDuplicates(items)
	return executeBatch(ctx, result, items)
}

// BindProxies is an API-friendly alias for AssignProxies.
func (s *Service) BindProxies(ctx context.Context, actorID, workspaceID string, input AssignProxiesInput) (BatchResult[proxyservice.Assignment], error) {
	return s.AssignProxies(ctx, actorID, workspaceID, input)
}

func (s *Service) ExecuteWorkflows(ctx context.Context, actorID, workspaceID string, input ExecuteWorkflowsInput) (BatchResult[taskservice.Task], error) {
	result := newBatchResult[taskservice.Task](OperationWorkflowExecute, true, len(input.Items))
	actorID, workspaceID, batchKey, err := normalizeRequest(ctx, actorID, workspaceID, input.IdempotencyKey, len(input.Items))
	if err != nil {
		return result, err
	}
	if s == nil || s.tasks == nil {
		return result, unavailable("task enqueueing")
	}
	if err := s.authorize(ctx, actorID, workspaceID,
		memberservice.PermissionTaskOperate,
		memberservice.PermissionInstanceOperate,
		memberservice.PermissionWorkflowRead,
	); err != nil {
		return result, err
	}

	items := make([]workItem[taskservice.Task], len(input.Items))
	for index := range input.Items {
		item := input.Items[index]
		item.WorkflowID = strings.TrimSpace(item.WorkflowID)
		item.WorkflowVersionID = strings.TrimSpace(item.WorkflowVersionID)
		item.InstanceID = strings.TrimSpace(item.InstanceID)
		identity := item.WorkflowID + "\x00" + item.WorkflowVersionID + "\x00" + item.InstanceID
		fallbackID := item.WorkflowID + ":" + item.WorkflowVersionID + ":" + item.InstanceID
		itemID, itemErr := normalizeOptional(item.ItemID, fallbackID)
		items[index] = workItem[taskservice.Task]{itemID: itemID, duplicateIdentity: identity}
		retryLimit := 0
		if item.RetryLimit != nil {
			retryLimit = *item.RetryLimit
		}
		switch {
		case item.WorkflowID == "" || item.WorkflowVersionID == "" || item.InstanceID == "":
			items[index].failure = invalidFailure(errors.New("workflowId, workflowVersionId and instanceId are required"))
			continue
		case itemErr != nil:
			items[index].failure = invalidFailure(itemErr)
			continue
		case item.Priority < -100 || item.Priority > 100:
			items[index].failure = invalidFailure(errors.New("priority must be between -100 and 100"))
			continue
		case retryLimit < 0 || retryLimit > 10:
			items[index].failure = invalidFailure(errors.New("retryLimit must be between 0 and 10"))
			continue
		}
		// AvailableAt is intentionally excluded: task-service treats a replay
		// with the same execution semantics as the same request even when the
		// original request used the default current time.
		canonical, _ := canonicalJSON(struct {
			WorkflowID        string `json:"workflowId"`
			WorkflowVersionID string `json:"workflowVersionId"`
			InstanceID        string `json:"instanceId"`
			Priority          int    `json:"priority"`
			RetryLimit        int    `json:"retryLimit"`
		}{item.WorkflowID, item.WorkflowVersionID, item.InstanceID, item.Priority, retryLimit})
		items[index].idempotencyKey = deriveItemKey(workspaceID, batchKey, OperationWorkflowExecute, canonical)
		items[index].execute = func(callCtx context.Context) (taskservice.Task, error) {
			return s.tasks.Enqueue(callCtx, actorID, workspaceID, items[index].idempotencyKey, taskservice.EnqueueInput{
				TaskType: OperationWorkflowExecute, WorkflowID: item.WorkflowID, WorkflowVersionID: item.WorkflowVersionID,
				Priority: item.Priority, Payload: map[string]interface{}{"instanceId": item.InstanceID},
				RetryLimit: item.RetryLimit, AvailableAt: item.AvailableAt,
			})
		}
	}
	rejectDuplicates(items)
	return executeBatch(ctx, result, items)
}

// SubmitWorkflowExecutions is an explicit alias for ExecuteWorkflows.
func (s *Service) SubmitWorkflowExecutions(ctx context.Context, actorID, workspaceID string, input ExecuteWorkflowsInput) (BatchResult[taskservice.Task], error) {
	return s.ExecuteWorkflows(ctx, actorID, workspaceID, input)
}

type workItem[T any] struct {
	itemID            string
	idempotencyKey    string
	duplicateIdentity string
	failure           *ItemError
	execute           func(context.Context) (T, error)
}

func newBatchResult[T any](operation string, replaySafe bool, total int) BatchResult[T] {
	return BatchResult[T]{
		Operation: operation, Atomic: false, ReplaySafe: replaySafe,
		Summary: Summary{Total: total}, Items: make([]ItemResult[T], total),
	}
}

func executeBatch[T any](ctx context.Context, result BatchResult[T], work []workItem[T]) (BatchResult[T], error) {
	for index := range work {
		result.Items[index] = ItemResult[T]{
			Index: index, ItemID: work[index].itemID,
			IdempotencyKey: work[index].idempotencyKey,
		}
	}
	for index := range work {
		if err := ctx.Err(); err != nil {
			cancelItems(result.Items[index:], err)
			result.Summary = summarize(result.Items)
			return result, err
		}
		if work[index].failure != nil {
			result.Items[index].Status = ItemFailed
			result.Items[index].Error = work[index].failure
			continue
		}
		value, err := work[index].execute(ctx)
		if err == nil {
			result.Items[index].Status = ItemSucceeded
			result.Items[index].Value = &value
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			cancelItems(result.Items[index:], err)
			result.Summary = summarize(result.Items)
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, err
		}
		result.Items[index].Status = ItemFailed
		result.Items[index].Error = classifyFailure(err)
	}
	result.Summary = summarize(result.Items)
	return result, nil
}

func cancelItems[T any](items []ItemResult[T], cause error) {
	for index := range items {
		items[index].Status = ItemCancelled
		items[index].Value = nil
		items[index].Error = cancellationFailure(cause)
	}
}

func summarize[T any](items []ItemResult[T]) Summary {
	summary := Summary{Total: len(items)}
	for _, item := range items {
		switch item.Status {
		case ItemSucceeded:
			summary.Succeeded++
		case ItemCancelled:
			summary.Cancelled++
		default:
			summary.Failed++
		}
	}
	return summary
}

func rejectDuplicates[T any](items []workItem[T]) {
	itemIDs := make(map[string]int, len(items))
	identities := make(map[string]int, len(items))
	for index := range items {
		if items[index].itemID != "" {
			if first, exists := itemIDs[items[index].itemID]; exists {
				items[index].failure = duplicateFailure("itemId", first)
			} else {
				itemIDs[items[index].itemID] = index
			}
		}
		if items[index].duplicateIdentity != "" {
			if first, exists := identities[items[index].duplicateIdentity]; exists {
				items[index].failure = duplicateFailure("target", first)
			} else {
				identities[items[index].duplicateIdentity] = index
			}
		}
	}
}

func normalizeRequest(ctx context.Context, actorID, workspaceID, batchKey string, count int) (string, string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", "", err
	}
	actorID, workspaceID = strings.TrimSpace(actorID), strings.TrimSpace(workspaceID)
	if actorID == "" || workspaceID == "" {
		return "", "", "", ErrInvalidScope
	}
	if count == 0 {
		return "", "", "", ErrEmptyBatch
	}
	if count > MaxBatchSize {
		return "", "", "", fmt.Errorf("%w: maximum is %d items", ErrBatchTooLarge, MaxBatchSize)
	}
	batchKey = strings.TrimSpace(batchKey)
	if batchKey == "" {
		return "", "", "", ErrIdempotencyKeyRequired
	}
	if len(batchKey) > 200 {
		return "", "", "", ErrInvalidIdempotencyKey
	}
	return actorID, workspaceID, batchKey, nil
}

func (s *Service) authorize(ctx context.Context, actorID, workspaceID string, permissions ...memberservice.Permission) error {
	if s == nil || s.authorizer == nil {
		return unavailable("authorizer")
	}
	for _, permission := range permissions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.authorizer.Require(ctx, workspaceID, actorID, permission); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func unavailable(name string) error {
	return fmt.Errorf("%w: %s", ErrDependencyUnavailable, name)
}

func normalizeCommandAction(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "start", OperationInstanceStart:
		return OperationInstanceStart, nil
	case "stop", OperationInstanceStop:
		return OperationInstanceStop, nil
	default:
		return "", ErrInvalidAction
	}
}

func normalizeRequired(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 200 {
		return value, fmt.Errorf("%s must be a non-empty string no longer than 200 bytes", name)
	}
	return value, nil
}

func normalizeOptional(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return normalizeRequired(value, "itemId")
}

func validAccountBindingType(value string) bool {
	return value == "profile" || value == "browser_instance" || value == "proxy"
}

func validProxyTargetType(value string) bool {
	return value == "account" || value == "profile" || value == "browser_instance"
}

func canonicalJSON(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// deriveItemKey is content-addressed so retries and request reordering produce
// the same per-item key. Actor ID and list index are deliberately excluded.
func deriveItemKey(workspaceID, batchKey, operation, canonicalItem string) string {
	encoded, _ := json.Marshal([]string{workspaceID, batchKey, operation, canonicalItem})
	digest := sha256.Sum256(encoded)
	return "batch:v1:" + hex.EncodeToString(digest[:])
}

func invalidFailure(err error) *ItemError {
	return &ItemError{Code: ErrorCodeInvalidItem, Message: err.Error(), Cause: err}
}

func duplicateFailure(kind string, first int) *ItemError {
	err := fmt.Errorf("%w: %s duplicates item at index %d", ErrDuplicateItem, kind, first)
	return &ItemError{Code: ErrorCodeDuplicateItem, Message: err.Error(), Cause: err}
}

func cancellationFailure(err error) *ItemError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &ItemError{Code: ErrorCodeDeadlineExceeded, Message: context.DeadlineExceeded.Error(), Retryable: true, Cause: err}
	}
	return &ItemError{Code: ErrorCodeCancelled, Message: context.Canceled.Error(), Cause: err}
}

func classifyFailure(err error) *ItemError {
	result := &ItemError{Code: ErrorCodeOperationFailed, Message: err.Error(), Cause: err}
	switch {
	case errors.Is(err, workspaceservice.ErrForbidden):
		result.Code, result.Message = ErrorCodeForbidden, "workspace permission denied"
	case errors.Is(err, browserinstanceservice.ErrNotFound),
		errors.Is(err, accountservice.ErrNotFound),
		errors.Is(err, proxyservice.ErrNotFound),
		errors.Is(err, taskservice.ErrNotFound):
		result.Code = ErrorCodeNotFound
	case errors.Is(err, browserinstanceservice.ErrVersionConflict),
		errors.Is(err, accountservice.ErrVersionConflict),
		errors.Is(err, proxyservice.ErrVersionConflict):
		result.Code = ErrorCodeVersionConflict
	case errors.Is(err, proxyservice.ErrAssignmentConflict),
		errors.Is(err, taskservice.ErrStateConflict):
		result.Code = ErrorCodeStateConflict
	case errors.Is(err, browserinstanceservice.ErrNameConflict),
		errors.Is(err, accountservice.ErrIdentifierConflict):
		result.Code = ErrorCodeNameConflict
	case errors.Is(err, accountservice.ErrUnsupported),
		errors.Is(err, proxyservice.ErrUnsupportedRoute):
		result.Code = ErrorCodeUnsupported
	}
	return result
}
