package profile

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
)

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
	_, err := NewSyncClient(SyncConfig{BaseURL: "http://example.invalid", AccessToken: "token", WorkspaceID: "w", ProfileID: "p", DeviceID: "d"})
	if !errors.Is(err, ErrInvalidSyncConfig) {
		t.Fatalf("TLS config error = %v", err)
	}
	client, err := NewSyncClient(SyncConfig{BaseURL: "http://127.0.0.1:1234", AccessToken: "token", WorkspaceID: "w", ProfileID: "p", DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("nil client")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" && !strings.HasPrefix(r.URL.Path, "/upload") {
			t.Errorf("missing bearer on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/lease"):
			write(LeaseGrant{Lease: CloudLease{ID: "lease"}, Token: "lease-token"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/profiles/p"):
			write(CloudProfile{ID: "p"})
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
			write(RevisionPlan{Revision: Revision{ID: "rev-1", Revision: 1, Status: "uploading"}, Manifest: Manifest{Files: []ManifestFile{{Path: archiveObjectPath, SizeBytes: input.Files[0].SizeBytes, CiphertextSHA256: input.Files[0].CiphertextSHA256, ContentType: archiveContentType}}}, Objects: []CloudObject{{ID: "obj-1", SizeBytes: input.Files[0].SizeBytes, ContentHash: input.Files[0].CiphertextSHA256, ContentType: archiveContentType}}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/objects/obj-1/upload"):
			_, _ = io.ReadAll(r.Body)
			write(ObjectGrant{ObjectID: "obj-1", Method: "PUT", URL: uploadURL, Headers: map[string]string{"Content-Type": archiveContentType}})
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("upload read: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rev-1/commit"):
			write(map[string]any{"revision": Revision{ID: "rev-1", Revision: 1, Status: "committed"}})
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
	client, err := NewSyncClient(SyncConfig{BaseURL: server.URL, AccessToken: "access", WorkspaceID: "w", ProfileID: "p", DeviceID: "d", Limits: Limits{MaxFiles: 4, MaxBytes: 4096, MaxFileBytes: 1024, MaxArchiveBytes: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Push(context.Background(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision.ID != "rev-1" || result.ArchiveBytes == 0 || len(uploaded) == 0 {
		t.Fatalf("push result=%+v uploaded=%d", result, len(uploaded))
	}
	if sawDelete {
		t.Fatal("commit should release lease without a second DELETE")
	}
}
