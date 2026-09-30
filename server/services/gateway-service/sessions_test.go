package gatewayservice_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

func performWithUserAgent(t *testing.T, handler http.Handler, method, path, userAgent string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	return performFrom(t, handler, "127.0.0.1:12345", method, path, "", body, map[string]string{"User-Agent": userAgent})
}

func TestUsersListAndRevokeTheirSessions(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	first := register(t, handler, "sessions-owner@example.test")
	login := func(userAgent string) authservice.TokenPair {
		response := performWithUserAgent(t, handler, http.MethodPost, "/api/v1/auth/login", userAgent, map[string]string{
			"email": "sessions-owner@example.test", "password": "SecurePassword123",
		})
		assertStatus(t, response, http.StatusOK)
		return decodeData[authservice.TokenPair](t, response)
	}
	laptop, phone := login("Mozilla/5.0 (Windows NT 10.0) Chrome/128.0"), login("Mozilla/5.0 (iPhone) Safari/605.1")
	stranger := register(t, handler, "sessions-stranger@example.test")

	list := func(token string) []authservice.SessionSummary {
		t.Helper()
		response := perform(t, handler, http.MethodGet, "/api/v1/me/sessions", token, "", nil)
		assertStatus(t, response, http.StatusOK)
		return decodeData[[]authservice.SessionSummary](t, response)
	}
	sessions := list(first.AccessToken)
	if len(sessions) != 3 || sessions[0].ID != first.SessionID || !sessions[0].Current || sessions[1].Current || sessions[2].Current {
		t.Fatalf("sessions=%+v", sessions)
	}
	userAgents := map[string]string{}
	for _, session := range sessions {
		userAgents[session.ID] = session.UserAgent
		if session.IPAddress != "127.0.0.1" {
			t.Fatalf("session %s ip=%q", session.ID, session.IPAddress)
		}
	}
	if !strings.Contains(userAgents[phone.SessionID], "iPhone") || !strings.Contains(userAgents[laptop.SessionID], "Windows") {
		t.Fatalf("user agents=%v", userAgents)
	}
	if others := list(stranger.AccessToken); len(others) != 1 || others[0].ID != stranger.SessionID {
		t.Fatalf("stranger sees sessions %+v", others)
	}

	revoke := func(token, sessionID string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodDelete, "/api/v1/me/sessions/"+sessionID, token, "", nil)
	}
	for _, id := range []string{stranger.SessionID, uuid.NewString(), "not-a-uuid"} {
		assertErrorCode(t, revoke(first.AccessToken, id), http.StatusNotFound, "not_found")
	}
	assertStatus(t, revoke(first.AccessToken, phone.SessionID), http.StatusNoContent)
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me", phone.AccessToken, "", nil), http.StatusUnauthorized)
	assertStatus(t, perform(t, handler, http.MethodPost, "/api/v1/auth/refresh", "", "", map[string]string{"refreshToken": phone.RefreshToken}), http.StatusUnauthorized)
	assertErrorCode(t, revoke(first.AccessToken, phone.SessionID), http.StatusNotFound, "not_found")
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me", stranger.AccessToken, "", nil), http.StatusOK)

	revokedOthers := perform(t, handler, http.MethodPost, "/api/v1/me/sessions/revoke-others", first.AccessToken, "", nil)
	assertStatus(t, revokedOthers, http.StatusOK)
	if count := decodeData[map[string]int](t, revokedOthers)["revoked"]; count != 1 {
		t.Fatalf("revoked %d other sessions, want 1", count)
	}
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me", laptop.AccessToken, "", nil), http.StatusUnauthorized)
	assertStatus(t, perform(t, handler, http.MethodPost, "/api/v1/auth/refresh", "", "", map[string]string{"refreshToken": laptop.RefreshToken}), http.StatusUnauthorized)
	if remaining := list(first.AccessToken); len(remaining) != 1 || !remaining[0].Current {
		t.Fatalf("remaining sessions=%+v", remaining)
	}

	// Ending the current session is a logout.
	assertStatus(t, revoke(first.AccessToken, first.SessionID), http.StatusNoContent)
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me/sessions", first.AccessToken, "", nil), http.StatusUnauthorized)
}

func TestRevokingASessionClosesItsNotificationSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := memory.New()
	bus := realtime.NewMemory()
	defer bus.Close()
	handler := newNotificationTestGateway(ctx, store, bus)
	owner := register(t, handler, "socket-sessions@example.test")
	workspace := createTestWorkspace(t, handler, owner.AccessToken)
	loginResponse := perform(t, handler, http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{
		"email": "socket-sessions@example.test", "password": "SecurePassword123",
	})
	assertStatus(t, loginResponse, http.StatusOK)
	other := decodeData[authservice.TokenPair](t, loginResponse)

	ticketResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/notifications/socket-ticket", other.AccessToken, "", nil)
	assertStatus(t, ticketResponse, http.StatusCreated)
	ticket := decodeData[struct {
		Ticket string `json:"ticket"`
	}](t, ticketResponse)
	server := httptest.NewServer(handler)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{testNotificationProtocol, testNotificationTicketPrefix + ticket.Ticket}, HandshakeTimeout: 2 * time.Second}
	connection, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/notifications/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	readNotificationMessage(t, connection, "server.hello")

	assertStatus(t, perform(t, handler, http.MethodPost, "/api/v1/me/sessions/revoke-others", owner.AccessToken, "", nil), http.StatusOK)
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("notification socket of a revoked session stayed open")
	} else if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
		t.Fatal("notification socket of a revoked session was not closed")
	}
}
