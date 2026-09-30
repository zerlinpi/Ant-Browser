package profilesync

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testWorkspaceID       = "11111111-1111-4111-8111-111111111111"
	testProfileID         = "22222222-2222-4222-8222-222222222222"
	testDeviceID          = "33333333-3333-4333-8333-333333333333"
	testRevisionID        = "44444444-4444-4444-8444-444444444444"
	testCurrentRevisionID = "77777777-7777-4777-8777-777777777777"
	testObjectID          = "55555555-5555-4555-8555-555555555555"
	testLeaseID           = "66666666-6666-4666-8666-666666666666"
	testObjectKey         = "workspaces/test/profile-object"
	testEncryptionKeyRef  = "test-profile-key-v1"
)

var testEncryptionKey = bytes.Repeat([]byte{0x42}, 32)

func TestCreateAndExtractProfileArchive(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "Default", "Session Storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Default", "Cookies"), []byte("cookie-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Local State"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	archivePath, summary, err := createArchive(context.Background(), source, Limits{MaxFiles: 4, MaxBytes: 1024, MaxFileBytes: 512, MaxArchiveBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archivePath)
	if summary.Size <= 0 || len(summary.Hash) != 64 {
		t.Fatalf("invalid archive summary: %+v", summary)
	}

	destination := filepath.Join(t.TempDir(), "profile")
	staging, err := extractArchiveToTemp(context.Background(), archivePath, Limits{MaxFiles: 4, MaxBytes: 1024, MaxFileBytes: 512, MaxArchiveBytes: 4096}, destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceDirectory(staging, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "Default", "Cookies"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "cookie-data" {
		t.Fatalf("restored data = %q", got)
	}
}

func TestArchiveRejectsTraversalAndLimits(t *testing.T) {
	t.Parallel()
	if _, err := safeArchivePath("../outside"); !errors.Is(err, ErrUnsafeArchivePath) {
		t.Fatalf("traversal error = %v", err)
	}
	if _, err := safeArchivePath(`C:\outside`); !errors.Is(err, ErrUnsafeArchivePath) {
		t.Fatalf("drive path error = %v", err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "large"), bytes.Repeat([]byte("x"), 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createArchive(context.Background(), source, Limits{MaxFiles: 2, MaxBytes: 16, MaxFileBytes: 16, MaxArchiveBytes: 4096}); !errors.Is(err, ErrArchiveLimit) {
		t.Fatalf("size error = %v", err)
	}

	archive := filepath.Join(t.TempDir(), "unsafe.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	entry, err := zw.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(entry, "bad")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchiveToTemp(context.Background(), archive, Limits{MaxArchiveBytes: 4096}, filepath.Join(t.TempDir(), "destination")); !errors.Is(err, ErrUnsafeArchivePath) {
		t.Fatalf("zip traversal error = %v", err)
	}
}

func TestNewSyncClientRequiresTLSOutsideLoopback(t *testing.T) {
	t.Parallel()
	_, err := NewSyncClient(SyncConfig{BaseURL: "http://example.invalid", AccessToken: "token", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID})
	if !errors.Is(err, ErrInvalidSyncConfig) {
		t.Fatalf("TLS config error = %v", err)
	}
	client, err := NewSyncClient(SyncConfig{BaseURL: "http://127.0.0.1:1234", AccessToken: "token", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID})
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
	if _, err := NewSyncClient(SyncConfig{BaseURL: "https://example.test", AccessToken: "access", DeviceCredential: "device", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID}); !errors.Is(err, ErrInvalidSyncConfig) {
		t.Fatalf("dual-auth config error = %v", err)
	}
}

func TestDeviceCredentialUsesScopedAgentProfileRoute(t *testing.T) {
	t.Parallel()
	expiresAt := time.Now().UTC().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/profiles/"+testProfileID+"/lease" {
			t.Errorf("device profile path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Device device-secret" {
			t.Errorf("device authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Device-ID") != testDeviceID {
			t.Errorf("device identity header = %q", r.Header.Get("X-Device-ID"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": LeaseGrant{Lease: CloudLease{
			ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID,
			HolderDeviceID: testDeviceID, ExpiresAt: expiresAt,
		}, Token: "lease-token"}})
	}))
	defer server.Close()
	client, err := NewSyncClient(SyncConfig{
		BaseURL: server.URL, DeviceCredential: "device-secret", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef,
		WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AcquireLease(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPushUsesRevisionObjectLeaseProtocol(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "Preferences"), []byte("prefs"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	var sawDelete bool
	uploadURL := ""
	expiresAt := time.Now().UTC().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" && !strings.HasPrefix(r.URL.Path, "/upload") {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			write(CloudProfile{ID: testProfileID})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revisions"):
			var input struct {
				Files []struct {
					Path             string `json:"path"`
					SizeBytes        int64  `json:"sizeBytes"`
					CiphertextSHA256 string `json:"ciphertextSha256"`
				} `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode begin: %v", err)
			}
			if len(input.Files) != 1 || input.Files[0].Path != archiveObjectPath {
				t.Errorf("begin files = %+v", input.Files)
			}
			write(RevisionPlan{
				Revision: Revision{ID: testRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 1, Status: "uploading"},
				Manifest: Manifest{RevisionID: testRevisionID, SchemaVersion: manifestSchema, Mode: "snapshot", FileCount: 1, TotalBytes: input.Files[0].SizeBytes, Files: []ManifestFile{{Path: archiveObjectPath, ObjectKey: testObjectKey, SizeBytes: input.Files[0].SizeBytes, CiphertextSHA256: input.Files[0].CiphertextSHA256, ContentType: archiveContentType}}},
				Objects:  []CloudObject{{ID: testObjectID, WorkspaceID: testWorkspaceID, RevisionID: testRevisionID, ObjectKey: testObjectKey, SizeBytes: input.Files[0].SizeBytes, ContentHash: input.Files[0].CiphertextSHA256, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: archiveContentType}},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/objects/"+testObjectID+"/upload"):
			_, _ = io.ReadAll(r.Body)
			write(ObjectGrant{ObjectID: testObjectID, ObjectKey: testObjectKey, Method: "PUT", URL: uploadURL, Headers: map[string]string{"Content-Type": archiveContentType}, ExpiresAt: expiresAt})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("upload read: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+testRevisionID+"/commit"):
			write(map[string]any{"revision": Revision{ID: testRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 1, Status: "committed"}})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			sawDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	// The handler's upload grant is assigned below through a request-aware
	// mutable URL, avoiding Authorization leakage to presigned requests.
	uploadURL = server.URL + "/upload"
	client, err := NewSyncClient(SyncConfig{BaseURL: server.URL, AccessToken: "access", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID, Limits: Limits{MaxFiles: 4, MaxBytes: 4096, MaxFileBytes: 1024, MaxArchiveBytes: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Push(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision.ID != testRevisionID || result.ArchiveBytes == 0 || len(uploaded) == 0 {
		t.Fatalf("push result=%+v uploaded=%d", result, len(uploaded))
	}
	if bytes.HasPrefix(uploaded, []byte("PK")) || bytes.Contains(uploaded, []byte("prefs")) {
		t.Fatal("profile object was uploaded without client-side encryption")
	}
	if sawDelete {
		t.Fatal("commit should release lease without a second DELETE")
	}
}

func TestPullVerifiesDecryptsAndSwapsBeforeRestore(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Default", "Cookies"), []byte("cloud-cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := Limits{MaxFiles: 8, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}
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
	encryptedBytes, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "old-state"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	downloadURL := ""
	var restored bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/download" && r.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		if r.URL.Path == "/download" && r.Header.Get("Authorization") != "" {
			t.Error("bearer token leaked to object download")
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			write(CloudProfile{ID: testProfileID, CurrentRevisionID: testCurrentRevisionID})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/revisions/"+testRevisionID):
			write(profileTestPlan("superseded", summary))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects/"+testObjectID+"/download"):
			write(ObjectGrant{ObjectID: testObjectID, ObjectKey: testObjectKey, Method: "GET", URL: downloadURL, ExpiresAt: expiresAt})
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			w.Header().Set("Content-Type", archiveContentType)
			_, _ = w.Write(encryptedBytes)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+testRevisionID+"/restore"):
			if got, readErr := os.ReadFile(filepath.Join(destination, "Default", "Cookies")); readErr != nil || string(got) != "cloud-cookie" {
				t.Errorf("local profile was not swapped before restore: data=%q err=%v", got, readErr)
			}
			if _, statErr := os.Stat(filepath.Join(destination, "old-state")); !os.IsNotExist(statErr) {
				t.Errorf("old profile still active during restore: %v", statErr)
			}
			restored = true
			write(map[string]any{"revision": Revision{ID: testRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 1, Status: "committed"}})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			t.Error("successful restore must not release an already-consumed lease")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	downloadURL = server.URL + "/download"
	client, err := NewSyncClient(SyncConfig{BaseURL: server.URL, AccessToken: "access", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := client.Pull(context.Background(), testRevisionID, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !restored || revision.ID != testRevisionID {
		t.Fatalf("restore result=%+v restored=%v", revision, restored)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Default", "Cookies")); err != nil || string(got) != "cloud-cookie" {
		t.Fatalf("restored cookie=%q err=%v", got, err)
	}
}

func TestPullCurrentRevisionDoesNotMutateCloudHistory(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "Preferences"), []byte("current-cloud-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := Limits{MaxFiles: 8, MaxBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxArchiveBytes: 1 << 20}
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
	ciphertext, err := os.ReadFile(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	downloadURL := ""
	var released bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease/renew"):
			write(renewedTestLease())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, HolderDeviceID: testDeviceID, ExpiresAt: expiresAt}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/"+testProfileID):
			write(CloudProfile{ID: testProfileID, CurrentRevisionID: testRevisionID})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/revisions/"+testRevisionID):
			write(profileTestPlan("committed", summary))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects/"+testObjectID+"/download"):
			write(ObjectGrant{ObjectID: testObjectID, ObjectKey: testObjectKey, Method: "GET", URL: downloadURL, ExpiresAt: expiresAt})
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			w.Header().Set("Content-Type", archiveContentType)
			_, _ = w.Write(ciphertext)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			released = true
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/restore"):
			t.Error("pulling the current revision must not rewrite cloud revision history")
			http.Error(w, "unexpected restore", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	downloadURL = server.URL + "/download"
	client, err := NewSyncClient(SyncConfig{BaseURL: server.URL, AccessToken: "access", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Pull(context.Background(), testRevisionID, destination); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("current-revision pull did not release its synchronization lease")
	}
	if got, err := os.ReadFile(filepath.Join(destination, "Preferences")); err != nil || string(got) != "current-cloud-state" {
		t.Fatalf("current profile state=%q err=%v", got, err)
	}
}

func TestDirectorySwapRollbackRestoresPreviousProfile(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "profile")
	staging, err := os.MkdirTemp(parent, ".ant-profile-restore-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "state"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "state"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	swap, err := beginDirectorySwap(staging, destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "state")); err != nil || string(got) != "new" {
		t.Fatalf("swapped state=%q err=%v", got, err)
	}
	if err := swap.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "state")); err != nil || string(got) != "old" {
		t.Fatalf("rolled-back state=%q err=%v", got, err)
	}
}

func TestLeaseRenewalExtendsLongProfileOperation(t *testing.T) {
	t.Parallel()
	renewed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/lease/renew") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Error("renewal request has no bearer token")
		}
		select {
		case renewed <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": CloudLease{
			ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID,
			HolderDeviceID: testDeviceID, ExpiresAt: time.Now().UTC().Add(time.Hour),
		}})
	}))
	defer server.Close()
	client, err := NewSyncClient(SyncConfig{
		BaseURL: server.URL, AccessToken: "access", EncryptionKey: testEncryptionKey, EncryptionKeyRef: testEncryptionKeyRef,
		WorkspaceID: testWorkspaceID, ProfileID: testProfileID, DeviceID: testDeviceID, LeaseTTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant := LeaseGrant{Lease: CloudLease{
		ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID,
		HolderDeviceID: testDeviceID, ExpiresAt: time.Now().UTC().Add(220 * time.Millisecond),
	}, Token: "lease-token"}
	workCtx, stop := client.keepLeaseRenewed(context.Background(), grant)
	select {
	case <-renewed:
	case <-workCtx.Done():
		t.Fatal("work context cancelled before a successful renewal")
	case <-time.After(2 * time.Second):
		t.Fatal("profile lease was not renewed")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

// renewedTestLease answers a lease renewal the way the gateway does.
func renewedTestLease() CloudLease {
	return CloudLease{
		ID: testLeaseID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID,
		HolderDeviceID: testDeviceID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

func profileTestPlan(status string, summary archiveSummary) RevisionPlan {
	return RevisionPlan{
		Revision: Revision{ID: testRevisionID, WorkspaceID: testWorkspaceID, ProfileID: testProfileID, Revision: 1, Status: status},
		Manifest: Manifest{RevisionID: testRevisionID, SchemaVersion: manifestSchema, Mode: "snapshot", FileCount: 1, TotalBytes: summary.Size, Files: []ManifestFile{{Path: archiveObjectPath, ObjectKey: testObjectKey, CiphertextSHA256: summary.Hash, SizeBytes: summary.Size, ContentType: archiveContentType}}},
		Objects:  []CloudObject{{ID: testObjectID, WorkspaceID: testWorkspaceID, RevisionID: testRevisionID, ObjectKey: testObjectKey, ContentHash: summary.Hash, SizeBytes: summary.Size, Encrypted: true, EncryptionKeyRef: testEncryptionKeyRef, ContentType: archiveContentType}},
	}
}
