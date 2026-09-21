package cloudagent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	browseragent "ant-chrome/desktop/browser-agent"
	"github.com/google/uuid"
)

func cloudCommand(action string) browseragent.InstanceCommand {
	return browseragent.InstanceCommand{ID: uuid.NewString(), WorkspaceID: uuid.NewString(), DeviceID: uuid.NewString(), InstanceID: uuid.NewString(), Action: action, ExpectedVersion: 1, Status: "pending", Payload: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Minute)}
}

func TestCommandExecutorLifecycle(t *testing.T) {
	for _, scenario := range []string{"start", "stop", "restart", "restart_start_failure", "foreign_binding", "payload_override", "migration"} {
		t.Run(scenario, func(t *testing.T) {
			calls := []string{}
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) {
					calls = append(calls, "resolve")
					if scenario == "foreign_binding" {
						return "", errors.New("not bound")
					}
					return "local-profile", nil
				},
				Start: func(context.Context, string) error {
					calls = append(calls, "start")
					if scenario == "restart_start_failure" {
						return errors.New("failed")
					}
					return nil
				},
				Stop: func(context.Context, string) error { calls = append(calls, "stop"); return nil },
			}
			action := map[string]string{"start": "instance.start", "stop": "instance.stop", "restart": "instance.restart", "restart_start_failure": "instance.restart", "foreign_binding": "instance.start", "payload_override": "instance.start", "migration": "instance.migrate"}[scenario]
			command := cloudCommand(action)
			if scenario == "payload_override" {
				command.Payload = json.RawMessage(`{"proxy":"attacker"}`)
			}
			state, err := executor.ExecuteCommand(context.Background(), command)
			wantCalls := map[string][]string{"start": {"resolve", "start"}, "stop": {"resolve", "stop"}, "restart": {"resolve", "stop", "start"}, "restart_start_failure": {"resolve", "stop", "start"}, "foreign_binding": {"resolve"}, "payload_override": {}, "migration": {}}[scenario]
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls=%v want=%v", calls, wantCalls)
			}
			if scenario == "start" || scenario == "restart" {
				if err != nil || state != "running" {
					t.Fatalf("state=%q err=%v", state, err)
				}
			}
			if scenario == "stop" {
				if err != nil || state != "offline" {
					t.Fatalf("state=%q err=%v", state, err)
				}
			}
			if scenario == "restart_start_failure" {
				if err == nil || state != "offline" {
					t.Fatalf("partial failure state=%q err=%v", state, err)
				}
			}
			if scenario == "foreign_binding" || scenario == "payload_override" || scenario == "migration" {
				if err == nil {
					t.Fatal("unsafe command accepted")
				}
			}
		})
	}
}

func TestCommandExecutorCancellationBeforeSideEffect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	executor := &CommandExecutor{ResolveProfile: func(context.Context, string) (string, error) { called = true; return "profile", nil }, Start: func(context.Context, string) error { called = true; return nil }, Stop: func(context.Context, string) error { called = true; return nil }}
	if _, err := executor.ExecuteCommand(ctx, cloudCommand("instance.start")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if called {
		t.Fatal("cancelled command performed work")
	}
}
