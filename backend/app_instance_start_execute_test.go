package backend

import (
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"ant-chrome/backend/internal/browser"
)

func launchProvenanceTestPlan(profile *BrowserProfile, binary string, args []string, debugPort int, userDataDir string) *browserStartPlan {
	return &browserStartPlan{
		profile:           profile,
		chromeBinaryPath:  binary,
		userDataDir:       userDataDir,
		args:              args,
		assignedDebugPort: debugPort,
		startReadyTimeout: 10 * time.Second,
		startStableWindow: 300 * time.Millisecond,
		maxStartAttempts:  1,
		totalReadyTimeout: 10 * time.Second,
	}
}

func startWithLaunchProvenancePlan(app *App, profile *BrowserProfile, plan *browserStartPlan) (*BrowserProfile, error) {
	app.browserMgr.Mutex.Lock()
	defer app.browserMgr.Mutex.Unlock()
	return app.startBrowserProfileWithPlan(browserStartInput{ProfileID: profile.ProfileId}, plan)
}

func TestFailedBrowserStartLeavesNoLaunchProvenance(t *testing.T) {
	t.Run("executable cannot start", func(t *testing.T) {
		app := newLaunchProvenanceTestApp(t)
		profile := &browser.Profile{ProfileId: "profile-missing-binary", LastLaunchArgs: []string{"--stale=previous-launch"}}
		app.browserMgr.Profiles[profile.ProfileId] = profile
		plan := launchProvenanceTestPlan(profile, filepath.Join(t.TempDir(), "missing-browser.exe"), []string{"--fingerprint=222"}, 0, t.TempDir())

		if _, err := startWithLaunchProvenancePlan(app, profile, plan); err == nil {
			t.Fatal("start with a missing executable succeeded")
		}
		if profile.Running || len(profile.LastLaunchArgs) != 0 {
			t.Fatalf("failed start left launch provenance: running=%v args=%q", profile.Running, profile.LastLaunchArgs)
		}
	})

	t.Run("process exits before debug port is ready", func(t *testing.T) {
		t.Setenv(launchProvenanceHelperEnv, "exit")
		app := newLaunchProvenanceTestApp(t)
		profile := &browser.Profile{ProfileId: "profile-early-exit", LastLaunchArgs: []string{"--stale=previous-launch"}}
		app.browserMgr.Profiles[profile.ProfileId] = profile
		args := []string{"-test.run=" + launchProvenanceHelperTest, "--", "--fingerprint=222"}
		plan := launchProvenanceTestPlan(profile, launchProvenanceTestBinary(t), args, 0, t.TempDir())

		if _, err := startWithLaunchProvenancePlan(app, profile, plan); err == nil {
			t.Fatal("start of an exiting process succeeded")
		}
		if profile.Running || len(profile.LastLaunchArgs) != 0 || app.browserMgr.BrowserProcesses[profile.ProfileId] != nil {
			t.Fatalf("failed start left launch provenance: running=%v args=%q", profile.Running, profile.LastLaunchArgs)
		}
	})
}

func TestSuccessfulBrowserStartRecordsProvenanceUntilStop(t *testing.T) {
	debugPort, err := nextAvailablePort()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(launchProvenanceHelperEnv, "devtools")
	app := newLaunchProvenanceTestApp(t)
	profile := &browser.Profile{ProfileId: "profile-launched", LastLaunchArgs: []string{"--stale=previous-launch"}}
	app.browserMgr.Profiles[profile.ProfileId] = profile
	args := []string{
		"-test.run=" + launchProvenanceHelperTest, "--",
		"--remote-debugging-port=" + strconv.Itoa(debugPort), "--fingerprint=222",
	}
	plan := launchProvenanceTestPlan(profile, launchProvenanceTestBinary(t), args, debugPort, t.TempDir())

	started, err := startWithLaunchProvenancePlan(app, profile, plan)
	app.browserMgr.Mutex.Lock()
	killLaunchProvenanceHelperOnCleanup(t, app.browserMgr.BrowserProcesses[profile.ProfileId])
	running, recorded := started.Running, append([]string(nil), started.LastLaunchArgs...)
	app.browserMgr.Mutex.Unlock()
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !running || !reflect.DeepEqual(recorded, args) {
		t.Fatalf("tracked launch provenance = %q (running=%v), want %q", recorded, running, args)
	}

	stopped, err := app.BrowserInstanceStop(profile.ProfileId)
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	app.browserMgr.Mutex.Lock()
	defer app.browserMgr.Mutex.Unlock()
	if stopped.Running || len(stopped.LastLaunchArgs) != 0 {
		t.Fatalf("stopped profile kept launch provenance: running=%v args=%q", stopped.Running, stopped.LastLaunchArgs)
	}
}
