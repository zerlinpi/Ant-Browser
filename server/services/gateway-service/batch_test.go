package gatewayservice_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	batchservice "github.com/zerlinpi/Ant-Browser/server/services/batch-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestBatchGatewayActionsReturnIndependentResults(t *testing.T) {
	handler := newTestGateway()
	owner := register(t, handler, "batch-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Batch Operations"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	basePath := "/api/v1/workspaces/" + workspace.ID
	device := createTestDevice(t, handler, owner.AccessToken, workspace.ID)

	createResponse := perform(t, handler, http.MethodPost, basePath+"/batch/browser-instances", owner.AccessToken, "batch-create-1", map[string]interface{}{
		"items": []map[string]interface{}{
			{"itemId": "import-row-1", "input": map[string]interface{}{"name": "Store One", "platform": "chromium", "assignedDeviceId": device.Data.Device.ID}},
			{"itemId": "import-row-bad", "input": map[string]interface{}{"name": "", "platform": "chromium"}},
			{"itemId": "import-row-2", "input": map[string]interface{}{"name": "Store Two", "platform": "chromium", "assignedDeviceId": device.Data.Device.ID}},
		},
	})
	assertStatus(t, createResponse, http.StatusMultiStatus)
	created := decodeData[batchservice.BatchResult[browserinstanceservice.BrowserInstance]](t, createResponse)
	if created.Atomic || created.ReplaySafe || created.Summary != (batchservice.Summary{Total: 3, Succeeded: 2, Failed: 1}) {
		t.Fatalf("batch create = %+v", created)
	}
	if created.Items[0].Value == nil || created.Items[2].Value == nil || created.Items[1].Status != batchservice.ItemFailed {
		t.Fatalf("create item results = %+v", created.Items)
	}
	first, second := *created.Items[0].Value, *created.Items[2].Value

	missingInstanceID := uuid.NewString()
	startBody := map[string]interface{}{"items": []map[string]interface{}{
		{"instanceId": first.ID, "expectedVersion": first.Version},
		{"instanceId": missingInstanceID, "expectedVersion": 1},
		{"instanceId": second.ID, "expectedVersion": second.Version},
	}}
	startResponse := perform(t, handler, http.MethodPost, basePath+"/batch/browser-instances/start", owner.AccessToken, "batch-start-1", startBody)
	assertStatus(t, startResponse, http.StatusMultiStatus)
	started := decodeData[batchservice.BatchResult[batchservice.CommandValue]](t, startResponse)
	if started.Summary != (batchservice.Summary{Total: 3, Succeeded: 2, Failed: 1}) || started.Items[1].Error == nil || started.Items[1].Error.Code != batchservice.ErrorCodeNotFound {
		t.Fatalf("batch start = %+v", started)
	}
	if started.Items[0].Value == nil || started.Items[0].Value.Instance.DesiredState != "running" || started.Items[0].Value.Command.Action != batchservice.OperationInstanceStart {
		t.Fatalf("start value = %+v", started.Items[0])
	}

	// The same batch key and item semantics replay the accepted command even
	// though the instance version has already advanced.
	replayedStartResponse := perform(t, handler, http.MethodPost, basePath+"/batch/browser-instances/start", owner.AccessToken, "batch-start-1", startBody)
	assertStatus(t, replayedStartResponse, http.StatusMultiStatus)
	replayedStart := decodeData[batchservice.BatchResult[batchservice.CommandValue]](t, replayedStartResponse)
	if replayedStart.Items[0].Value == nil || replayedStart.Items[0].Value.Command.ID != started.Items[0].Value.Command.ID || replayedStart.Items[0].IdempotencyKey != started.Items[0].IdempotencyKey {
		t.Fatalf("command replay was not idempotent: first=%+v replay=%+v", started.Items[0], replayedStart.Items[0])
	}

	stopResponse := perform(t, handler, http.MethodPost, basePath+"/batch/browser-instances/stop", owner.AccessToken, "batch-stop-1", map[string]interface{}{
		"items": []map[string]interface{}{
			{"instanceId": first.ID, "expectedVersion": started.Items[0].Value.Instance.Version},
			{"instanceId": missingInstanceID, "expectedVersion": 1},
			{"instanceId": second.ID, "expectedVersion": started.Items[2].Value.Instance.Version},
		},
	})
	assertStatus(t, stopResponse, http.StatusMultiStatus)
	stopped := decodeData[batchservice.BatchResult[batchservice.CommandValue]](t, stopResponse)
	if stopped.Summary != (batchservice.Summary{Total: 3, Succeeded: 2, Failed: 1}) || stopped.Items[0].Value == nil || stopped.Items[0].Value.Instance.DesiredState != "stopped" || stopped.Items[0].Value.Command.Action != batchservice.OperationInstanceStop {
		t.Fatalf("batch stop = %+v", stopped)
	}

	accountResponse := perform(t, handler, http.MethodPost, basePath+"/accounts", owner.AccessToken, "", map[string]interface{}{
		"platform": "amazon", "name": "Seller One", "identifier": "seller-one@example.test",
	})
	assertStatus(t, accountResponse, http.StatusCreated)
	account := decodeData[accountservice.Account](t, accountResponse)
	accountBindingResponse := perform(t, handler, http.MethodPost, basePath+"/batch/account-bindings", owner.AccessToken, "batch-accounts-1", map[string]interface{}{
		"items": []map[string]interface{}{
			{"accountId": account.ID, "bindingType": "browser_instance", "targetId": first.ID},
			{"accountId": uuid.NewString(), "bindingType": "browser_instance", "targetId": second.ID},
		},
	})
	assertStatus(t, accountBindingResponse, http.StatusMultiStatus)
	accountBindings := decodeData[batchservice.BatchResult[accountservice.AccountBinding]](t, accountBindingResponse)
	if accountBindings.Summary != (batchservice.Summary{Total: 2, Succeeded: 1, Failed: 1}) || accountBindings.Items[0].Value == nil || accountBindings.Items[1].Error.Code != batchservice.ErrorCodeNotFound {
		t.Fatalf("batch account binding = %+v", accountBindings)
	}

	proxyResponse := perform(t, handler, http.MethodPost, basePath+"/proxies", owner.AccessToken, "", map[string]interface{}{
		"name": "Direct Test", "protocol": "direct", "connectorType": "xray",
	})
	assertStatus(t, proxyResponse, http.StatusCreated)
	proxy := decodeData[proxyservice.Proxy](t, proxyResponse)
	proxyAssignmentResponse := perform(t, handler, http.MethodPost, basePath+"/batch/proxy-assignments", owner.AccessToken, "batch-proxies-1", map[string]interface{}{
		"items": []map[string]interface{}{
			{"proxyId": proxy.ID, "targetType": "browser_instance", "targetId": first.ID},
			{"proxyId": uuid.NewString(), "targetType": "browser_instance", "targetId": second.ID},
		},
	})
	assertStatus(t, proxyAssignmentResponse, http.StatusMultiStatus)
	proxyAssignments := decodeData[batchservice.BatchResult[proxyservice.Assignment]](t, proxyAssignmentResponse)
	if proxyAssignments.Summary != (batchservice.Summary{Total: 2, Succeeded: 1, Failed: 1}) || proxyAssignments.Items[0].Value == nil || proxyAssignments.Items[1].Error.Code != batchservice.ErrorCodeNotFound {
		t.Fatalf("batch proxy assignment = %+v", proxyAssignments)
	}

	workflowDefinition := map[string]interface{}{
		"engine": "playwright",
		"steps":  []map[string]interface{}{{"id": "finish", "action": "close"}},
	}
	workflowResponse := perform(t, handler, http.MethodPost, basePath+"/workflows", owner.AccessToken, "", map[string]interface{}{
		"name": "Batch workflow", "definition": workflowDefinition,
	})
	assertStatus(t, workflowResponse, http.StatusCreated)
	workflow := decodeData[struct {
		Workflow automationservice.Workflow        `json:"workflow"`
		Version  automationservice.WorkflowVersion `json:"version"`
	}](t, workflowResponse)
	publishResponse := perform(t, handler, http.MethodPost, basePath+"/workflows/"+workflow.Workflow.ID+"/publish", owner.AccessToken, "", map[string]interface{}{
		"expectedVersion": workflow.Workflow.Version, "workflowVersion": workflow.Version.Version,
	})
	assertStatus(t, publishResponse, http.StatusOK)

	workflowBatchBody := map[string]interface{}{"items": []map[string]interface{}{
		{"workflowId": workflow.Workflow.ID, "workflowVersionId": workflow.Version.ID, "instanceId": first.ID},
		{"workflowId": workflow.Workflow.ID, "workflowVersionId": workflow.Version.ID, "instanceId": missingInstanceID},
	}}
	workflowBatchResponse := perform(t, handler, http.MethodPost, basePath+"/batch/workflow-executions", owner.AccessToken, "batch-workflow-1", workflowBatchBody)
	assertStatus(t, workflowBatchResponse, http.StatusMultiStatus)
	executions := decodeData[batchservice.BatchResult[taskservice.Task]](t, workflowBatchResponse)
	if executions.Summary != (batchservice.Summary{Total: 2, Succeeded: 1, Failed: 1}) || executions.Items[0].Value == nil || executions.Items[0].Value.TaskType != batchservice.OperationWorkflowExecute || executions.Items[1].Error.Code != batchservice.ErrorCodeNotFound {
		t.Fatalf("batch workflow execution = %+v", executions)
	}
	replayedWorkflowResponse := perform(t, handler, http.MethodPost, basePath+"/batch/workflow-executions", owner.AccessToken, "batch-workflow-1", workflowBatchBody)
	assertStatus(t, replayedWorkflowResponse, http.StatusMultiStatus)
	replayedExecutions := decodeData[batchservice.BatchResult[taskservice.Task]](t, replayedWorkflowResponse)
	if replayedExecutions.Items[0].Value == nil || replayedExecutions.Items[0].Value.ID != executions.Items[0].Value.ID || replayedExecutions.Items[0].IdempotencyKey != executions.Items[0].IdempotencyKey {
		t.Fatalf("workflow replay was not idempotent: first=%+v replay=%+v", executions.Items[0], replayedExecutions.Items[0])
	}
}

func TestBatchGatewayAuthenticationRBACAndEnvelopeErrors(t *testing.T) {
	handler := newTestGateway()
	owner := register(t, handler, "batch-security-owner@example.com")
	outsider := register(t, handler, "batch-security-outsider@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Secured Batch"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	path := "/api/v1/workspaces/" + workspace.ID + "/batch/browser-instances"
	body := map[string]interface{}{"items": []map[string]interface{}{{"itemId": "one", "input": map[string]interface{}{"name": "One"}}}}

	unauthenticated := perform(t, handler, http.MethodPost, path, "", "batch-auth", body)
	assertStatus(t, unauthenticated, http.StatusUnauthorized)
	forbidden := perform(t, handler, http.MethodPost, path, outsider.AccessToken, "batch-auth", body)
	assertStatus(t, forbidden, http.StatusForbidden)
	missingKey := perform(t, handler, http.MethodPost, path, owner.AccessToken, "", body)
	assertStatus(t, missingKey, http.StatusUnprocessableEntity)
	var missingKeyError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, missingKey, &missingKeyError)
	if missingKeyError.Error.Code != "invalid_batch" {
		t.Fatalf("missing key error = %+v", missingKeyError)
	}

	items := make([]map[string]interface{}, batchservice.MaxBatchSize+1)
	for index := range items {
		items[index] = map[string]interface{}{"itemId": uuid.NewString(), "input": map[string]interface{}{"name": "Instance"}}
	}
	tooLarge := perform(t, handler, http.MethodPost, path, owner.AccessToken, "batch-large", map[string]interface{}{"items": items})
	assertStatus(t, tooLarge, http.StatusRequestEntityTooLarge)
	var tooLargeError struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]int `json:"details"`
		} `json:"error"`
	}
	decodeResponse(t, tooLarge, &tooLargeError)
	if tooLargeError.Error.Code != "batch_too_large" || tooLargeError.Error.Details["maxItems"] != batchservice.MaxBatchSize {
		t.Fatalf("large batch error = %+v", tooLargeError)
	}
}
