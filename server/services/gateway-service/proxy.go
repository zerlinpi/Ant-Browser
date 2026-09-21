package gatewayservice

import (
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	"net/http"
	"strconv"
	"strings"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
)

func (g *Gateway) listProxies(w http.ResponseWriter, r *http.Request) {
	items, err := g.proxies.List(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createProxy(w http.ResponseWriter, r *http.Request) {
	var input proxyservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	proxy, err := g.proxies.Create(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": proxy})
}

func (g *Gateway) getProxy(w http.ResponseWriter, r *http.Request) {
	proxy, err := g.proxies.Get(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": proxy})
}

func (g *Gateway) updateProxy(w http.ResponseWriter, r *http.Request) {
	var input proxyservice.UpdateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if input.Version == 0 {
		version, err := versionPrecondition(r)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		input.Version = version
	}
	proxy, err := g.proxies.Update(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": proxy})
}

func (g *Gateway) deleteProxy(w http.ResponseWriter, r *http.Request) {
	version, err := versionPrecondition(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.proxies.Delete(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"), version); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) assignProxy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TargetID   string `json:"targetId"`
		TargetType string `json:"targetType"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.proxies.AssignProxy(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"), input.TargetID, input.TargetType)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}

func (g *Gateway) unassignProxy(w http.ResponseWriter, r *http.Request) {
	version, err := versionPrecondition(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.proxies.Unassign(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("targetID"), r.PathValue("targetType"), version); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listProxyHealthChecks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_limit", Message: "Limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	items, err := g.proxies.ListHealthChecks(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"), limit)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) requestProxyHealthCheck(w http.ResponseWriter, r *http.Request) {
	check, err := g.proxies.RequestProxyHealthCheck(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("proxyID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	// The repository commits the check and its task together; wake delivery is
	// advisory because durable task polling remains authoritative.
	if err := g.taskWake.Notify(r.Context(), taskwake.Signal{WorkspaceID: check.WorkspaceID, TaskID: check.RequestID, TaskType: "proxy.health_check", QueuedAt: check.CreatedAt}); err != nil {
		g.logger.WarnContext(r.Context(), "proxy_health_wakeup_failed", "task_id", check.RequestID, "error", err)
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"data": check, "taskId": check.RequestID, "dispatchStatus": "queued",
	})
}

func (g *Gateway) getProxyHealthCheck(w http.ResponseWriter, r *http.Request) {
	check, err := g.proxies.GetHealthCheck(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("checkID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": check})
}
