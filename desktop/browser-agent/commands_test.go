package browseragent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
					var protocol commandProtocolError
					if !errors.As(err, &protocol) {
						t.Fatalf("foreign scope not rejected: %v", err)
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
	id := uuid.NewString()
	receipt, exists, err := journal.begin(id, "digest")
	if err != nil || exists || receipt.Status != "started" {
		t.Fatalf("begin failed: %v", err)
	}
	journal, err = NewCommandJournal(root)
	if err != nil {
		t.Fatal(err)
	}
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

func TestCorruptJournalStopsClientInsteadOfReconnectLoop(t *testing.T) {
	root := t.TempDir()
	journal, err := NewCommandJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	command := InstanceCommand{ID: uuid.NewString(), WorkspaceID: uuid.NewString(), DeviceID: uuid.NewString(), InstanceID: uuid.NewString(), Action: "instance.start", ExpectedVersion: 1, Status: "pending", Deadline: time.Now().Add(time.Minute)}
	if err := os.WriteFile(filepath.Join(root, command.ID+".json"), []byte(`{"id":`), 0600); err != nil {
		t.Fatal(err)
	}
	client := &CommandClient{journal: journal}
	err = client.execute(context.Background(), nil, command)
	var fatal commandLocalFatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("journal corruption was not fatal: %v", err)
	}
}
