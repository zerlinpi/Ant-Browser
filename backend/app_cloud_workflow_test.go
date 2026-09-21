package backend

import (
	"ant-chrome/backend/internal/config"
	"ant-chrome/backend/internal/launchcode"
	"context"
	"testing"
	"time"
)

func TestCloudWorkflowsDisabledByDefault(t *testing.T) {
	t.Setenv("ANT_CLOUD_WORKFLOWS_ENABLED", "")
	app := NewApp(t.TempDir())
	app.startupInitCloudWorkflows(nil)
	if app.cloudWorkflowCancel != nil {
		t.Fatal("cloud executor enabled without explicit opt-in")
	}
}

func TestCloudWorkflowsEnabledWithNilLoggerDoesNotPanic(t *testing.T) {
	t.Setenv("ANT_CLOUD_WORKFLOWS_ENABLED", "true")
	app := NewApp(t.TempDir())
	app.startupInitCloudWorkflows(nil)
	if app.cloudWorkflowCancel != nil {
		t.Fatal("cloud executor started without a ready automation runtime")
	}
}

func TestCloudWorkflowEndpointRequiresActiveLaunchAuthentication(t *testing.T) {
	app := NewApp(t.TempDir())
	app.config = config.DefaultConfig()
	app.launchServer = launchcode.NewLaunchServer(nil, nil, nil, 19876)
	if _, _, _, err := app.cloudWorkflowEndpoint(); err == nil {
		t.Fatal("unauthenticated launch endpoint accepted")
	}
	app.config.LaunchServer.Auth.Enabled = true
	app.config.LaunchServer.Auth.APIKey = "private-key"
	app.config.LaunchServer.Auth.Header = "X-Test-Key"
	app.launchServer.SetAPIAuthConfig(launchcode.APIAuthConfig{Enabled: true, APIKey: "private-key", Header: "X-Test-Key"})
	base, header, value, err := app.cloudWorkflowEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	if base != "http://127.0.0.1:19876" || header != "X-Test-Key" || value != "private-key" {
		t.Fatalf("unexpected authenticated endpoint: %q %q %q", base, header, value)
	}
}

func TestCloudWorkflowStopCancelsAndWaits(t *testing.T) {
	app := NewApp(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	app.cloudWorkflowCancel = cancel
	app.cloudWorkflowDone = done
	go func() { <-ctx.Done(); close(done) }()
	app.stopCloudWorkflows()
	select {
	case <-done:
	default:
		t.Fatal("shutdown returned before executor stopped")
	}
	if app.cloudWorkflowCancel != nil || app.cloudWorkflowDone != nil {
		t.Fatal("stopped cloud executor state was not cleared")
	}
	started := time.Now()
	app.stopCloudWorkflows()
	if time.Since(started) > time.Second {
		t.Fatal("repeated stop blocked")
	}
}
