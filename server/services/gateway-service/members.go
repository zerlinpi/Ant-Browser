package gatewayservice

import (
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

// updateMember changes a member's role. Ownership cannot be granted here;
// only admin, manager, operator, and viewer are accepted.
func (g *Gateway) updateMember(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	role, ok := memberservice.ParseRole(input.Role)
	if !ok || role == memberservice.RoleOwner {
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUnprocessableEntity, Code: "invalid_role",
			Message: "Role must be admin, manager, operator, or viewer",
		})
		return
	}
	membership, err := g.workspaces.ChangeRole(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("userID"), role,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": membership})
}

// removeMember removes a member and revokes the member's devices in the
// workspace; live agent sockets of those devices are closed before replying.
func (g *Gateway) removeMember(w http.ResponseWriter, r *http.Request) {
	removal, err := g.workspaces.RemoveMember(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("userID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.disconnectAgentDevices(r.Context(), removal.RevokedDeviceIDs...)
	w.WriteHeader(http.StatusNoContent)
}
