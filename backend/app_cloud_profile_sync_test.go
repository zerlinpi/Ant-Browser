package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/config"
	cloudprofile "ant-chrome/desktop/profile-sync"
)

const (
	testSyncInstanceID     = "11111111-1111-4111-8111-111111111111"
	testSyncWorkspaceID    = "22222222-2222-4222-8222-222222222222"
	testSyncDeviceID       = "33333333-3333-4333-8333-333333333333"
	testSyncCloudProfileID = "44444444-4444-4444-8444-444444444444"
	testSyncUnboundID      = "55555555-5555-4555-8555-555555555555"
	testSyncLocalProfileID = "local-profile"
)

func newTestCloudProfileSyncBridge(t *testing.T, baseURL string) (*App, *cloudProfileSyncBridge) {
	t.Helper()
	cloudConfig := cloudagent.Config{
		BaseURL: baseURL, WorkspaceID: testSyncWorkspaceID, DeviceID: testSyncDeviceID,
		Bindings:      map[string]string{testSyncInstanceID: testSyncLocalProfileID, testSyncUnboundID: "other-profile"},
		CloudProfiles: map[string]string{testSyncInstanceID: testSyncCloudProfileID},
	}
	t.Setenv("ANT_CLOUD_DEVICE_CREDENTIAL", "device-secret")
	t.Setenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF", "workspace-profile-key-v1")
	t.Setenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	app := NewApp(t.TempDir())
	managerConfig := &config.Config{}
	managerConfig.Browser.UserDataRoot = "profiles"
	app.browserMgr = browser.NewManager(managerConfig, app.appRoot)
	app.browserMgr.Profiles[testSyncLocalProfileID] = &browser.Profile{ProfileId: testSyncLocalProfileID, UserDataDir: testSyncLocalProfileID}
	bridge, err := app.newCloudProfileSyncBridge(cloudConfig)
	if err != nil {
		t.Fatal(err)
	}
	return app, bridge
}

// stubBrowserProcessDiscovery replaces OS process discovery, which shells
// out to PowerShell on Windows, for the duration of a test.
func stubBrowserProcessDiscovery(t *testing.T, find func(string) ([]browserUserDataProcess, error)) {
	t.Helper()
	previous := findBrowserUserDataProcesses
	findBrowserUserDataProcesses = find
	t.Cleanup(func() { findBrowserUserDataProcesses = previous })
}

func TestCloudProfileSyncBridgeRequiresKeyMaterialAndStoppedBoundProfile(t *testing.T) {
	cloudConfig := cloudagent.Config{
		BaseURL: "https://cloud.example.test", WorkspaceID: testSyncWorkspaceID, DeviceID: testSyncDeviceID,
		Bindings: map[string]string{testSyncInstanceID: testSyncLocalProfileID}, CloudProfiles: map[string]string{testSyncInstanceID: testSyncCloudProfileID},
	}
	t.Setenv("ANT_CLOUD_DEVICE_CREDENTIAL", "device-secret")
	t.Setenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF", "workspace-profile-key-v1")
	t.Setenv("ANT_CLOUD_PROFILE_ENCRYPTION_KEY", "invalid")
	if _, err := NewApp(t.TempDir()).newCloudProfileSyncBridge(cloudConfig); err == nil {
		t.Fatal("invalid profile encryption key was accepted")
	}

	app, bridge := newTestCloudProfileSyncBridge(t, "https://cloud.example.test")
	target, err := bridge.resolveTarget(testSyncInstanceID, testSyncLocalProfileID)
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(app.appRoot, "profiles", testSyncLocalProfileID)
	if target.directory != wantDirectory || target.cloudProfileID != testSyncCloudProfileID || target.running {
		t.Fatalf("target=%+v want directory %q", target, wantDirectory)
	}
	if _, err := bridge.resolveTarget(testSyncInstanceID, "other-profile"); err == nil {
		t.Fatal("a profile outside the configured binding was accepted")
	}
	if !bridge.Synchronizes(testSyncInstanceID) || bridge.Synchronizes(testSyncUnboundID) {
		t.Fatal("cloud profile binding lookup is wrong")
	}

	// The running flag alone refuses an upload before any network access.
	app.browserMgr.Profiles[testSyncLocalProfileID].Running = true
	if err := bridge.Push(context.Background(), testSyncInstanceID, testSyncLocalProfileID); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("running browser profile was accepted for upload: %v", err)
	}
}

func TestCloudProfilePullSkipsLiveBrowser(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	app, bridge := newTestCloudProfileSyncBridge(t, server.URL)
	profile := app.browserMgr.Profiles[testSyncLocalProfileID]
	profile.Running = true
	profile.Pid = os.Getpid() // a process that is certainly alive
	if err := bridge.PullLatest(context.Background(), testSyncInstanceID, testSyncLocalProfileID); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("pull contacted the cloud for a live browser (%d requests)", requests.Load())
	}
}

func TestCloudProfilePullOfUncommittedProfileStartsLocally(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	var released atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Device device-secret" || r.Header.Get("X-Device-ID") != testSyncDeviceID {
			t.Errorf("unauthenticated profile request %s %s", r.Method, r.URL.Path)
		}
		base := "/api/v1/agent/profiles/" + testSyncCloudProfileID
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base+"/lease":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": cloudprofile.LeaseGrant{Lease: cloudprofile.CloudLease{
				ID: "66666666-6666-4666-8666-666666666666", WorkspaceID: testSyncWorkspaceID, ProfileID: testSyncCloudProfileID,
				HolderDeviceID: testSyncDeviceID, ExpiresAt: expiresAt,
			}, Token: "lease-token"}})
		case r.Method == http.MethodGet && r.URL.Path == base:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": cloudprofile.CloudProfile{ID: testSyncCloudProfileID, Status: "syncing"}})
		case r.Method == http.MethodDelete && r.URL.Path == base+"/lease":
			released.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, bridge := newTestCloudProfileSyncBridge(t, server.URL)
	if err := bridge.PullLatest(context.Background(), testSyncInstanceID, testSyncLocalProfileID); err != nil {
		t.Fatalf("a profile without cloud revisions must start from local state: %v", err)
	}
	if !released.Load() {
		t.Fatal("pull did not release its lease")
	}
}

func TestCloudProfilePushReportsUnresolvedConflict(t *testing.T) {
	stubBrowserProcessDiscovery(t, func(string) ([]browserUserDataProcess, error) { return nil, nil })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
			"code": "profile_conflict_unresolved", "message": "Resolve the open profile synchronization conflict first",
		}})
	}))
	defer server.Close()
	app, bridge := newTestCloudProfileSyncBridge(t, server.URL)
	directory := filepath.Join(app.appRoot, "profiles", testSyncLocalProfileID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Preferences"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := bridge.Push(context.Background(), testSyncInstanceID, testSyncLocalProfileID)
	if !errors.Is(err, cloudprofile.ErrConflictUnresolved) {
		t.Fatalf("push error=%v, want ErrConflictUnresolved", err)
	}
}

func TestWaitForBrowserProfileRelease(t *testing.T) {
	var calls atomic.Int32
	stubBrowserProcessDiscovery(t, func(directory string) ([]browserUserDataProcess, error) {
		if directory != "profile-dir" {
			t.Errorf("directory=%q", directory)
		}
		if calls.Add(1) <= 2 {
			return []browserUserDataProcess{{PID: 42}}, nil
		}
		return nil, nil
	})
	if err := waitForBrowserProfileRelease(context.Background(), "profile-dir", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("discovery calls=%d, want 3", calls.Load())
	}

	stubBrowserProcessDiscovery(t, func(string) ([]browserUserDataProcess, error) {
		return []browserUserDataProcess{{PID: 42}}, nil
	})
	if err := waitForBrowserProfileRelease(context.Background(), "profile-dir", 0); err == nil {
		t.Fatal("a profile still held by a browser process was released")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForBrowserProfileRelease(ctx, "profile-dir", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait error=%v", err)
	}

	stubBrowserProcessDiscovery(t, func(string) ([]browserUserDataProcess, error) {
		return nil, errors.New("process discovery unavailable")
	})
	if err := waitForBrowserProfileRelease(context.Background(), "profile-dir", time.Minute); err != nil {
		t.Fatalf("unavailable discovery blocked synchronization: %v", err)
	}
}
