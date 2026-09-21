package backend

import (
	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/logger"
	browseragent "ant-chrome/desktop/browser-agent"
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

func (a *App) startupInitCloudWorkflows(log *logger.Logger) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ANT_CLOUD_WORKFLOWS_ENABLED")), "true") {
		return
	}
	if log == nil {
		log = logger.New("CloudWorkflow")
	}
	if a.config == nil || !a.config.Automation.Enabled || a.automationMgr == nil || !a.automationMgr.CurrentState().Ready {
		log.Warn("cloud_workflows_not_started: local automation runtime is not ready")
		return
	}
	config, err := cloudagent.LoadConfig(os.Getenv("ANT_CLOUD_WORKFLOWS_CONFIG"))
	if err != nil {
		log.Warn("cloud_workflows_not_started: invalid configuration")
		return
	}
	base, header, value, err := a.cloudWorkflowEndpoint()
	if err != nil {
		log.Warn("cloud_workflows_not_started: authenticated local launch endpoint unavailable")
		return
	}
	executor := &cloudagent.Executor{Runtime: a.automationMgr, LaunchBaseURL: base, LaunchAuthHeader: header, LaunchAuthValue: value, ArtifactRoot: a.resolveAppPath("data/cloud-workflows/artifacts"), ResolveProfile: func(ctx context.Context, instanceID string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		profileID, ok := config.Bindings[instanceID]
		if !ok || a.browserMgr == nil {
			return "", errors.New("cloud instance is not bound locally")
		}
		a.browserMgr.Mutex.Lock()
		profile := a.browserMgr.Profiles[profileID]
		a.browserMgr.Mutex.Unlock()
		if profile == nil {
			return "", errors.New("bound local profile no longer exists")
		}
		return profileID, nil
	}}
	client, err := browseragent.New(browseragent.Config{BaseURL: config.BaseURL, DeviceID: config.DeviceID, WorkspaceID: config.WorkspaceID, Credential: os.Getenv("ANT_CLOUD_DEVICE_CREDENTIAL")}, executor)
	if err != nil {
		log.Warn("cloud_workflows_not_started: invalid endpoint or device identity")
		return
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	a.cloudWorkflowMu.Lock()
	if a.cloudWorkflowCancel != nil {
		a.cloudWorkflowMu.Unlock()
		cancel()
		return
	}
	done := make(chan struct{})
	a.cloudWorkflowCancel = cancel
	a.cloudWorkflowDone = done
	a.cloudWorkflowMu.Unlock()
	go func() {
		defer func() {
			close(done)
			a.clearCloudWorkflowRun(done)
		}()
		if err := client.Run(ctx, 5*time.Second); err != nil && ctx.Err() == nil {
			log.Warn("cloud_workflows_stopped: claim, execution report or protocol error")
		}
	}()
	log.Info("cloud_workflows_started")
}

func (a *App) cloudWorkflowEndpoint() (string, string, string, error) {
	if a.launchServer == nil || !a.launchServer.APIAuthEnabled() {
		return "", "", "", errors.New("launch API authentication is required for cloud workflows")
	}
	base, header, value, err := a.automationDemoEndpoint()
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(header) == "" || strings.TrimSpace(value) == "" {
		return "", "", "", errors.New("launch API authentication is incomplete")
	}
	return base, header, value, nil
}

func (a *App) clearCloudWorkflowRun(done chan struct{}) {
	a.cloudWorkflowMu.Lock()
	if a.cloudWorkflowDone == done {
		a.cloudWorkflowCancel = nil
		a.cloudWorkflowDone = nil
	}
	a.cloudWorkflowMu.Unlock()
}

func (a *App) stopCloudWorkflows() {
	a.cloudWorkflowMu.Lock()
	cancel, done := a.cloudWorkflowCancel, a.cloudWorkflowDone
	a.cloudWorkflowMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	stopped := false
	select {
	case <-done:
		stopped = true
	case <-timer.C:
	}
	if stopped {
		a.clearCloudWorkflowRun(done)
	}
}
