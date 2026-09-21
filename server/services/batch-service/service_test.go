package batchservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type authorizationCall struct {
	workspaceID string
	actorID     string
	permission  memberservice.Permission
}

type fakeAuthorizer struct {
	calls []authorizationCall
	deny  memberservice.Permission
}

func (a *fakeAuthorizer) Require(ctx context.Context, workspaceID, actorID string, permission memberservice.Permission) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.calls = append(a.calls, authorizationCall{workspaceID: workspaceID, actorID: actorID, permission: permission})
	if permission == a.deny {
		return workspaceservice.ErrForbidden
	}
	return nil
}

type instanceCreateCall struct {
	actorID     string
	workspaceID string
	input       browserinstanceservice.CreateInput
}

type instanceCommandCall struct {
	actorID        string
	workspaceID    string
	instanceID     string
	idempotencyKey string
	input          browserinstanceservice.CommandInput
}

type fakeInstances struct {
	createCalls  []instanceCreateCall
	commandCalls []instanceCommandCall
	create       func(context.Context, browserinstanceservice.CreateInput) (browserinstanceservice.BrowserInstance, error)
	command      func(context.Context, instanceCommandCall) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error)
}

func (f *fakeInstances) Create(ctx context.Context, actorID, workspaceID string, input browserinstanceservice.CreateInput) (browserinstanceservice.BrowserInstance, error) {
	f.createCalls = append(f.createCalls, instanceCreateCall{actorID: actorID, workspaceID: workspaceID, input: input})
	if f.create != nil {
		return f.create(ctx, input)
	}
	return browserinstanceservice.BrowserInstance{ID: input.Name, WorkspaceID: workspaceID}, nil
}

func (f *fakeInstances) RequestCommand(ctx context.Context, actorID, workspaceID, instanceID, idempotencyKey string, input browserinstanceservice.CommandInput) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error) {
	call := instanceCommandCall{actorID: actorID, workspaceID: workspaceID, instanceID: instanceID, idempotencyKey: idempotencyKey, input: input}
	f.commandCalls = append(f.commandCalls, call)
	if f.command != nil {
		return f.command(ctx, call)
	}
	return browserinstanceservice.Command{ID: "command-" + instanceID, InstanceID: instanceID, Action: input.Action, IdempotencyKey: idempotencyKey}, browserinstanceservice.BrowserInstance{ID: instanceID, WorkspaceID: workspaceID}, nil
}

type accountBindingCall struct {
	actorID     string
	workspaceID string
	accountID   string
	bindingType string
	targetID    string
}

type fakeAccounts struct {
	calls []accountBindingCall
	bind  func(context.Context, accountBindingCall) (accountservice.AccountBinding, error)
}

func (f *fakeAccounts) Bind(ctx context.Context, actorID, workspaceID, accountID, bindingType, targetID string) (accountservice.AccountBinding, error) {
	call := accountBindingCall{actorID: actorID, workspaceID: workspaceID, accountID: accountID, bindingType: bindingType, targetID: targetID}
	f.calls = append(f.calls, call)
	if f.bind != nil {
		return f.bind(ctx, call)
	}
	return accountservice.AccountBinding{ID: accountID + "-" + bindingType, WorkspaceID: workspaceID, AccountID: accountID, BindingType: bindingType, TargetID: targetID}, nil
}

type proxyAssignmentCall struct {
	actorID     string
	workspaceID string
	proxyID     string
	targetID    string
	targetType  string
}

type fakeProxies struct {
	calls  []proxyAssignmentCall
	assign func(context.Context, proxyAssignmentCall) (proxyservice.Assignment, error)
}

func (f *fakeProxies) Assign(ctx context.Context, actorID, workspaceID, proxyID, targetID, targetType string) (proxyservice.Assignment, error) {
	call := proxyAssignmentCall{actorID: actorID, workspaceID: workspaceID, proxyID: proxyID, targetID: targetID, targetType: targetType}
	f.calls = append(f.calls, call)
	if f.assign != nil {
		return f.assign(ctx, call)
	}
	return proxyservice.Assignment{ID: proxyID + "-" + targetID, WorkspaceID: workspaceID, ProxyID: proxyID, TargetID: targetID, TargetType: targetType}, nil
}

type enqueueCall struct {
	actorID        string
	workspaceID    string
	idempotencyKey string
	input          taskservice.EnqueueInput
}

type fakeTasks struct {
	calls   []enqueueCall
	enqueue func(context.Context, enqueueCall) (taskservice.Task, error)
}

func (f *fakeTasks) Enqueue(ctx context.Context, actorID, workspaceID, idempotencyKey string, input taskservice.EnqueueInput) (taskservice.Task, error) {
	call := enqueueCall{actorID: actorID, workspaceID: workspaceID, idempotencyKey: idempotencyKey, input: input}
	f.calls = append(f.calls, call)
	if f.enqueue != nil {
		return f.enqueue(ctx, call)
	}
	instanceID, _ := input.Payload["instanceId"].(string)
	return taskservice.Task{ID: "task-" + instanceID, WorkspaceID: workspaceID, TaskType: input.TaskType, WorkflowID: input.WorkflowID, WorkflowVersionID: input.WorkflowVersionID, IdempotencyKey: idempotencyKey}, nil
}

func TestCreateInstancesReportsPartialSuccessAndDuplicate(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	instances := &fakeInstances{create: func(_ context.Context, input browserinstanceservice.CreateInput) (browserinstanceservice.BrowserInstance, error) {
		if input.Name == "bad" {
			return browserinstanceservice.BrowserInstance{}, errors.New("invalid instance fixture")
		}
		return browserinstanceservice.BrowserInstance{ID: "instance-" + input.Name, Name: input.Name}, nil
	}}
	service := New(instances, nil, nil, nil, authorizer)

	result, err := service.CreateInstances(context.Background(), " actor ", " workspace ", CreateInstancesInput{
		IdempotencyKey: "create-import-42",
		Items: []CreateInstanceItem{
			{ItemID: "row-a", Input: browserinstanceservice.CreateInput{Name: "one"}},
			{ItemID: "row-b", Input: browserinstanceservice.CreateInput{Name: "bad"}},
			{ItemID: "row-a", Input: browserinstanceservice.CreateInput{Name: "duplicate"}},
			{ItemID: "row-c", Input: browserinstanceservice.CreateInput{Name: "three"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateInstances error = %v", err)
	}
	if result.Atomic || result.ReplaySafe {
		t.Fatalf("creation must report non-atomic and non-replay-safe: %+v", result)
	}
	if result.Summary != (Summary{Total: 4, Succeeded: 2, Failed: 2}) {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if len(instances.createCalls) != 3 || instances.createCalls[2].input.Name != "three" {
		t.Fatalf("single failure or duplicate stopped later work: %+v", instances.createCalls)
	}
	if got := result.Items[2]; got.Status != ItemFailed || got.Error == nil || got.Error.Code != ErrorCodeDuplicateItem || !errors.Is(got.Error, ErrDuplicateItem) {
		t.Fatalf("duplicate result = %+v", got)
	}
	if result.Items[0].IdempotencyKey == "" || result.Items[0].IdempotencyKey != result.Items[2].IdempotencyKey {
		t.Fatalf("stable item keys not derived by item identity: %q / %q", result.Items[0].IdempotencyKey, result.Items[2].IdempotencyKey)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != (authorizationCall{"workspace", "actor", memberservice.PermissionInstanceCreate}) {
		t.Fatalf("authorization calls = %+v", authorizer.calls)
	}
}

func TestInstanceCommandsUseStablePerItemKeysAndContinue(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	instances := &fakeInstances{command: func(_ context.Context, call instanceCommandCall) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error) {
		if call.instanceID == "stale" {
			return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrVersionConflict
		}
		return browserinstanceservice.Command{ID: "command-" + call.instanceID, IdempotencyKey: call.idempotencyKey, Action: call.input.Action}, browserinstanceservice.BrowserInstance{ID: call.instanceID}, nil
	}}
	service := New(instances, nil, nil, nil, authorizer)
	input := InstanceCommandsInput{IdempotencyKey: "launch-202", Items: []InstanceCommandItem{
		{InstanceID: "first", ExpectedVersion: 4, Payload: map[string]interface{}{"reason": "campaign"}},
		{InstanceID: "stale", ExpectedVersion: 2},
		{ItemID: "same-target-again", InstanceID: "first", ExpectedVersion: 4},
		{InstanceID: "last", ExpectedVersion: 8},
	}}

	result, err := service.StartInstances(context.Background(), "actor", "workspace", input)
	if err != nil {
		t.Fatalf("StartInstances error = %v", err)
	}
	if result.Atomic || !result.ReplaySafe || result.Operation != OperationInstanceStart {
		t.Fatalf("command result flags = %+v", result)
	}
	if result.Summary != (Summary{Total: 4, Succeeded: 2, Failed: 2}) {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if len(instances.commandCalls) != 3 || instances.commandCalls[2].instanceID != "last" {
		t.Fatalf("command calls = %+v", instances.commandCalls)
	}
	if instances.commandCalls[0].input.Action != OperationInstanceStart || instances.commandCalls[0].idempotencyKey != result.Items[0].IdempotencyKey {
		t.Fatalf("downstream command did not receive item key/action: %+v", instances.commandCalls[0])
	}
	if result.Items[1].Error == nil || result.Items[1].Error.Code != ErrorCodeVersionConflict || !errors.Is(result.Items[1].Error, browserinstanceservice.ErrVersionConflict) {
		t.Fatalf("version conflict result = %+v", result.Items[1])
	}
	if result.Items[2].Error == nil || result.Items[2].Error.Code != ErrorCodeDuplicateItem {
		t.Fatalf("duplicate target result = %+v", result.Items[2])
	}

	firstKey := result.Items[0].IdempotencyKey
	resultAgain, err := service.StartInstances(context.Background(), "actor", "workspace", input)
	if err != nil || resultAgain.Items[0].IdempotencyKey != firstKey {
		t.Fatalf("same request key changed: %q -> %q, err=%v", firstKey, resultAgain.Items[0].IdempotencyKey, err)
	}
	stop, err := service.StopInstances(context.Background(), "actor", "workspace", InstanceCommandsInput{IdempotencyKey: input.IdempotencyKey, Items: []InstanceCommandItem{{InstanceID: "first", ExpectedVersion: 4, Payload: map[string]interface{}{"reason": "campaign"}}}})
	if err != nil {
		t.Fatalf("StopInstances error = %v", err)
	}
	if stop.Items[0].IdempotencyKey == firstKey {
		t.Fatal("different operations must not share an item idempotency key")
	}
}

func TestAccountAndProxyBindingsUseNaturalDuplicateKeys(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	accounts := &fakeAccounts{bind: func(_ context.Context, call accountBindingCall) (accountservice.AccountBinding, error) {
		if call.accountID == "missing" {
			return accountservice.AccountBinding{}, accountservice.ErrNotFound
		}
		return accountservice.AccountBinding{ID: call.accountID, AccountID: call.accountID, BindingType: call.bindingType, TargetID: call.targetID}, nil
	}}
	proxies := &fakeProxies{}
	service := New(nil, accounts, proxies, nil, authorizer)

	accountResult, err := service.BindAccounts(context.Background(), "actor", "workspace", BindAccountsInput{IdempotencyKey: "accounts", Items: []AccountBindingItem{
		{AccountID: "a-1", BindingType: " BROWSER_INSTANCE ", TargetID: "i-1"},
		{AccountID: "missing", BindingType: "profile", TargetID: "p-1"},
		{ItemID: "move-same-binding", AccountID: "a-1", BindingType: "browser_instance", TargetID: "i-2"},
		{AccountID: "a-2", BindingType: "proxy", TargetID: "proxy-2"},
	}})
	if err != nil {
		t.Fatalf("BindAccounts error = %v", err)
	}
	if accountResult.Summary != (Summary{Total: 4, Succeeded: 2, Failed: 2}) || len(accounts.calls) != 3 {
		t.Fatalf("account result=%+v calls=%+v", accountResult.Summary, accounts.calls)
	}
	if accountResult.Items[1].Error.Code != ErrorCodeNotFound || accountResult.Items[2].Error.Code != ErrorCodeDuplicateItem {
		t.Fatalf("account failures = %+v / %+v", accountResult.Items[1], accountResult.Items[2])
	}
	if accounts.calls[0].workspaceID != "workspace" || accounts.calls[0].bindingType != "browser_instance" {
		t.Fatalf("account call not normalized/scoped: %+v", accounts.calls[0])
	}

	proxyResult, err := service.AssignProxies(context.Background(), "actor", "workspace", AssignProxiesInput{IdempotencyKey: "proxies", Items: []ProxyAssignmentItem{
		{ProxyID: "proxy-1", TargetType: " BROWSER_INSTANCE ", TargetID: "i-1"},
		{ProxyID: "proxy-2", TargetType: "browser_instance", TargetID: "i-1", ItemID: "conflicting-proxy"},
		{ProxyID: "proxy-3", TargetType: "account", TargetID: "a-3"},
	}})
	if err != nil {
		t.Fatalf("AssignProxies error = %v", err)
	}
	if proxyResult.Summary != (Summary{Total: 3, Succeeded: 2, Failed: 1}) || len(proxies.calls) != 2 {
		t.Fatalf("proxy result=%+v calls=%+v", proxyResult.Summary, proxies.calls)
	}
	if proxyResult.Items[1].Error == nil || proxyResult.Items[1].Error.Code != ErrorCodeDuplicateItem {
		t.Fatalf("proxy duplicate = %+v", proxyResult.Items[1])
	}
	if !proxyResult.ReplaySafe || proxyResult.Atomic {
		t.Fatalf("proxy flags = %+v", proxyResult)
	}
}

func TestExecuteWorkflowsChecksAllPermissionsAndEnqueuesIndependently(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	tasks := &fakeTasks{enqueue: func(_ context.Context, call enqueueCall) (taskservice.Task, error) {
		instanceID, _ := call.input.Payload["instanceId"].(string)
		if instanceID == "missing" {
			return taskservice.Task{}, taskservice.ErrNotFound
		}
		return taskservice.Task{ID: "task-" + instanceID, IdempotencyKey: call.idempotencyKey}, nil
	}}
	service := New(nil, nil, nil, tasks, authorizer)
	retry := 0
	input := ExecuteWorkflowsInput{IdempotencyKey: "workflow-run", Items: []WorkflowExecutionItem{
		{WorkflowID: "workflow", WorkflowVersionID: "version", InstanceID: "one", Priority: 5, RetryLimit: &retry},
		{WorkflowID: "workflow", WorkflowVersionID: "version", InstanceID: "missing"},
		{WorkflowID: "workflow", WorkflowVersionID: "version", InstanceID: "one", ItemID: "duplicate-one"},
		{WorkflowID: "workflow", WorkflowVersionID: "version", InstanceID: "last"},
	}}

	result, err := service.ExecuteWorkflows(context.Background(), "actor", "workspace", input)
	if err != nil {
		t.Fatalf("ExecuteWorkflows error = %v", err)
	}
	wantPermissions := []memberservice.Permission{memberservice.PermissionTaskOperate, memberservice.PermissionInstanceOperate, memberservice.PermissionWorkflowRead}
	if len(authorizer.calls) != len(wantPermissions) {
		t.Fatalf("authorization calls = %+v", authorizer.calls)
	}
	for index, permission := range wantPermissions {
		if authorizer.calls[index].permission != permission {
			t.Fatalf("permission[%d] = %q, want %q", index, authorizer.calls[index].permission, permission)
		}
	}
	if result.Summary != (Summary{Total: 4, Succeeded: 2, Failed: 2}) || len(tasks.calls) != 3 {
		t.Fatalf("workflow result=%+v calls=%+v", result.Summary, tasks.calls)
	}
	if tasks.calls[0].input.TaskType != OperationWorkflowExecute || tasks.calls[0].input.Payload["instanceId"] != "one" || tasks.calls[0].idempotencyKey != result.Items[0].IdempotencyKey {
		t.Fatalf("enqueue contract = %+v", tasks.calls[0])
	}
	if result.Items[1].Error.Code != ErrorCodeNotFound || result.Items[2].Error.Code != ErrorCodeDuplicateItem || result.Items[3].Status != ItemSucceeded {
		t.Fatalf("per-item workflow results = %+v", result.Items)
	}

	firstKey := result.Items[0].IdempotencyKey
	again, err := service.SubmitWorkflowExecutions(context.Background(), "actor", "workspace", input)
	if err != nil || again.Items[0].IdempotencyKey != firstKey {
		t.Fatalf("workflow key changed: %q -> %q, err=%v", firstKey, again.Items[0].IdempotencyKey, err)
	}
}

func TestAuthorizationIsPreflightedBeforeAnyItemMutation(t *testing.T) {
	authorizer := &fakeAuthorizer{deny: memberservice.PermissionInstanceOperate}
	instances := &fakeInstances{}
	service := New(instances, nil, nil, nil, authorizer)
	result, err := service.StartInstances(context.Background(), "actor", "workspace", InstanceCommandsInput{
		IdempotencyKey: "denied", Items: []InstanceCommandItem{{InstanceID: "one", ExpectedVersion: 1}},
	})
	if !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("error = %v", err)
	}
	if len(instances.commandCalls) != 0 || result.Summary.Total != 1 {
		t.Fatalf("unauthorized request invoked items: result=%+v calls=%+v", result, instances.commandCalls)
	}
}

func TestWorkflowAuthorizationFailureOnLaterPermissionStillPreventsEnqueue(t *testing.T) {
	authorizer := &fakeAuthorizer{deny: memberservice.PermissionWorkflowRead}
	tasks := &fakeTasks{}
	service := New(nil, nil, nil, tasks, authorizer)
	_, err := service.ExecuteWorkflows(context.Background(), "actor", "workspace", ExecuteWorkflowsInput{
		IdempotencyKey: "denied", Items: []WorkflowExecutionItem{{WorkflowID: "workflow", WorkflowVersionID: "version", InstanceID: "instance"}},
	})
	if !errors.Is(err, workspaceservice.ErrForbidden) || len(tasks.calls) != 0 || len(authorizer.calls) != 3 {
		t.Fatalf("late permission preflight: err=%v tasks=%d auth=%+v", err, len(tasks.calls), authorizer.calls)
	}
}

func TestContextCancellationStopsLaunchingItemsAndMarksRemainder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	authorizer := &fakeAuthorizer{}
	instances := &fakeInstances{create: func(_ context.Context, input browserinstanceservice.CreateInput) (browserinstanceservice.BrowserInstance, error) {
		cancel()
		return browserinstanceservice.BrowserInstance{ID: input.Name}, nil
	}}
	service := New(instances, nil, nil, nil, authorizer)
	result, err := service.CreateInstances(ctx, "actor", "workspace", CreateInstancesInput{IdempotencyKey: "cancel", Items: []CreateInstanceItem{
		{ItemID: "one", Input: browserinstanceservice.CreateInput{Name: "one"}},
		{ItemID: "two", Input: browserinstanceservice.CreateInput{Name: "two"}},
		{ItemID: "three", Input: browserinstanceservice.CreateInput{Name: "three"}},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(instances.createCalls) != 1 || result.Summary != (Summary{Total: 3, Succeeded: 1, Cancelled: 2}) {
		t.Fatalf("cancellation result=%+v calls=%+v", result.Summary, instances.createCalls)
	}
	for _, index := range []int{1, 2} {
		if result.Items[index].Status != ItemCancelled || result.Items[index].Error == nil || result.Items[index].Error.Code != ErrorCodeCancelled {
			t.Fatalf("item %d = %+v", index, result.Items[index])
		}
	}
}

func TestDownstreamDeadlineStopsBatch(t *testing.T) {
	authorizer := &fakeAuthorizer{}
	instances := &fakeInstances{command: func(_ context.Context, _ instanceCommandCall) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error) {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, context.DeadlineExceeded
	}}
	service := New(instances, nil, nil, nil, authorizer)
	result, err := service.StopInstances(context.Background(), "actor", "workspace", InstanceCommandsInput{IdempotencyKey: "deadline", Items: []InstanceCommandItem{
		{InstanceID: "one", ExpectedVersion: 1}, {InstanceID: "two", ExpectedVersion: 2},
	}})
	if !errors.Is(err, context.DeadlineExceeded) || len(instances.commandCalls) != 1 {
		t.Fatalf("deadline err=%v calls=%d", err, len(instances.commandCalls))
	}
	if result.Summary != (Summary{Total: 2, Cancelled: 2}) || result.Items[0].Error.Code != ErrorCodeDeadlineExceeded || !result.Items[0].Error.Retryable {
		t.Fatalf("deadline result = %+v", result)
	}
}

func TestBatchEnvelopeLimitsAndDependencies(t *testing.T) {
	service := New(nil, nil, nil, nil, &fakeAuthorizer{})
	_, err := service.CreateInstances(context.Background(), "actor", "workspace", CreateInstancesInput{IdempotencyKey: "empty"})
	if !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("empty error = %v", err)
	}
	tooMany := make([]CreateInstanceItem, MaxBatchSize+1)
	_, err = service.CreateInstances(context.Background(), "actor", "workspace", CreateInstancesInput{IdempotencyKey: "large", Items: tooMany})
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("large error = %v", err)
	}
	_, err = service.CreateInstances(context.Background(), "actor", "workspace", CreateInstancesInput{Items: []CreateInstanceItem{{ItemID: "one"}}})
	if !errors.Is(err, ErrIdempotencyKeyRequired) {
		t.Fatalf("missing key error = %v", err)
	}
	_, err = service.CreateInstances(context.Background(), "actor", "workspace", CreateInstancesInput{IdempotencyKey: "key", Items: []CreateInstanceItem{{ItemID: "one"}}})
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("dependency error = %v", err)
	}
}

func TestItemKeyIsBoundToWorkspaceSemanticsAndWithinDownstreamLimit(t *testing.T) {
	keyA := deriveItemKey("workspace-a", "batch", OperationInstanceStart, `{"instanceId":"one"}`)
	keyAgain := deriveItemKey("workspace-a", "batch", OperationInstanceStart, `{"instanceId":"one"}`)
	keyWorkspace := deriveItemKey("workspace-b", "batch", OperationInstanceStart, `{"instanceId":"one"}`)
	keyPayload := deriveItemKey("workspace-a", "batch", OperationInstanceStart, `{"instanceId":"two"}`)
	if keyA != keyAgain || keyA == keyWorkspace || keyA == keyPayload {
		t.Fatalf("unexpected key derivation: %q %q %q %q", keyA, keyAgain, keyWorkspace, keyPayload)
	}
	if len(keyA) > 200 || !strings.HasPrefix(keyA, "batch:v1:") {
		t.Fatalf("derived key violates downstream contract: %q", keyA)
	}
}

func TestResultJSONExposesNonAtomicityWithoutInternalCause(t *testing.T) {
	result := BatchResult[string]{Operation: OperationInstanceCreate, Atomic: false, ReplaySafe: false, Summary: Summary{Total: 1, Failed: 1}, Items: []ItemResult[string]{
		{Index: 0, Status: ItemFailed, Error: &ItemError{Code: ErrorCodeOperationFailed, Message: "safe", Cause: errors.New("internal database detail")}},
	}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"atomic":false`) || !strings.Contains(text, `"replaySafe":false`) || strings.Contains(text, "internal database detail") {
		t.Fatalf("JSON = %s", text)
	}
}
