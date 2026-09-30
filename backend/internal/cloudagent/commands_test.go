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

func TestCommandExecutorSynchronizesProfileLifecycle(t *testing.T) {
	targetDeviceID := uuid.NewString()
	for _, scenario := range []struct {
		action    string
		payload   json.RawMessage
		wantState string
		wantCalls []string
	}{
		{"instance.start", json.RawMessage(`{}`), "running", []string{"resolve", "pull", "start"}},
		{"instance.stop", json.RawMessage(`{}`), "offline", []string{"resolve", "stop", "push"}},
		{"instance.restart", json.RawMessage(`{}`), "running", []string{"resolve", "stop", "push", "start"}},
		{"instance.migrate", json.RawMessage(`{"targetDeviceId":"` + targetDeviceID + `"}`), "offline", []string{"resolve", "stop", "push"}},
	} {
		t.Run(scenario.action, func(t *testing.T) {
			calls := []string{}
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) {
					calls = append(calls, "resolve")
					return "local-profile", nil
				},
				Start:       func(context.Context, string) error { calls = append(calls, "start"); return nil },
				Stop:        func(context.Context, string) error { calls = append(calls, "stop"); return nil },
				PushProfile: func(context.Context, string, string) error { calls = append(calls, "push"); return nil },
				PullProfile: func(context.Context, string, string) error { calls = append(calls, "pull"); return nil },
			}
			command := cloudCommand(scenario.action)
			command.Payload = scenario.payload
			state, err := executor.ExecuteCommand(context.Background(), command)
			if err != nil || state != scenario.wantState || !reflect.DeepEqual(calls, scenario.wantCalls) {
				t.Fatalf("state=%q err=%v calls=%v wantState=%q wantCalls=%v", state, err, calls, scenario.wantState, scenario.wantCalls)
			}
		})
	}
}

func TestCommandExecutorSkipsSynchronizationForUnboundInstances(t *testing.T) {
	for _, scenario := range []struct {
		action    string
		payload   json.RawMessage
		wantState string
		wantCalls []string
		wantErr   bool
	}{
		{"instance.start", json.RawMessage(`{}`), "running", []string{"resolve", "start"}, false},
		{"instance.stop", json.RawMessage(`{}`), "offline", []string{"resolve", "stop"}, false},
		{"instance.restart", json.RawMessage(`{}`), "running", []string{"resolve", "stop", "start"}, false},
		{"instance.migrate", json.RawMessage(`{"targetDeviceId":"` + uuid.NewString() + `"}`), "", []string{}, true},
	} {
		t.Run(scenario.action, func(t *testing.T) {
			calls := []string{}
			command := cloudCommand(scenario.action)
			command.Payload = scenario.payload
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) {
					calls = append(calls, "resolve")
					return "local-profile", nil
				},
				Start:       func(context.Context, string) error { calls = append(calls, "start"); return nil },
				Stop:        func(context.Context, string) error { calls = append(calls, "stop"); return nil },
				PushProfile: func(context.Context, string, string) error { calls = append(calls, "push"); return nil },
				PullProfile: func(context.Context, string, string) error { calls = append(calls, "pull"); return nil },
				SynchronizesProfile: func(instanceID string) bool {
					if instanceID != command.InstanceID {
						t.Errorf("binding lookup for %q", instanceID)
					}
					return false
				},
			}
			state, err := executor.ExecuteCommand(context.Background(), command)
			if (err != nil) != scenario.wantErr || state != scenario.wantState || !reflect.DeepEqual(calls, scenario.wantCalls) {
				t.Fatalf("state=%q err=%v calls=%v wantState=%q wantCalls=%v", state, err, calls, scenario.wantState, scenario.wantCalls)
			}
		})
	}
}

func TestMigrationUploadFailureRestartsSource(t *testing.T) {
	calls := []string{}
	executor := &CommandExecutor{
		ResolveProfile: func(context.Context, string) (string, error) {
			calls = append(calls, "resolve")
			return "local-profile", nil
		},
		Start: func(context.Context, string) error { calls = append(calls, "start"); return nil },
		Stop:  func(context.Context, string) error { calls = append(calls, "stop"); return nil },
		PushProfile: func(context.Context, string, string) error {
			calls = append(calls, "push")
			return errors.New("upload failed")
		},
	}
	command := cloudCommand("instance.migrate")
	command.Payload = json.RawMessage(`{"targetDeviceId":"` + uuid.NewString() + `"}`)
	state, err := executor.ExecuteCommand(context.Background(), command)
	if err == nil || state != "running" || !reflect.DeepEqual(calls, []string{"resolve", "stop", "push", "start"}) {
		t.Fatalf("state=%q err=%v calls=%v", state, err, calls)
	}
}

func TestMigrationUploadFailureUsesResolvedRuntimeForRollback(t *testing.T) {
	command := cloudCommand("instance.migrate")
	command.Payload = json.RawMessage(`{"targetDeviceId":"` + uuid.NewString() + `"}`)
	wantConfig := InstanceRuntimeConfig{InstanceID: command.InstanceID, Fingerprint: &FingerprintRuntime{ID: uuid.NewString(), Version: 9}}
	calls := []string{}
	executor := &CommandExecutor{
		ResolveProfile: func(context.Context, string) (string, error) {
			calls = append(calls, "profile")
			return "local-profile", nil
		},
		ResolveRuntimeConfig: func(context.Context, string) (InstanceRuntimeConfig, error) {
			calls = append(calls, "runtime")
			return wantConfig, nil
		},
		StartWithRuntimeConfig: func(_ context.Context, instanceID, profile string, runtimeConfig InstanceRuntimeConfig) error {
			calls = append(calls, "start")
			if instanceID != command.InstanceID || profile != "local-profile" || !reflect.DeepEqual(runtimeConfig, wantConfig) {
				t.Fatalf("rollback start instance=%q profile=%q runtime=%+v", instanceID, profile, runtimeConfig)
			}
			return nil
		},
		Stop: func(context.Context, string) error {
			calls = append(calls, "stop")
			return nil
		},
		PushProfile: func(context.Context, string, string) error {
			calls = append(calls, "push")
			return errors.New("upload failed")
		},
	}
	state, err := executor.ExecuteCommand(context.Background(), command)
	if err == nil || state != "running" || !reflect.DeepEqual(calls, []string{"profile", "stop", "push", "runtime", "start"}) {
		t.Fatalf("state=%q err=%v calls=%v", state, err, calls)
	}
}

func TestCommandExecutorPassesResolvedRuntimeConfigToStart(t *testing.T) {
	for _, action := range []string{"instance.start", "instance.restart"} {
		t.Run(action, func(t *testing.T) {
			command := cloudCommand(action)
			wantConfig := InstanceRuntimeConfig{
				InstanceID:  command.InstanceID,
				Fingerprint: &FingerprintRuntime{ID: uuid.NewString(), Version: 7},
			}
			calls := []string{}
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) {
					calls = append(calls, "profile")
					return "local-profile", nil
				},
				ResolveRuntimeConfig: func(_ context.Context, instanceID string) (InstanceRuntimeConfig, error) {
					calls = append(calls, "runtime")
					if instanceID != command.InstanceID {
						t.Fatalf("instanceID=%q want=%q", instanceID, command.InstanceID)
					}
					return wantConfig, nil
				},
				StartWithRuntimeConfig: func(_ context.Context, instanceID, profile string, gotConfig InstanceRuntimeConfig) error {
					calls = append(calls, "start")
					if instanceID != command.InstanceID || profile != "local-profile" {
						t.Fatalf("instance=%q profile=%q", instanceID, profile)
					}
					if !reflect.DeepEqual(gotConfig, wantConfig) {
						t.Fatalf("runtime config=%+v want=%+v", gotConfig, wantConfig)
					}
					return nil
				},
				Stop: func(context.Context, string) error {
					calls = append(calls, "stop")
					return nil
				},
			}

			state, err := executor.ExecuteCommand(context.Background(), command)
			wantCalls := []string{"profile", "runtime", "start"}
			if action == "instance.restart" {
				wantCalls = []string{"profile", "runtime", "stop", "start"}
			}
			if err != nil || state != "running" || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("state=%q err=%v calls=%v wantCalls=%v", state, err, calls, wantCalls)
			}
		})
	}
}

func TestCommandExecutorRuntimeConfigFailurePreventsStart(t *testing.T) {
	resolveErr := errors.New("runtime config unavailable")
	for _, action := range []string{"instance.start", "instance.restart"} {
		t.Run(action, func(t *testing.T) {
			started := false
			stopped := false
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) { return "local-profile", nil },
				ResolveRuntimeConfig: func(context.Context, string) (InstanceRuntimeConfig, error) {
					return InstanceRuntimeConfig{}, resolveErr
				},
				Start: func(context.Context, string) error {
					started = true
					return nil
				},
				StartWithRuntimeConfig: func(context.Context, string, string, InstanceRuntimeConfig) error {
					started = true
					return nil
				},
				Stop: func(context.Context, string) error {
					stopped = true
					return nil
				},
			}

			state, err := executor.ExecuteCommand(context.Background(), cloudCommand(action))
			wantState := ""
			if !errors.Is(err, resolveErr) || state != wantState {
				t.Fatalf("state=%q err=%v wantState=%q", state, err, wantState)
			}
			if started {
				t.Fatal("start called after runtime configuration resolution failed")
			}
			if stopped {
				t.Fatal("stop called before runtime configuration preflight succeeded")
			}
		})
	}
}

func TestCommandExecutorRequiresRuntimeCallbacksAsPair(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		resolver bool
		starter  bool
	}{
		{name: "resolver only", resolver: true},
		{name: "starter only", starter: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			profileResolved := false
			executor := &CommandExecutor{
				ResolveProfile: func(context.Context, string) (string, error) {
					profileResolved = true
					return "local-profile", nil
				},
				Start: func(context.Context, string) error { return nil },
				Stop:  func(context.Context, string) error { return nil },
			}
			if scenario.resolver {
				executor.ResolveRuntimeConfig = func(context.Context, string) (InstanceRuntimeConfig, error) {
					return InstanceRuntimeConfig{}, nil
				}
			}
			if scenario.starter {
				executor.StartWithRuntimeConfig = func(context.Context, string, string, InstanceRuntimeConfig) error { return nil }
			}

			if _, err := executor.ExecuteCommand(context.Background(), cloudCommand("instance.start")); err == nil {
				t.Fatal("partially configured runtime callbacks were accepted")
			}
			if profileResolved {
				t.Fatal("invalid executor performed work")
			}
		})
	}
}
