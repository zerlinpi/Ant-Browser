package gatewayservice_test

import (
	"net/http"
	"strings"
	"testing"

	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestInvitationIdentityIsolationConsumptionAndQuota(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "invite-owner@example.com")
	invitee := register(t, handler, "invitee@example.com")
	outsider := register(t, handler, "outsider@example.com")
	created := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]any{"name": "Invitations"})
	assertStatus(t, created, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, created)
	base := "/api/v1/workspaces/" + workspace.ID + "/invitations"
	create := func(email, role string) workspaceservice.Invitation {
		t.Helper()
		response := perform(t, handler, http.MethodPost, base, owner.AccessToken, "", map[string]any{"email": email, "role": role})
		assertStatus(t, response, http.StatusCreated)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("token response must not be cached")
		}
		item := decodeData[workspaceservice.Invitation](t, response)
		if len(item.Token) != 43 {
			t.Fatal("invitation must carry a 256-bit one-time token")
		}
		return item
	}
	for _, input := range []map[string]any{
		{"email": "bad@", "role": "viewer"}, {"email": "Invitee <invitee@example.com>", "role": "viewer"},
		{"email": "invitee@example.com", "role": "owner"},
	} {
		assertStatus(t, perform(t, handler, http.MethodPost, base, owner.AccessToken, "", input), http.StatusUnprocessableEntity)
	}
	assertStatus(t, perform(t, handler, http.MethodPost, base, outsider.AccessToken, "", map[string]any{"email": "invitee@example.com", "role": "viewer"}), http.StatusForbidden)
	invitation := create(" INVITEE@example.com ", "viewer")
	listed := perform(t, handler, http.MethodGet, base, owner.AccessToken, "", nil)
	assertStatus(t, listed, http.StatusOK)
	if strings.Contains(listed.Body.String(), invitation.Token) || strings.Contains(listed.Body.String(), `"token"`) {
		t.Fatal("listing disclosed bearer token")
	}
	assertStatus(t, perform(t, handler, http.MethodGet, base, invitee.AccessToken, "", nil), http.StatusForbidden)
	accept := map[string]any{"token": invitation.Token}
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/accept", outsider.AccessToken, "", accept), http.StatusUnprocessableEntity)
	other := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]any{"name": "Other"})
	assertStatus(t, other, http.StatusCreated)
	otherWorkspace := decodeData[workspaceservice.Workspace](t, other)
	assertStatus(t, perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+otherWorkspace.ID+"/invitations/accept", invitee.AccessToken, "", accept), http.StatusUnprocessableEntity)
	accepted := perform(t, handler, http.MethodPost, base+"/accept", invitee.AccessToken, "", accept)
	assertStatus(t, accepted, http.StatusCreated)
	member := decodeData[workspaceservice.Membership](t, accepted)
	if member.UserID != invitee.User.ID || member.WorkspaceID != workspace.ID || member.Role != "viewer" {
		t.Fatalf("wrong membership: %+v", member)
	}
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/accept", invitee.AccessToken, "", accept), http.StatusUnprocessableEntity)
	assertStatus(t, perform(t, handler, http.MethodPost, base, invitee.AccessToken, "", map[string]any{"email": "outsider@example.com", "role": "admin"}), http.StatusForbidden)
	revoked := create("outsider@example.com", "operator")
	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/"+revoked.ID, owner.AccessToken, "", nil), http.StatusNoContent)
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/accept", outsider.AccessToken, "", map[string]any{"token": revoked.Token}), http.StatusUnprocessableEntity)
	quota := create("outsider@example.com", "operator")
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/accept", outsider.AccessToken, "", map[string]any{"token": quota.Token}), http.StatusPaymentRequired)
	listed = perform(t, handler, http.MethodGet, base, owner.AccessToken, "", nil)
	items := decodeData[[]workspaceservice.Invitation](t, listed)
	if len(items) != 1 || items[0].ID != quota.ID {
		t.Fatal("quota failure consumed the invitation")
	}
}
