package gatewayservice

import (
	"errors"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	tasks "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	"net/http"
)

func (g *Gateway) claimAgentTask(w http.ResponseWriter, r *http.Request) {
	device, err := g.authenticateAgent(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_device_credential", Message: "Device credential is invalid or revoked"})
		return
	}
	requestContext := postgres.WithTenantScope(r.Context(), postgres.TenantScope{WorkspaceID: device.WorkspaceID})
	execution, err := g.tasks.ClaimDevice(requestContext, device.UserID, device.WorkspaceID, device.ID)
	if errors.Is(err, tasks.ErrNoWork) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": execution})
}

func (g *Gateway) reportAgentTask(w http.ResponseWriter, r *http.Request) {
	device, err := g.authenticateAgent(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_device_credential", Message: "Device credential is invalid or revoked"})
		return
	}
	var report tasks.DeviceReport
	if err := httpx.DecodeJSON(w, r, &report); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	requestContext := postgres.WithTenantScope(r.Context(), postgres.TenantScope{WorkspaceID: device.WorkspaceID})
	if err := g.tasks.ReportDevice(requestContext, device.UserID, device.WorkspaceID, device.ID, r.PathValue("taskID"), report); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
