package backend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"ant-chrome/backend/internal/logger"
	browseragent "ant-chrome/desktop/browser-agent"
)

func TestCloudCommandSessionsRestartUntilCredentialRejected(t *testing.T) {
	results := []error{
		browseragent.ErrProtocolMismatch,
		fmt.Errorf("%w: disk full", browseragent.ErrJournalUnavailable),
		errors.New("unexpected session end"),
		fmt.Errorf("handshake: %w", browseragent.ErrDeviceRejected),
		errors.New("must not run after a credential rejection"),
	}
	calls := 0
	var delays []time.Duration
	runCloudCommandSessions(context.Background(), logger.New("CloudCommandsTest"), func(context.Context) error {
		err := results[calls]
		calls++
		return err
	}, func(_ context.Context, delay time.Duration) bool {
		delays = append(delays, delay)
		return true
	})
	if calls != 4 {
		t.Fatalf("sessions=%d, want 4", calls)
	}
	want := []time.Duration{cloudCommandRestartMin, 2 * cloudCommandRestartMin, 4 * cloudCommandRestartMin}
	if !reflect.DeepEqual(delays, want) {
		t.Fatalf("restart delays=%v, want %v", delays, want)
	}
}

func TestCloudCommandSessionsStopWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	runCloudCommandSessions(ctx, logger.New("CloudCommandsTest"), func(context.Context) error {
		calls++
		return browseragent.ErrProtocolMismatch
	}, func(context.Context, time.Duration) bool {
		cancel()
		return false
	})
	if calls != 1 {
		t.Fatalf("sessions=%d after cancellation, want 1", calls)
	}

	calls = 0
	runCloudCommandSessions(ctx, logger.New("CloudCommandsTest"), func(context.Context) error {
		calls++
		return nil
	}, func(context.Context, time.Duration) bool { return true })
	if calls != 0 {
		t.Fatal("a cancelled context started a session")
	}
}

func TestCloudCommandRestartBackoffIsBounded(t *testing.T) {
	calls := 0
	var delays []time.Duration
	runCloudCommandSessions(context.Background(), logger.New("CloudCommandsTest"), func(context.Context) error {
		calls++
		if calls > 8 {
			return browseragent.ErrDeviceRejected
		}
		return browseragent.ErrProtocolMismatch
	}, func(_ context.Context, delay time.Duration) bool {
		delays = append(delays, delay)
		return true
	})
	if last := delays[len(delays)-1]; last != cloudCommandRestartMax {
		t.Fatalf("backoff grew to %v, want cap %v (delays %v)", last, cloudCommandRestartMax, delays)
	}
}

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
