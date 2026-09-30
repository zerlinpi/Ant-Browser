package browseragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
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

// Terminal conditions reported by Run. Use errors.Is to classify them.
var (
	// ErrDeviceRejected means the control plane refused the device credential
	// during the WebSocket handshake (HTTP 401 or 403), for example after the
	// device was revoked or its credential rotated. Retrying with the same
	// credential cannot succeed.
	ErrDeviceRejected = errors.New("cloud rejected the device credential")
	// ErrProtocolMismatch means the control plane refused the protocol
	// upgrade (HTTP 426), negotiated another subprotocol, or announced a
	// different device or workspace identity than this agent is configured for.
	ErrProtocolMismatch = errors.New("cloud command protocol or identity mismatch")
	// ErrJournalUnavailable means the durable command journal could not be
	// read or written, so no command can be executed safely.
	ErrJournalUnavailable = errors.New("local command journal is unavailable")
)

// Failure codes reported for dispatches that are never executed.
const (
	failureInvalidCommand     = "invalid_command"
	failureUnsupportedCommand = "unsupported_command"
	failureCommandConflict    = "command_conflict"
	maxFailureMessageRunes    = 300
	// maxCommandExecution bounds any single command even when the control
	// plane grants a longer deadline. Profile synchronisation during start,
	// stop, restart and migration can legitimately take tens of minutes.
	maxCommandExecution = 35 * time.Minute
)

type commandProtocolError struct{ cause error }

func (e commandProtocolError) Error() string {
	if e.cause == nil {
		return ErrProtocolMismatch.Error()
	}
	return e.cause.Error()
}

func (e commandProtocolError) Unwrap() error {
	if e.cause == nil {
		return ErrProtocolMismatch
	}
	return e.cause
}

type commandLocalFatalError struct{ cause error }

func (e commandLocalFatalError) Error() string { return "local command safety state is unavailable" }
func (e commandLocalFatalError) Unwrap() error { return e.cause }

// Is lets callers classify every local fatal condition as ErrJournalUnavailable.
func (e commandLocalFatalError) Is(target error) bool { return target == ErrJournalUnavailable }

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

// Run consumes cloud commands until ctx ends. Transport failures reconnect
// with backoff internally, and a single malformed, foreign or conflicting
// dispatch is reported (or ignored) without ending the session. Run returns
// only when ctx is done or on a terminal condition:
//
//   - ErrDeviceRejected: the credential was refused; the host should stop
//     and wait for a new credential instead of retrying.
//   - ErrProtocolMismatch: the control plane is incompatible or announced a
//     different identity; the host may retry later with a long backoff.
//   - ErrJournalUnavailable: the durable journal failed; commands cannot be
//     executed safely until the host reopens a healthy journal.
//
// A journal prevents re-execution across sessions and app restarts.
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
		if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return commandProtocolError{cause: ErrDeviceRejected}
			case http.StatusUpgradeRequired:
				return commandProtocolError{cause: ErrProtocolMismatch}
			}
		}
		return errors.New("cloud command connection failed")
	}
	defer conn.Close()
	if conn.Subprotocol() != commandProtocol {
		return commandProtocolError{cause: ErrProtocolMismatch}
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
		return commandProtocolError{cause: ErrProtocolMismatch}
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
	if err := s.report("agent.hello", map[string]any{"agentVersion": c.config.AgentVersion, "capabilities": map[string]any{"instanceCommands": true, "instanceActions": []string{"start", "stop", "restart", "migrate"}, "durableCommandJournal": true}}); err != nil {
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
		decodeErr := json.Unmarshal(frame.Payload, &command)
		disposition, failureCode := c.classifyCommand(command, decodeErr)
		if disposition == commandIgnored || handled[command.ID] {
			continue
		}
		if disposition == commandRejected {
			// The command belongs to this device but can never run here;
			// report it so the control plane does not wait for a deadline.
			message := "command was rejected by the agent before execution"
			if failureCode == failureUnsupportedCommand {
				message = "command action is not supported by this agent"
			}
			if err := s.report("command.failed", failurePayload(command.ID, failureCode, message)); err != nil {
				return err
			}
		} else if err := c.execute(ctx, s, command); err != nil {
			return err
		}
		handled[command.ID] = true
		if len(handled) >= 4096 {
			return nil
		} // reconnect to bound session memory
	}
}

type commandDisposition int

const (
	commandAccepted commandDisposition = iota
	// commandIgnored frames are dropped silently: they cannot be attributed
	// to this device (undecodable, non-canonical ID, another device or
	// workspace) or are already terminal on the control plane.
	commandIgnored
	// commandRejected frames are addressed to this device but are malformed
	// or unsupported; they are reported as failed and never executed.
	commandRejected
)

func (c *CommandClient) classifyCommand(command InstanceCommand, decodeErr error) (commandDisposition, string) {
	if decodeErr != nil || !canonicalUUID(command.ID) {
		return commandIgnored, ""
	}
	if command.WorkspaceID != c.config.WorkspaceID || command.DeviceID != c.config.DeviceID {
		// Never execute or answer for another device's command.
		return commandIgnored, ""
	}
	switch command.Status {
	case "pending", "queued", "accepted", "running":
	case "completed", "failed", "cancelled", "expired":
		return commandIgnored, ""
	default:
		return commandRejected, failureInvalidCommand
	}
	if !canonicalUUID(command.InstanceID) || command.ExpectedVersion < 1 || command.Deadline.IsZero() {
		return commandRejected, failureInvalidCommand
	}
	switch command.Action {
	case "instance.start", "instance.stop", "instance.restart", "instance.migrate":
		return commandAccepted, ""
	default:
		return commandRejected, failureUnsupportedCommand
	}
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func failurePayload(commandID, code, message string) map[string]any {
	payload := map[string]any{"commandId": commandID, "failureCode": code}
	if message = sanitizeFailureMessage(message); message != "" {
		payload["failureMessage"] = message
	}
	return payload
}

var (
	windowsPathPattern = regexp.MustCompile(`(?i)(?:\\\\\?\\)?[a-z]:\\[^\s"'<>|:]*`)
	uncPathPattern     = regexp.MustCompile(`\\\\[^\s"'<>|:\\]+\\[^\s"'<>|:]*`)
	unixPathPattern    = regexp.MustCompile(`(^|[\s"'(=])(/[^\s"'<>|:/]+){2,}/?`)
	urlQueryPattern    = regexp.MustCompile(`(https?://[^\s"'?#]+)[?#][^\s"']*`)
)

// sanitizeFailureMessage turns a local error into a single-line diagnostic
// for the control plane. Local filesystem paths (which reveal user names and
// directory layout) are redacted and the result is bounded.
func sanitizeFailureMessage(message string) string {
	message = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || (r < 0x20 && r != ' ') || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	// Presigned object URLs carry signatures in the query string.
	message = urlQueryPattern.ReplaceAllString(message, "$1?<redacted>")
	message = windowsPathPattern.ReplaceAllString(message, "<path>")
	message = uncPathPattern.ReplaceAllString(message, "<path>")
	message = unixPathPattern.ReplaceAllString(message, "$1<path>")
	message = strings.Join(strings.Fields(message), " ")
	if runes := []rune(message); len(runes) > maxFailureMessageRunes {
		message = string(runes[:maxFailureMessageRunes-1]) + "…"
	}
	return message
}

func (c *CommandClient) execute(ctx context.Context, s *commandSession, command InstanceCommand) error {
	// Decode JSON payload before hashing so object key order does not affect replay.
	var payload any
	if len(command.Payload) > 0 {
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return s.report("command.failed", failurePayload(command.ID, failureInvalidCommand, "command payload is not valid JSON"))
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
		if exists && errors.Is(err, errJournalRecordConflict) {
			// The ID was already journaled with different content, or its
			// record is unreadable. Either way the dispatch must not run and
			// the existing record is kept as evidence.
			return s.report("command.failed", failurePayload(command.ID, failureCommandConflict, "command identifier was reused with different content"))
		}
		return commandLocalFatalError{cause: err}
	}
	failureMessage := ""
	if exists && receipt.Status == "started" {
		receipt.Status = "failed"
		receipt.FailureCode = "execution_uncertain"
		failureMessage = "a previous execution attempt was interrupted; the outcome is uncertain"
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
			return s.report("command.failed", failurePayload(command.ID, receipt.FailureCode, "command was already in progress without a local journal record; the outcome is uncertain"))
		}
		if !command.Deadline.After(time.Now()) {
			receipt.Status = "failed"
			receipt.FailureCode = "command_expired"
			failureMessage = "command deadline passed before execution started"
		} else {
			if err := s.report("command.running", map[string]any{"commandId": command.ID}); err != nil {
				return err
			}
			// The control plane's deadline is authoritative (profile sync can
			// legitimately take many minutes); it is only bounded locally so a
			// skewed or hostile deadline cannot pin the executor indefinitely.
			deadline := command.Deadline
			if maximum := time.Now().Add(maxCommandExecution); deadline.After(maximum) {
				deadline = maximum
			}
			executionCtx, cancel := context.WithDeadline(ctx, deadline)
			state, executionErr := c.executor.ExecuteCommand(executionCtx, command)
			deadlineExceeded := errors.Is(executionCtx.Err(), context.DeadlineExceeded)
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
				switch {
				case deadlineExceeded:
					failureMessage = "command deadline exceeded during local execution"
				case executionErr != nil:
					failureMessage = executionErr.Error()
				default:
					failureMessage = "local executor did not report an instance state"
				}
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
	if receipt.Status == "failed" {
		// The message is diagnostic only and is not persisted in the journal,
		// so a replayed report carries the stable failure code alone.
		return s.report("command.failed", failurePayload(command.ID, receipt.FailureCode, failureMessage))
	}
	return s.report("command."+receipt.Status, map[string]any{"commandId": command.ID, "failureCode": receipt.FailureCode})
}
