package browseragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const commandProtocol = "ant-browser-agent.v1"

type InstanceCommand struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspaceId"`
	DeviceID        string          `json:"deviceId"`
	InstanceID      string          `json:"instanceId"`
	Action          string          `json:"action"`
	ExpectedVersion int64           `json:"expectedVersion"`
	Status          string          `json:"status"`
	Payload         json.RawMessage `json:"payload"`
	Deadline        time.Time       `json:"deadline"`
}

type CommandExecutor interface {
	ExecuteCommand(context.Context, InstanceCommand) (observedState string, err error)
}

type CommandClient struct {
	config   Config
	executor CommandExecutor
	journal  *CommandJournal
	dialer   *websocket.Dialer
	running  atomic.Bool
}

type commandFrame struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlationId,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

type commandProtocolError struct{}

func (commandProtocolError) Error() string { return "cloud command protocol or identity rejected" }

type commandLocalFatalError struct{ cause error }

func (e commandLocalFatalError) Error() string { return "local command safety state is unavailable" }
func (e commandLocalFatalError) Unwrap() error { return e.cause }

func NewCommandClient(config Config, executor CommandExecutor, journal *CommandJournal) (*CommandClient, error) {
	config, err := validateConnectionConfig(config)
	if err != nil {
		return nil, err
	}
	if executor == nil || journal == nil {
		return nil, errors.New("command executor and durable journal are required")
	}
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{commandProtocol}
	dialer.HandshakeTimeout = 10 * time.Second
	return &CommandClient{config: config, executor: executor, journal: journal, dialer: &dialer}, nil
}

// Run reconnects transport failures, but not rejected authentication or tenant
// identity. A journal prevents re-execution across sessions and app restarts.
func (c *CommandClient) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("command client is already running")
	}
	defer c.running.Store(false)
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := c.runSession(ctx)
		var fatal commandProtocolError
		if errors.As(err, &fatal) {
			return err
		}
		var localFatal commandLocalFatalError
		if errors.As(err, &localFatal) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
	return ctx.Err()
}

type commandSession struct {
	conn    *websocket.Conn
	frames  chan commandFrame
	ctx     context.Context
	writeMu sync.Mutex
	pending []commandFrame
}

func (s *commandSession) send(kind string, payload any) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return "", err
	}
	id := uuid.NewString()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := s.conn.WriteJSON(map[string]any{"id": id, "type": kind, "payload": payload})
	return id, err
}

func (s *commandSession) report(kind string, payload any) error {
	id, err := s.send(kind, payload)
	if err != nil {
		return err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-timer.C:
			return errors.New("command acknowledgement timed out")
		case frame := <-s.frames:
			if frame.CorrelationID == id {
				if frame.Type == "server.ack" {
					return nil
				}
				return errors.New("command report rejected")
			}
			if frame.Type == "command.dispatch" {
				if len(s.pending) >= 128 {
					return errors.New("command queue is full")
				}
				s.pending = append(s.pending, frame)
			}
		}
	}
}

func (c *CommandClient) runSession(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	headers := http.Header{"Authorization": []string{"Device " + c.config.Credential}, "X-Device-ID": []string{c.config.DeviceID}}
	endpoint := "wss" + strings.TrimPrefix(c.config.BaseURL, "https") + "/api/v1/agent/ws"
	conn, response, err := c.dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		if response != nil && (response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 426) {
			return commandProtocolError{}
		}
		return errors.New("cloud command connection failed")
	}
	defer conn.Close()
	if conn.Subprotocol() != commandProtocol {
		return commandProtocolError{}
	}
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello commandFrame
	if err := conn.ReadJSON(&hello); err != nil {
		return err
	}
	var identity struct {
		DeviceID                 string `json:"deviceId"`
		WorkspaceID              string `json:"workspaceId"`
		Protocol                 string `json:"protocol"`
		HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	}
	if hello.Type != "server.hello" || json.Unmarshal(hello.Payload, &identity) != nil || identity.DeviceID != c.config.DeviceID || identity.WorkspaceID != c.config.WorkspaceID || identity.Protocol != commandProtocol {
		return commandProtocolError{}
	}
	s := &commandSession{conn: conn, frames: make(chan commandFrame, 128), ctx: ctx}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
			var frame commandFrame
			if conn.ReadJSON(&frame) != nil {
				return
			}
			select {
			case s.frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { <-ctx.Done(); _ = conn.Close() }()
	defer func() { cancel(); _ = conn.Close(); <-readerDone }()
	if err := s.report("agent.hello", map[string]any{"agentVersion": c.config.AgentVersion, "capabilities": map[string]any{"instanceCommands": true, "instanceActions": []string{"start", "stop", "restart"}, "durableCommandJournal": true}}); err != nil {
		return err
	}
	heartbeatInterval := time.Duration(identity.HeartbeatIntervalSeconds) * time.Second
	if heartbeatInterval < 10*time.Second || heartbeatInterval > 50*time.Second {
		heartbeatInterval = 20 * time.Second
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.send("agent.heartbeat", map[string]any{"agentVersion": c.config.AgentVersion}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	handled := map[string]bool{}
	for {
		var frame commandFrame
		if len(s.pending) > 0 {
			frame = s.pending[0]
			s.pending = s.pending[1:]
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case frame = <-s.frames:
			}
		}
		if frame.Type != "command.dispatch" {
			continue
		}
		var command InstanceCommand
		if json.Unmarshal(frame.Payload, &command) != nil || !c.validCommand(command) {
			return commandProtocolError{}
		}
		if handled[command.ID] {
			continue
		}
		if err := c.execute(ctx, s, command); err != nil {
			return err
		}
		handled[command.ID] = true
		if len(handled) >= 4096 {
			return nil
		} // reconnect to bound session memory
	}
}

func (c *CommandClient) validCommand(command InstanceCommand) bool {
	if command.WorkspaceID != c.config.WorkspaceID || command.DeviceID != c.config.DeviceID || command.ExpectedVersion < 1 || command.Deadline.IsZero() {
		return false
	}
	for _, id := range []string{command.ID, command.InstanceID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return false
		}
	}
	switch command.Status {
	case "pending", "queued", "accepted", "running":
	default:
		return false
	}
	switch command.Action {
	case "instance.start", "instance.stop", "instance.restart", "instance.migrate":
		return true
	default:
		return false
	}
}

func (c *CommandClient) execute(ctx context.Context, s *commandSession, command InstanceCommand) error {
	// Decode JSON payload before hashing so object key order does not affect replay.
	var payload any
	if len(command.Payload) > 0 {
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return commandProtocolError{}
		}
	}
	canonical, _ := json.Marshal(payload)
	command.Payload = canonical
	identity := command
	identity.Status = "" // lifecycle progress is not part of request identity
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	receipt, exists, err := c.journal.begin(command.ID, hex.EncodeToString(digest[:]))
	if err != nil {
		return commandLocalFatalError{cause: err}
	}
	if exists && receipt.Status == "started" {
		receipt.Status = "failed"
		receipt.FailureCode = "execution_uncertain"
		if err := c.journal.finish(receipt); err != nil {
			return commandLocalFatalError{cause: err}
		}
	} else if !exists {
		if command.Status == "accepted" || command.Status == "running" {
			receipt.Status = "failed"
			receipt.FailureCode = "execution_uncertain"
			if err := c.journal.finish(receipt); err != nil {
				return commandLocalFatalError{cause: err}
			}
			return s.report("command.failed", map[string]any{"commandId": command.ID, "failureCode": receipt.FailureCode})
		}
		if !command.Deadline.After(time.Now()) {
			receipt.Status = "failed"
			receipt.FailureCode = "command_expired"
		} else {
			if err := s.report("command.running", map[string]any{"commandId": command.ID}); err != nil {
				return err
			}
			deadline := command.Deadline
			if maximum := time.Now().Add(2 * time.Minute); deadline.After(maximum) {
				deadline = maximum
			}
			executionCtx, cancel := context.WithDeadline(ctx, deadline)
			state, executionErr := c.executor.ExecuteCommand(executionCtx, command)
			cancel()
			receipt.Status = "completed"
			if state != "" && state != "running" && state != "offline" {
				executionErr = errors.New("local executor returned an invalid state")
			}
			if state == "running" || state == "offline" {
				receipt.ObservedState = state
			}
			if executionErr != nil || receipt.ObservedState == "" {
				receipt.Status = "failed"
				receipt.FailureCode = "local_execution_failed"
			}
		}
		if err := c.journal.finish(receipt); err != nil {
			return commandLocalFatalError{cause: err}
		}
	}
	if receipt.ObservedState != "" {
		if err := s.report("instance.observed", map[string]any{"instanceId": command.InstanceID, "state": receipt.ObservedState}); err != nil {
			return err
		}
	}
	return s.report("command."+receipt.Status, map[string]any{"commandId": command.ID, "failureCode": receipt.FailureCode})
}
