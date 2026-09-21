package cloudagent

import (
	"context"
	"encoding/json"
	"errors"

	browseragent "ant-chrome/desktop/browser-agent"
)

// CommandExecutor resolves only pre-approved local bindings. Cloud commands
// cannot inject local paths, launch arguments, proxy overrides or shell input.
type CommandExecutor struct {
	ResolveProfile func(context.Context, string) (string, error)
	Start          func(context.Context, string) error
	Stop           func(context.Context, string) error
}

func (e *CommandExecutor) ExecuteCommand(ctx context.Context, command browseragent.InstanceCommand) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.ResolveProfile == nil || e.Start == nil || e.Stop == nil {
		return "", errors.New("local command executor is not configured")
	}
	// Migration must not be reported successful until Profile upload/restore
	// and destination ownership have completed. Do not stop the source early.
	if command.Action == "instance.migrate" {
		return "", errors.New("migration requires profile synchronization")
	}
	if command.Action != "instance.start" && command.Action != "instance.stop" && command.Action != "instance.restart" {
		return "", errors.New("unsupported instance command")
	}
	var payload map[string]json.RawMessage
	if len(command.Payload) > 0 && json.Unmarshal(command.Payload, &payload) != nil {
		return "", errors.New("invalid instance command payload")
	}
	if len(payload) != 0 {
		return "", errors.New("runtime overrides are not allowed in cloud commands")
	}
	profile, err := e.ResolveProfile(ctx, command.InstanceID)
	if err != nil || profile == "" {
		return "", errors.New("cloud instance has no authorized local binding")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if command.Action == "instance.stop" || command.Action == "instance.restart" {
		if err := e.Stop(ctx, profile); err != nil {
			return "", err
		}
		if command.Action == "instance.stop" {
			return "offline", nil
		}
		if err := ctx.Err(); err != nil {
			return "offline", err
		}
	}
	if err := e.Start(ctx, profile); err != nil {
		if command.Action == "instance.restart" {
			return "offline", err
		}
		return "", err
	}
	return "running", nil
}
