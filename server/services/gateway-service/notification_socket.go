package gatewayservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
)

const (
	notificationSubprotocol  = "ant-browser-notifications.v1"
	notificationTicketPrefix = "ant-browser-ticket."
	notificationTicketTTL    = time.Minute
	notificationWriteWait    = 10 * time.Second
	notificationPongWait     = 60 * time.Second
	notificationPingPeriod   = 25 * time.Second
	notificationMaxMessage   = 1 << 20
	// notificationSessionCheck bounds how long a socket outlives its session
	// when the session is revoked on another node or simply expires.
	notificationSessionCheck = time.Minute
)

type notificationHub struct {
	mu      sync.RWMutex
	clients map[string]map[*notificationClient]struct{}
}

type notificationClient struct {
	workspaceID string
	userID      string
	sessionID   string
	conn        *websocket.Conn
	send        chan []byte
	done        chan struct{}
	once        sync.Once
}

func newNotificationHub() *notificationHub {
	return &notificationHub{clients: make(map[string]map[*notificationClient]struct{})}
}

func notificationRecipientKey(workspaceID, userID string) string {
	return workspaceID + "\x00" + userID
}

func (h *notificationHub) register(client *notificationClient) {
	h.mu.Lock()
	key := notificationRecipientKey(client.workspaceID, client.userID)
	if h.clients[key] == nil {
		h.clients[key] = make(map[*notificationClient]struct{})
	}
	h.clients[key][client] = struct{}{}
	h.mu.Unlock()
}

func (h *notificationHub) unregister(client *notificationClient) {
	h.mu.Lock()
	key := notificationRecipientKey(client.workspaceID, client.userID)
	delete(h.clients[key], client)
	if len(h.clients[key]) == 0 {
		delete(h.clients, key)
	}
	h.mu.Unlock()
}

// closeSessions closes this node's sockets opened by the given sessions of
// one user, in every workspace.
func (h *notificationHub) closeSessions(userID string, sessionIDs ...string) {
	if len(sessionIDs) == 0 {
		return
	}
	revoked := make(map[string]struct{}, len(sessionIDs))
	for _, id := range sessionIDs {
		revoked[id] = struct{}{}
	}
	h.mu.RLock()
	var clients []*notificationClient
	for _, set := range h.clients {
		for client := range set {
			if _, ok := revoked[client.sessionID]; ok && client.userID == userID {
				clients = append(clients, client)
			}
		}
	}
	h.mu.RUnlock()
	for _, client := range clients {
		client.close()
	}
}

func (h *notificationHub) dispatch(value realtime.UserNotification) {
	h.mu.RLock()
	set := h.clients[notificationRecipientKey(value.WorkspaceID, value.UserID)]
	clients := make([]*notificationClient, 0, len(set))
	for client := range set {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		if !client.enqueue(value.Payload) {
			client.close()
		}
	}
}

func (c *notificationClient) enqueue(payload []byte) bool {
	select {
	case <-c.done:
		return false
	case c.send <- append([]byte(nil), payload...):
		return true
	default:
		return false
	}
}

func (c *notificationClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (g *Gateway) issueNotificationSocketTicket(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	identity := mustPrincipal(r.Context())
	if err := g.notifications.Authorize(r.Context(), identity.UserID, workspaceID); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	ticket, expiresAt, err := g.tokens.IssueNotificationTicket(identity.UserID, identity.SessionID, workspaceID, notificationTicketTTL)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": map[string]interface{}{
		"ticket": ticket, "expiresAt": expiresAt, "subprotocol": notificationSubprotocol,
	}})
}

func (g *Gateway) notificationSocket(w http.ResponseWriter, r *http.Request) {
	ticket, protocolOK := notificationSocketTicket(r)
	if !protocolOK {
		w.Header().Set("Sec-WebSocket-Protocol", notificationSubprotocol)
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUpgradeRequired, Code: "notification_protocol_required",
			Message: "Notification WebSocket protocol and ticket are required",
		})
		return
	}
	claims, err := g.tokens.ParseNotificationTicket(ticket)
	if err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_socket_ticket", Message: "Notification socket ticket is invalid or expired"})
		return
	}
	ctx := postgres.WithTenantScope(r.Context(), postgres.TenantScope{
		WorkspaceID: claims.WorkspaceID, UserID: claims.UserID,
	})
	if err := g.auth.ValidateSession(ctx, claims.UserID, claims.SessionID); err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_socket_ticket", Message: "Notification socket session is no longer active"})
		return
	}
	if err := g.notifications.Authorize(ctx, claims.UserID, claims.WorkspaceID); err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusForbidden, Code: "forbidden", Message: "Notification workspace access is denied"})
		return
	}
	r = r.WithContext(ctx)
	upgrader := websocket.Upgrader{
		HandshakeTimeout: notificationWriteWait,
		ReadBufferSize:   4096, WriteBufferSize: 4096,
		Subprotocols: []string{notificationSubprotocol}, CheckOrigin: g.originAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &notificationClient{
		workspaceID: claims.WorkspaceID, userID: claims.UserID, sessionID: claims.SessionID, conn: conn,
		send: make(chan []byte, 128), done: make(chan struct{}),
	}
	g.notificationHub.register(client)
	defer func() {
		g.notificationHub.unregister(client)
		client.close()
	}()
	go client.writePump()
	go g.watchNotificationSession(ctx, client)
	hello, _ := json.Marshal(map[string]interface{}{
		"type": "server.hello", "sentAt": time.Now().UTC(),
		"data": map[string]string{"workspaceId": claims.WorkspaceID, "protocol": notificationSubprotocol},
	})
	if !client.enqueue(hello) {
		return
	}
	client.readPump()
}

// watchNotificationSession closes the socket once its session is no longer
// active. Revocations on this node close sockets at once (closeSessions);
// this check covers other nodes and expiry.
func (g *Gateway) watchNotificationSession(ctx context.Context, client *notificationClient) {
	ticker := time.NewTicker(notificationSessionCheck)
	defer ticker.Stop()
	for {
		select {
		case <-client.done:
			return
		case <-ticker.C:
			if err := g.auth.ValidateSession(ctx, client.userID, client.sessionID); err != nil {
				client.close()
				return
			}
		}
	}
}

func (g *Gateway) subscribeNotifications(ctx context.Context) {
	err := g.realtime.SubscribeNotifications(ctx, func(value realtime.UserNotification) {
		if value.WorkspaceID == "" || value.UserID == "" || len(value.Payload) == 0 || len(value.Payload) > notificationMaxMessage || !json.Valid(value.Payload) {
			g.logger.Warn("realtime_notification_rejected", "workspace_id", value.WorkspaceID, "user_id", value.UserID)
			return
		}
		g.notificationHub.dispatch(value)
	})
	if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		g.logger.ErrorContext(ctx, "realtime_notification_subscription_failed", "error", err)
	}
}

func notificationSocketTicket(r *http.Request) (string, bool) {
	hasProtocol := false
	ticket := ""
	for _, protocol := range websocket.Subprotocols(r) {
		switch {
		case protocol == notificationSubprotocol:
			hasProtocol = true
		case strings.HasPrefix(protocol, notificationTicketPrefix):
			ticket = strings.TrimPrefix(protocol, notificationTicketPrefix)
		}
	}
	return ticket, hasProtocol && ticket != "" && len(ticket) <= 4096
}

func (c *notificationClient) readPump() {
	c.conn.SetReadLimit(1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(notificationPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(notificationPongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "notification socket is receive-only"),
			time.Now().Add(notificationWriteWait),
		)
		return
	}
}

func (c *notificationClient) writePump() {
	ticker := time.NewTicker(notificationPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(notificationWriteWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				c.close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(notificationWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}
