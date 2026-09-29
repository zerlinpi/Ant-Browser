package backend

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/config"
)

// launchProvenanceHelperEnv switches the test binary into a stand-in browser
// process. "exit" terminates immediately with a failure status; "devtools"
// serves a minimal DevTools HTTP endpoint on the --remote-debugging-port it
// was launched with and exits when a Browser.close websocket is requested.
const launchProvenanceHelperEnv = "ANT_LAUNCH_PROVENANCE_HELPER"

const launchProvenanceHelperTest = "^TestLaunchProvenanceHelperProcess$"

// TestLaunchProvenanceHelperProcess is not a real test. The launch provenance
// tests execute the test binary itself as a fake browser process.
func TestLaunchProvenanceHelperProcess(t *testing.T) {
	mode := os.Getenv(launchProvenanceHelperEnv)
	if mode == "" {
		return
	}
	// Never outlive a failed parent test.
	time.AfterFunc(60*time.Second, func() { os.Exit(5) })
	switch mode {
	case "exit":
		os.Exit(3)
	case "devtools":
		port := ""
		for _, argument := range os.Args {
			if value, ok := strings.CutPrefix(argument, "--remote-debugging-port="); ok {
				port = value
			}
		}
		listener, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			os.Exit(4)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"Browser":"LaunchProvenanceHelper/1.0","webSocketDebuggerUrl":"ws://127.0.0.1:%s/devtools/browser/helper"}`, port)
		})
		mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
			// The only other request is the Browser.close websocket upgrade.
			_ = listener.Close()
			os.Exit(0)
		})
		_ = http.Serve(listener, mux)
		os.Exit(0)
	}
	os.Exit(2)
}

func newLaunchProvenanceTestApp(t *testing.T) *App {
	t.Helper()
	app := NewApp(t.TempDir())
	app.config = &config.Config{}
	app.browserMgr = browser.NewManager(app.config, app.appRoot)
	return app
}

func launchProvenanceTestBinary(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Skipf("test binary path is unavailable: %v", err)
	}
	return executable
}

func launchProvenanceHelperCommand(t *testing.T, mode string, extraArgs ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"-test.run=" + launchProvenanceHelperTest, "--"}, extraArgs...)
	cmd := exec.Command(launchProvenanceTestBinary(t), args...)
	cmd.Env = append(os.Environ(), launchProvenanceHelperEnv+"="+mode)
	return cmd
}

func killLaunchProvenanceHelperOnCleanup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	process := cmd.Process
	t.Cleanup(func() { _ = process.Kill() })
}

func TestMarkProfileRunningLockedClearsProvenanceForDiscoveredProcess(t *testing.T) {
	app := newLaunchProvenanceTestApp(t)
	profile := &browser.Profile{ProfileId: "profile-adopted", LastLaunchArgs: []string{"--fingerprint=111", "--load-extension=C:\\stale"}}
	app.browserMgr.Profiles[profile.ProfileId] = profile

	app.browserMgr.Mutex.Lock()
	app.markProfileRunningLocked(profile.ProfileId, profile, nil, 4242, 9222, true, "")
	app.browserMgr.Mutex.Unlock()

	if !profile.Running || profile.Pid != 4242 {
		t.Fatalf("discovered process was not attached: %+v", profile)
	}
	if len(profile.LastLaunchArgs) != 0 {
		t.Fatalf("discovered process inherited launch provenance %q", profile.LastLaunchArgs)
	}
}

func TestMarkProfileLastLaunchArgsLockedCopiesAndClears(t *testing.T) {
	app := newLaunchProvenanceTestApp(t)
	profile := &browser.Profile{ProfileId: "profile-args"}
	args := []string{"--fingerprint=222", "--lang=en-US"}

	app.markProfileLastLaunchArgsLocked(profile, args)
	args[0] = "--fingerprint=mutated"
	if strings.Join(profile.LastLaunchArgs, " ") != "--fingerprint=222 --lang=en-US" {
		t.Fatalf("launch provenance aliased the caller slice: %q", profile.LastLaunchArgs)
	}

	app.markProfileLastLaunchArgsLocked(profile, nil)
	if profile.LastLaunchArgs != nil {
		t.Fatalf("empty launch arguments did not clear provenance: %#v", profile.LastLaunchArgs)
	}
}

func TestFingerprintCheckDoesNotBackfillProvenanceFromProcessTable(t *testing.T) {
	originalFinder := findBrowserUserDataProcesses
	defer func() { findBrowserUserDataProcesses = originalFinder }()

	app := newLaunchProvenanceTestApp(t)
	profile := &browser.Profile{ProfileId: "profile-discovered", Running: true, Pid: 1234, DebugPort: 9222}
	app.browserMgr.Profiles[profile.ProfileId] = profile
	findBrowserUserDataProcesses = func(string) ([]browserUserDataProcess, error) {
		return []browserUserDataProcess{{
			PID: 1234, DebugPort: 9222,
			CommandLine: `"C:\Chrome\chrome.exe" --remote-debugging-port=9222 --fingerprint=676448312360042767 --lang=zh-CN`,
		}}, nil
	}

	for name, derive := range map[string]func(*BrowserProfile) []string{
		"snapshot": app.fingerprintCheckExpectedArgsFromProfile,
		"locked":   app.fingerprintCheckExpectedArgsFromLockedProfile,
	} {
		expected := buildBrowserFingerprintExpected(derive(profile))
		if expected.Seed != "676448312360042767" {
			t.Fatalf("%s: recovered seed = %q", name, expected.Seed)
		}
		if len(profile.LastLaunchArgs) != 0 {
			t.Fatalf("%s: recovered command line became launch provenance: %q", name, profile.LastLaunchArgs)
		}
	}
}
