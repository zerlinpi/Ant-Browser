package gatewayservice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// relaxedRegistration lets a single test register many users from one
// address without tripping the default per-IP registration limit.
var relaxedRegistration = gatewayservice.RateLimits{
	RegisterPerIP: gatewayservice.RateLimit{Requests: 1000, Interval: time.Minute, Burst: 1000},
}

// teamFixture is a workspace owned by "owner" with the named members added
// through the API. The organization seat limit is lifted for the test.
type teamFixture struct {
	handler   http.Handler
	store     *memory.Store
	workspace workspaceservice.Workspace
	users     map[string]authservice.TokenPair
}

func newTeamFixture(t *testing.T, roles map[string]string, options ...interface{}) teamFixture {
	t.Helper()
	handler, store := newTestGatewayWithStore(append([]interface{}{relaxedRegistration}, options...)...)
	f := teamFixture{handler: handler, store: store, users: map[string]authservice.TokenPair{}}
	f.users["owner"] = register(t, handler, "team-owner@example.com")
	created := perform(t, handler, http.MethodPost, "/api/v1/workspaces", f.users["owner"].AccessToken, "", map[string]string{"name": "Team"})
	assertStatus(t, created, http.StatusCreated)
	f.workspace = decodeData[workspaceservice.Workspace](t, created)
	if err := store.UpsertEntitlement(context.Background(), billingservice.Entitlement{
		OrganizationID: f.workspace.OrganizationID, Code: billingservice.EntitlementTeamMembers,
		FeatureEnabled: true, ValidFrom: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	for name, role := range roles {
		f.users[name] = register(t, handler, "team-"+name+"@example.com")
		added := perform(t, handler, http.MethodPost, f.membersPath(), f.users["owner"].AccessToken, "", map[string]string{
			"userId": f.users[name].User.ID, "role": role,
		})
		assertStatus(t, added, http.StatusCreated)
	}
	return f
}

func (f teamFixture) membersPath() string {
	return "/api/v1/workspaces/" + f.workspace.ID + "/members"
}

func (f teamFixture) memberPath(name string) string {
	return f.membersPath() + "/" + f.users[name].User.ID
}

func (f teamFixture) token(name string) string { return f.users[name].AccessToken }

func (f teamFixture) registerDevice(t *testing.T, name string) deviceservice.Registration {
	t.Helper()
	response := perform(t, f.handler, http.MethodPost, "/api/v1/devices", f.token(name), "", map[string]interface{}{
		"workspaceId": f.workspace.ID, "name": name + " workstation", "platform": "windows", "agentVersion": "1.0.0",
	})
	assertStatus(t, response, http.StatusCreated)
	return decodeData[deviceservice.Registration](t, response)
}

func requireErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	assertStatus(t, response, status)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("error code %q, want %q; body=%s", body.Error.Code, code, response.Body.String())
	}
}

// performFrom sends a request from a specific peer address with optional
// extra headers.
func performFrom(t *testing.T, handler http.Handler, remoteAddr, method, path, accessToken string, body interface{}, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = remoteAddr
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestWorkspaceResponsesCarryCallerRoleAndMemberProfiles(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, map[string]string{"admin": "admin", "viewer": "viewer"})
	if f.workspace.Role != "owner" {
		t.Fatalf("create response role = %q, want owner", f.workspace.Role)
	}
	for name, want := range map[string]string{"owner": "owner", "admin": "admin", "viewer": "viewer"} {
		listed := perform(t, f.handler, http.MethodGet, "/api/v1/workspaces", f.token(name), "", nil)
		assertStatus(t, listed, http.StatusOK)
		items := decodeData[[]workspaceservice.Workspace](t, listed)
		if len(items) != 1 || items[0].ID != f.workspace.ID || string(items[0].Role) != want {
			t.Fatalf("%s workspace list = %+v, want role %s", name, items, want)
		}
		got := perform(t, f.handler, http.MethodGet, "/api/v1/workspaces/"+f.workspace.ID, f.token(name), "", nil)
		assertStatus(t, got, http.StatusOK)
		if workspace := decodeData[workspaceservice.Workspace](t, got); string(workspace.Role) != want {
			t.Fatalf("%s workspace role = %q, want %s", name, workspace.Role, want)
		}
	}

	listed := perform(t, f.handler, http.MethodGet, f.membersPath(), f.token("viewer"), "", nil)
	assertStatus(t, listed, http.StatusOK)
	members := decodeData[[]workspaceservice.Membership](t, listed)
	if len(members) != 3 {
		t.Fatalf("members = %+v", members)
	}
	for _, member := range members {
		var want authservice.User
		for _, pair := range f.users {
			if pair.User.ID == member.UserID {
				want = pair.User
			}
		}
		if want.ID == "" || member.Email != want.Email || member.DisplayName != want.DisplayName {
			t.Fatalf("member profile = %+v, want user %+v", member, want)
		}
	}
}

func TestMemberRoleChangeRoute(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, map[string]string{"admin": "admin", "manager": "manager", "target": "operator"})

	updated := perform(t, f.handler, http.MethodPatch, f.memberPath("target"), f.token("admin"), "", map[string]string{"role": "viewer"})
	assertStatus(t, updated, http.StatusOK)
	membership := decodeData[workspaceservice.Membership](t, updated)
	if membership.UserID != f.users["target"].User.ID || membership.Role != "viewer" || membership.Status != "active" || membership.Email != "team-target@example.com" {
		t.Fatalf("updated membership = %+v", membership)
	}

	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.memberPath("target"), f.token("manager"), "", map[string]string{"role": "operator"}), http.StatusForbidden, "forbidden")
	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.memberPath("target"), f.token("target"), "", map[string]string{"role": "admin"}), http.StatusForbidden, "forbidden")
	for _, role := range []string{"owner", "superuser", ""} {
		requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.memberPath("target"), f.token("owner"), "", map[string]string{"role": role}), http.StatusUnprocessableEntity, "invalid_role")
	}
	// Owners can only be changed by owners, and the last owner cannot be demoted.
	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.memberPath("owner"), f.token("admin"), "", map[string]string{"role": "viewer"}), http.StatusForbidden, "forbidden")
	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.memberPath("owner"), f.token("owner"), "", map[string]string{"role": "admin"}), http.StatusConflict, "last_owner")
	unknown := f.membersPath() + "/00000000-0000-4000-8000-000000000000"
	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, unknown, f.token("owner"), "", map[string]string{"role": "viewer"}), http.StatusNotFound, "not_found")
	requireErrorCode(t, perform(t, f.handler, http.MethodPatch, f.membersPath()+"/not-a-user", f.token("owner"), "", map[string]string{"role": "viewer"}), http.StatusNotFound, "not_found")
	assertStatus(t, perform(t, f.handler, http.MethodPatch, f.memberPath("target"), f.token("owner"), "", map[string]string{"role": "viewer", "extra": "x"}), http.StatusBadRequest)
}

func TestMemberRemovalRouteRevokesDevicesAndAccess(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, map[string]string{"admin": "admin", "manager": "manager", "target": "operator"})
	device := f.registerDevice(t, "target")
	claim := func() *httptest.ResponseRecorder {
		return performDevice(t, f.handler, http.MethodPost, "/api/v1/agent/tasks/claim", device.Device.ID, device.Credential, nil)
	}
	assertStatus(t, claim(), http.StatusNoContent)

	requireErrorCode(t, perform(t, f.handler, http.MethodDelete, f.memberPath("target"), f.token("manager"), "", nil), http.StatusForbidden, "forbidden")
	requireErrorCode(t, perform(t, f.handler, http.MethodDelete, f.memberPath("owner"), f.token("admin"), "", nil), http.StatusForbidden, "forbidden")
	requireErrorCode(t, perform(t, f.handler, http.MethodDelete, f.memberPath("owner"), f.token("owner"), "", nil), http.StatusConflict, "last_owner")

	removed := perform(t, f.handler, http.MethodDelete, f.memberPath("target"), f.token("admin"), "", nil)
	assertStatus(t, removed, http.StatusNoContent)
	if removed.Body.Len() != 0 {
		t.Fatalf("204 response has a body: %q", removed.Body.String())
	}
	requireErrorCode(t, claim(), http.StatusUnauthorized, "invalid_device_credential")
	requireErrorCode(t, perform(t, f.handler, http.MethodGet, "/api/v1/workspaces/"+f.workspace.ID, f.token("target"), "", nil), http.StatusForbidden, "forbidden")
	devices := perform(t, f.handler, http.MethodGet, "/api/v1/devices", f.token("target"), "", nil)
	assertStatus(t, devices, http.StatusOK)
	if items := decodeData[[]deviceservice.Device](t, devices); len(items) != 1 || items[0].Status != "revoked" || items[0].RevokedAt == nil {
		t.Fatalf("removed member's devices = %+v", items)
	}
	listed := perform(t, f.handler, http.MethodGet, f.membersPath(), f.token("owner"), "", nil)
	for _, member := range decodeData[[]workspaceservice.Membership](t, listed) {
		if member.UserID == f.users["target"].User.ID {
			t.Fatal("removed member is still listed")
		}
	}
	requireErrorCode(t, perform(t, f.handler, http.MethodDelete, f.memberPath("target"), f.token("admin"), "", nil), http.StatusNotFound, "not_found")

	// Removed members can be added back; their revoked devices stay revoked.
	readded := perform(t, f.handler, http.MethodPost, f.membersPath(), f.token("owner"), "", map[string]string{"userId": f.users["target"].User.ID, "role": "viewer"})
	assertStatus(t, readded, http.StatusCreated)
	assertStatus(t, perform(t, f.handler, http.MethodGet, "/api/v1/workspaces/"+f.workspace.ID, f.token("target"), "", nil), http.StatusOK)
	requireErrorCode(t, claim(), http.StatusUnauthorized, "invalid_device_credential")
}
