package gatewayservice

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	batchservice "github.com/zerlinpi/Ant-Browser/server/services/batch-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

// Batch endpoints take the idempotency key only from the standard header.
// Keeping it out of the JSON request prevents two competing sources of truth.
func (g *Gateway) batchCreateInstances(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Items []batchservice.CreateInstanceItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.batch.CreateInstances(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
		batchservice.CreateInstancesInput{IdempotencyKey: batchIdempotencyKey(r), Items: request.Items},
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	writeBatchResult(w, http.StatusCreated, result)
}

func (g *Gateway) batchStartInstances(w http.ResponseWriter, r *http.Request) {
	g.batchInstanceCommands(w, r, true)
}

func (g *Gateway) batchStopInstances(w http.ResponseWriter, r *http.Request) {
	g.batchInstanceCommands(w, r, false)
}

func (g *Gateway) batchInstanceCommands(w http.ResponseWriter, r *http.Request, start bool) {
	var request struct {
		Items []batchservice.InstanceCommandItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	input := batchservice.InstanceCommandsInput{IdempotencyKey: batchIdempotencyKey(r), Items: request.Items}
	var result batchservice.BatchResult[batchservice.CommandValue]
	var err error
	if start {
		result, err = g.batch.StartInstances(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	} else {
		result, err = g.batch.StopInstances(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	}
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	for _, item := range result.Items {
		if item.Status == batchservice.ItemSucceeded && item.Value != nil {
			g.dispatchInstanceCommand(r.Context(), item.Value.Command)
		}
	}
	writeBatchResult(w, http.StatusAccepted, result)
}

func (g *Gateway) batchBindAccounts(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Items []batchservice.AccountBindingItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.batch.BindAccounts(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
		batchservice.BindAccountsInput{IdempotencyKey: batchIdempotencyKey(r), Items: request.Items},
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	writeBatchResult(w, http.StatusOK, result)
}

func (g *Gateway) batchAssignProxies(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Items []batchservice.ProxyAssignmentItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.batch.AssignProxies(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
		batchservice.AssignProxiesInput{IdempotencyKey: batchIdempotencyKey(r), Items: request.Items},
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	writeBatchResult(w, http.StatusOK, result)
}

func (g *Gateway) batchExecuteWorkflows(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Items []batchservice.WorkflowExecutionItem `json:"items"`
	}
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.batch.ExecuteWorkflows(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
		batchservice.ExecuteWorkflowsInput{IdempotencyKey: batchIdempotencyKey(r), Items: request.Items},
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.notifyBatchTasks(r.Context(), result)
	writeBatchResult(w, http.StatusAccepted, result)
}

func (g *Gateway) notifyBatchTasks(ctx context.Context, result batchservice.BatchResult[taskservice.Task]) {
	for _, item := range result.Items {
		if item.Status != batchservice.ItemSucceeded || item.Value == nil {
			continue
		}
		task := *item.Value
		if err := g.taskWake.Notify(ctx, taskwake.Signal{
			WorkspaceID: task.WorkspaceID, TaskID: task.ID, TaskType: task.TaskType, QueuedAt: time.Now().UTC(),
		}); err != nil {
			g.logger.WarnContext(ctx, "task_wakeup_failed", "task_id", task.ID, "error", err)
		}
	}
}

func batchIdempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}

func writeBatchResult[T any](w http.ResponseWriter, successStatus int, result batchservice.BatchResult[T]) {
	status := successStatus
	if result.Summary.Failed > 0 || result.Summary.Cancelled > 0 {
		status = http.StatusMultiStatus
	}
	httpx.WriteJSON(w, status, map[string]interface{}{"data": result})
}
