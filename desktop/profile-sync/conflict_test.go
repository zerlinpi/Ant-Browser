package profilesync

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testConflictID         = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	testConflictRevisionID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	testConflictObjectID   = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	testConflictObjectKey  = "workspaces/test/conflict-object"
)

var conflictTestLimits = Limits{MaxFiles: 32, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}

// conflictGateway fakes the gateway's revision protocol for a profile whose
// current revision the test controls.
type conflictGateway struct {
	t         *testing.T
	mu        sync.Mutex
	current   string
	uploadURL string
	begins    []beginRecord
	uploaded  []byte
	released  int
	committed int
}

type beginRecord struct {
	BaseRevisionID string              `json:"baseRevisionId"`
	Mode           string              `json:"mode"`
	Files          []revisionFileInput `json:"files"`
}

func newConflictGateway(t *testing.T, current string) (*conflictGateway, *httptest.Server) {
	gateway := &conflictGateway{t: t, current: current}
	server := httptest.NewServer(http.HandlerFunc(gateway.serve))
	t.Cleanup(server.Close)
	gateway.uploadURL = server.URL + "/upload"
	return gateway, server
}

func (g *conflictGateway) setCurrent(revisionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.current = revisionID
}

func (g *conflictGateway) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
		write(renewedTestLease())
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
		write(LeaseGrant{Lease: renewedTestLease(), Token: "lease-token"})
	case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
		g.released++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
		write(CloudProfile{ID: testProfileID, CurrentRevisionID: g.current, Status: "syncing"})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revisions"):
		var input beginRecord
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			g.t.Errorf("decode begin: %v", err)
			return
		}
		g.begins = append(g.begins, input)
		if input.BaseRevisionID == g.current || len(input.Files) != 1 {
			g.t.Errorf("fake gateway only models conflicting snapshots: %+v current=%q", input, g.current)
			return
		}
		file := input.Files[0]
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": RevisionPlan{
				Revision: Revision{ID: testConflictRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 3, BaseRevisionID: input.BaseRevisionID, Status: "uploading"},
				Manifest: Manifest{RevisionID: testConflictRevisionID, SchemaVersion: manifestSchema, Mode: "snapshot", FileCount: 1, TotalBytes: file.SizeBytes, Files: []ManifestFile{{Path: file.Path, ObjectKey: testConflictObjectKey, CiphertextSHA256: file.CiphertextSHA256, SizeBytes: file.SizeBytes, ContentType: file.ContentType}}},
				Objects:  []CloudObject{{ID: testConflictObjectID, WorkspaceID: testWorkspaceID, RevisionID: testConflictRevisionID, ObjectKey: testConflictObjectKey, ContentHash: file.CiphertextSHA256, SizeBytes: file.SizeBytes, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: file.ContentType}},
				Conflict: &CloudConflict{ID: testConflictID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, LocalRevisionID: testConflictRevisionID, RemoteRevisionID: g.current, Status: "open"},
			},
			"error": map[string]any{"code": codeRevisionConflict, "message": "The cloud profile changed after the requested base revision", "details": map[string]string{"conflictId": testConflictID}},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revisions/"+testConflictRevisionID+"/objects/"+testConflictObjectID+"/upload"):
		write(ObjectGrant{ObjectID: testConflictObjectID, ObjectKey: testConflictObjectKey, Method: http.MethodPut, URL: g.uploadURL, Headers: map[string]string{"Content-Type": archiveContentType}, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	case r.Method == http.MethodPut && r.URL.Path == "/upload":
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			g.t.Errorf("read upload: %v", err)
		}
		g.uploaded = payload
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(r.URL.Path, "/commit"):
		g.committed++
		g.t.Error("a conflicting revision must never be committed by the device")
		http.Error(w, "unexpected commit", http.StatusInternalServerError)
	default:
		g.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func TestPushOpensConflictInsteadOfOverwritingCloud(t *testing.T) {
	for _, test := range []struct {
		name      string
		withState bool
		wantBase  string
	}{
		{name: "stale local baseline", withState: true, wantBase: testBaseRevisionID},
		{name: "device never synchronized", withState: false, wantBase: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "profile")
			writeProfileTestFile(t, source, "Default/Preferences", "local-changes")
			gateway, server := newConflictGateway(t, testCurrentRevisionID)
			client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
			if test.withState {
				baseline, err := scanProfileDirectory(context.Background(), source, conflictTestLimits)
				if err != nil {
					t.Fatal(err)
				}
				if err := client.writeLocalState(source, localSyncState{RevisionID: testBaseRevisionID, ChainDepth: 1, Files: baseline}); err != nil {
					t.Fatal(err)
				}
			}

			_, err := client.Push(context.Background(), source, "")
			var conflict *RevisionConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("push error=%v, want *RevisionConflictError", err)
			}
			if conflict.ConflictID() != testConflictID || !conflict.SnapshotUploaded || conflict.UploadErr != nil || !strings.Contains(err.Error(), testConflictID) {
				t.Fatalf("conflict=%+v message=%q", conflict, err.Error())
			}
			gateway.mu.Lock()
			begins, uploaded, released := append([]beginRecord(nil), gateway.begins...), len(gateway.uploaded), gateway.released
			gateway.mu.Unlock()
			if len(begins) != 1 || begins[0].Mode != "snapshot" || begins[0].BaseRevisionID != test.wantBase {
				t.Fatalf("begin requests=%+v, want one snapshot on base %q", begins, test.wantBase)
			}
			if uploaded == 0 || released != 1 {
				t.Fatalf("uploaded=%d released=%d: keep_local needs the snapshot uploaded and the lease free", uploaded, released)
			}
			state, found, err := client.loadLocalState(source)
			if err != nil || !found {
				t.Fatalf("state found=%v err=%v", found, err)
			}
			wantRevision := ""
			if test.withState {
				wantRevision = testBaseRevisionID
			}
			if state.RevisionID != wantRevision || state.PendingRevisionID != testConflictRevisionID || state.PendingFiles["Default/Preferences"].Size != int64(len("local-changes")) {
				t.Fatalf("conflict state=%+v", state)
			}

			// keep_local promotes the uploaded snapshot; the device adopts it
			// as its baseline instead of opening a second conflict.
			gateway.setCurrent(testConflictRevisionID)
			result, err := client.Push(context.Background(), source, "")
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoChanges || result.Revision.ID != testConflictRevisionID {
				t.Fatalf("push after keep_local=%+v", result)
			}
			state, _, err = client.loadLocalState(source)
			if err != nil || state.RevisionID != testConflictRevisionID || state.PendingRevisionID != "" || len(state.PendingFiles) != 0 {
				t.Fatalf("state after keep_local=%+v err=%v", state, err)
			}
			gateway.mu.Lock()
			defer gateway.mu.Unlock()
			if len(gateway.begins) != 1 || gateway.released != 2 {
				t.Fatalf("begins=%d released=%d after adopting keep_local", len(gateway.begins), gateway.released)
			}
		})
	}
}

func TestCorruptLocalStateCannotOverwriteCloudOrBlockSync(t *testing.T) {
	source := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, source, "Default/Preferences", "local")
	gateway, server := newConflictGateway(t, testCurrentRevisionID)
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
	statePath, err := client.statePath(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"schemaVersion":"truncated`), 0o600); err != nil {
		t.Fatal(err)
	}
	var conflict *RevisionConflictError
	if _, err := client.Push(context.Background(), source, ""); !errors.As(err, &conflict) || !conflict.SnapshotUploaded {
		t.Fatalf("push with corrupt state error=%v, want an uploaded conflict", err)
	}
	gateway.mu.Lock()
	begins := append([]beginRecord(nil), gateway.begins...)
	gateway.mu.Unlock()
	if len(begins) != 1 || begins[0].BaseRevisionID != "" {
		t.Fatalf("corrupt state produced begin requests %+v, want one without a base", begins)
	}
	state, found, err := client.loadLocalState(source)
	if err != nil || !found || state.PendingRevisionID != testConflictRevisionID {
		t.Fatalf("state was not rewritten: %+v found=%v err=%v", state, found, err)
	}
}

func TestPushAfterKeepRemoteConflictsAgain(t *testing.T) {
	source := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, source, "Default/Preferences", "rejected-local-changes")
	gateway, server := newConflictGateway(t, testCurrentRevisionID)
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
	baseline, err := scanProfileDirectory(context.Background(), source, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	// keep_remote left the cloud on its current revision and superseded the
	// pending snapshot, even though the recorded baseline equals current.
	if err := client.writeLocalState(source, localSyncState{
		RevisionID: testCurrentRevisionID, Files: baseline,
		PendingRevisionID: testConflictRevisionID, PendingFiles: baseline,
	}); err != nil {
		t.Fatal(err)
	}
	var conflict *RevisionConflictError
	if _, err := client.Push(context.Background(), source, ""); !errors.As(err, &conflict) {
		t.Fatalf("rejected local changes were pushed: %v", err)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if len(gateway.begins) != 1 || gateway.begins[0].BaseRevisionID != "" || gateway.begins[0].Mode != "snapshot" {
		t.Fatalf("begin requests=%+v", gateway.begins)
	}
}

func TestSyncRefusesUnresolvedConflict(t *testing.T) {
	for _, test := range []struct {
		name          string
		leaseConflict bool
	}{
		{name: "lease blocked by server", leaseConflict: true},
		{name: "profile reports conflict", leaseConflict: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls = append(calls, r.Method+" "+r.URL.Path[strings.LastIndex(r.URL.Path, "/"):])
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
					if test.leaseConflict {
						w.WriteHeader(http.StatusConflict)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": codeConflictUnresolved, "message": "Resolve the open profile synchronization conflict first"}})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": LeaseGrant{Lease: renewedTestLease(), Token: "lease-token"}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": CloudProfile{ID: testProfileID, CurrentRevisionID: testCurrentRevisionID, Status: "conflict"}})
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("synchronization continued during a conflict: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := filepath.Join(t.TempDir(), "profile")
			writeProfileTestFile(t, source, "Preferences", "local")
			client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
			if _, err := client.Push(context.Background(), source, ""); !errors.Is(err, ErrConflictUnresolved) {
				t.Fatalf("push error=%v", err)
			}
			if _, err := client.PullCurrent(context.Background(), source); !errors.Is(err, ErrConflictUnresolved) {
				t.Fatalf("pull error=%v", err)
			}
			if got, err := os.ReadFile(filepath.Join(source, "Preferences")); err != nil || string(got) != "local" {
				t.Fatalf("local profile changed during a conflict: %q %v", got, err)
			}
			if !test.leaseConflict {
				mu.Lock()
				defer mu.Unlock()
				releases := 0
				for _, call := range calls {
					if call == "DELETE /lease" {
						releases++
					}
				}
				if releases != 2 {
					t.Fatalf("leases released=%d, want 2 (calls %v)", releases, calls)
				}
			}
		})
	}
}

// pullGateway serves one committed snapshot revision as the profile's current
// revision and records the order of requests.
func newPullGateway(t *testing.T, current string, ciphertext []byte, summary archiveSummary, renewStatus int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	downloadURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			if renewStatus != http.StatusOK {
				w.WriteHeader(renewStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "profile_lease_invalid", "message": "lease expired"}})
				return
			}
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: renewedTestLease(), Token: "lease-token"})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			write(CloudProfile{ID: testProfileID, CurrentRevisionID: current, Status: "syncing"})
		case r.Method == http.MethodGet && current != "" && strings.HasSuffix(r.URL.Path, "/revisions/"+current):
			write(snapshotTestPlan(current, testObjectID, testObjectKey, "committed", 2, summary))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects/"+testObjectID+"/download"):
			write(ObjectGrant{ObjectID: testObjectID, ObjectKey: testObjectKey, Method: http.MethodGet, URL: downloadURL, ExpiresAt: time.Now().UTC().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			_, _ = w.Write(ciphertext)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	downloadURL = server.URL + "/download"
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func TestPullCurrentUsesRevisionReadUnderLease(t *testing.T) {
	cloudSource := t.TempDir()
	writeProfileTestFile(t, cloudSource, "Default/Cookies", "cloud-cookie")
	ciphertext, summary := makeEncryptedProfileObject(t, cloudSource, conflictTestLimits)
	server, calls := newPullGateway(t, testCurrentRevisionID, ciphertext, summary, http.StatusOK)
	destination := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, destination, "stale", "old")
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)

	revision, err := client.PullCurrent(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if revision.ID != testCurrentRevisionID {
		t.Fatalf("revision=%+v", revision)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Default", "Cookies")); err != nil || string(got) != "cloud-cookie" {
		t.Fatalf("pulled cookie=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale local file survived the pull: %v", err)
	}
	state, found, err := client.loadLocalState(destination)
	if err != nil || !found || state.RevisionID != testCurrentRevisionID {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
	recorded := calls()
	if len(recorded) < 2 || !strings.HasSuffix(recorded[0], "/lease") || !strings.HasSuffix(recorded[1], "/profiles/"+testProfileID) {
		t.Fatalf("the current revision was not read under the lease: %v", recorded)
	}
	renewedBeforeRelease := false
	for index, call := range recorded {
		if strings.HasSuffix(call, "/revisions") {
			t.Fatalf("pull listed revisions outside the lease: %v", recorded)
		}
		if strings.HasSuffix(call, "/lease/renew") && index+1 < len(recorded) && strings.HasPrefix(recorded[index+1], http.MethodDelete) {
			renewedBeforeRelease = true
		}
	}
	if !renewedBeforeRelease {
		t.Fatalf("lease was not confirmed before the local swap: %v", recorded)
	}

	// The directory now derives from the current revision; a second pull
	// keeps it, including changes made locally since, without downloading.
	writeProfileTestFile(t, destination, "Default/Cookies", "newer-local-cookie")
	if _, err := client.PullCurrent(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Default", "Cookies")); err != nil || string(got) != "newer-local-cookie" {
		t.Fatalf("local changes on top of the current revision were discarded: %q %v", got, err)
	}
	for _, call := range calls()[len(recorded):] {
		if strings.Contains(call, "/revisions/") || strings.HasSuffix(call, "/download") {
			t.Fatalf("an up-to-date profile was downloaded again: %v", calls()[len(recorded):])
		}
	}

	// A wiped directory is restored even though the state still matches.
	if err := os.RemoveAll(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PullCurrent(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Default", "Cookies")); err != nil || string(got) != "cloud-cookie" {
		t.Fatalf("wiped profile was not restored: %q %v", got, err)
	}
}

func TestPullCurrentAdoptsKeepLocalSnapshotWithoutDownloading(t *testing.T) {
	server, calls := newPullGateway(t, testConflictRevisionID, nil, archiveSummary{}, http.StatusOK)
	destination := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, destination, "Default/Preferences", "kept-local")
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
	inventory, err := scanProfileDirectory(context.Background(), destination, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.writeLocalState(destination, localSyncState{
		RevisionID: testBaseRevisionID, Files: map[string]fileSignature{},
		PendingRevisionID: testConflictRevisionID, PendingFiles: inventory,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PullCurrent(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls() {
		if strings.Contains(call, "/revisions/") {
			t.Fatalf("promoted local snapshot was downloaded: %v", calls())
		}
	}
	state, _, err := client.loadLocalState(destination)
	if err != nil || state.RevisionID != testConflictRevisionID || state.PendingRevisionID != "" || !reflect.DeepEqual(state.Files, inventory) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestPullCurrentWithoutCloudRevision(t *testing.T) {
	server, calls := newPullGateway(t, "", nil, archiveSummary{}, http.StatusOK)
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
	if _, err := client.PullCurrent(context.Background(), filepath.Join(t.TempDir(), "profile")); !errors.Is(err, ErrNoCloudRevision) {
		t.Fatalf("error=%v, want ErrNoCloudRevision", err)
	}
	recorded := calls()
	if len(recorded) == 0 || !strings.HasPrefix(recorded[len(recorded)-1], http.MethodDelete) {
		t.Fatalf("lease was not released: %v", recorded)
	}
}

func TestPullKeepsLocalProfileWhenLeaseIsLostBeforeSwap(t *testing.T) {
	cloudSource := t.TempDir()
	writeProfileTestFile(t, cloudSource, "Preferences", "cloud")
	ciphertext, summary := makeEncryptedProfileObject(t, cloudSource, conflictTestLimits)
	server, _ := newPullGateway(t, testCurrentRevisionID, ciphertext, summary, http.StatusConflict)
	destination := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, destination, "Preferences", "local")
	client := newIncrementalTestClient(t, server.URL, conflictTestLimits)
	if _, err := client.PullCurrent(context.Background(), destination); err == nil || !strings.Contains(err.Error(), "confirm profile lease") {
		t.Fatalf("pull error=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Preferences")); err != nil || string(got) != "local" {
		t.Fatalf("local profile replaced without a confirmed lease: %q %v", got, err)
	}
	if _, found, err := client.loadLocalState(destination); err != nil || found {
		t.Fatalf("state recorded without a confirmed lease: found=%v err=%v", found, err)
	}
}

func TestTransientProfileEntriesAreNeverSynchronized(t *testing.T) {
	source := filepath.Join(t.TempDir(), "profile")
	for _, name := range []string{
		"Local State", "Default/Preferences", "Default/Network/Cookies",
		"Default/Extensions/abc/1.0/Cache/kept.js", "Default/Service Worker/CacheStorage/kept",
		"lockfile", "DevToolsActivePort", "BrowserMetrics-spare.pma", "BrowserMetrics/BrowserMetrics-1.pma",
		"Crashpad/reports/dump", "GrShaderCache/data_0", "ShaderCache/data_0", "component_crx_cache/x",
		"Default/Cache/Cache_Data/data_0", "Default/Code Cache/js/index", "Default/GPUCache/data_0",
		"Default/DawnWebGPUCache/data_0", "Default/DawnGraphiteCache/data_0", "Default/Preferences.tmp",
	} {
		writeProfileTestFile(t, source, name, "x")
	}
	// Chromium's Singleton* entries are symbolic links; they are skipped by
	// name instead of failing the whole synchronization.
	if err := os.Symlink("host-12345", filepath.Join(source, "SingletonLock")); err != nil {
		t.Logf("Singleton symlink check skipped on this platform: %v", err)
	}
	want := []string{
		"Default/Extensions/abc/1.0/Cache/kept.js", "Default/Network/Cookies", "Default/Preferences",
		"Default/Service Worker/CacheStorage/kept", "Local State",
	}

	inventory, err := scanProfileDirectory(context.Background(), source, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(inventory); !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned entries=%v want=%v", got, want)
	}
	archivePath, _, err := createArchive(context.Background(), source, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archivePath)
	archived, err := scanArchiveInventory(context.Background(), archivePath, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(archived); !reflect.DeepEqual(got, want) {
		t.Fatalf("archived entries=%v want=%v", got, want)
	}
	if !localProfileHasFiles(source) {
		t.Fatal("profile with files reported empty")
	}
	onlyTransient := filepath.Join(t.TempDir(), "transient")
	writeProfileTestFile(t, onlyTransient, "lockfile", "x")
	writeProfileTestFile(t, onlyTransient, "Default/Cache/data_0", "x")
	if localProfileHasFiles(onlyTransient) {
		t.Fatal("a directory holding only transient files was treated as a profile")
	}

	// Archives from older clients may still carry transient entries.
	legacy := filepath.Join(t.TempDir(), "legacy.zip")
	file, err := os.Create(legacy)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range []string{"Default/Preferences", "DevToolsActivePort", "lockfile", "Default/Cache/Cache_Data/data_0"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(entry, "x")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	staging, err := extractArchiveToTemp(context.Background(), legacy, conflictTestLimits, filepath.Join(t.TempDir(), "restored"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staging)
	restored, err := scanProfileDirectory(context.Background(), staging, conflictTestLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(restored); !reflect.DeepEqual(got, []string{"Default/Preferences"}) {
		t.Fatalf("restored entries=%v", got)
	}
	for _, name := range []string{"DevToolsActivePort", "lockfile"} {
		if _, err := os.Lstat(filepath.Join(staging, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("transient %s was restored: %v", name, err)
		}
	}
}

func TestLocalSyncStatePendingAndDirectoryBinding(t *testing.T) {
	client := newIncrementalTestClient(t, "http://127.0.0.1:1", conflictTestLimits)
	parent := t.TempDir()
	profileDir := filepath.Join(parent, "profile")
	signature := fileSignature{Size: 1, SHA256: strings.Repeat("a", 64)}
	pendingOnly := localSyncState{PendingRevisionID: testConflictRevisionID, PendingFiles: map[string]fileSignature{"Preferences": signature}}
	if err := client.writeLocalState(profileDir, pendingOnly); err != nil {
		t.Fatalf("pending-only state rejected: %v", err)
	}
	loaded, found, err := client.loadLocalState(profileDir)
	if err != nil || !found || loaded.RevisionID != "" || loaded.PendingRevisionID != testConflictRevisionID {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	for name, invalid := range map[string]localSyncState{
		"no revision":             {},
		"pending files alone":     {RevisionID: testBaseRevisionID, PendingFiles: map[string]fileSignature{"Preferences": signature}},
		"pending equals baseline": {RevisionID: testBaseRevisionID, PendingRevisionID: testBaseRevisionID},
		"pending-only with depth": {ChainDepth: 1, PendingRevisionID: testConflictRevisionID},
		"unsafe pending path":     {RevisionID: testBaseRevisionID, PendingRevisionID: testConflictRevisionID, PendingFiles: map[string]fileSignature{"../escape": signature}},
	} {
		if err := client.writeLocalState(profileDir, invalid); err == nil {
			t.Fatalf("%s: invalid state accepted", name)
		}
	}

	// The state file sits beside the directory and is keyed by the cloud
	// profile, so a sibling directory bound to the same cloud profile must
	// not inherit it.
	if err := client.writeLocalState(profileDir, localSyncState{RevisionID: testBaseRevisionID, Files: map[string]fileSignature{"Preferences": signature}}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := client.loadLocalState(filepath.Join(parent, "other-profile")); err != nil || found {
		t.Fatalf("rebound directory reused another directory's baseline: found=%v err=%v", found, err)
	}
	if _, found, err := client.loadLocalState(profileDir); err != nil || !found {
		t.Fatalf("own baseline not found: found=%v err=%v", found, err)
	}
}

func TestTransientProfileEntryClassification(t *testing.T) {
	for _, test := range []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"lockfile", false, true},
		{"Default/lockfile", false, false},
		{"SingletonCookie", false, true},
		{"DevToolsActivePort", false, true},
		{"BrowserMetrics-6571.pma", false, true},
		{"Default/Cache", true, true},
		{"Profile 3/Code Cache", true, true},
		{"GraphiteDawnCache", true, true},
		{"Default/DawnCache", true, true},
		{"Default/Extensions/id/1.0/Cache", true, false},
		{"Default/Cache", false, false},
		{"Local State", false, false},
		{"Default/Preferences", false, false},
		{"Local State.tmp", false, true},
		{"Default/Extensions/id/1.0/template.tmp", false, false},
	} {
		if got := isTransientProfileEntry(test.path, test.isDir); got != test.want {
			t.Errorf("isTransientProfileEntry(%q, dir=%v)=%v want %v", test.path, test.isDir, got, test.want)
		}
	}
	if !isTransientProfilePath("Default/GPUCache/data_1") || isTransientProfilePath("Default/Network/Cookies") {
		t.Fatal("archive path classification is wrong")
	}
}

func sortedKeys(values map[string]fileSignature) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
