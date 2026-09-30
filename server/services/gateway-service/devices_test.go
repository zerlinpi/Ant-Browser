package gatewayservice_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func agentSocketURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/agent/ws"
}

// dialAgent opens the agent WebSocket and returns the handshake status.
func dialAgent(serverURL, deviceID, credential string) (*websocket.Conn, int, error) {
	dialer := websocket.Dialer{Subprotocols: []string{"ant-browser-agent.v1"}, HandshakeTimeout: 2 * time.Second}
	headers := http.Header{}
	headers.Set("X-Device-ID", deviceID)
	headers.Set("Authorization", "Device "+credential)
	connection, response, err := dialer.Dial(agentSocketURL(serverURL), headers)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	return connection, status, err
}

func TestDeviceRegistrationRequiresInstanceOperate(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, map[string]string{"viewer": "viewer", "operator": "operator"})
	body := map[string]interface{}{"workspaceId": f.workspace.ID, "name": "Agent", "platform": "linux"}
	requireErrorCode(t, perform(t, f.handler, http.MethodPost, "/api/v1/devices", f.token("viewer"), "", body), http.StatusForbidden, "forbidden")
	registered := perform(t, f.handler, http.MethodPost, "/api/v1/devices", f.token("operator"), "", body)
	assertStatus(t, registered, http.StatusCreated)
	if registration := decodeData[deviceservice.Registration](t, registered); registration.Credential == "" || registration.Device.UserID != f.users["operator"].User.ID {
		t.Fatalf("operator registration = %+v", registration)
	}
}

func TestRotateDeviceCredentialRoute(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, map[string]string{"operator": "operator"})
	device := f.registerDevice(t, "operator")
	server := httptest.NewServer(f.handler)
	defer server.Close()

	live, status, err := dialAgent(server.URL, device.Device.ID, device.Credential)
	if err != nil {
		t.Fatalf("dial with original credential: status=%d err=%v", status, err)
	}
	defer live.Close()
	if err := live.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Type string `json:"type"`
	}
	if err := live.ReadJSON(&hello); err != nil || hello.Type != "server.hello" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}

	rotatePath := "/api/v1/devices/" + device.Device.ID + "/rotate-credential"
	requireErrorCode(t, perform(t, f.handler, http.MethodPost, rotatePath, f.token("owner"), "", nil), http.StatusNotFound, "not_found")
	rotatedResponse := perform(t, f.handler, http.MethodPost, rotatePath, f.token("operator"), "", nil)
	assertStatus(t, rotatedResponse, http.StatusOK)
	if rotatedResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response must not be cached")
	}
	rotated := decodeData[deviceservice.Registration](t, rotatedResponse)
	if rotated.Device.ID != device.Device.ID || rotated.Credential == "" || rotated.Credential == device.Credential {
		t.Fatalf("rotation = %+v", rotated)
	}

	// The socket authenticated with the old credential is closed.
	if err := live.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var message map[string]interface{}
		err := live.ReadJSON(&message)
		if err == nil {
			continue // drain anything queued before the close
		}
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("agent socket stayed open after credential rotation")
		}
		break
	}

	claimPath := "/api/v1/agent/tasks/claim"
	requireErrorCode(t, performDevice(t, f.handler, http.MethodPost, claimPath, device.Device.ID, device.Credential, nil), http.StatusUnauthorized, "invalid_device_credential")
	assertStatus(t, performDevice(t, f.handler, http.MethodPost, claimPath, device.Device.ID, rotated.Credential, nil), http.StatusNoContent)
	if connection, status, err := dialAgent(server.URL, device.Device.ID, device.Credential); err == nil || status != http.StatusUnauthorized {
		if connection != nil {
			connection.Close()
		}
		t.Fatalf("old credential handshake status=%d err=%v", status, err)
	}
	fresh, status, err := dialAgent(server.URL, device.Device.ID, rotated.Credential)
	if err != nil {
		t.Fatalf("new credential handshake status=%d err=%v", status, err)
	}
	fresh.Close()

	requireErrorCode(t, perform(t, f.handler, http.MethodPost, "/api/v1/devices/not-a-device/rotate-credential", f.token("operator"), "", nil), http.StatusNotFound, "not_found")
	assertStatus(t, perform(t, f.handler, http.MethodDelete, "/api/v1/devices/"+device.Device.ID, f.token("operator"), "", nil), http.StatusNoContent)
	requireErrorCode(t, perform(t, f.handler, http.MethodPost, rotatePath, f.token("operator"), "", nil), http.StatusForbidden, "resource_disabled")
	requireErrorCode(t, performDevice(t, f.handler, http.MethodPost, claimPath, device.Device.ID, rotated.Credential, nil), http.StatusUnauthorized, "invalid_device_credential")
}

func TestRevokeDeviceClosesLiveAgentSocket(t *testing.T) {
	t.Parallel()
	f := newTeamFixture(t, nil)
	device := f.registerDevice(t, "owner")
	server := httptest.NewServer(f.handler)
	defer server.Close()
	live, status, err := dialAgent(server.URL, device.Device.ID, device.Credential)
	if err != nil {
		t.Fatalf("dial: status=%d err=%v", status, err)
	}
	defer live.Close()
	assertStatus(t, perform(t, f.handler, http.MethodDelete, "/api/v1/devices/"+device.Device.ID, f.token("owner"), "", nil), http.StatusNoContent)
	if err := live.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var message map[string]interface{}
		err := live.ReadJSON(&message)
		if err == nil {
			continue
		}
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("agent socket stayed open after revocation")
		}
		return
	}
}

// outageRepository fails device authentication like an unreachable database
// while failing is set; everything else is served by the memory store.
type outageRepository struct {
	*memory.Store
	failing atomic.Bool
}

func (r *outageRepository) AuthenticateDevice(ctx context.Context, deviceID, credentialHash string) (deviceservice.Device, error) {
	if r.failing.Load() {
		return deviceservice.Device{}, errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")
	}
	return r.Store.AuthenticateDevice(ctx, deviceID, credentialHash)
}

func TestAgentRoutesReportAuthenticationOutageAs503(t *testing.T) {
	t.Parallel()
	store := memory.New()
	repository := &outageRepository{Store: store}
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	auth := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	workspaces := workspaceservice.New(store)
	devices := deviceservice.New(repository, security.NewOpaqueToken, workspaces)
	instances := browserinstanceservice.New(store, workspaces)
	tasks := taskservice.New(store, workspaces)
	fingerprints := fingerprintservice.New(store, workspaces)
	profiles := profilesyncservice.New(store, workspaces, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{}, "test-profile-key", "metadata")
	handler := gatewayservice.NewWithInfrastructure(
		context.Background(), auth, workspaces, devices, instances, tokens, store,
		realtime.NewDisabled(), tasks, taskwake.NewDisabled(), fingerprints, profiles, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	owner := register(t, handler, "outage-owner@example.com")
	created := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Outage"})
	assertStatus(t, created, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, created)
	registered := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Agent", "platform": "linux",
	})
	assertStatus(t, registered, http.StatusCreated)
	device := decodeData[deviceservice.Registration](t, registered)
	server := httptest.NewServer(handler)
	defer server.Close()

	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/agent/tasks/claim"},
		{http.MethodPost, "/api/v1/agent/tasks/00000000-0000-4000-8000-000000000001/report"},
		{http.MethodGet, "/api/v1/agent/instances/00000000-0000-4000-8000-000000000002/runtime-config"},
		{http.MethodGet, "/api/v1/agent/profiles/00000000-0000-4000-8000-000000000003"},
	}
	repository.failing.Store(true)
	for _, route := range routes {
		requireErrorCode(t, performDevice(t, handler, route.method, route.path, device.Device.ID, device.Credential, nil), http.StatusServiceUnavailable, "dependency_unavailable")
	}
	if connection, status, err := dialAgent(server.URL, device.Device.ID, device.Credential); err == nil || status != http.StatusServiceUnavailable {
		if connection != nil {
			connection.Close()
		}
		t.Fatalf("websocket handshake during outage: status=%d err=%v", status, err)
	}
	// Requests without a usable credential never reach the repository and are
	// still rejected as unauthorized.
	requireErrorCode(t, performDevice(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", "not-a-device", device.Credential, nil), http.StatusUnauthorized, "invalid_device_credential")

	repository.failing.Store(false)
	assertStatus(t, performDevice(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", device.Device.ID, device.Credential, nil), http.StatusNoContent)
	requireErrorCode(t, performDevice(t, handler, http.MethodPost, "/api/v1/agent/tasks/claim", device.Device.ID, "wrong-secret", nil), http.StatusUnauthorized, "invalid_device_credential")
}
