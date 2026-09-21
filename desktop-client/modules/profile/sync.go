package profile

// This file deliberately stays at the desktop-client boundary.  It speaks the
// existing gateway profile revision protocol and does not reach into the
// browser launcher or command agent.  A profile directory is represented as a
// single ZIP object so a sync is atomic from the cloud's point of view.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultMaxFiles     = 10_000
	defaultMaxBytes     = int64(20 << 30)
	defaultMaxFileBytes = int64(4 << 30)
	defaultMaxArchive   = int64(20 << 30)
	defaultLeaseSeconds = 600
	maxJSONResponse     = int64(4 << 20)
	leaseReleaseTimeout = 15 * time.Second
	archiveObjectPath   = "profile.zip.enc"
	archiveContentType  = "application/octet-stream"
)

var (
	ErrInvalidSyncConfig = errors.New("invalid profile sync configuration")
	ErrArchiveLimit      = errors.New("profile archive exceeds configured limit")
	ErrEmptyArchive      = errors.New("profile source directory contains no files")
	ErrUnsafeArchivePath = errors.New("profile archive contains an unsafe path")
	ErrSymlinkProfile    = errors.New("profile archive refuses symbolic links")
)

// Limits are applied to both local archive creation and remote extraction.
// The defaults mirror the server's profile limits but callers can choose
// smaller values for a particular desktop installation.
type Limits struct {
	MaxFiles        int
	MaxBytes        int64
	MaxFileBytes    int64
	MaxArchiveBytes int64
}

func (l Limits) withDefaults() Limits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaultMaxFiles
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultMaxBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = defaultMaxFileBytes
	}
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = defaultMaxArchive
	}
	return l
}

// SyncConfig contains only short-lived access and lease data. The access
// token and encryption key are never sent to an object-store presigned URL.
// EncryptionKey must be provisioned from the desktop's secure credential
// store; callers must not persist it beside the browser profile.
type SyncConfig struct {
	BaseURL           string
	AccessToken       string
	EncryptionKey     []byte
	EncryptionKeyRef  string
	WorkspaceID       string
	ProfileID         string
	DeviceID          string
	HTTPClient        *http.Client
	Limits            Limits
	LeaseTTLSeconds   int
	AllowInsecureHTTP bool // intended for loopback development gateways only
}

type SyncClient struct {
	baseURL     *url.URL
	accessToken string
	encryptionKey [32]byte
	encryptionKeyRef string
	workspaceID string
	profileID   string
	deviceID    string
	http        *http.Client
	limits      Limits
	leaseTTL    int
	allowHTTP   bool
}

// Cloud wire types intentionally duplicate the stable JSON contract instead
// of importing server packages into the desktop client.
type CloudProfile struct {
	ID                string `json:"id"`
	CurrentRevisionID string `json:"currentRevisionId,omitempty"`
}

type CloudLease struct {
	ID             string    `json:"id"`
	HolderDeviceID string    `json:"holderDeviceId"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type LeaseGrant struct {
	Lease CloudLease `json:"lease"`
	Token string     `json:"token"`
}

type CloudObject struct {
	ID               string `json:"id"`
	ObjectKey        string `json:"objectKey"`
	ContentHash      string `json:"contentHash"`
	SizeBytes        int64  `json:"sizeBytes"`
	Encrypted        bool   `json:"encrypted"`
	EncryptionKeyRef string `json:"encryptionKeyRef"`
	ContentType      string `json:"contentType"`
}

type ManifestFile struct {
	Path             string `json:"path"`
	ObjectKey        string `json:"objectKey"`
	CiphertextSHA256 string `json:"ciphertextSha256"`
	SizeBytes        int64  `json:"sizeBytes"`
	ContentType      string `json:"contentType"`
}

type Manifest struct {
	Files []ManifestFile `json:"files"`
}

type Revision struct {
	ID             string `json:"id"`
	Revision       int64  `json:"revision"`
	BaseRevisionID string `json:"baseRevisionId,omitempty"`
	Status         string `json:"status"`
}

type RevisionPlan struct {
	Revision Revision      `json:"revision"`
	Manifest Manifest      `json:"manifest"`
	Objects  []CloudObject `json:"objects"`
}

type ObjectGrant struct {
	ObjectID  string            `json:"objectId"`
	ObjectKey string            `json:"objectKey"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type SyncResult struct {
	Revision      Revision
	ArchiveBytes  int64
	ArchiveSHA256 string
}

type HTTPError struct {
	Status  int
	Code    string
	Message string
}

// RevisionConflictError preserves the server's conflict plan so a UI can
// offer an explicit keep-local/keep-remote decision instead of retrying a
// stale upload blindly.
type RevisionConflictError struct {
	Plan  RevisionPlan
	Cause *HTTPError
}

func (e *RevisionConflictError) Error() string {
	if e.Cause == nil {
		return "profile revision conflicts with the current cloud revision"
	}
	return e.Cause.Error()
}

func (e *RevisionConflictError) Unwrap() error { return e.Cause }

func (e *HTTPError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("profile sync HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("profile sync HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

func NewSyncClient(config SyncConfig) (*SyncClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("%w: BaseURL must be an HTTP(S) origin", ErrInvalidSyncConfig)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("%w: BaseURL must not contain a path", ErrInvalidSyncConfig)
	}
	if parsed.Scheme == "http" && !config.AllowInsecureHTTP && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("%w: HTTPS is required for non-loopback gateways", ErrInvalidSyncConfig)
	}
	for name, value := range map[string]string{"workspaceID": config.WorkspaceID, "profileID": config.ProfileID, "deviceID": config.DeviceID} {
		if err := validateID(value); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidSyncConfig, name, err)
		}
	}
	accessToken := strings.TrimSpace(config.AccessToken)
	if accessToken == "" || len(accessToken) > 8192 || strings.ContainsAny(accessToken, "\x00\r\n") {
		return nil, fmt.Errorf("%w: access token is required", ErrInvalidSyncConfig)
	}
	keyRef := strings.TrimSpace(config.EncryptionKeyRef)
	if len(config.EncryptionKey) != 32 {
		return nil, fmt.Errorf("%w: encryption key must contain exactly 32 bytes", ErrInvalidSyncConfig)
	}
	if keyRef == "" || len(keyRef) > 512 || strings.ContainsAny(keyRef, "\x00\r\n") {
		return nil, fmt.Errorf("%w: encryption key reference is invalid", ErrInvalidSyncConfig)
	}
	leaseTTL := config.LeaseTTLSeconds
	if leaseTTL == 0 {
		leaseTTL = defaultLeaseSeconds
	}
	if leaseTTL < 60 || leaseTTL > 1800 {
		return nil, fmt.Errorf("%w: lease TTL must be between 60 and 1800 seconds", ErrInvalidSyncConfig)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	} else {
		cloned := *client
		client = &cloned
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	result := &SyncClient{
		baseURL: parsed, accessToken: accessToken, encryptionKeyRef: keyRef, workspaceID: strings.TrimSpace(config.WorkspaceID),
		profileID: strings.TrimSpace(config.ProfileID), deviceID: strings.TrimSpace(config.DeviceID),
		http: client,
		limits: config.Limits.withDefaults(), leaseTTL: leaseTTL, allowHTTP: config.AllowInsecureHTTP,
	}
	copy(result.encryptionKey[:], config.EncryptionKey)
	return result, nil
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() || strings.EqualFold(host, "localhost")
}

func validateID(value string) error {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != strings.ToLower(value) {
		return errors.New("must be a canonical UUID")
	}
	return nil
}

func (c *SyncClient) endpoint(suffix string) string {
	base := *c.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/api/v1" + suffix
	base.RawQuery, base.Fragment = "", ""
	return base.String()
}

type jsonEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *SyncClient) requestJSON(ctx context.Context, method, suffix string, input, output any, accepted ...int) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(suffix), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponse+1))
	if readErr != nil {
		return fmt.Errorf("read profile sync response: %w", readErr)
	}
	if int64(len(payload)) > maxJSONResponse {
		return errors.New("profile sync response exceeds the supported limit")
	}
	allowed := resp.StatusCode >= 200 && resp.StatusCode < 300
	acceptedNonSuccess := false
	for _, status := range accepted {
		if resp.StatusCode == status {
			allowed = true
			acceptedNonSuccess = true
			break
		}
	}
	var envelope jsonEnvelope
	var decodeErr error
	if len(payload) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decodeErr = decoder.Decode(&envelope)
		if decodeErr == nil {
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				if err == nil {
					decodeErr = errors.New("multiple JSON values")
				} else {
					decodeErr = err
				}
			}
		}
	} else {
		decodeErr = io.EOF
	}
	if !allowed {
		h := &HTTPError{Status: resp.StatusCode, Message: resp.Status}
		if decodeErr == nil && envelope.Error != nil {
			h.Code, h.Message = envelope.Error.Code, envelope.Error.Message
		}
		return h
	}
	if output == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if decodeErr != nil {
		return fmt.Errorf("decode profile sync response: %w", decodeErr)
	}
	if len(envelope.Data) != 0 {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return err
		}
	}
	if acceptedNonSuccess {
		h := &HTTPError{Status: resp.StatusCode, Message: resp.Status}
		if envelope.Error != nil {
			h.Code, h.Message = envelope.Error.Code, envelope.Error.Message
		}
		return h
	}
	if len(envelope.Data) == 0 {
		return errors.New("profile sync response has no data")
	}
	return nil
}

func (c *SyncClient) AcquireLease(ctx context.Context) (LeaseGrant, error) {
	var grant LeaseGrant
	err := c.requestJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(c.workspaceID)+"/profiles/"+url.PathEscape(c.profileID)+"/lease", struct {
		DeviceID   string `json:"deviceId"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{c.deviceID, c.leaseTTL}, &grant)
	return grant, err
}

func (c *SyncClient) releaseLease(ctx context.Context, grant LeaseGrant) error {
	return c.requestJSON(ctx, http.MethodDelete, "/workspaces/"+url.PathEscape(c.workspaceID)+"/profiles/"+url.PathEscape(c.profileID)+"/lease", struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}{c.deviceID, grant.Token}, nil)
}

func (c *SyncClient) releaseLeaseBounded(grant LeaseGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), leaseReleaseTimeout)
	defer cancel()
	return c.releaseLease(ctx, grant)
}

func (c *SyncClient) getProfile(ctx context.Context) (CloudProfile, error) {
	var profile CloudProfile
	err := c.requestJSON(ctx, http.MethodGet, "/workspaces/"+url.PathEscape(c.workspaceID)+"/profiles/"+url.PathEscape(c.profileID), nil, &profile)
	return profile, err
}

func (c *SyncClient) beginRevision(ctx context.Context, grant LeaseGrant, base string, archiveSize int64, archiveHash string) (RevisionPlan, error) {
	var plan RevisionPlan
	input := struct {
		DeviceID       string `json:"deviceId"`
		LeaseToken     string `json:"leaseToken"`
		BaseRevisionID string `json:"baseRevisionId,omitempty"`
		Mode           string `json:"mode"`
		Files          []struct {
			Path             string `json:"path"`
			CiphertextSHA256 string `json:"ciphertextSha256"`
			SizeBytes        int64  `json:"sizeBytes"`
			ContentType      string `json:"contentType"`
		} `json:"files"`
	}{c.deviceID, grant.Token, base, "snapshot", []struct {
		Path             string `json:"path"`
		CiphertextSHA256 string `json:"ciphertextSha256"`
		SizeBytes        int64  `json:"sizeBytes"`
		ContentType      string `json:"contentType"`
	}{{archiveObjectPath, archiveHash, archiveSize, archiveContentType}}}
	path := "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + "/revisions"
	if err := c.requestJSON(ctx, http.MethodPost, path, input, &plan, http.StatusConflict); err != nil {
		return plan, err
	}
	return plan, nil
}

func (c *SyncClient) objectGrant(ctx context.Context, revisionID, objectID string, upload bool, grant LeaseGrant) (ObjectGrant, error) {
	var result ObjectGrant
	verb, suffix := http.MethodGet, "/objects/"+url.PathEscape(objectID)+"/download"
	if upload {
		verb, suffix = http.MethodPost, "/objects/"+url.PathEscape(objectID)+"/upload"
	}
	path := "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + "/revisions/" + url.PathEscape(revisionID) + suffix
	var input any
	if upload {
		input = struct {
			DeviceID string `json:"deviceId"`
			Token    string `json:"token"`
		}{c.deviceID, grant.Token}
	}
	if err := c.requestJSON(ctx, verb, path, input, &result); err != nil {
		return result, err
	}
	expectedMethod := http.MethodGet
	if upload {
		expectedMethod = http.MethodPut
	}
	if result.Method != "" && !strings.EqualFold(result.Method, expectedMethod) {
		return result, fmt.Errorf("unexpected presigned object method %q", result.Method)
	}
	if strings.TrimSpace(result.URL) == "" {
		return result, errors.New("presigned object grant has no URL")
	}
	return result, nil
}

func validatePresigned(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("presigned object URL is invalid")
	}
	if u.Scheme == "http" && !allowHTTP && !isLoopbackHost(u.Hostname()) {
		return nil, errors.New("presigned object URL must use HTTPS")
	}
	return u, nil
}

func (c *SyncClient) upload(ctx context.Context, grant ObjectGrant, archivePath string, size int64) error {
	u, err := validatePresigned(grant.URL, c.allowHTTP)
	if err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), io.LimitReader(f, size+1))
	if err != nil {
		return err
	}
	req.ContentLength = size
	for key, value := range grant.Headers {
		if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("presigned object header is invalid")
		}
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Message: resp.Status}
	}
	return nil
}

func (c *SyncClient) download(ctx context.Context, grant ObjectGrant, destination string, expectedSize int64, expectedHash string) error {
	u, err := validatePresigned(grant.URL, c.allowHTTP)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	for key, value := range grant.Headers {
		if strings.ContainsAny(key, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("presigned object header is invalid")
		}
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Message: resp.Status}
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, c.limits.MaxArchiveBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > c.limits.MaxArchiveBytes || (expectedSize >= 0 && written != expectedSize) {
		return ErrArchiveLimit
	}
	if expectedHash != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedHash) {
		return errors.New("downloaded profile object hash does not match manifest")
	}
	return nil
}

// Push creates a bounded local archive, uploads it as one cloud object, and
// commits the revision only after the object-store upload has completed.
func (c *SyncClient) Push(ctx context.Context, sourceDir string, baseRevisionID string) (result SyncResult, err error) {
	grant, err := c.AcquireLease(ctx)
	if err != nil {
		return result, err
	}
	releaseNeeded := true
	defer func() {
		// Commit/restore releases the server lease itself; a second DELETE
		// would turn an otherwise successful sync into a lease error.
		if !releaseNeeded {
			return
		}
		releaseErr := c.releaseLeaseBounded(grant)
		if err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	plainArchivePath, _, err := createArchive(ctx, sourceDir, c.limits)
	if err != nil {
		return result, err
	}
	defer os.Remove(plainArchivePath)
	archivePath, summary, err := encryptArchive(ctx, plainArchivePath, c.encryptionKey, c.limits)
	if err != nil {
		return result, err
	}
	defer os.Remove(archivePath)
	if strings.TrimSpace(baseRevisionID) == "" {
		profile, getErr := c.getProfile(ctx)
		if getErr != nil {
			return result, getErr
		}
		baseRevisionID = profile.CurrentRevisionID
	}
	plan, err := c.beginRevision(ctx, grant, strings.TrimSpace(baseRevisionID), summary.Size, summary.Hash)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.Status == http.StatusConflict {
			return result, &RevisionConflictError{Plan: plan, Cause: httpErr}
		}
		return result, err
	}
	if len(plan.Objects) != 1 || len(plan.Manifest.Files) != 1 || plan.Manifest.Files[0].Path != archiveObjectPath {
		return result, errors.New("cloud returned an unsupported profile object plan")
	}
	object, manifestFile := plan.Objects[0], plan.Manifest.Files[0]
	if object.SizeBytes != summary.Size || !strings.EqualFold(object.ContentHash, summary.Hash) ||
		!strings.EqualFold(manifestFile.CiphertextSHA256, summary.Hash) || manifestFile.SizeBytes != summary.Size ||
		!strings.EqualFold(manifestFile.ContentType, archiveContentType) || !strings.EqualFold(object.ContentType, archiveContentType) ||
		!object.Encrypted || object.EncryptionKeyRef != c.encryptionKeyRef {
		return result, errors.New("cloud object metadata does not match local archive")
	}
	uploadGrant, err := c.objectGrant(ctx, plan.Revision.ID, object.ID, true, grant)
	if err != nil {
		return result, err
	}
	if err := c.upload(ctx, uploadGrant, archivePath, summary.Size); err != nil {
		return result, err
	}
	var committed struct {
		Revision Revision `json:"revision"`
	}
	commitPath := "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + "/revisions/" + url.PathEscape(plan.Revision.ID) + "/commit"
	if err := c.requestJSON(ctx, http.MethodPost, commitPath, struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}{c.deviceID, grant.Token}, &committed); err != nil {
		return result, err
	}
	releaseNeeded = false
	result.Revision, result.ArchiveBytes, result.ArchiveSHA256 = committed.Revision, summary.Size, summary.Hash
	return result, nil
}

// Pull downloads and verifies a cloud revision before asking the server to
// restore it.  Local files are swapped only after both checks succeed.
func (c *SyncClient) Pull(ctx context.Context, revisionID, destinationDir string) (revision Revision, err error) {
	if err := validateID(revisionID); err != nil {
		return Revision{}, err
	}
	if strings.TrimSpace(destinationDir) == "" {
		return Revision{}, errors.New("destination directory is required")
	}
	grant, err := c.AcquireLease(ctx)
	if err != nil {
		return Revision{}, err
	}
	releaseNeeded := true
	defer func() {
		if !releaseNeeded {
			return
		}
		releaseErr := c.releaseLeaseBounded(grant)
		if err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	plan := RevisionPlan{}
	getPath := "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + "/revisions/" + url.PathEscape(revisionID)
	if err := c.requestJSON(ctx, http.MethodGet, getPath, nil, &plan); err != nil {
		return Revision{}, err
	}
	if len(plan.Objects) != 1 || len(plan.Manifest.Files) != 1 || plan.Manifest.Files[0].Path != archiveObjectPath {
		return Revision{}, errors.New("cloud revision does not contain a supported profile archive")
	}
	object, file := plan.Objects[0], plan.Manifest.Files[0]
	if object.SizeBytes != file.SizeBytes || !strings.EqualFold(object.ContentHash, file.CiphertextSHA256) ||
		!strings.EqualFold(file.ContentType, archiveContentType) || !strings.EqualFold(object.ContentType, archiveContentType) ||
		object.SizeBytes > c.limits.MaxArchiveBytes || !object.Encrypted || object.EncryptionKeyRef != c.encryptionKeyRef {
		return Revision{}, errors.New("cloud profile object metadata is invalid")
	}
	downloadGrant, err := c.objectGrant(ctx, revisionID, object.ID, false, grant)
	if err != nil {
		return Revision{}, err
	}
	tmp, err := os.CreateTemp("", "ant-profile-sync-*.enc")
	if err != nil {
		return Revision{}, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := c.download(ctx, downloadGrant, tmpPath, object.SizeBytes, object.ContentHash); err != nil {
		return Revision{}, err
	}
	plainArchivePath, err := decryptArchive(ctx, tmpPath, c.encryptionKey, c.limits)
	if err != nil {
		return Revision{}, err
	}
	defer os.Remove(plainArchivePath)
	staging, err := extractArchiveToTemp(ctx, plainArchivePath, c.limits, destinationDir)
	if err != nil {
		return Revision{}, err
	}
	defer os.RemoveAll(staging)
	swap, err := beginDirectorySwap(staging, destinationDir)
	if err != nil {
		return Revision{}, err
	}
	rollbackNeeded := true
	defer func() {
		if rollbackNeeded {
			_ = swap.Rollback()
		}
	}()
	restorePath := getPath + "/restore"
	var restored struct {
		Revision Revision `json:"revision"`
	}
	if err := c.requestJSON(ctx, http.MethodPost, restorePath, struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}{c.deviceID, grant.Token}, &restored); err != nil {
		return Revision{}, err
	}
	releaseNeeded = false
	// The server has now atomically selected this revision, so a failure to
	// remove the local backup must not put the device back on the old profile.
	rollbackNeeded = false
	if err := swap.Commit(); err != nil {
		return Revision{}, err
	}
	return restored.Revision, nil
}

type archiveSummary struct {
	Size int64
	Hash string
}

func createArchive(ctx context.Context, sourceDir string, limits Limits) (string, archiveSummary, error) {
	limits = limits.withDefaults()
	root, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", archiveSummary{}, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", archiveSummary{}, err
	}
	if !info.IsDir() {
		return "", archiveSummary{}, errors.New("profile source must be a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", archiveSummary{}, ErrSymlinkProfile
	}
	f, err := os.CreateTemp("", "ant-profile-sync-*.zip")
	if err != nil {
		return "", archiveSummary{}, err
	}
	archivePath := f.Name()
	cleanup := func(e error) (string, archiveSummary, error) {
		f.Close()
		os.Remove(archivePath)
		return "", archiveSummary{}, e
	}
	limited := &limitedWriter{w: f, max: limits.MaxArchiveBytes}
	zw := zip.NewWriter(limited)
	files := 0
	var total int64
	err = filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if full == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrSymlinkProfile
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported profile entry %q", full)
		}
		files++
		if files > limits.MaxFiles {
			return ErrArchiveLimit
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		name, err := safeArchivePath(rel)
		if err != nil {
			return err
		}
		input, err := os.Open(full)
		if err != nil {
			return err
		}
		defer input.Close()
		stat, err := input.Stat()
		if err != nil {
			return err
		}
		if stat.Size() > limits.MaxFileBytes || total > limits.MaxBytes-stat.Size() {
			return ErrArchiveLimit
		}
		total += stat.Size()
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		out, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, &countingLimitReader{ctx: ctx, r: input, remaining: stat.Size()})
		return err
	})
	if err == nil && files == 0 {
		err = ErrEmptyArchive
	}
	if err == nil {
		err = zw.Close()
	} else {
		_ = zw.Close()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return cleanup(err)
	}
	stat, err := os.Stat(archivePath)
	if err != nil {
		return cleanup(err)
	}
	if stat.Size() > limits.MaxArchiveBytes {
		return cleanup(ErrArchiveLimit)
	}
	hash, err := fileSHA256(archivePath)
	if err != nil {
		return cleanup(err)
	}
	return archivePath, archiveSummary{Size: stat.Size(), Hash: hash}, nil
}

type limitedWriter struct {
	w            io.Writer
	max, written int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.written {
		return 0, ErrArchiveLimit
	}
	n, err := w.w.Write(p)
	w.written += int64(n)
	return n, err
}

type countingLimitReader struct {
	ctx       context.Context
	r         io.Reader
	remaining int64
}

func (r *countingLimitReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func fileSHA256(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func safeArchivePath(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, `\`, "/")
	if raw == "" || strings.ContainsRune(raw, 0) || strings.Contains(raw, ":") || strings.HasPrefix(raw, "/") {
		return "", ErrUnsafeArchivePath
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrUnsafeArchivePath
	}
	return cleaned, nil
}

func extractArchiveToTemp(ctx context.Context, archivePath string, limits Limits, destination string) (string, error) {
	limits = limits.withDefaults()
	stat, err := os.Stat(archivePath)
	if err != nil {
		return "", err
	}
	if stat.Size() > limits.MaxArchiveBytes {
		return "", ErrArchiveLimit
	}
	data, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer data.Close()
	reader, err := zip.NewReader(data, stat.Size())
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".ant-profile-restore-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	files := 0
	var total int64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			os.RemoveAll(staging)
			return "", ErrSymlinkProfile
		}
		files++
		if files > limits.MaxFiles || entry.UncompressedSize64 > uint64(limits.MaxFileBytes) || entry.UncompressedSize64 > uint64(limits.MaxBytes-total) {
			os.RemoveAll(staging)
			return "", ErrArchiveLimit
		}
		total += int64(entry.UncompressedSize64)
		target := filepath.Join(staging, filepath.FromSlash(name))
		rel, err := filepath.Rel(staging, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			os.RemoveAll(staging)
			return "", ErrUnsafeArchivePath
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		in, err := entry.Open()
		if err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			in.Close()
			os.RemoveAll(staging)
			return "", err
		}
		written, copyErr := io.Copy(out, &countingLimitReader{ctx: ctx, r: io.LimitReader(in, limits.MaxFileBytes+1), remaining: limits.MaxFileBytes + 1})
		closeErr := out.Close()
		in.Close()
		if copyErr != nil || closeErr != nil || written != int64(entry.UncompressedSize64) {
			os.RemoveAll(staging)
			if copyErr != nil {
				return "", copyErr
			}
			return "", ErrArchiveLimit
		}
	}
	return staging, nil
}

type directorySwap struct {
	destination    string
	backup         string
	hadDestination bool
	active         bool
}

func beginDirectorySwap(staging, destination string) (*directorySwap, error) {
	absStaging, err := filepath.Abs(staging)
	if err != nil {
		return nil, err
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(absStaging) == filepath.Clean(absDestination) {
		return nil, errors.New("staging and destination must differ")
	}
	parent := filepath.Dir(absDestination)
	if filepath.Dir(absStaging) != parent || filepath.Clean(absDestination) == filepath.Clean(parent) {
		return nil, errors.New("profile staging and destination must share a safe parent directory")
	}
	stagingInfo, err := os.Lstat(absStaging)
	if err != nil {
		return nil, err
	}
	if stagingInfo.Mode()&os.ModeSymlink != 0 || !stagingInfo.IsDir() {
		return nil, errors.New("profile staging path must be a real directory")
	}
	swap := &directorySwap{
		destination: absDestination,
		backup:      filepath.Join(parent, ".ant-profile-old-"+uuid.NewString()),
	}
	if existing, statErr := os.Lstat(absDestination); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.IsDir() {
			return nil, errors.New("profile destination must be a real directory")
		}
		if err := os.Rename(absDestination, swap.backup); err != nil {
			return nil, fmt.Errorf("stage existing profile: %w", err)
		}
		swap.hadDestination = true
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err := os.Rename(absStaging, absDestination); err != nil {
		if swap.hadDestination {
			if rollbackErr := os.Rename(swap.backup, absDestination); rollbackErr != nil {
				return nil, errors.Join(err, fmt.Errorf("restore previous profile after failed swap: %w", rollbackErr))
			}
		}
		return nil, err
	}
	swap.active = true
	return swap, nil
}

func (s *directorySwap) Commit() error {
	if s == nil || !s.active {
		return nil
	}
	s.active = false
	if !s.hadDestination {
		return nil
	}
	if err := os.RemoveAll(s.backup); err != nil {
		return fmt.Errorf("remove previous profile backup %q: %w", s.backup, err)
	}
	return nil
}

func (s *directorySwap) Rollback() error {
	if s == nil || !s.active {
		return nil
	}
	discard := filepath.Join(filepath.Dir(s.destination), ".ant-profile-rollback-"+uuid.NewString())
	if err := os.Rename(s.destination, discard); err != nil {
		return fmt.Errorf("stage restored profile for rollback: %w", err)
	}
	if s.hadDestination {
		if err := os.Rename(s.backup, s.destination); err != nil {
			_ = os.Rename(discard, s.destination)
			return fmt.Errorf("restore previous profile: %w", err)
		}
	}
	s.active = false
	if err := os.RemoveAll(discard); err != nil {
		return fmt.Errorf("remove rolled-back profile: %w", err)
	}
	return nil
}

func replaceDirectory(staging, destination string) error {
	swap, err := beginDirectorySwap(staging, destination)
	if err != nil {
		return err
	}
	return swap.Commit()
}

// LatestRevision selects the newest committed revision without guessing from
// server ordering.  It is useful to UI callers that do not persist a revision.
func (c *SyncClient) LatestRevision(ctx context.Context) (Revision, error) {
	var revisions []Revision
	path := "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + "/revisions"
	if err := c.requestJSON(ctx, http.MethodGet, path, nil, &revisions); err != nil {
		return Revision{}, err
	}
	committed := revisions[:0]
	for _, revision := range revisions {
		if revision.Status == "committed" {
			committed = append(committed, revision)
		}
	}
	if len(committed) == 0 {
		return Revision{}, errors.New("cloud profile has no committed revision")
	}
	sort.Slice(committed, func(i, j int) bool { return committed[i].Revision > committed[j].Revision })
	return committed[0], nil
}
