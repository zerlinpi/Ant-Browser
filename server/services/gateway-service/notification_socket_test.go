package gatewayservice_test

import (
	"context"
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
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

const (
	testNotificationProtocol     = "ant-browser-notifications.v1"
	testNotificationTicketPrefix = "ant-browser-ticket."
)

func TestNotificationWebSocketUsesShortLivedTicketAndCrossNodeBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := memory.New()
	bus := realtime.NewMemory()
	defer bus.Close()
	apiNode := newNotificationTestGateway(ctx, store, bus)
	socketNode := newNotificationTestGateway(ctx, store, bus)
	owner := register(t, apiNode, "notification-socket@example.com")
	workspace := createTestWorkspace(t, apiNode, owner.AccessToken)

	ticketResponse := perform(t, apiNode, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/notifications/socket-ticket",
		owner.AccessToken, "", nil)
	assertStatus(t, ticketResponse, http.StatusCreated)
	var ticket envelope[struct {
		Ticket      string    `json:"ticket"`
		ExpiresAt   time.Time `json:"expiresAt"`
		Subprotocol string    `json:"subprotocol"`
	}]
	decodeResponse(t, ticketResponse, &ticket)
	if ticket.Data.Ticket == "" || ticket.Data.Subprotocol != testNotificationProtocol || !ticket.Data.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid ticket response: %+v", ticket.Data)
	}

	server := httptest.NewServer(socketNode)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{
		testNotificationProtocol, testNotificationTicketPrefix + ticket.Data.Ticket,
	}, HandshakeTimeout: 2 * time.Second}
	connection, response, err := dialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/notifications/ws", nil,
	)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial notification socket: status=%d err=%v", status, err)
	}
	defer connection.Close()
	if connection.Subprotocol() != testNotificationProtocol {
		t.Fatalf("negotiated subprotocol=%q", connection.Subprotocol())
	}
	readNotificationMessage(t, connection, "server.hello")

	payload := []byte(`{"type":"notification.created","data":{"id":"notification-1"}}`)
	if err := bus.PublishNotification(ctx, realtime.UserNotification{
		WorkspaceID: workspace.ID, UserID: owner.User.ID, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	message := readNotificationMessage(t, connection, "notification.created")
	if message.Data["id"] != "notification-1" {
		t.Fatalf("unexpected notification payload: %+v", message)
	}
}

func TestNotificationWebSocketRejectsAccessTokenInPlaceOfTicket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := memory.New()
	bus := realtime.NewMemory()
	defer bus.Close()
	handler := newNotificationTestGateway(ctx, store, bus)
	owner := register(t, handler, "notification-invalid-ticket@example.com")
	server := httptest.NewServer(handler)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{
		testNotificationProtocol, testNotificationTicketPrefix + owner.AccessToken,
	}}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/notifications/ws", nil)
	if connection != nil {
		connection.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("access token socket response=%v err=%v", response, err)
	}
}

type notificationTestMessage struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

func readNotificationMessage(t *testing.T, connection *websocket.Conn, expectedType string) notificationTestMessage {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message notificationTestMessage
	if err := connection.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	if message.Type != expectedType {
		t.Fatalf("message type=%q want %q", message.Type, expectedType)
	}
	return message
}

func newNotificationTestGateway(ctx context.Context, store *memory.Store, bus realtime.Bus) http.Handler {
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	auth := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	workspaces := workspaceservice.New(store)
	devices := deviceservice.New(store, security.NewOpaqueToken, workspaces)
	instances := browserinstanceservice.New(store, workspaces)
	notifications := notificationservice.New(store, workspaces)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return gatewayservice.NewWithInfrastructure(
		ctx, auth, workspaces, devices, instances, tokens, store,
		bus, nil, taskwake.NewDisabled(), nil, nil, nil, nil, nil, logger,
		notifications,
	)
}
