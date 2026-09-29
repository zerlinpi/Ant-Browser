package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/logger"
	browseragent "ant-chrome/desktop/browser-agent"
)

const (
	// cloudCommandRestartMin/Max bound the host-level retry after the command
	// client returns a terminal-but-recoverable condition (protocol mismatch
	// during a rolling upgrade, or a journal that failed and must be
	// reopened). Transport failures are already retried inside Run.
	cloudCommandRestartMin = 30 * time.Second
	cloudCommandRestartMax = 15 * time.Minute
	// A session that stayed healthy this long resets the restart backoff.
	cloudCommandHealthyRun = 10 * time.Minute
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
	deviceCredential := os.Getenv("ANT_CLOUD_DEVICE_CREDENTIAL")
	runtimeConfigClient, err := cloudagent.NewRuntimeConfigClient(
		config.BaseURL, config.WorkspaceID, config.DeviceID, deviceCredential, nil,
	)
	if err != nil {
		log.Warn("cloud_commands_not_started: invalid runtime configuration identity")
		return
	}
	journalDir := a.resolveAppPath(filepath.Join("data", "cloud-commands", config.WorkspaceID, config.DeviceID))
	journal, err := browseragent.NewCommandJournal(journalDir)
	if err != nil {
		log.Warn("cloud_commands_not_started: command journal unavailable")
		return
	}
	var profileSync *cloudProfileSyncBridge
	if len(config.CloudProfiles) != 0 {
		profileSync, err = a.newCloudProfileSyncBridge(config)
		if err != nil {
			log.Warn("cloud_profile_sync_not_started: invalid encryption or device configuration")
		}
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
		ResolveRuntimeConfig: runtimeConfigClient.Resolve,
		StartWithRuntimeConfig: func(ctx context.Context, instanceID, profile string, runtimeConfig cloudagent.InstanceRuntimeConfig) error {
			_, err := a.browserInstanceStartWithCloudRuntimeContext(ctx, instanceID, profile, runtimeConfig)
			return err
		},
		Stop: func(ctx context.Context, profile string) error {
			_, err := a.browserInstanceStopContext(ctx, profile)
			return err
		},
	}
	if profileSync != nil {
		executor.PushProfile = profileSync.Push
		executor.PullProfile = profileSync.PullLatest
		executor.SynchronizesProfile = profileSync.Synchronizes
	}
	clientConfig := browseragent.Config{BaseURL: config.BaseURL, DeviceID: config.DeviceID, WorkspaceID: config.WorkspaceID, Credential: deviceCredential, AgentVersion: a.version}
	// The first client validates the connection configuration; later
	// sessions reuse the same configuration and only reopen the journal.
	client, err := browseragent.NewCommandClient(clientConfig, executor, journal)
	if err != nil {
		_ = journal.Close()
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
		_ = journal.Close()
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
		defer func() {
			if journal != nil {
				_ = journal.Close()
			}
		}()
		session := func(ctx context.Context) error {
			if client == nil {
				reopened, err := browseragent.NewCommandJournal(journalDir)
				if err != nil {
					return fmt.Errorf("%w: %v", browseragent.ErrJournalUnavailable, err)
				}
				next, err := browseragent.NewCommandClient(clientConfig, executor, reopened)
				if err != nil {
					_ = reopened.Close()
					return err
				}
				client, journal = next, reopened
			}
			err := client.Run(ctx)
			_ = journal.Close()
			client, journal = nil, nil
			return err
		}
		runCloudCommandSessions(ctx, log, session, sleepWithContext)
	}()
	log.Info("cloud_commands_started")
}

// runCloudCommandSessions keeps the command channel alive for the lifetime of
// ctx. Only a rejected device credential stops it for good: retrying the same
// credential cannot succeed and would only add load to the control plane.
// Every other terminal condition is retried with a bounded backoff.
func runCloudCommandSessions(ctx context.Context, log *logger.Logger, session func(context.Context) error, sleep func(context.Context, time.Duration) bool) {
	backoff := cloudCommandRestartMin
	for ctx.Err() == nil {
		started := time.Now()
		err := session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= cloudCommandHealthyRun {
			backoff = cloudCommandRestartMin
		}
		switch {
		case errors.Is(err, browseragent.ErrDeviceRejected):
			log.Warn("cloud_commands_stopped: device credential rejected; provision a new credential and restart")
			return
		case errors.Is(err, browseragent.ErrProtocolMismatch):
			log.Warn("cloud_commands_paused: control plane protocol or identity mismatch", logger.F("retry_in", backoff.String()))
		case errors.Is(err, browseragent.ErrJournalUnavailable):
			log.Warn("cloud_commands_paused: command journal unavailable", logger.F("retry_in", backoff.String()))
		default:
			log.Warn("cloud_commands_paused: command session ended", logger.F("retry_in", backoff.String()))
		}
		if !sleep(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > cloudCommandRestartMax {
			backoff = cloudCommandRestartMax
		}
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
