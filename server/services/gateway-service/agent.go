package gatewayservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
)

const (
	agentSubprotocol = "ant-browser-agent.v1"
	agentWriteWait   = 10 * time.Second
	agentPongWait    = 60 * time.Second
	agentPingPeriod  = 25 * time.Second
	agentMaxMessage  = 1 << 20
	agentPresenceTTL = 75 * time.Second
)

type agentHub struct {
	mu      sync.RWMutex
	clients map[string]*agentClient
}

type agentClient struct {
	deviceID    string
	workspaceID string
	ctx         context.Context
	conn        *websocket.Conn
	send        chan agentOutbound
	done        chan struct{}
	once        sync.Once
	presence    realtime.DevicePresence
}

type agentOutbound struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	CorrelationID string      `json:"correlationId,omitempty"`
	SentAt        time.Time   `json:"sentAt"`
	Payload       interface{} `json:"payload,omitempty"`
}

type agentInbound struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type agentHelloPayload struct {
	AgentVersion string                 `json:"agentVersion"`
	Capabilities map[string]interface{} `json:"capabilities"`
}

type agentCommandEventPayload struct {
	CommandID      string `json:"commandId"`
	FailureCode    string `json:"failureCode,omitempty"`
	FailureMessage string `json:"failureMessage,omitempty"`
}

type agentObservedStatePayload struct {
	InstanceID string                 `json:"instanceId"`
	State      string                 `json:"state"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type agentProtocolProblem struct {
	message string
}

func (p agentProtocolProblem) Error() string { return p.message }

func newAgentHub() *agentHub {
	return &agentHub{clients: make(map[string]*agentClient)}
}

func (h *agentHub) register(client *agentClient) {
	h.mu.Lock()
	previous := h.clients[client.deviceID]
	h.clients[client.deviceID] = client
	h.mu.Unlock()
	if previous != nil && previous != client {
		previous.close()
	}
}

func (h *agentHub) unregister(client *agentClient) {
	h.mu.Lock()
	if h.clients[client.deviceID] == client {
		delete(h.clients, client.deviceID)
	}
	h.mu.Unlock()
}

func (h *agentHub) dispatch(command browserinstanceservice.Command) bool {
	if command.DeviceID == "" {
		return false
	}
	h.mu.RLock()
	client := h.clients[command.DeviceID]
	h.mu.RUnlock()
	if client == nil {
		return false
	}
	return client.enqueue("command.dispatch", "", command)
}

func (c *agentClient) enqueue(messageType, correlationID string, payload interface{}) bool {
	message := agentOutbound{
		ID: uuid.NewString(), Type: messageType, CorrelationID: correlationID,
		SentAt: time.Now().UTC(), Payload: payload,
	}
	select {
	case <-c.done:
		return false
	case c.send <- message:
		return true
	default:
		return false
	}
}

func (c *agentClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (g *Gateway) agentSocket(w http.ResponseWriter, r *http.Request) {
	if !supportsAgentSubprotocol(r) {
		w.Header().Set("Sec-WebSocket-Protocol", agentSubprotocol)
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUpgradeRequired, Code: "agent_protocol_required",
			Message: "WebSocket subprotocol ant-browser-agent.v1 is required",
		})
		return
	}
	device, err := g.authenticateAgent(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUnauthorized, Code: "invalid_device_credential",
			Message: "Device credential is invalid or revoked",
		})
		return
	}
	// Authentication is a constrained device-auth operation. All subsequent
	// repository calls run with the device's workspace scope.
	r = r.WithContext(postgres.WithTenantScope(r.Context(), postgres.TenantScope{WorkspaceID: device.WorkspaceID}))

	upgrader := websocket.Upgrader{
		HandshakeTimeout: agentWriteWait,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		Subprotocols:     []string{agentSubprotocol},
		CheckOrigin:      g.originAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &agentClient{
		deviceID: device.ID, workspaceID: device.WorkspaceID,
		ctx:  r.Context(),
		conn: conn, send: make(chan agentOutbound, 128), done: make(chan struct{}),
	}
	now := time.Now().UTC()
	client.presence = realtime.DevicePresence{
		DeviceID: device.ID, WorkspaceID: device.WorkspaceID, NodeID: g.nodeID,
		ConnectionID: uuid.NewString(), ConnectedAt: now, LastSeenAt: now,
	}
	g.agentHub.register(client)
	defer func() {
		g.agentHub.unregister(client)
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if removeErr := g.realtime.RemovePresence(cleanupContext, client.deviceID, client.presence.ConnectionID); removeErr != nil {
			g.logger.Warn("agent_presence_remove_failed", "device_id", client.deviceID, "error", removeErr)
		}
		client.close()
	}()
	if err := g.realtime.SetPresence(r.Context(), client.presence, agentPresenceTTL); err != nil {
		g.logger.WarnContext(r.Context(), "agent_presence_set_failed", "device_id", device.ID, "error", err)
	}

	if _, heartbeatErr := g.devices.Heartbeat(r.Context(), device.ID, device.AgentVersion, nil); heartbeatErr != nil {
		g.logger.WarnContext(r.Context(), "agent_initial_heartbeat_failed", "device_id", device.ID, "error", heartbeatErr)
		return
	}
	go client.writePump()
	go client.presencePump(g)
	if !client.enqueue("server.hello", "", map[string]interface{}{
		"deviceId": device.ID, "workspaceId": device.WorkspaceID,
		"connectionId": client.presence.ConnectionID,
		"protocol":     agentSubprotocol, "heartbeatIntervalSeconds": int(agentPingPeriod.Seconds()),
	}) {
		return
	}
	commands, err := g.instances.PendingCommands(r.Context(), device.WorkspaceID, device.ID)
	if err != nil {
		g.logger.ErrorContext(r.Context(), "agent_pending_commands_failed", "device_id", device.ID, "error", err)
		client.enqueue("server.error", "", map[string]string{"code": "pending_commands_unavailable", "message": "Pending commands could not be loaded"})
		return
	}
	for _, command := range commands {
		if !client.enqueue("command.dispatch", "", command) {
			g.logger.WarnContext(r.Context(), "agent_send_queue_full", "device_id", device.ID)
			return
		}
	}
	client.readPump(g)
}

func (g *Gateway) subscribeCommands(ctx context.Context) {
	err := g.realtime.SubscribeCommands(ctx, func(deviceID string, payload []byte) {
		var command browserinstanceservice.Command
		if err := json.Unmarshal(payload, &command); err != nil || command.ID == "" || command.DeviceID != deviceID {
			g.logger.Warn("realtime_command_rejected", "device_id", deviceID)
			return
		}
		g.agentHub.dispatch(command)
	})
	if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		g.logger.ErrorContext(ctx, "realtime_command_subscription_failed", "error", err)
	}
}

func (g *Gateway) authenticateAgent(r *http.Request) (deviceservice.Device, error) {
	// Agent routes authenticate once in withAgentAuthentication (devices.go),
	// which answers infrastructure failures with 503; reuse its result.
	if cached, ok := cachedAgentAuthentication(r); ok {
		return cached.device, cached.err
	}
	deviceID := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(header, " ", 2)
	if deviceID == "" || len(parts) != 2 || !strings.EqualFold(parts[0], "Device") {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	credential := strings.TrimSpace(parts[1])
	if credential == "" || len(credential) > 512 {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	return g.devices.Authenticate(r.Context(), deviceID, security.HashOpaqueToken(credential))
}

func (c *agentClient) readPump(g *Gateway) {
	c.conn.SetReadLimit(agentMaxMessage)
	_ = c.conn.SetReadDeadline(time.Now().Add(agentPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(agentPongWait))
	})
	for {
		var message agentInbound
		if err := c.conn.ReadJSON(&message); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				g.logger.Debug("agent_socket_closed", "device_id", c.deviceID, "error", err)
			}
			return
		}
		if err := c.handleMessage(g, message); err != nil {
			code, publicMessage, terminal := agentPublicError(err)
			if code == "internal_error" {
				g.logger.Error("agent_event_failed", "device_id", c.deviceID, "message_type", message.Type, "error", err)
			}
			if terminal {
				_ = c.conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, publicMessage),
					time.Now().Add(agentWriteWait),
				)
				return
			}
			if !c.enqueue("server.error", message.ID, map[string]string{"code": code, "message": publicMessage}) {
				return
			}
			continue
		}
		if !c.enqueue("server.ack", message.ID, map[string]string{"type": message.Type}) {
			return
		}
	}
}

func (c *agentClient) handleMessage(g *Gateway, message agentInbound) error {
	switch message.Type {
	case "agent.hello", "agent.heartbeat":
		var payload agentHelloPayload
		if len(message.Payload) > 0 {
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return agentProtocolProblem{message: "heartbeat payload is invalid"}
			}
		}
		_, err := g.devices.Heartbeat(c.connContext(), c.deviceID, payload.AgentVersion, payload.Capabilities)
		return err
	case "command.accepted", "command.running", "command.completed", "command.failed":
		var payload agentCommandEventPayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil || strings.TrimSpace(payload.CommandID) == "" {
			return agentProtocolProblem{message: "command event payload is invalid"}
		}
		updated, err := g.instances.ApplyAgentCommandEvent(
			c.connContext(), c.workspaceID, c.deviceID, payload.CommandID,
			browserinstanceservice.AgentCommandEventInput{
				Status:      strings.TrimPrefix(message.Type, "command."),
				FailureCode: payload.FailureCode, FailureMessage: payload.FailureMessage,
			},
		)
		if err == nil && updated.Action == "instance.migrate" && updated.Status == "completed" {
			targetDeviceID, _ := updated.Payload["targetDeviceId"].(string)
			commands, pendingErr := g.instances.PendingCommands(c.connContext(), c.workspaceID, targetDeviceID)
			if pendingErr != nil {
				return pendingErr
			}
			followUpKey := browserinstanceservice.MigrationStartIdempotencyKey(updated.ID)
			for _, command := range commands {
				if command.IdempotencyKey == followUpKey {
					g.dispatchInstanceCommand(c.connContext(), command)
					break
				}
			}
		}
		return err
	case "instance.observed":
		var payload agentObservedStatePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil || strings.TrimSpace(payload.InstanceID) == "" {
			return agentProtocolProblem{message: "observed state payload is invalid"}
		}
		_, err := g.instances.ApplyObservedState(
			c.connContext(), c.workspaceID, c.deviceID,
			payload.InstanceID, payload.State, payload.Metadata,
		)
		return err
	default:
		return agentProtocolProblem{message: "agent message type is unsupported"}
	}
}

func agentPublicError(err error) (code, message string, terminal bool) {
	var protocolProblem agentProtocolProblem
	switch {
	case errors.As(err, &protocolProblem):
		return "invalid_agent_message", protocolProblem.message, false
	case errors.Is(err, browserinstanceservice.ErrNotFound):
		return "resource_not_found", "The command or browser instance was not found", false
	case errors.Is(err, browserinstanceservice.ErrInvalidCommandTransition):
		return "command_transition_conflict", browserinstanceservice.ErrInvalidCommandTransition.Error(), false
	case errors.Is(err, browserinstanceservice.ErrStateConflict):
		return "instance_state_conflict", browserinstanceservice.ErrStateConflict.Error(), false
	case errors.Is(err, deviceservice.ErrRevoked), errors.Is(err, deviceservice.ErrNotFound):
		return "device_revoked", "Device credential is no longer active", true
	default:
		return "internal_error", "The event could not be persisted", false
	}
}

func (c *agentClient) connContext() context.Context {
	base := c.ctx
	if base == nil {
		base = context.Background()
	}
	return contextWithDone{Context: base, done: c.done}
}

type contextWithDone struct {
	context.Context
	done <-chan struct{}
}

func (c contextWithDone) Done() <-chan struct{} { return c.done }
func (c contextWithDone) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return c.Context.Err()
	}
}

func (c *agentClient) writePump() {
	ticker := time.NewTicker(agentPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case message := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(agentWriteWait))
			if err := c.conn.WriteJSON(message); err != nil {
				c.close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(agentWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *agentClient) presencePump(g *Gateway) {
	ticker := time.NewTicker(agentPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case seenAt := <-ticker.C:
			err := g.realtime.RefreshPresence(
				c.connContext(), c.deviceID, c.presence.ConnectionID, seenAt.UTC(), agentPresenceTTL,
			)
			if errors.Is(err, realtime.ErrPresenceNotFound) {
				c.close()
				return
			}
			if err != nil {
				g.logger.Warn("agent_presence_refresh_failed", "device_id", c.deviceID, "error", err)
			}
		}
	}
}

func supportsAgentSubprotocol(r *http.Request) bool {
	for _, protocol := range websocket.Subprotocols(r) {
		if protocol == agentSubprotocol {
			return true
		}
	}
	return false
}

func (g *Gateway) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err == nil && strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	for _, allowed := range g.allowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}
