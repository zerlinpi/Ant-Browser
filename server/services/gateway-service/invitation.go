package gatewayservice

import (
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func (g *Gateway) createInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	role, ok := memberservice.ParseRole(in.Role)
	if !ok {
		httpx.WriteError(w, r, httpx.Problem{Status: 422, Code: "invalid_role", Message: "Role is invalid"})
		return
	}
	item, err := g.workspaces.Invite(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), workspaceservice.InviteInput{Email: in.Email, Role: role})
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}
func (g *Gateway) listInvitations(w http.ResponseWriter, r *http.Request) {
	items, err := g.workspaces.Invitations(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}
func (g *Gateway) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	if err := g.workspaces.RevokeInvitation(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("invitationID")); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (g *Gateway) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	p := mustPrincipal(r.Context())
	membership, err := g.workspaces.Accept(r.Context(), p.UserID, r.PathValue("workspaceID"), in.Token)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": membership})
}
