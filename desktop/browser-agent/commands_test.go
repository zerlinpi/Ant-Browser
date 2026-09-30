package browseragent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type commandExecutorFunc func(context.Context, InstanceCommand) (string, error)

func (f commandExecutorFunc) ExecuteCommand(ctx context.Context, c InstanceCommand) (string, error) {
	return f(ctx, c)
}

func TestCommandSocketReplayAndIdentity(t *testing.T) {
	for _, scenario := range []string{"success", "replay", "expired", "foreign", "executor_failure", "preexisting_running"} {
		t.Run(scenario, func(t *testing.T) {
			config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "test-device-secret"}
			command := InstanceCommand{ID: uuid.NewString(), WorkspaceID: config.WorkspaceID, DeviceID: config.DeviceID, InstanceID: uuid.NewString(), Action: "instance.restart", ExpectedVersion: 1, Status: "pending", Deadline: time.Now().UTC().Add(time.Minute)}
			if scenario == "expired" {
				command.Deadline = time.Now().Add(-time.Minute)
			}
			if scenario == "foreign" {
				command.WorkspaceID = uuid.NewString()
			}
			if scenario == "preexisting_running" {
				command.Status = "running"
			}
			var executions atomic.Int32
			journal, err := NewCommandJournal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = journal.Close() })
			results := make(chan string, 4)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Device "+config.Credential || r.Header.Get("X-Device-ID") != config.DeviceID {
					t.Error("missing scoped device credentials")
					w.WriteHeader(401)
					return
				}
				upgrader := websocket.Upgrader{Subprotocols: []string{commandProtocol}}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_ = conn.WriteJSON(map[string]any{"type": "server.hello", "payload": map[string]any{"deviceId": config.DeviceID, "workspaceId": config.WorkspaceID, "protocol": commandProtocol}})
				for {
					var frame commandFrame
					if err := conn.ReadJSON(&frame); err != nil {
						return
					}
					_ = conn.WriteJSON(map[string]any{"type": "server.ack", "correlationId": frame.ID})
					if frame.Type == "agent.hello" {
						_ = conn.WriteJSON(map[string]any{"type": "command.dispatch", "payload": command})
						_ = conn.WriteJSON(map[string]any{"type": "command.dispatch", "payload": command})
					}
					if frame.Type == "command.completed" || frame.Type == "command.failed" {
						results <- frame.Type
						return
					}
				}
			}))
			defer server.Close()
			config.BaseURL = server.URL
			client, err := NewCommandClient(config, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) {
				executions.Add(1)
				if scenario == "executor_failure" {
					return "", errors.New("private local error")
				}
				return "running", nil
			}), journal)
			if err != nil {
				t.Fatal(err)
			}
			client.dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			runs := 1
			if scenario == "replay" {
				runs = 2
			}
			for i := 0; i < runs; i++ {
				if scenario == "replay" && i == 1 {
					command.Status = "running"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				err = client.runSession(ctx)
				cancel()
				if scenario == "foreign" {
					// Another device's command is neither executed nor answered,
					// and it must not tear down this device's session as fatal.
					var protocol commandProtocolError
					if errors.As(err, &protocol) {
						t.Fatalf("foreign dispatch ended the session as a protocol failure: %v", err)
					}
					select {
					case result := <-results:
						t.Fatalf("foreign dispatch was answered with %s", result)
					default:
					}
				} else {
					select {
					case result := <-results:
						want := "command.completed"
						if scenario == "expired" || scenario == "executor_failure" || scenario == "preexisting_running" {
							want = "command.failed"
						}
						if result != want {
							t.Fatalf("result %s, want %s", result, want)
						}
					default:
						t.Fatalf("missing terminal report: %v", err)
					}
				}
			}
			want := int32(1)
			if scenario == "expired" || scenario == "foreign" || scenario == "preexisting_running" {
				want = 0
			}
			if executions.Load() != want {
				t.Fatalf("executed %d times, want %d", executions.Load(), want)
			}
		})
	}
}

func TestCommandJournalPersistsIdentityAndRefusesCorruption(t *testing.T) {
	root := t.TempDir()
	journal, err := NewCommandJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	firstJournal := journal
	t.Cleanup(func() { _ = firstJournal.Close() })
	id := uuid.NewString()
	receipt, exists, err := journal.begin(id, "digest")
	if err != nil || exists || receipt.Status != "started" {
		t.Fatalf("begin failed: %v", err)
	}
	if competing, competingErr := NewCommandJournal(root); competingErr == nil {
		_ = competing.Close()
		t.Fatal("a second process acquired the active command journal")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err = NewCommandJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	secondJournal := journal
	t.Cleanup(func() { _ = secondJournal.Close() })
	loaded, exists, err := journal.begin(id, "digest")
	if err != nil || !exists || loaded.Status != "started" {
		t.Fatalf("restart lost intent: %v", err)
	}
	if _, _, err := journal.begin(id, "changed"); err == nil {
		t.Fatal("changed identity accepted")
	}
	receipt.Status = "completed"
	receipt.ObservedState = "running"
	if err := journal.finish(receipt); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = journal.begin(id, "digest")
	if err != nil || loaded.Status != "completed" {
		t.Fatal("completion not durable")
	}
	if _, _, err := journal.begin("../escape", "digest"); err == nil {
		t.Fatal("journal path traversal accepted")
	}
	if err := os.WriteFile(filepath.Join(root, id+".json"), []byte(`{"id":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.begin(id, "digest"); err == nil {
		t.Fatal("corrupted journal was silently replaced")
	}
}

func TestUncertainReceiptDoesNotExecute(t *testing.T) {
	// Use a socket pair through the real TLS transport, dropping execution's
	// running acknowledgement. The persisted intent survives and cannot be retried.
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	command := InstanceCommand{ID: uuid.NewString(), WorkspaceID: config.WorkspaceID, DeviceID: config.DeviceID, InstanceID: uuid.NewString(), Action: "instance.restart", ExpectedVersion: 1, Status: "pending", Deadline: time.Now().Add(time.Minute)}
	var running, executions atomic.Int32
	result := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{commandProtocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteJSON(map[string]any{"type": "server.hello", "payload": map[string]any{"deviceId": config.DeviceID, "workspaceId": config.WorkspaceID, "protocol": commandProtocol}})
		for {
			var frame commandFrame
			if conn.ReadJSON(&frame) != nil {
				return
			}
			if frame.Type == "command.running" {
				running.Add(1)
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": "server.ack", "correlationId": frame.ID})
			if frame.Type == "agent.hello" {
				_ = conn.WriteJSON(map[string]any{"type": "command.dispatch", "payload": command})
			}
			if frame.Type == "command.failed" {
				var payload struct {
					FailureCode string `json:"failureCode"`
				}
				_ = json.Unmarshal(frame.Payload, &payload)
				result <- payload.FailureCode
				return
			}
		}
	}))
	defer server.Close()
	config.BaseURL = server.URL
	journal, err := NewCommandJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	client, err := NewCommandClient(config, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) { executions.Add(1); return "running", nil }), journal)
	if err != nil {
		t.Fatal(err)
	}
	client.dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		_ = client.runSession(ctx)
		cancel()
	}
	if running.Load() != 1 || executions.Load() != 0 {
		t.Fatal("uncertain restart was re-executed")
	}
	select {
	case code := <-result:
		if code != "execution_uncertain" {
			t.Fatal(code)
		}
	default:
		t.Fatal("missing uncertain execution report")
	}
}

// agentTestServer is a minimal control plane: it acknowledges every frame,
// dispatches the given commands after agent.hello, forwards terminal command
// reports, and closes the socket after `terminal` of them.
type agentTestServer struct {
	*httptest.Server
	reports chan commandFrame
}

func startAgentTestServer(t *testing.T, config Config, commands []InstanceCommand, terminal int) *agentTestServer {
	t.Helper()
	reports := make(chan commandFrame, 16)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{commandProtocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteJSON(map[string]any{"type": "server.hello", "payload": map[string]any{"deviceId": config.DeviceID, "workspaceId": config.WorkspaceID, "protocol": commandProtocol}})
		seen := 0
		for {
			var frame commandFrame
			if conn.ReadJSON(&frame) != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": "server.ack", "correlationId": frame.ID})
			if frame.Type == "agent.hello" {
				for _, command := range commands {
					_ = conn.WriteJSON(map[string]any{"type": "command.dispatch", "payload": command})
				}
			}
			if frame.Type == "command.completed" || frame.Type == "command.failed" {
				reports <- frame
				if seen++; seen >= terminal {
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return &agentTestServer{Server: server, reports: reports}
}

func newTestCommandClient(t *testing.T, config Config, server *agentTestServer, journal *CommandJournal, executor CommandExecutor) *CommandClient {
	t.Helper()
	config.BaseURL = server.URL
	client, err := NewCommandClient(config, executor, journal)
	if err != nil {
		t.Fatal(err)
	}
	client.dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	return client
}

type commandReport struct {
	CommandID      string `json:"commandId"`
	FailureCode    string `json:"failureCode"`
	FailureMessage string `json:"failureMessage"`
}

func nextReport(t *testing.T, reports <-chan commandFrame) (string, commandReport) {
	t.Helper()
	select {
	case frame := <-reports:
		var report commandReport
		if err := json.Unmarshal(frame.Payload, &report); err != nil {
			t.Fatal(err)
		}
		return frame.Type, report
	default:
		t.Fatal("missing terminal command report")
		return "", commandReport{}
	}
}

func newTestJournal(t *testing.T, root string) *CommandJournal {
	t.Helper()
	journal, err := NewCommandJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal
}

func testCommand(config Config, action string) InstanceCommand {
	return InstanceCommand{ID: uuid.NewString(), WorkspaceID: config.WorkspaceID, DeviceID: config.DeviceID, InstanceID: uuid.NewString(), Action: action, ExpectedVersion: 1, Status: "pending", Deadline: time.Now().UTC().Add(time.Minute)}
}

func TestCorruptJournalRecordIsReportedAsConflictWithoutExecution(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	root := t.TempDir()
	journal := newTestJournal(t, root)
	corrupt := testCommand(config, "instance.start")
	if err := os.WriteFile(filepath.Join(root, corrupt.ID+".json"), []byte(`{"id":`), 0600); err != nil {
		t.Fatal(err)
	}
	healthy := testCommand(config, "instance.stop")
	server := startAgentTestServer(t, config, []InstanceCommand{corrupt, healthy}, 2)
	var executed []string
	client := newTestCommandClient(t, config, server, journal, commandExecutorFunc(func(_ context.Context, command InstanceCommand) (string, error) {
		executed = append(executed, command.ID)
		return "offline", nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	err := client.runSession(ctx)
	cancel()
	if errors.Is(err, ErrJournalUnavailable) {
		t.Fatalf("one corrupt record disabled the whole journal: %v", err)
	}
	kind, report := nextReport(t, server.reports)
	if kind != "command.failed" || report.CommandID != corrupt.ID || report.FailureCode != failureCommandConflict || report.FailureMessage == "" {
		t.Fatalf("corrupt record report = %s %+v", kind, report)
	}
	kind, report = nextReport(t, server.reports)
	if kind != "command.completed" || report.CommandID != healthy.ID {
		t.Fatalf("session did not continue after the conflict: %s %+v", kind, report)
	}
	if len(executed) != 1 || executed[0] != healthy.ID {
		t.Fatalf("executed %v, want only the healthy command", executed)
	}
	if contents, readErr := os.ReadFile(filepath.Join(root, corrupt.ID+".json")); readErr != nil || string(contents) != `{"id":` {
		t.Fatalf("corrupt record was replaced: %q, %v", contents, readErr)
	}
}

func TestReusedCommandIDWithOtherContentIsReportedAsConflict(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	journal := newTestJournal(t, t.TempDir())
	command := testCommand(config, "instance.start")
	if _, _, err := journal.begin(command.ID, "digest-of-other-content"); err != nil {
		t.Fatal(err)
	}
	server := startAgentTestServer(t, config, []InstanceCommand{command}, 1)
	var executions atomic.Int32
	client := newTestCommandClient(t, config, server, journal, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) {
		executions.Add(1)
		return "running", nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	_ = client.runSession(ctx)
	cancel()
	kind, report := nextReport(t, server.reports)
	if kind != "command.failed" || report.FailureCode != failureCommandConflict {
		t.Fatalf("reused command ID report = %s %+v", kind, report)
	}
	if executions.Load() != 0 {
		t.Fatal("a command whose ID was reused with different content was executed")
	}
}

func TestInvalidAndUnsupportedCommandsDoNotEndTheSession(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	journal := newTestJournal(t, t.TempDir())
	unsupported := testCommand(config, "instance.snapshot")
	invalid := testCommand(config, "instance.start")
	invalid.ExpectedVersion = 0
	unknownStatus := testCommand(config, "instance.start")
	unknownStatus.Status = "paused"
	terminal := testCommand(config, "instance.start")
	terminal.Status = "completed"
	valid := testCommand(config, "instance.start")
	server := startAgentTestServer(t, config, []InstanceCommand{unsupported, invalid, unknownStatus, terminal, valid}, 4)
	var executed []string
	client := newTestCommandClient(t, config, server, journal, commandExecutorFunc(func(_ context.Context, command InstanceCommand) (string, error) {
		executed = append(executed, command.ID)
		return "running", nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	err := client.runSession(ctx)
	cancel()
	var protocol commandProtocolError
	if errors.As(err, &protocol) {
		t.Fatalf("a single bad dispatch ended the session: %v", err)
	}
	want := []struct{ id, kind, code string }{
		{unsupported.ID, "command.failed", failureUnsupportedCommand},
		{invalid.ID, "command.failed", failureInvalidCommand},
		{unknownStatus.ID, "command.failed", failureInvalidCommand},
		{valid.ID, "command.completed", ""},
	}
	for _, expected := range want {
		kind, report := nextReport(t, server.reports)
		if kind != expected.kind || report.CommandID != expected.id || report.FailureCode != expected.code {
			t.Fatalf("report = %s %+v, want %+v", kind, report, expected)
		}
	}
	if len(executed) != 1 || executed[0] != valid.ID {
		t.Fatalf("executed %v, want only the valid command", executed)
	}
}

func TestExecutionHonorsServerDeadlineAndReportsSanitizedFailure(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	journal := newTestJournal(t, t.TempDir())
	command := testCommand(config, "instance.stop")
	command.Deadline = time.Now().UTC().Add(20 * time.Minute)
	server := startAgentTestServer(t, config, []InstanceCommand{command}, 1)
	var granted time.Duration
	client := newTestCommandClient(t, config, server, journal, commandExecutorFunc(func(ctx context.Context, _ InstanceCommand) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("executor context has no deadline")
		}
		granted = time.Until(deadline)
		return "offline", errors.New("upload cloud profile after stop:\nopen C:\\Users\\alice\\AppData\\Ant\\data\\profile-1\\Cookies: access denied")
	}))
	// A cancel-only parent keeps the test bounded without imposing its own
	// deadline on the executor context.
	ctx, cancel := context.WithCancel(context.Background())
	safety := time.AfterFunc(4*time.Second, cancel)
	_ = client.runSession(ctx)
	safety.Stop()
	cancel()
	if granted < 15*time.Minute {
		t.Fatalf("executor received %s, want the server's 20 minute deadline", granted)
	}
	kind, report := nextReport(t, server.reports)
	if kind != "command.failed" || report.FailureCode != "local_execution_failed" {
		t.Fatalf("report = %s %+v", kind, report)
	}
	if strings.Contains(report.FailureMessage, "alice") || strings.Contains(report.FailureMessage, "\n") ||
		!strings.Contains(report.FailureMessage, "upload cloud profile after stop") || !strings.Contains(report.FailureMessage, "<path>") {
		t.Fatalf("failure message was not sanitized: %q", report.FailureMessage)
	}
}

func TestSanitizeFailureMessage(t *testing.T) {
	for input, want := range map[string]string{
		"restore failed: open /home/alice/.config/ant/Default/Cookies: permission denied": "restore failed: open <path>: permission denied",
		"copy \\\\fileserver\\share\\profile.zip failed":                                  "copy <path> failed",
		"line one\r\nline two\tend":                                                       "line one line two end",
		"HTTP 409 (profile_revision_conflict): conflict":                                  "HTTP 409 (profile_revision_conflict): conflict",
		`Put "https://objects.example.com/bucket/key?X-Amz-Signature=abc": EOF`:           `Put "https://objects.example.com/bucket/key?<redacted>": EOF`,
		"open C:\\Users\\alice\\data\\Cookies: access denied":                             "open <path>: access denied",
	} {
		if got := sanitizeFailureMessage(input); got != want {
			t.Errorf("sanitizeFailureMessage(%q) = %q, want %q", input, got, want)
		}
	}
	long := sanitizeFailureMessage(strings.Repeat("失败", 400))
	if runes := []rune(long); len(runes) != maxFailureMessageRunes || !strings.HasSuffix(long, "…") {
		t.Fatalf("long message was not bounded: %d runes", len(runes))
	}
}

func TestRunStopsWithErrDeviceRejectedOnCredentialRejection(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret", BaseURL: server.URL}
		client, err := NewCommandClient(config, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) { return "running", nil }), newTestJournal(t, t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		client.dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err = client.Run(ctx)
		cancel()
		server.Close()
		if !errors.Is(err, ErrDeviceRejected) || errors.Is(err, ErrProtocolMismatch) {
			t.Fatalf("HTTP %d: Run returned %v, want ErrDeviceRejected", status, err)
		}
	}
}

func TestRunStopsWithErrProtocolMismatchOnForeignIdentity(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{commandProtocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"type": "server.hello", "payload": map[string]any{"deviceId": uuid.NewString(), "workspaceId": config.WorkspaceID, "protocol": commandProtocol}})
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var frame commandFrame
		_ = conn.ReadJSON(&frame)
	}))
	defer server.Close()
	config.BaseURL = server.URL
	client, err := NewCommandClient(config, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) { return "running", nil }), newTestJournal(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	client.dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	err = client.Run(ctx)
	cancel()
	if !errors.Is(err, ErrProtocolMismatch) || errors.Is(err, ErrDeviceRejected) {
		t.Fatalf("Run returned %v, want ErrProtocolMismatch", err)
	}
}

func TestUnusableJournalIsFatal(t *testing.T) {
	config := Config{DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "secret"}
	journal := newTestJournal(t, t.TempDir())
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	server := startAgentTestServer(t, config, []InstanceCommand{testCommand(config, "instance.start")}, 1)
	var executions atomic.Int32
	client := newTestCommandClient(t, config, server, journal, commandExecutorFunc(func(context.Context, InstanceCommand) (string, error) {
		executions.Add(1)
		return "running", nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	err := client.runSession(ctx)
	cancel()
	var fatal commandLocalFatalError
	if !errors.As(err, &fatal) || !errors.Is(err, ErrJournalUnavailable) {
		t.Fatalf("unusable journal was not fatal: %v", err)
	}
	if executions.Load() != 0 {
		t.Fatal("a command executed without a durable journal")
	}
}
