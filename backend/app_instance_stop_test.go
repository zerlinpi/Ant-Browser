package backend

import (
	"testing"

	"ant-chrome/backend/internal/browser"
)

func TestBrowserInstanceStopClearsLaunchProvenance(t *testing.T) {
	app := newLaunchProvenanceTestApp(t)
	profile := &browser.Profile{
		ProfileId: "profile-stop", Running: true, DebugReady: true,
		LastLaunchArgs: []string{"--user-data-dir=profile-stop", "--fingerprint=222"},
	}
	app.browserMgr.Profiles[profile.ProfileId] = profile

	stopped, err := app.BrowserInstanceStop(profile.ProfileId)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Running || len(stopped.LastLaunchArgs) != 0 {
		t.Fatalf("stopped profile kept launch provenance: running=%v args=%q", stopped.Running, stopped.LastLaunchArgs)
	}
}

func TestBrowserProcessExitClearsLaunchProvenance(t *testing.T) {
	app := newLaunchProvenanceTestApp(t)
	cmd := launchProvenanceHelperCommand(t, "exit")
	monitor, err := newBrowserProcessMonitor(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killLaunchProvenanceHelperOnCleanup(t, cmd)
	monitor.Start()

	profile := &browser.Profile{
		ProfileId: "profile-crash", Running: true, Pid: cmd.Process.Pid,
		LastLaunchArgs: []string{"--fingerprint=222"},
	}
	app.browserMgr.Profiles[profile.ProfileId] = profile
	app.browserMgr.BrowserProcesses[profile.ProfileId] = cmd

	app.waitBrowserProcess(profile.ProfileId, monitor)

	app.browserMgr.Mutex.Lock()
	defer app.browserMgr.Mutex.Unlock()
	if profile.Running || len(profile.LastLaunchArgs) != 0 || app.browserMgr.BrowserProcesses[profile.ProfileId] != nil {
		t.Fatalf("crashed profile kept launch provenance: running=%v args=%q", profile.Running, profile.LastLaunchArgs)
	}
}

func TestDetachedBrowserDisappearanceClearsLaunchProvenance(t *testing.T) {
	app := newLaunchProvenanceTestApp(t)
	closedPort, err := nextAvailablePort()
	if err != nil {
		t.Fatal(err)
	}
	profile := &browser.Profile{
		ProfileId: "profile-detached", Running: true, DebugReady: true, DebugPort: closedPort,
		LastLaunchArgs: []string{"--remote-debugging-port=1", "--fingerprint=222"},
	}
	app.browserMgr.Profiles[profile.ProfileId] = profile

	app.waitDetachedBrowser(profile.ProfileId, closedPort)

	app.browserMgr.Mutex.Lock()
	defer app.browserMgr.Mutex.Unlock()
	if profile.Running || len(profile.LastLaunchArgs) != 0 {
		t.Fatalf("vanished detached browser kept launch provenance: running=%v args=%q", profile.Running, profile.LastLaunchArgs)
	}
}
