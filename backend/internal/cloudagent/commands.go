package cloudagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	browseragent "ant-chrome/desktop/browser-agent"
	"github.com/google/uuid"
)

// CommandExecutor resolves only pre-approved local bindings. Cloud commands
// cannot inject local paths, launch arguments, proxy overrides or shell input.
type CommandExecutor struct {
	ResolveProfile       func(context.Context, string) (string, error)
	ResolveRuntimeConfig func(context.Context, string) (InstanceRuntimeConfig, error)
	Start                func(context.Context, string) error
	// StartWithRuntimeConfig receives the cloud instance ID the runtime
	// configuration was resolved for and the local profile bound to it.
	StartWithRuntimeConfig func(ctx context.Context, instanceID, profile string, runtimeConfig InstanceRuntimeConfig) error
	Stop                   func(context.Context, string) error
	PushProfile            func(context.Context, string, string) error
	PullProfile            func(context.Context, string, string) error
	// SynchronizesProfile reports whether an instance has a cloud profile
	// binding on this device. Unbound instances start, stop and restart
	// without profile synchronization; migration requires a binding and is
	// rejected before any local side effect. A nil callback treats every
	// instance as bound when PushProfile/PullProfile are configured.
	SynchronizesProfile func(instanceID string) bool
}

func (e *CommandExecutor) synchronizes(instanceID string) bool {
	return e.SynchronizesProfile == nil || e.SynchronizesProfile(instanceID)
}

func (e *CommandExecutor) ExecuteCommand(ctx context.Context, command browseragent.InstanceCommand) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	hasRuntimeResolver := e.ResolveRuntimeConfig != nil
	hasRuntimeStarter := e.StartWithRuntimeConfig != nil
	if e.ResolveProfile == nil || e.Stop == nil || hasRuntimeResolver != hasRuntimeStarter || (!hasRuntimeResolver && e.Start == nil) {
		return "", errors.New("local command executor is not configured")
	}
	if command.Action != "instance.start" && command.Action != "instance.stop" && command.Action != "instance.restart" {
		if command.Action != "instance.migrate" {
			return "", errors.New("unsupported instance command")
		}
	}
	var payload map[string]json.RawMessage
	if len(command.Payload) > 0 {
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return "", errors.New("invalid instance command payload")
		}
	}
	if command.Action == "instance.migrate" {
		var targetDeviceID string
		if len(payload) != 1 || json.Unmarshal(payload["targetDeviceId"], &targetDeviceID) != nil {
			return "", errors.New("migration target device is invalid")
		}
		parsed, err := uuid.Parse(targetDeviceID)
		if err != nil || parsed.String() != targetDeviceID {
			return "", errors.New("migration target device is invalid")
		}
		if e.PushProfile == nil || !e.synchronizes(command.InstanceID) {
			return "", errors.New("migration requires profile synchronization")
		}
	} else if len(payload) != 0 {
		return "", errors.New("runtime overrides are not allowed in cloud commands")
	}
	profile, err := e.ResolveProfile(ctx, command.InstanceID)
	if err != nil || profile == "" {
		return "", errors.New("cloud instance has no authorized local binding")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pushProfile, pullProfile := e.PushProfile, e.PullProfile
	if !e.synchronizes(command.InstanceID) {
		pushProfile, pullProfile = nil, nil
	}
	var restartRuntimeConfig *InstanceRuntimeConfig
	if command.Action == "instance.restart" && e.ResolveRuntimeConfig != nil {
		resolved, resolveErr := e.resolveRuntimeConfig(ctx, command.InstanceID)
		if resolveErr != nil {
			return "", resolveErr
		}
		restartRuntimeConfig = &resolved
	}
	if command.Action == "instance.start" {
		if pullProfile != nil {
			if err := pullProfile(ctx, command.InstanceID, profile); err != nil {
				return "offline", fmt.Errorf("restore cloud profile before start: %w", err)
			}
		}
		if err := e.start(ctx, command.InstanceID, profile, nil); err != nil {
			return "", err
		}
		return "running", nil
	}
	if command.Action == "instance.stop" || command.Action == "instance.restart" || command.Action == "instance.migrate" {
		if err := e.Stop(ctx, profile); err != nil {
			return "", err
		}
		if command.Action == "instance.stop" {
			if pushProfile != nil {
				if err := pushProfile(ctx, command.InstanceID, profile); err != nil {
					return "offline", fmt.Errorf("upload cloud profile after stop: %w", err)
				}
			}
			return "offline", nil
		}
		if err := ctx.Err(); err != nil {
			return "offline", err
		}
		if pushProfile != nil {
			if pushErr := pushProfile(ctx, command.InstanceID, profile); pushErr != nil {
				// The source remains authoritative when upload fails. Best-effort
				// restart keeps the account available and reports its real state.
				if startErr := e.start(ctx, command.InstanceID, profile, nil); startErr != nil {
					return "offline", errors.Join(fmt.Errorf("upload cloud profile: %w", pushErr), fmt.Errorf("restart source profile: %w", startErr))
				}
				return "running", fmt.Errorf("upload cloud profile: %w", pushErr)
			}
		}
		if command.Action == "instance.migrate" {
			return "offline", nil
		}
	}
	if err := e.start(ctx, command.InstanceID, profile, restartRuntimeConfig); err != nil {
		if command.Action == "instance.restart" {
			return "offline", err
		}
		return "", err
	}
	return "running", nil
}

func (e *CommandExecutor) start(ctx context.Context, instanceID, profile string, resolved *InstanceRuntimeConfig) error {
	if e.ResolveRuntimeConfig == nil {
		return e.Start(ctx, profile)
	}
	if resolved == nil {
		runtimeConfig, err := e.resolveRuntimeConfig(ctx, instanceID)
		if err != nil {
			return err
		}
		resolved = &runtimeConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.StartWithRuntimeConfig(ctx, instanceID, profile, *resolved)
}

func (e *CommandExecutor) resolveRuntimeConfig(ctx context.Context, instanceID string) (InstanceRuntimeConfig, error) {
	runtimeConfig, err := e.ResolveRuntimeConfig(ctx, instanceID)
	if err != nil {
		return InstanceRuntimeConfig{}, fmt.Errorf("resolve instance runtime configuration: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return InstanceRuntimeConfig{}, err
	}
	return runtimeConfig, nil
}
