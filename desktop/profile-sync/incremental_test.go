package profilesync

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	testBaseRevisionID        = "88888888-8888-4888-8888-888888888888"
	testIncrementalRevisionID = "99999999-9999-4999-8999-999999999999"
	testIncrementalObjectID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testBaseObjectID          = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testIncrementalObjectKey  = "workspaces/test/profile-delta-object"
)

func TestLocalSyncStateRoundTripAndCorruption(t *testing.T) {
	client := newIncrementalTestClient(t, "http://127.0.0.1:1", Limits{MaxFiles: 8, MaxBytes: 4096, MaxFileBytes: 2048, MaxArchiveBytes: 4096})
	profileDir := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := localSyncState{
		RevisionID: testBaseRevisionID,
		ChainDepth: 3,
		Files: map[string]fileSignature{
			"Default/Preferences": {Size: 5, SHA256: strings.Repeat("a", 64)},
		},
	}
	if err := client.writeLocalState(profileDir, state); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := client.loadLocalState(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if !found || loaded.RevisionID != state.RevisionID || loaded.ChainDepth != state.ChainDepth || !reflect.DeepEqual(loaded.Files, state.Files) {
		t.Fatalf("loaded state = %+v, found=%v", loaded, found)
	}

	statePath, err := client.statePath(profileDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"schemaVersion":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.loadLocalState(profileDir); err == nil {
		t.Fatal("corrupt local state was accepted")
	}

	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(profileDir), "state-target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, statePath); err != nil {
		t.Logf("symlink state check skipped on this platform: %v", err)
		return
	}
	if _, _, err := client.loadLocalState(profileDir); err == nil {
		t.Fatal("symbolic-link local state was accepted")
	}
}

func TestDeltaPlannerAndArchiveContainOnlyChanges(t *testing.T) {
	limits := Limits{MaxFiles: 8, MaxBytes: 4096, MaxFileBytes: 2048, MaxArchiveBytes: 4096}
	source := t.TempDir()
	writeProfileTestFile(t, source, "Default/Changed", "new")
	writeProfileTestFile(t, source, "Default/Same", "same")
	writeProfileTestFile(t, source, "Added", "added")
	current, err := scanProfileDirectory(context.Background(), source, limits)
	if err != nil {
		t.Fatal(err)
	}
	baseline := map[string]fileSignature{
		"Default/Changed": {Size: 3, SHA256: strings.Repeat("0", 64)},
		"Default/Same":    current["Default/Same"],
		"Removed":         {Size: 7, SHA256: strings.Repeat("1", 64)},
	}
	changed, deleted := profileChanges(current, baseline)
	if !reflect.DeepEqual(changed, []string{"Added", "Default/Changed"}) || !reflect.DeepEqual(deleted, []string{"Removed"}) {
		t.Fatalf("changed=%v deleted=%v", changed, deleted)
	}

	deltaPath, summary, err := createDeltaArchive(context.Background(), source, changed, current, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(deltaPath)
	if summary.Size <= 0 || !validSHA256(summary.Hash) {
		t.Fatalf("delta summary = %+v", summary)
	}
	reader, err := zip.OpenReader(deltaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var names []string
	for _, entry := range reader.File {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, changed) {
		t.Fatalf("delta entries = %v, want %v", names, changed)
	}

	tamperedInventory := make(map[string]fileSignature, len(current))
	for name, signature := range current {
		tamperedInventory[name] = signature
	}
	signature := tamperedInventory["Default/Changed"]
	signature.SHA256 = strings.Repeat("f", 64)
	tamperedInventory["Default/Changed"] = signature
	if _, _, err := createDeltaArchive(context.Background(), source, []string{"Default/Changed"}, tamperedInventory, limits); err == nil {
		t.Fatal("delta creation accepted a file that did not match its scanned signature")
	}
}

func TestPushUsesIncrementalRevisionAndSkipsUnchangedProfile(t *testing.T) {
	limits := Limits{MaxFiles: 16, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}
	source := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, source, "Default/Changed", "old")
	writeProfileTestFile(t, source, "Default/Same", "same")
	writeProfileTestFile(t, source, "Removed", "remove-me")

	clientForState := newIncrementalTestClient(t, "http://127.0.0.1:1", limits)
	baseline, err := scanProfileDirectory(context.Background(), source, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientForState.writeLocalState(source, localSyncState{RevisionID: testBaseRevisionID, ChainDepth: 2, Files: baseline}); err != nil {
		t.Fatal(err)
	}
	writeProfileTestFile(t, source, "Default/Changed", "new")
	writeProfileTestFile(t, source, "Added", "added")
	if err := os.Remove(filepath.Join(source, "Removed")); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	currentRevisionID := testBaseRevisionID
	beginCount := 0
	releaseCount := 0
	var uploaded []byte
	var beginInput struct {
		BaseRevisionID string              `json:"baseRevisionId"`
		Mode           string              `json:"mode"`
		Files          []revisionFileInput `json:"files"`
		DeletedPaths   []string            `json:"deletedPaths"`
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	uploadURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			mu.Lock()
			current := currentRevisionID
			mu.Unlock()
			write(CloudProfile{ID: testProfileID, CurrentRevisionID: current})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revisions"):
			mu.Lock()
			beginCount++
			mu.Unlock()
			if err := json.NewDecoder(r.Body).Decode(&beginInput); err != nil {
				t.Errorf("decode begin: %v", err)
				return
			}
			if beginInput.Mode != "incremental" || beginInput.BaseRevisionID != testBaseRevisionID || len(beginInput.Files) != 1 || !reflect.DeepEqual(beginInput.DeletedPaths, []string{"Removed"}) {
				t.Errorf("incremental begin = %+v", beginInput)
			}
			file := beginInput.Files[0]
			write(RevisionPlan{
				Revision: Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testBaseRevisionID, Status: "uploading"},
				Manifest: Manifest{RevisionID: testIncrementalRevisionID, SchemaVersion: manifestSchema, Mode: "incremental", FileCount: 1, TotalBytes: file.SizeBytes, Files: []ManifestFile{{Path: file.Path, ObjectKey: testIncrementalObjectKey, CiphertextSHA256: file.CiphertextSHA256, SizeBytes: file.SizeBytes, ContentType: file.ContentType}}, DeletedPaths: append([]string(nil), beginInput.DeletedPaths...)},
				Objects:  []CloudObject{{ID: testIncrementalObjectID, WorkspaceID: testWorkspaceID, RevisionID: testIncrementalRevisionID, ObjectKey: testIncrementalObjectKey, ContentHash: file.CiphertextSHA256, SizeBytes: file.SizeBytes, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: file.ContentType}},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/objects/"+testIncrementalObjectID+"/upload"):
			write(ObjectGrant{ObjectID: testIncrementalObjectID, ObjectKey: testIncrementalObjectKey, Method: http.MethodPut, URL: uploadURL, Headers: map[string]string{"Content-Type": archiveContentType}, ExpiresAt: expiresAt})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			var readErr error
			uploaded, readErr = io.ReadAll(r.Body)
			if readErr != nil {
				t.Errorf("read upload: %v", readErr)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+testIncrementalRevisionID+"/commit"):
			mu.Lock()
			currentRevisionID = testIncrementalRevisionID
			mu.Unlock()
			write(map[string]any{"revision": Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testBaseRevisionID, Status: "committed"}})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			mu.Lock()
			releaseCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	uploadURL = server.URL + "/upload"
	client := newIncrementalTestClient(t, server.URL, limits)

	result, err := client.Push(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.NoChanges || result.Revision.ID != testIncrementalRevisionID || result.ArchiveBytes <= 0 || len(uploaded) == 0 {
		t.Fatalf("incremental push result=%+v upload=%d", result, len(uploaded))
	}
	state, found, err := client.loadLocalState(source)
	if err != nil || !found {
		t.Fatalf("load committed state: found=%v err=%v", found, err)
	}
	if state.RevisionID != testIncrementalRevisionID || state.ChainDepth != 3 {
		t.Fatalf("committed state = %+v", state)
	}

	plainDelta := decryptTestObject(t, uploaded, client.encryptionKey, limits)
	defer os.Remove(plainDelta)
	deltaInventory, err := scanArchiveInventory(context.Background(), plainDelta, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := deltaInventory["Default/Same"]; exists {
		t.Fatal("unchanged file was included in the incremental object")
	}
	if _, exists := deltaInventory["Default/Changed"]; !exists {
		t.Fatal("changed file was omitted from the incremental object")
	}
	if _, exists := deltaInventory["Added"]; !exists {
		t.Fatal("new file was omitted from the incremental object")
	}

	unchanged, err := client.Push(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.NoChanges || unchanged.Revision.ID != testIncrementalRevisionID {
		t.Fatalf("unchanged push result = %+v", unchanged)
	}
	mu.Lock()
	defer mu.Unlock()
	if beginCount != 1 || releaseCount != 1 {
		t.Fatalf("beginCount=%d releaseCount=%d", beginCount, releaseCount)
	}
}

func TestPushSupportsDeletionOnlyIncrementalRevision(t *testing.T) {
	limits := Limits{MaxFiles: 8, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}
	source := filepath.Join(t.TempDir(), "profile")
	writeProfileTestFile(t, source, "Keep", "keep")
	writeProfileTestFile(t, source, "Delete", "delete")
	clientForState := newIncrementalTestClient(t, "http://127.0.0.1:1", limits)
	baseline, err := scanProfileDirectory(context.Background(), source, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientForState.writeLocalState(source, localSyncState{RevisionID: testBaseRevisionID, Files: baseline}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "Delete")); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	var began bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			write(CloudProfile{ID: testProfileID, CurrentRevisionID: testBaseRevisionID})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revisions"):
			began = true
			var input struct {
				BaseRevisionID string              `json:"baseRevisionId"`
				Mode           string              `json:"mode"`
				Files          []revisionFileInput `json:"files"`
				DeletedPaths   []string            `json:"deletedPaths"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode begin: %v", err)
				return
			}
			if input.Mode != "incremental" || input.BaseRevisionID != testBaseRevisionID || len(input.Files) != 0 || !reflect.DeepEqual(input.DeletedPaths, []string{"Delete"}) {
				t.Errorf("deletion-only begin = %+v", input)
			}
			write(RevisionPlan{
				Revision: Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testBaseRevisionID, Status: "uploading"},
				Manifest: Manifest{RevisionID: testIncrementalRevisionID, SchemaVersion: manifestSchema, Mode: "incremental", DeletedPaths: append([]string(nil), input.DeletedPaths...)},
			})
		case strings.Contains(r.URL.Path, "/objects/"):
			t.Error("deletion-only revision attempted object storage")
			http.Error(w, "unexpected object request", http.StatusInternalServerError)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+testIncrementalRevisionID+"/commit"):
			write(map[string]any{"revision": Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testBaseRevisionID, Status: "committed"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newIncrementalTestClient(t, server.URL, limits)
	result, err := client.Push(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	if !began || result.Revision.ID != testIncrementalRevisionID || result.ArchiveBytes != 0 || result.ArchiveSHA256 != "" {
		t.Fatalf("deletion-only result=%+v began=%v", result, began)
	}
	state, found, err := client.loadLocalState(source)
	if err != nil || !found || state.RevisionID != testIncrementalRevisionID || state.ChainDepth != 1 {
		t.Fatalf("deletion-only state=%+v found=%v err=%v", state, found, err)
	}
	if _, exists := state.Files["Delete"]; exists {
		t.Fatal("deleted file remained in local sync state")
	}
}

func TestPullReconstructsIncrementalChain(t *testing.T) {
	limits := Limits{MaxFiles: 16, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}
	baseSource := t.TempDir()
	writeProfileTestFile(t, baseSource, "Default/Keep", "base")
	writeProfileTestFile(t, baseSource, "Default/Delete", "delete")
	baseCiphertext, baseSummary := makeEncryptedProfileObject(t, baseSource, limits)
	deltaSource := t.TempDir()
	writeProfileTestFile(t, deltaSource, "Default/Keep", "changed")
	writeProfileTestFile(t, deltaSource, "Added", "added")
	deltaCiphertext, deltaSummary := makeEncryptedProfileObject(t, deltaSource, limits)

	for _, test := range []struct {
		name       string
		current    string
		targetStat string
		restore    bool
	}{
		{name: "current revision", current: testIncrementalRevisionID, targetStat: "committed"},
		{name: "historical restore", current: testCurrentRevisionID, targetStat: "superseded", restore: true},
		{name: "tampered delta", current: testIncrementalRevisionID, targetStat: "committed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tampered := test.name == "tampered delta"
			destination := filepath.Join(t.TempDir(), "profile")
			writeProfileTestFile(t, destination, "local", "old")
			expiresAt := time.Now().UTC().Add(time.Hour)
			baseURL, deltaURL := "", ""
			restoreCalled := false
			releaseCalled := false
			basePlan := snapshotTestPlan(testBaseRevisionID, testBaseObjectID, "base-object", "superseded", 1, baseSummary)
			deltaPlan := deltaTestPlan(testIncrementalRevisionID, testBaseRevisionID, testIncrementalObjectID, testIncrementalObjectKey, test.targetStat, 2, deltaSummary, []string{"Default/Delete"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
					write(renewedTestLease())
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
					write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
					write(CloudProfile{ID: testProfileID, CurrentRevisionID: test.current})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/revisions/"+testIncrementalRevisionID):
					write(deltaPlan)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/revisions/"+testBaseRevisionID):
					write(basePlan)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects/"+testBaseObjectID+"/download"):
					write(ObjectGrant{ObjectID: testBaseObjectID, ObjectKey: "base-object", Method: http.MethodGet, URL: baseURL, ExpiresAt: expiresAt})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects/"+testIncrementalObjectID+"/download"):
					write(ObjectGrant{ObjectID: testIncrementalObjectID, ObjectKey: testIncrementalObjectKey, Method: http.MethodGet, URL: deltaURL, ExpiresAt: expiresAt})
				case r.Method == http.MethodGet && r.URL.Path == "/download/base":
					_, _ = w.Write(baseCiphertext)
				case r.Method == http.MethodGet && r.URL.Path == "/download/delta":
					payload := deltaCiphertext
					if tampered {
						payload = append([]byte(nil), deltaCiphertext...)
						payload[len(payload)-1] ^= 0xff
					}
					_, _ = w.Write(payload)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+testIncrementalRevisionID+"/restore"):
					restoreCalled = true
					if got, readErr := os.ReadFile(filepath.Join(destination, "Default", "Keep")); readErr != nil || string(got) != "changed" {
						t.Errorf("profile was not active before restore: value=%q err=%v", got, readErr)
					}
					write(map[string]any{"revision": Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testBaseRevisionID, Status: "committed"}})
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
					releaseCalled = true
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			baseURL = server.URL + "/download/base"
			deltaURL = server.URL + "/download/delta"
			client := newIncrementalTestClient(t, server.URL, limits)

			revision, err := client.Pull(context.Background(), testIncrementalRevisionID, destination)
			if tampered {
				if err == nil {
					t.Fatal("tampered delta was accepted")
				}
				if got, readErr := os.ReadFile(filepath.Join(destination, "local")); readErr != nil || string(got) != "old" {
					t.Fatalf("destination changed after tampered delta: value=%q err=%v", got, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if revision.ID != testIncrementalRevisionID || restoreCalled != test.restore {
				t.Fatalf("revision=%+v restoreCalled=%v", revision, restoreCalled)
			}
			if test.restore == releaseCalled {
				t.Fatalf("restore=%v releaseCalled=%v", test.restore, releaseCalled)
			}
			if got, err := os.ReadFile(filepath.Join(destination, "Default", "Keep")); err != nil || string(got) != "changed" {
				t.Fatalf("changed file=%q err=%v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(destination, "Added")); err != nil || string(got) != "added" {
				t.Fatalf("added file=%q err=%v", got, err)
			}
			if _, err := os.Stat(filepath.Join(destination, "Default", "Delete")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted file still exists: %v", err)
			}
			state, found, err := client.loadLocalState(destination)
			if err != nil || !found || state.RevisionID != testIncrementalRevisionID || state.ChainDepth != 1 {
				t.Fatalf("restored state=%+v found=%v err=%v", state, found, err)
			}
		})
	}
}

func TestRevisionChainRejectsCycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plan := RevisionPlan{
			Revision: Revision{ID: testIncrementalRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 2, BaseRevisionID: testIncrementalRevisionID, Status: "committed"},
			Manifest: Manifest{RevisionID: testIncrementalRevisionID, SchemaVersion: manifestSchema, Mode: "incremental", DeletedPaths: []string{"obsolete"}},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": plan})
	}))
	defer server.Close()
	client := newIncrementalTestClient(t, server.URL, Limits{MaxFiles: 8, MaxBytes: 4096, MaxFileBytes: 2048, MaxArchiveBytes: 4096})
	if _, err := client.loadRevisionChain(context.Background(), testIncrementalRevisionID, testIncrementalRevisionID); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestRevisionChainRejectsMoreThanMaximumDeltas(t *testing.T) {
	revisionID := func(index int) string {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
	}
	plans := make(map[string]RevisionPlan, maxDeltaChainSize+1)
	for index := maxDeltaChainSize + 1; index >= 1; index-- {
		id := revisionID(index)
		status := "superseded"
		if index == maxDeltaChainSize+1 {
			status = "committed"
		}
		plans[id] = RevisionPlan{
			Revision: Revision{ID: id, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: int64(index + 1), BaseRevisionID: revisionID(index - 1), Status: status},
			Manifest: Manifest{RevisionID: id, SchemaVersion: manifestSchema, Mode: "incremental", DeletedPaths: []string{"obsolete"}},
		}
	}
	target := revisionID(maxDeltaChainSize + 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		plan, exists := plans[id]
		if !exists {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": plan})
	}))
	defer server.Close()
	client := newIncrementalTestClient(t, server.URL, Limits{MaxFiles: 8, MaxBytes: 4096, MaxFileBytes: 2048, MaxArchiveBytes: 4096})
	if _, err := client.loadRevisionChain(context.Background(), target, target); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("chain limit error = %v", err)
	}
}

func newIncrementalTestClient(t *testing.T, baseURL string, limits Limits) *SyncClient {
	t.Helper()
	client, err := NewSyncClient(SyncConfig{
		BaseURL: baseURL, AccessToken: "access", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef,
		WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeProfileTestFile(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decryptTestObject(t *testing.T, ciphertext []byte, key [32]byte, limits Limits) string {
	t.Helper()
	file, err := os.CreateTemp("", "ant-profile-test-object-*.enc")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if _, err := file.Write(ciphertext); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		t.Fatal(err)
	}
	plain, err := decryptArchive(context.Background(), path, key, limits)
	_ = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func makeEncryptedProfileObject(t *testing.T, source string, limits Limits) ([]byte, archiveSummary) {
	t.Helper()
	plain, _, err := createArchive(context.Background(), source, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(plain)
	var key [32]byte
	copy(key[:], testEncryptionKey)
	encrypted, summary, err := encryptArchive(context.Background(), plain, key, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(encrypted)
	payload, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	return payload, summary
}

func snapshotTestPlan(revisionID, objectID, objectKey, status string, number int64, summary archiveSummary) RevisionPlan {
	return RevisionPlan{
		Revision: Revision{ID: revisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: number, Status: status},
		Manifest: Manifest{RevisionID: revisionID, SchemaVersion: manifestSchema, Mode: "snapshot", FileCount: 1, TotalBytes: summary.Size, Files: []ManifestFile{{Path: archiveObjectPath, ObjectKey: objectKey, CiphertextSHA256: summary.Hash, SizeBytes: summary.Size, ContentType: archiveContentType}}},
		Objects:  []CloudObject{{ID: objectID, WorkspaceID: testWorkspaceID, RevisionID: revisionID, ObjectKey: objectKey, ContentHash: summary.Hash, SizeBytes: summary.Size, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: archiveContentType}},
	}
}

func deltaTestPlan(revisionID, baseRevisionID, objectID, objectKey, status string, number int64, summary archiveSummary, deleted []string) RevisionPlan {
	path := "deltas/" + summary.Hash[:16] + deltaObjectSuffix
	return RevisionPlan{
		Revision: Revision{ID: revisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: number, BaseRevisionID: baseRevisionID, Status: status},
		Manifest: Manifest{RevisionID: revisionID, SchemaVersion: manifestSchema, Mode: "incremental", FileCount: 1, TotalBytes: summary.Size, Files: []ManifestFile{{Path: path, ObjectKey: objectKey, CiphertextSHA256: summary.Hash, SizeBytes: summary.Size, ContentType: archiveContentType}}, DeletedPaths: append([]string(nil), deleted...)},
		Objects:  []CloudObject{{ID: objectID, WorkspaceID: testWorkspaceID, RevisionID: revisionID, ObjectKey: objectKey, ContentHash: summary.Hash, SizeBytes: summary.Size, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: archiveContentType}},
	}
}
