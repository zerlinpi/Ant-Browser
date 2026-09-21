package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fingerprintloader "ant-chrome/browser-runtime/fingerprint-loader"
	"ant-chrome/browser-runtime/launcher"
	profileloader "ant-chrome/browser-runtime/profile-loader"
)

type fakeLauncher struct {
	mu     sync.Mutex
	starts int
	stops  int
	config launcher.Config
}

func (f *fakeLauncher) StartProcess(_ context.Context, config launcher.Config) (launcher.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.config = config
	return launcher.Process{PID: 42, CDPEndpoint: "ws://127.0.0.1:9222/devtools/browser/test", StartedAt: time.Now()}, nil
}
func (f *fakeLauncher) Stop(_ int) error { f.mu.Lock(); f.stops++; f.mu.Unlock(); return nil }

type fakeProfiles struct{ path string }

func (f fakeProfiles) PrepareContext(context.Context, profileloader.Profile) (profileloader.Prepared, error) {
	return profileloader.Prepared{ID: "profile", Path: f.path}, nil
}

type fakeFingerprints struct{}

func (fakeFingerprints) Prepare(fingerprintloader.Template) (fingerprintloader.Runtime, error) {
	return fingerprintloader.Runtime{ID: "fp", Args: []string{"--headless=new"}}, nil
}

type fakeCDP struct {
	mu       sync.Mutex
	attempts int
	fail     int
	closed   bool
}

type contextAwareCDP struct {
	mu       sync.Mutex
	attempts int
	closed   bool
}

func (f *contextAwareCDP) Connect() error { return errors.New("legacy connect should not be used") }
func (f *contextAwareCDP) ConnectContext(ctx context.Context) error {
	f.mu.Lock()
	f.attempts++
	f.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}
func (f *contextAwareCDP) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeCDP) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.fail {
		return errors.New("not ready")
	}
	return nil
}
func (f *fakeCDP) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }

func TestStartFlowRetriesCDPAndStops(t *testing.T) {
	launcherFake := &fakeLauncher{}
	cdpFake := &fakeCDP{fail: 2}
	flow := NewStartFlow(WithLauncher(launcherFake), WithProfileLoader(fakeProfiles{path: filepath.Join(t.TempDir(), "profile")}), WithFingerprintLoader(fakeFingerprints{}), WithCDPFactory(func(string) CDPClient { return cdpFake }), WithCDPReadiness(100*time.Millisecond, time.Millisecond))
	session, err := flow.StartContext(context.Background(), StartSpec{InstanceID: "instance-1", Fingerprint: fingerprintloader.Template{ID: "fp", Config: map[string]interface{}{}}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "running" || cdpFake.attempts != 3 {
		t.Fatalf("unexpected startup: session=%+v attempts=%d", session, cdpFake.attempts)
	}
	if err := flow.Stop("instance-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := flow.manager.Get("instance-1"); ok || launcherFake.stops != 1 || !cdpFake.closed {
		t.Fatalf("stop did not clean up")
	}
}

func TestStartFlowCleansProcessWhenCDPNeverReady(t *testing.T) {
	launcherFake := &fakeLauncher{}
	cdpFake := &fakeCDP{fail: 100}
	flow := NewStartFlow(WithLauncher(launcherFake), WithProfileLoader(fakeProfiles{path: filepath.Join(t.TempDir(), "profile")}), WithFingerprintLoader(fakeFingerprints{}), WithCDPFactory(func(string) CDPClient { return cdpFake }), WithCDPReadiness(8*time.Millisecond, time.Millisecond))
	if _, err := flow.StartContext(context.Background(), StartSpec{InstanceID: "instance-timeout", Fingerprint: fingerprintloader.Template{ID: "fp", Config: map[string]interface{}{}}}); err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	if launcherFake.stops != 1 || !cdpFake.closed {
		t.Fatalf("failed startup leaked resources: stops=%d closed=%v", launcherFake.stops, cdpFake.closed)
	}
	if _, ok := flow.manager.Get("instance-timeout"); ok {
		t.Fatal("failed startup registered a session")
	}
}

func TestStartFlowBoundsContextAwareCDPConnect(t *testing.T) {
	launcherFake := &fakeLauncher{}
	cdpFake := &contextAwareCDP{}
	flow := NewStartFlow(WithLauncher(launcherFake), WithProfileLoader(fakeProfiles{path: filepath.Join(t.TempDir(), "profile")}), WithFingerprintLoader(fakeFingerprints{}), WithCDPFactory(func(string) CDPClient { return cdpFake }), WithCDPReadiness(15*time.Millisecond, time.Millisecond))
	started := time.Now()
	if _, err := flow.StartContext(context.Background(), StartSpec{InstanceID: "instance-context-timeout", Fingerprint: fingerprintloader.Template{ID: "fp", Config: map[string]interface{}{}}}); err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("context-aware CDP connect exceeded readiness bound: %v", elapsed)
	}
	if cdpFake.attempts != 1 || !cdpFake.closed || launcherFake.stops != 1 {
		t.Fatalf("failed startup did not clean up: attempts=%d closed=%v stops=%d", cdpFake.attempts, cdpFake.closed, launcherFake.stops)
	}
}
