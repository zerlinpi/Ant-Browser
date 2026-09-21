package gatewayservice

import (
	"net/http"
	"strconv"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

func (g *Gateway) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := g.tasks.List(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), limit,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) enqueueTask(w http.ResponseWriter, r *http.Request) {
	var input taskservice.EnqueueInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	task, err := g.tasks.Enqueue(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
		r.Header.Get("Idempotency-Key"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	if err := g.taskWake.Notify(r.Context(), taskwake.Signal{
		WorkspaceID: task.WorkspaceID, TaskID: task.ID, TaskType: task.TaskType, QueuedAt: time.Now().UTC(),
	}); err != nil {
		g.logger.WarnContext(r.Context(), "task_wakeup_failed", "task_id", task.ID, "error", err)
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]interface{}{"data": task})
}

func (g *Gateway) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := g.tasks.Get(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("taskID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": task})
}

func (g *Gateway) cancelTask(w http.ResponseWriter, r *http.Request) {
	task, err := g.tasks.Cancel(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("taskID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": task})
}
