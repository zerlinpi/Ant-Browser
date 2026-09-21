package backend

import (
	"context"
	"testing"
	"time"
)

func TestCloudCommandsRequireExplicitOptIn(t *testing.T) {
	t.Setenv("ANT_CLOUD_COMMANDS_ENABLED", "")
	app := NewApp(t.TempDir())
	app.startupInitCloudCommands(nil)
	if app.cloudCommandCancel != nil || app.cloudCommandDone != nil {
		t.Fatal("cloud command socket enabled by default")
	}
}

func TestCloudCommandsFailClosedWithoutBrowserManagerOrConfig(t *testing.T) {
	t.Setenv("ANT_CLOUD_COMMANDS_ENABLED", "true")
	app := NewApp(t.TempDir())
	app.startupInitCloudCommands(nil)
	if app.cloudCommandCancel != nil {
		t.Fatal("cloud command socket started without local browser manager")
	}
}

func TestCloudCommandStopCancelsAndWaits(t *testing.T) {
	app := NewApp(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	app.cloudCommandCancel = cancel
	app.cloudCommandDone = done
	go func() {
		<-ctx.Done()
		app.cloudCommandMu.Lock()
		app.cloudCommandCancel = nil
		app.cloudCommandDone = nil
		app.cloudCommandMu.Unlock()
		close(done)
	}()
	app.stopCloudCommands()
	select {
	case <-done:
	default:
		t.Fatal("shutdown returned before command client stopped")
	}
	app.cloudCommandMu.Lock()
	stillRunning := app.cloudCommandCancel != nil || app.cloudCommandDone != nil
	app.cloudCommandMu.Unlock()
	if stillRunning {
		t.Fatal("stopped command client state was not cleared")
	}
	started := time.Now()
	app.stopCloudCommands()
	if time.Since(started) > time.Second {
		t.Fatal("repeated stop blocked")
	}
}
