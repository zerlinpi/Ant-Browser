package gatewayservice_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

const testAgentSubprotocol = "ant-browser-agent.v1"

type agentTestEnvelope struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlationId"`
	Payload       json.RawMessage `json:"payload"`
}

func TestAgentWebSocketDispatchAndStateEvents(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "agent-owner@example.com")
	workspace := createTestWorkspace(t, handler, owner.AccessToken)
	registration := createTestDevice(t, handler, owner.AccessToken, workspace.ID)
	instance := createTestInstance(t, handler, owner.AccessToken, workspace.ID, registration.Data.Device.ID)

	server := httptest.NewServer(handler)
	defer server.Close()
	connection := dialTestAgent(t, server.URL, registration.Data.Device.ID, registration.Data.Credential)
	defer connection.Close()

	hello := readAgentEnvelope(t, connection)
	if hello.Type != "server.hello" {
		t.Fatalf("first websocket message type %q, want server.hello", hello.Type)
	}

	commandResponse := perform(
		t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+instance.ID+"/commands",
		owner.AccessToken, "agent-live-start", map[string]interface{}{
			"action": "instance.start", "expectedVersion": instance.Version,
		},
	)
	assertStatus(t, commandResponse, http.StatusAccepted)
	dispatch := readAgentEnvelope(t, connection)
	if dispatch.Type != "command.dispatch" {
		t.Fatalf("websocket message type %q, want command.dispatch", dispatch.Type)
	}
	var command browserinstanceservice.Command
	if err := json.Unmarshal(dispatch.Payload, &command); err != nil {
		t.Fatal(err)
	}
	if command.DeviceID != registration.Data.Device.ID || command.InstanceID != instance.ID {
		t.Fatalf("command was not scoped to the connected device: %+v", command)
	}

	writeAgentMessage(t, connection, "event-accepted", "command.accepted", map[string]string{"commandId": command.ID})
	assertAgentAck(t, connection, "event-accepted")
	writeAgentMessage(t, connection, "event-running", "instance.observed", map[string]interface{}{
		"instanceId": instance.ID,
		"state":      "running",
		"metadata":   map[string]interface{}{"pid": 4242},
	})
	assertAgentAck(t, connection, "event-running")
	writeAgentMessage(t, connection, "event-completed", "command.completed", map[string]string{"commandId": command.ID})
	assertAgentAck(t, connection, "event-completed")

	instanceResponse := perform(
		t, handler, http.MethodGet,
		"/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+instance.ID,
		owner.AccessToken, "", nil,
	)
	assertStatus(t, instanceResponse, http.StatusOK)
	observed := decodeData[browserinstanceservice.BrowserInstance](t, instanceResponse)
	if observed.ObservedState != "running" || observed.LastSeenAt == nil {
		t.Fatalf("observed state was not persisted: %+v", observed)
	}

	second := dialTestAgent(t, server.URL, registration.Data.Device.ID, registration.Data.Credential)
	defer second.Close()
	if replayHello := readAgentEnvelope(t, second); replayHello.Type != "server.hello" {
		t.Fatalf("reconnect first message type %q, want server.hello", replayHello.Type)
	}
	if err := second.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var unexpected agentTestEnvelope
	if err := second.ReadJSON(&unexpected); err == nil {
		t.Fatalf("completed command was replayed on reconnect: %+v", unexpected)
	}
}

func TestAgentWebSocketRejectsInvalidCredential(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newTestGateway())
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{testAgentSubprotocol}}
	headers := http.Header{}
	headers.Set("X-Device-ID", "00000000-0000-0000-0000-000000000000")
	headers.Set("Authorization", "Device invalid")
	connection, response, err := dialer.Dial(agentWebSocketURL(server.URL), headers)
	if connection != nil {
		connection.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid credential handshake response=%v err=%v", response, err)
	}
}

func TestAgentCommandDispatchCrossesGatewayNodes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := memory.New()
	bus := realtime.NewMemory()
	defer bus.Close()
	apiNode := newTestGatewayNode(ctx, store, bus)
	agentNode := newTestGatewayNode(ctx, store, bus)

	owner := register(t, apiNode, "cross-node-owner@example.com")
	workspace := createTestWorkspace(t, apiNode, owner.AccessToken)
	registration := createTestDevice(t, apiNode, owner.AccessToken, workspace.ID)
	instance := createTestInstance(t, apiNode, owner.AccessToken, workspace.ID, registration.Data.Device.ID)
	server := httptest.NewServer(agentNode)
	defer server.Close()
	connection := dialTestAgent(t, server.URL, registration.Data.Device.ID, registration.Data.Credential)
	defer connection.Close()
	if hello := readAgentEnvelope(t, connection); hello.Type != "server.hello" {
		t.Fatalf("first websocket message type %q, want server.hello", hello.Type)
	}

	response := perform(
		t, apiNode, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+instance.ID+"/commands",
		owner.AccessToken, "cross-node-command", map[string]interface{}{
			"action": "instance.start", "expectedVersion": instance.Version,
		},
	)
	assertStatus(t, response, http.StatusAccepted)
	if dispatch := readAgentEnvelope(t, connection); dispatch.Type != "command.dispatch" {
		t.Fatalf("cross-node websocket message type %q, want command.dispatch", dispatch.Type)
	}
}

func newTestGatewayNode(ctx context.Context, store *memory.Store, bus realtime.Bus) http.Handler {
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	auth := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	workspaces := workspaceservice.New(store)
	devices := deviceservice.New(store, security.NewOpaqueToken, workspaces)
	instances := browserinstanceservice.New(store, workspaces)
	tasks := taskservice.New(store, workspaces)
	fingerprints := fingerprintservice.New(store, workspaces)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return gatewayservice.NewWithInfrastructure(
		ctx, auth, workspaces, devices, instances, tokens, store,
		bus, tasks, taskwake.NewDisabled(), fingerprints, nil, nil, nil, nil, logger,
	)
}

func createTestWorkspace(t *testing.T, handler http.Handler, accessToken string) workspaceservice.Workspace {
	t.Helper()
	response := perform(t, handler, http.MethodPost, "/api/v1/workspaces", accessToken, "", map[string]string{"name": "Agent Workspace"})
	assertStatus(t, response, http.StatusCreated)
	return decodeData[workspaceservice.Workspace](t, response)
}

func createTestDevice(t *testing.T, handler http.Handler, accessToken, workspaceID string) envelope[deviceservice.Registration] {
	t.Helper()
	response := perform(t, handler, http.MethodPost, "/api/v1/devices", accessToken, "", map[string]interface{}{
		"workspaceId": workspaceID, "name": "Agent workstation", "platform": "windows",
		"agentVersion": "0.1.0", "capabilities": map[string]interface{}{"chromium": true},
	})
	assertStatus(t, response, http.StatusCreated)
	var registration envelope[deviceservice.Registration]
	decodeResponse(t, response, &registration)
	return registration
}

func createTestInstance(t *testing.T, handler http.Handler, accessToken, workspaceID, deviceID string) browserinstanceservice.BrowserInstance {
	t.Helper()
	response := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/browser-instances", accessToken, "", map[string]interface{}{
		"name": "Agent Instance", "platform": "chromium", "assignedDeviceId": deviceID,
	})
	assertStatus(t, response, http.StatusCreated)
	return decodeData[browserinstanceservice.BrowserInstance](t, response)
}

func dialTestAgent(t *testing.T, serverURL, deviceID, credential string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{testAgentSubprotocol}, HandshakeTimeout: 2 * time.Second}
	headers := http.Header{}
	headers.Set("X-Device-ID", deviceID)
	headers.Set("Authorization", "Device "+credential)
	connection, response, err := dialer.Dial(agentWebSocketURL(serverURL), headers)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial agent websocket: status=%d err=%v", status, err)
	}
	return connection
}

func agentWebSocketURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/agent/ws"
}

func readAgentEnvelope(t *testing.T, connection *websocket.Conn) agentTestEnvelope {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message agentTestEnvelope
	if err := connection.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	return message
}

func writeAgentMessage(t *testing.T, connection *websocket.Conn, id, messageType string, payload interface{}) {
	t.Helper()
	if err := connection.WriteJSON(map[string]interface{}{"id": id, "type": messageType, "payload": payload}); err != nil {
		t.Fatal(err)
	}
}

func assertAgentAck(t *testing.T, connection *websocket.Conn, correlationID string) {
	t.Helper()
	message := readAgentEnvelope(t, connection)
	if message.Type != "server.ack" || message.CorrelationID != correlationID {
		t.Fatalf("agent acknowledgement = %+v", message)
	}
}
