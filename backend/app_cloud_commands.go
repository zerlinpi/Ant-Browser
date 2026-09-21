package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/logger"
	browseragent "ant-chrome/desktop/browser-agent"
)

func (a *App) startupInitCloudCommands(log *logger.Logger) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ANT_CLOUD_COMMANDS_ENABLED")), "true") {
		return
	}
	if log == nil {
		log = logger.New("CloudCommands")
	}
	if a.browserMgr == nil {
		log.Warn("cloud_commands_not_started: browser manager unavailable")
		return
	}
	configPath := os.Getenv("ANT_CLOUD_COMMANDS_CONFIG")
	if configPath == "" {
		configPath = os.Getenv("ANT_CLOUD_WORKFLOWS_CONFIG")
	}
	config, err := cloudagent.LoadConfig(configPath)
	if err != nil {
		log.Warn("cloud_commands_not_started: invalid configuration")
		return
	}
	journal, err := browseragent.NewCommandJournal(a.resolveAppPath(filepath.Join("data", "cloud-commands", config.WorkspaceID, config.DeviceID)))
	if err != nil {
		log.Warn("cloud_commands_not_started: command journal unavailable")
		return
	}
	executor := &cloudagent.CommandExecutor{
		ResolveProfile: func(ctx context.Context, instanceID string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			profileID, ok := config.Bindings[instanceID]
			if !ok {
				return "", errors.New("instance is not bound locally")
			}
			a.browserMgr.Mutex.Lock()
			defer a.browserMgr.Mutex.Unlock()
			profile := a.browserMgr.Profiles[profileID]
			if profile == nil || profile.DeletedAt != "" {
				return "", errors.New("bound profile is unavailable")
			}
			return profileID, nil
		},
		Start: func(ctx context.Context, profile string) error {
			_, err := a.browserInstanceStartInternalContext(ctx, profile, nil, nil, false, false, false, "", "")
			return err
		},
		Stop: func(ctx context.Context, profile string) error {
			_, err := a.browserInstanceStopContext(ctx, profile)
			return err
		},
	}
	client, err := browseragent.NewCommandClient(browseragent.Config{BaseURL: config.BaseURL, DeviceID: config.DeviceID, WorkspaceID: config.WorkspaceID, Credential: os.Getenv("ANT_CLOUD_DEVICE_CREDENTIAL"), AgentVersion: a.version}, executor, journal)
	if err != nil {
		log.Warn("cloud_commands_not_started: invalid connection configuration")
		return
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.cloudCommandMu.Lock()
	if a.cloudCommandCancel != nil {
		a.cloudCommandMu.Unlock()
		cancel()
		return
	}
	done := make(chan struct{})
	a.cloudCommandCancel = cancel
	a.cloudCommandDone = done
	a.cloudCommandMu.Unlock()
	go func() {
		defer func() {
			a.cloudCommandMu.Lock()
			if a.cloudCommandDone == done {
				a.cloudCommandCancel = nil
				a.cloudCommandDone = nil
			}
			a.cloudCommandMu.Unlock()
			close(done)
		}()
		if err := client.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("cloud_commands_stopped: device identity or protocol rejected")
		}
	}()
	log.Info("cloud_commands_started")
}

func (a *App) stopCloudCommands() {
	a.cloudCommandMu.Lock()
	cancel, done := a.cloudCommandCancel, a.cloudCommandDone
	a.cloudCommandMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
