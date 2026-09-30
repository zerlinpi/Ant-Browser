package profilesync

// This file deliberately stays at the desktop sync boundary.  It speaks the
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
	"sync"
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
	manifestSchema      = "ant-profile/v2"
	archiveObjectPath   = "profile.zip.enc"
	deltaObjectSuffix   = "/profile.delta.zip.enc"
	archiveContentType  = "application/octet-stream"
)

var (
	ErrInvalidSyncConfig = errors.New("invalid profile sync configuration")
	ErrArchiveLimit      = errors.New("profile archive exceeds configured limit")
	ErrEmptyArchive      = errors.New("profile source directory contains no files")
	ErrUnsafeArchivePath = errors.New("profile archive contains an unsafe path")
	ErrSymlinkProfile    = errors.New("profile archive refuses symbolic links")
	ErrNoCloudRevision   = errors.New("cloud profile has no committed revision")
	// ErrConflictUnresolved means the cloud profile has an open
	// synchronization conflict. No device may pull or push it until the
	// conflict is resolved with keep_local or keep_remote.
	ErrConflictUnresolved = errors.New("cloud profile has an unresolved synchronization conflict; resolve it before synchronizing")
)

const (
	codeConflictUnresolved = "profile_conflict_unresolved"
	codeRevisionConflict   = "profile_revision_conflict"
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
	DeviceCredential  string
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
	baseURL          *url.URL
	authorization    string
	agentAuth        bool
	encryptionKey    [32]byte
	encryptionKeyRef string
	workspaceID      string
	profileID        string
	deviceID         string
	http             *http.Client
	limits           Limits
	leaseTTL         int
	allowHTTP        bool
}

// Cloud wire types intentionally duplicate the stable JSON contract instead
// of importing server packages into the desktop client.
type CloudProfile struct {
	ID                string `json:"id"`
	CurrentRevisionID string `json:"currentRevisionId,omitempty"`
	Status            string `json:"status,omitempty"`
}

// CloudConflict is opened by the server when a revision's base is not the
// profile's current revision.
type CloudConflict struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspaceId"`
	ProfileID        string `json:"profileId"`
	LocalRevisionID  string `json:"localRevisionId"`
	RemoteRevisionID string `json:"remoteRevisionId"`
	Status           string `json:"status"`
}

type CloudLease struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspaceId"`
	ProfileID      string    `json:"profileId"`
	HolderDeviceID string    `json:"holderDeviceId"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type LeaseGrant struct {
	Lease CloudLease `json:"lease"`
	Token string     `json:"token"`
}

type CloudObject struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspaceId"`
	RevisionID       string `json:"revisionId"`
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
	RevisionID    string         `json:"revisionId"`
	SchemaVersion string         `json:"schemaVersion"`
	Mode          string         `json:"mode"`
	FileCount     int            `json:"fileCount"`
	TotalBytes    int64          `json:"totalBytes"`
	Files         []ManifestFile `json:"files"`
	DeletedPaths  []string       `json:"deletedPaths,omitempty"`
}

type Revision struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspaceId"`
	ProfileID      string `json:"profileId"`
	Revision       int64  `json:"revision"`
	BaseRevisionID string `json:"baseRevisionId,omitempty"`
	Status         string `json:"status"`
	DeviceID       string `json:"deviceId,omitempty"`
}

type RevisionPlan struct {
	Revision Revision       `json:"revision"`
	Manifest Manifest       `json:"manifest"`
	Objects  []CloudObject  `json:"objects"`
	Conflict *CloudConflict `json:"conflict,omitempty"`
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
	NoChanges     bool
}

type revisionFileInput struct {
	Path             string `json:"path"`
	CiphertextSHA256 string `json:"ciphertextSha256"`
	SizeBytes        int64  `json:"sizeBytes"`
	ContentType      string `json:"contentType"`
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
	// SnapshotUploaded reports that the local snapshot of the conflicting
	// revision reached object storage, so keep_local can promote it.
	SnapshotUploaded bool
	// UploadErr explains why the local snapshot could not be uploaded; the
	// conflict can then only be resolved with keep_remote.
	UploadErr error
}

// ConflictID returns the server's conflict identifier, if one was opened.
func (e *RevisionConflictError) ConflictID() string {
	if e == nil || e.Plan.Conflict == nil {
		return ""
	}
	return e.Plan.Conflict.ID
}

func (e *RevisionConflictError) Error() string {
	id := e.ConflictID()
	if id == "" {
		if e.Cause == nil {
			return "profile revision conflicts with the current cloud revision"
		}
		return e.Cause.Error()
	}
	message := "cloud profile changed since this device last synchronized; resolve conflict " + id + " before synchronizing again"
	switch {
	case e.SnapshotUploaded:
		message += " (this device's snapshot is available to keep_local)"
	case e.UploadErr != nil:
		message += " (this device's snapshot could not be uploaded, so only keep_remote can resolve it)"
	}
	return message
}

func (e *RevisionConflictError) Unwrap() error { return e.Cause }

func (e *HTTPError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("profile sync HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("profile sync HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

// Is classifies the server's conflict-blocking response as
// ErrConflictUnresolved.
func (e *HTTPError) Is(target error) bool {
	return target == ErrConflictUnresolved && e.Code == codeConflictUnresolved
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
	deviceCredential := strings.TrimSpace(config.DeviceCredential)
	if (accessToken == "") == (deviceCredential == "") {
		return nil, fmt.Errorf("%w: exactly one access token or device credential is required", ErrInvalidSyncConfig)
	}
	credential := accessToken
	authorizationScheme := "Bearer"
	agentAuth := false
	if deviceCredential != "" {
		credential = deviceCredential
		authorizationScheme = "Device"
		agentAuth = true
	}
	if len(credential) > 8192 || strings.ContainsAny(credential, "\x00\r\n") {
		return nil, fmt.Errorf("%w: authorization credential is invalid", ErrInvalidSyncConfig)
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
		baseURL: parsed, authorization: authorizationScheme + " " + credential, agentAuth: agentAuth,
		encryptionKeyRef: keyRef, workspaceID: strings.TrimSpace(config.WorkspaceID),
		profileID: strings.TrimSpace(config.ProfileID), deviceID: strings.TrimSpace(config.DeviceID),
		http:   client,
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

func (c *SyncClient) profilePath(suffix string) string {
	if c.agentAuth {
		return "/agent/profiles/" + url.PathEscape(c.profileID) + suffix
	}
	return "/workspaces/" + url.PathEscape(c.workspaceID) + "/profiles/" + url.PathEscape(c.profileID) + suffix
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
	req.Header.Set("Authorization", c.authorization)
	if c.agentAuth {
		req.Header.Set("X-Device-ID", c.deviceID)
	}
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
	err := c.requestJSON(ctx, http.MethodPost, c.profilePath("/lease"), struct {
		DeviceID   string `json:"deviceId"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{c.deviceID, c.leaseTTL}, &grant)
	if err != nil {
		return LeaseGrant{}, err
	}
	if validateID(grant.Lease.ID) != nil || grant.Lease.WorkspaceID != c.workspaceID || grant.Lease.ProfileID != c.profileID || grant.Lease.HolderDeviceID != c.deviceID ||
		strings.TrimSpace(grant.Token) == "" || len(grant.Token) > 4096 || strings.ContainsAny(grant.Token, "\x00\r\n") ||
		grant.Lease.ExpiresAt.IsZero() || !grant.Lease.ExpiresAt.After(time.Now().UTC()) {
		return LeaseGrant{}, errors.New("cloud returned an invalid profile lease")
	}
	return grant, nil
}

func (c *SyncClient) renewLease(ctx context.Context, grant LeaseGrant) (CloudLease, error) {
	var lease CloudLease
	err := c.requestJSON(ctx, http.MethodPost, c.profilePath("/lease/renew"), struct {
		DeviceID   string `json:"deviceId"`
		Token      string `json:"token"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{c.deviceID, grant.Token, c.leaseTTL}, &lease)
	if err != nil {
		return CloudLease{}, err
	}
	if lease.ID != grant.Lease.ID || lease.WorkspaceID != c.workspaceID || lease.ProfileID != c.profileID ||
		lease.HolderDeviceID != c.deviceID || lease.ExpiresAt.IsZero() || !lease.ExpiresAt.After(time.Now().UTC()) {
		return CloudLease{}, errors.New("cloud returned an invalid renewed profile lease")
	}
	return lease, nil
}

// keepLeaseRenewed cancels workCtx if the lease can no longer be renewed. The
// returned stop function is idempotent and must be called before commit or
// restore so a renewal request cannot race the lease-consuming operation.
func (c *SyncClient) keepLeaseRenewed(parent context.Context, grant LeaseGrant) (workCtx context.Context, stop func() error) {
	workCtx, cancel := context.WithCancel(parent)
	stopRequested := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		expiresAt := grant.Lease.ExpiresAt
		for {
			remaining := time.Until(expiresAt)
			if remaining <= 0 {
				err := errors.New("profile lease expired during synchronization")
				cancel()
				done <- err
				return
			}
			delay := remaining / 2
			configuredHalf := time.Duration(c.leaseTTL) * time.Second / 2
			if delay > configuredHalf {
				delay = configuredHalf
			}
			if delay < 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
			timer := time.NewTimer(delay)
			select {
			case <-stopRequested:
				timer.Stop()
				done <- nil
				return
			case <-parent.Done():
				timer.Stop()
				done <- nil
				return
			case <-timer.C:
			}
			renewed, err := c.renewLease(workCtx, grant)
			if err != nil {
				select {
				case <-stopRequested:
					done <- nil
				case <-parent.Done():
					done <- nil
				default:
					cancel()
					done <- fmt.Errorf("renew profile lease: %w", err)
				}
				return
			}
			expiresAt = renewed.ExpiresAt
		}
	}()
	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() {
			close(stopRequested)
			cancel()
			stopErr = <-done
		})
		return stopErr
	}
	return workCtx, stop
}

func (c *SyncClient) releaseLease(ctx context.Context, grant LeaseGrant) error {
	return c.requestJSON(ctx, http.MethodDelete, c.profilePath("/lease"), struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}{c.deviceID, grant.Token}, nil)
}

func (c *SyncClient) releaseLeaseBounded(grant LeaseGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), leaseReleaseTimeout)
	defer cancel()
	return c.releaseLease(ctx, grant)
}

// getProfile reads the cloud profile. Callers hold the synchronization lease,
// so the current revision cannot change until they commit, restore or
// release it.
func (c *SyncClient) getProfile(ctx context.Context) (CloudProfile, error) {
	var profile CloudProfile
	if err := c.requestJSON(ctx, http.MethodGet, c.profilePath(""), nil, &profile); err != nil {
		return CloudProfile{}, err
	}
	if profile.ID != c.profileID || (profile.CurrentRevisionID != "" && validateID(profile.CurrentRevisionID) != nil) {
		return CloudProfile{}, errors.New("cloud returned an invalid profile")
	}
	if profile.Status == "conflict" {
		// Servers block leases during a conflict; this covers older ones.
		return CloudProfile{}, ErrConflictUnresolved
	}
	return profile, nil
}

// validConflictPlan reports whether a 409 plan describes an open conflict for
// the revision it was returned with.
func (c *SyncClient) validConflictPlan(plan RevisionPlan) bool {
	conflict := plan.Conflict
	return conflict != nil && validateID(conflict.ID) == nil && conflict.WorkspaceID == c.workspaceID &&
		conflict.ProfileID == c.profileID && conflict.LocalRevisionID == plan.Revision.ID && conflict.Status == "open"
}

// confirmLease renews the lease once more after background renewal stopped
// and before an irreversible step. Only the lease holder can commit or
// restore, so a confirmed lease also proves the cloud's current revision has
// not moved since it was read.
func (c *SyncClient) confirmLease(ctx context.Context, grant LeaseGrant) error {
	if _, err := c.renewLease(ctx, grant); err != nil {
		return fmt.Errorf("confirm profile lease: %w", err)
	}
	return nil
}

func (c *SyncClient) beginRevision(ctx context.Context, grant LeaseGrant, base, mode string, file *revisionFileInput, deletedPaths []string) (RevisionPlan, error) {
	var plan RevisionPlan
	input := struct {
		DeviceID       string              `json:"deviceId"`
		LeaseToken     string              `json:"leaseToken"`
		BaseRevisionID string              `json:"baseRevisionId,omitempty"`
		Mode           string              `json:"mode"`
		Files          []revisionFileInput `json:"files"`
		DeletedPaths   []string            `json:"deletedPaths,omitempty"`
	}{DeviceID: c.deviceID, LeaseToken: grant.Token, BaseRevisionID: base, Mode: mode, DeletedPaths: deletedPaths}
	if file != nil {
		input.Files = []revisionFileInput{*file}
	}
	path := c.profilePath("/revisions")
	if err := c.requestJSON(ctx, http.MethodPost, path, input, &plan, http.StatusConflict); err != nil {
		return plan, err
	}
	return plan, nil
}

func (c *SyncClient) validateArchivePlan(plan RevisionPlan, expectedStatus string) (CloudObject, ManifestFile, error) {
	if validateID(plan.Revision.ID) != nil || plan.Revision.WorkspaceID != c.workspaceID || plan.Revision.ProfileID != c.profileID ||
		plan.Revision.Revision <= 0 || plan.Revision.Status != expectedStatus {
		return CloudObject{}, ManifestFile{}, errors.New("cloud returned an invalid profile revision")
	}
	if plan.Revision.BaseRevisionID != "" && validateID(plan.Revision.BaseRevisionID) != nil {
		return CloudObject{}, ManifestFile{}, errors.New("cloud returned an invalid base profile revision")
	}
	if plan.Manifest.RevisionID != plan.Revision.ID || plan.Manifest.SchemaVersion != manifestSchema || plan.Manifest.Mode != "snapshot" ||
		plan.Manifest.FileCount != 1 || len(plan.Manifest.Files) != 1 || len(plan.Manifest.DeletedPaths) != 0 || plan.Manifest.TotalBytes <= 0 {
		return CloudObject{}, ManifestFile{}, errors.New("cloud revision does not contain a supported profile snapshot")
	}
	if len(plan.Objects) != 1 {
		return CloudObject{}, ManifestFile{}, errors.New("cloud revision does not contain a supported profile object")
	}
	object, file := plan.Objects[0], plan.Manifest.Files[0]
	minimumCiphertextSize := int64(encryptedHeaderSize + 4 + 16)
	if validateID(object.ID) != nil || object.WorkspaceID != c.workspaceID || object.RevisionID != plan.Revision.ID ||
		file.Path != archiveObjectPath || file.ObjectKey == "" || file.ObjectKey != object.ObjectKey ||
		object.SizeBytes != file.SizeBytes || object.SizeBytes != plan.Manifest.TotalBytes ||
		object.SizeBytes < minimumCiphertextSize || object.SizeBytes > c.limits.MaxArchiveBytes ||
		!validSHA256(object.ContentHash) || !strings.EqualFold(object.ContentHash, file.CiphertextSHA256) ||
		!strings.EqualFold(file.ContentType, archiveContentType) || !strings.EqualFold(object.ContentType, archiveContentType) ||
		!object.Encrypted || object.EncryptionKeyRef != c.encryptionKeyRef {
		return CloudObject{}, ManifestFile{}, errors.New("cloud profile object metadata is invalid")
	}
	return object, file, nil
}

func (c *SyncClient) validateDeltaPlan(plan RevisionPlan, expectedStatus string) (CloudObject, ManifestFile, bool, error) {
	if validateID(plan.Revision.ID) != nil || plan.Revision.WorkspaceID != c.workspaceID || plan.Revision.ProfileID != c.profileID ||
		plan.Revision.Revision <= 0 || plan.Revision.Status != expectedStatus || validateID(plan.Revision.BaseRevisionID) != nil {
		return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned an invalid incremental profile revision")
	}
	manifest := plan.Manifest
	if manifest.RevisionID != plan.Revision.ID || manifest.SchemaVersion != manifestSchema || manifest.Mode != "incremental" ||
		manifest.FileCount != len(manifest.Files) || len(manifest.Files) > 1 || len(plan.Objects) != len(manifest.Files) ||
		len(manifest.Files)+len(manifest.DeletedPaths) == 0 || len(manifest.Files)+len(manifest.DeletedPaths) > c.limits.MaxFiles {
		return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned an invalid incremental profile manifest")
	}
	seen := make(map[string]struct{}, len(manifest.DeletedPaths)+len(manifest.Files))
	for _, deleted := range manifest.DeletedPaths {
		clean, err := safeArchivePath(deleted)
		if err != nil || clean != deleted {
			return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned an unsafe deleted profile path")
		}
		if _, exists := seen[clean]; exists {
			return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned duplicate incremental profile paths")
		}
		seen[clean] = struct{}{}
	}
	if len(manifest.Files) == 0 {
		if manifest.TotalBytes != 0 {
			return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned invalid deletion-only profile metadata")
		}
		return CloudObject{}, ManifestFile{}, false, nil
	}
	object, file := plan.Objects[0], manifest.Files[0]
	if _, exists := seen[file.Path]; exists {
		return CloudObject{}, ManifestFile{}, false, errors.New("cloud returned conflicting incremental profile paths")
	}
	cleanObjectPath, pathErr := safeArchivePath(file.Path)
	minimumCiphertextSize := int64(encryptedHeaderSize + 4 + 16)
	if pathErr != nil || cleanObjectPath != file.Path || !strings.HasPrefix(file.Path, "deltas/") || !strings.HasSuffix(file.Path, deltaObjectSuffix) ||
		file.ObjectKey == "" || file.ObjectKey != object.ObjectKey || object.SizeBytes != file.SizeBytes ||
		object.SizeBytes != manifest.TotalBytes || object.SizeBytes < minimumCiphertextSize || object.SizeBytes > c.limits.MaxArchiveBytes ||
		validateID(object.ID) != nil || object.WorkspaceID != c.workspaceID || object.RevisionID != plan.Revision.ID ||
		!validSHA256(object.ContentHash) || !strings.EqualFold(object.ContentHash, file.CiphertextSHA256) ||
		!strings.EqualFold(file.ContentType, archiveContentType) || !strings.EqualFold(object.ContentType, archiveContentType) ||
		!object.Encrypted || object.EncryptionKeyRef != c.encryptionKeyRef {
		return CloudObject{}, ManifestFile{}, false, errors.New("cloud incremental profile object metadata is invalid")
	}
	return object, file, true, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *SyncClient) objectGrant(ctx context.Context, revisionID, objectID string, upload bool, grant LeaseGrant) (ObjectGrant, error) {
	var result ObjectGrant
	verb, suffix := http.MethodGet, "/objects/"+url.PathEscape(objectID)+"/download"
	if upload {
		verb, suffix = http.MethodPost, "/objects/"+url.PathEscape(objectID)+"/upload"
	}
	path := c.profilePath("/revisions/" + url.PathEscape(revisionID) + suffix)
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
	if result.Method == "" {
		return result, errors.New("presigned object grant has no method")
	}
	if result.ObjectID != "" && result.ObjectID != objectID {
		return result, errors.New("presigned object grant is for a different object")
	}
	if strings.TrimSpace(result.URL) == "" {
		return result, errors.New("presigned object grant has no URL")
	}
	if result.ExpiresAt.IsZero() || !result.ExpiresAt.After(time.Now().UTC()) {
		return result, errors.New("presigned object grant is expired")
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

// Push creates a full snapshot when the local baseline is missing or needs
// compaction. Otherwise it uploads only changed files and records deletions in
// an incremental revision. A local inventory is advanced only after the cloud
// commit succeeds.
//
// Without an explicit baseRevisionID the base is the revision this device
// last synchronized (none for a device that never did). A device whose
// baseline is not the cloud's current revision therefore opens a conflict
// instead of silently overwriting changes made elsewhere. Its snapshot is
// still uploaded so the conflict can be resolved with keep_local, and the
// returned *RevisionConflictError carries the conflict ID.
func (c *SyncClient) Push(ctx context.Context, sourceDir string, baseRevisionID string) (result SyncResult, err error) {
	baseRevisionID = strings.TrimSpace(baseRevisionID)
	explicitBase := baseRevisionID != ""
	if explicitBase {
		if err := validateID(baseRevisionID); err != nil {
			return result, fmt.Errorf("base revision ID: %w", err)
		}
	}
	state, hasState := c.loadBaselineState(sourceDir)
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
	syncCtx, stopRenewal := c.keepLeaseRenewed(ctx, grant)
	renewalActive := true
	defer func() {
		if !renewalActive {
			return
		}
		renewalErr := stopRenewal()
		if renewalErr != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = renewalErr
		}
	}()
	profile, err := c.getProfile(syncCtx)
	if err != nil {
		return result, err
	}
	// A missing state yields an empty baseline: a device that never
	// synchronized has nothing it may overwrite.
	baseline, adoptedPending := state.baselineFor(profile.CurrentRevisionID)
	if !explicitBase {
		baseRevisionID = baseline.RevisionID
		if profile.CurrentRevisionID == "" {
			// Nothing in the cloud can be overwritten; a stale local
			// baseline would only be rejected.
			baseRevisionID = ""
		}
	}
	useIncremental := hasState && baseRevisionID != "" && baseline.RevisionID == baseRevisionID &&
		baseline.RevisionID == profile.CurrentRevisionID && baseline.ChainDepth < maxDeltaChainSize

	mode := "snapshot"
	objectPath := archiveObjectPath
	chainDepth := 0
	var inventory map[string]fileSignature
	var archivePath string
	var summary archiveSummary
	var deletedPaths []string
	if useIncremental {
		inventory, err = scanProfileDirectory(syncCtx, sourceDir, c.limits)
		if err != nil {
			return result, err
		}
		changedPaths, deleted := profileChanges(inventory, baseline.Files)
		deletedPaths = deleted
		if len(changedPaths) == 0 && len(deletedPaths) == 0 {
			if err := stopRenewal(); err != nil {
				renewalActive = false
				return result, err
			}
			renewalActive = false
			if adoptedPending {
				if err := c.writeLocalState(sourceDir, baseline); err != nil {
					return result, err
				}
			}
			result.Revision = Revision{
				ID: baseline.RevisionID, WorkspaceID: c.workspaceID, ProfileID: c.profileID, Status: "committed",
			}
			result.NoChanges = true
			return result, nil
		}
		mode = "incremental"
		chainDepth = baseline.ChainDepth + 1
		if len(changedPaths) != 0 {
			plainDeltaPath, _, deltaErr := createDeltaArchive(syncCtx, sourceDir, changedPaths, inventory, c.limits)
			if deltaErr != nil {
				return result, deltaErr
			}
			defer os.Remove(plainDeltaPath)
			archivePath, summary, err = encryptArchive(syncCtx, plainDeltaPath, c.encryptionKey, c.limits)
			if err != nil {
				return result, err
			}
			defer os.Remove(archivePath)
			objectPath = "deltas/" + summary.Hash + deltaObjectSuffix
		}
	} else {
		plainArchivePath, _, archiveErr := createArchive(syncCtx, sourceDir, c.limits)
		if archiveErr != nil {
			return result, archiveErr
		}
		defer os.Remove(plainArchivePath)
		inventory, err = scanArchiveInventory(syncCtx, plainArchivePath, c.limits)
		if err != nil {
			return result, err
		}
		archivePath, summary, err = encryptArchive(syncCtx, plainArchivePath, c.encryptionKey, c.limits)
		if err != nil {
			return result, err
		}
		defer os.Remove(archivePath)
	}

	var revisionFile *revisionFileInput
	if archivePath != "" {
		revisionFile = &revisionFileInput{
			Path: objectPath, CiphertextSHA256: summary.Hash, SizeBytes: summary.Size, ContentType: archiveContentType,
		}
	}
	plan, err := c.beginRevision(syncCtx, grant, baseRevisionID, mode, revisionFile, deletedPaths)
	if err != nil {
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != http.StatusConflict || httpErr.Code != codeRevisionConflict {
			return result, err
		}
		conflictErr := &RevisionConflictError{Plan: plan, Cause: httpErr}
		// Only snapshots can be promoted by keep_local; an incremental
		// revision's delta would apply to the wrong base.
		if mode == "snapshot" && c.validConflictPlan(plan) {
			c.uploadConflictSnapshot(syncCtx, grant, conflictErr, archivePath, summary)
			if conflictErr.SnapshotUploaded {
				pending := localSyncState{}
				if hasState {
					pending = state
				}
				pending.PendingRevisionID, pending.PendingFiles = plan.Revision.ID, inventory
				if writeErr := c.writeLocalState(sourceDir, pending); writeErr != nil {
					return result, errors.Join(conflictErr, fmt.Errorf("record conflict snapshot locally: %w", writeErr))
				}
			}
		}
		// The deferred release frees the lease: keep_local requires that no
		// device holds one.
		return result, conflictErr
	}
	if plan.Revision.BaseRevisionID != baseRevisionID {
		return result, errors.New("cloud revision base does not match the requested baseline")
	}
	var object CloudObject
	var manifestFile ManifestFile
	var hasObject bool
	if mode == "snapshot" {
		object, manifestFile, err = c.validateArchivePlan(plan, "uploading")
		hasObject = err == nil
	} else {
		object, manifestFile, hasObject, err = c.validateDeltaPlan(plan, "uploading")
	}
	if err != nil {
		return result, err
	}
	if hasObject {
		if archivePath == "" || manifestFile.Path != objectPath || object.SizeBytes != summary.Size ||
			!strings.EqualFold(object.ContentHash, summary.Hash) ||
			!strings.EqualFold(manifestFile.CiphertextSHA256, summary.Hash) {
			return result, errors.New("cloud object metadata does not match local archive")
		}
		uploadGrant, grantErr := c.objectGrant(syncCtx, plan.Revision.ID, object.ID, true, grant)
		if grantErr != nil {
			return result, grantErr
		}
		if err := c.upload(syncCtx, uploadGrant, archivePath, summary.Size); err != nil {
			return result, err
		}
	} else if archivePath != "" {
		return result, errors.New("cloud omitted the incremental profile object")
	}
	if err := stopRenewal(); err != nil {
		renewalActive = false
		return result, err
	}
	renewalActive = false
	if err := c.confirmLease(ctx, grant); err != nil {
		return result, err
	}
	var committed struct {
		Revision Revision `json:"revision"`
	}
	commitPath := c.profilePath("/revisions/" + url.PathEscape(plan.Revision.ID) + "/commit")
	if err := c.requestJSON(ctx, http.MethodPost, commitPath, struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}{c.deviceID, grant.Token}, &committed); err != nil {
		return result, err
	}
	releaseNeeded = false
	if committed.Revision.ID != plan.Revision.ID || committed.Revision.WorkspaceID != c.workspaceID ||
		committed.Revision.ProfileID != c.profileID || committed.Revision.Status != "committed" {
		return result, errors.New("cloud returned an invalid committed profile revision")
	}
	if err := c.writeLocalState(sourceDir, localSyncState{
		RevisionID: committed.Revision.ID, ChainDepth: chainDepth, Files: inventory,
	}); err != nil {
		return result, err
	}
	result.Revision, result.ArchiveBytes, result.ArchiveSHA256 = committed.Revision, summary.Size, summary.Hash
	return result, nil
}

// uploadConflictSnapshot uploads the snapshot of a conflicting revision. The
// revision stays uncommitted; the server promotes it only if the conflict is
// resolved with keep_local, after verifying the uploaded object.
func (c *SyncClient) uploadConflictSnapshot(ctx context.Context, grant LeaseGrant, conflict *RevisionConflictError, archivePath string, summary archiveSummary) {
	object, manifestFile, err := c.validateArchivePlan(conflict.Plan, "uploading")
	if err == nil && (archivePath == "" || manifestFile.Path != archiveObjectPath || object.SizeBytes != summary.Size ||
		!strings.EqualFold(object.ContentHash, summary.Hash) || !strings.EqualFold(manifestFile.CiphertextSHA256, summary.Hash)) {
		err = errors.New("cloud object metadata does not match local archive")
	}
	if err == nil {
		var uploadGrant ObjectGrant
		uploadGrant, err = c.objectGrant(ctx, conflict.Plan.Revision.ID, object.ID, true, grant)
		if err == nil {
			err = c.upload(ctx, uploadGrant, archivePath, summary.Size)
		}
	}
	conflict.SnapshotUploaded = err == nil
	conflict.UploadErr = err
}

func (c *SyncClient) loadRevisionChain(ctx context.Context, revisionID, currentRevisionID string) ([]RevisionPlan, error) {
	chain := make([]RevisionPlan, 0, maxDeltaChainSize+1)
	seen := make(map[string]struct{}, maxDeltaChainSize+1)
	nextID := revisionID
	var childRevision int64
	for depth := 0; depth <= maxDeltaChainSize; depth++ {
		if _, exists := seen[nextID]; exists {
			return nil, errors.New("cloud profile revision chain contains a cycle")
		}
		seen[nextID] = struct{}{}
		var plan RevisionPlan
		getPath := c.profilePath("/revisions/" + url.PathEscape(nextID))
		if err := c.requestJSON(ctx, http.MethodGet, getPath, nil, &plan); err != nil {
			return nil, err
		}
		if plan.Revision.ID != nextID {
			return nil, errors.New("cloud returned a different profile revision")
		}
		if childRevision != 0 && plan.Revision.Revision >= childRevision {
			return nil, errors.New("cloud profile revision chain is not strictly ordered")
		}
		expectedStatus := "superseded"
		if nextID == currentRevisionID {
			expectedStatus = "committed"
		}
		switch plan.Manifest.Mode {
		case "snapshot":
			if _, _, err := c.validateArchivePlan(plan, expectedStatus); err != nil {
				return nil, err
			}
			chain = append(chain, plan)
			return chain, nil
		case "incremental":
			if _, _, _, err := c.validateDeltaPlan(plan, expectedStatus); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("cloud returned an unsupported profile revision mode")
		}
		chain = append(chain, plan)
		childRevision = plan.Revision.Revision
		nextID = plan.Revision.BaseRevisionID
	}
	return nil, errors.New("cloud profile incremental chain exceeds the supported limit")
}

func (c *SyncClient) downloadPlainArchive(ctx context.Context, grant LeaseGrant, plan RevisionPlan, object CloudObject) (string, error) {
	downloadGrant, err := c.objectGrant(ctx, plan.Revision.ID, object.ID, false, grant)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "ant-profile-sync-*.enc")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", closeErr
	}
	defer os.Remove(tmpPath)
	if err := c.download(ctx, downloadGrant, tmpPath, object.SizeBytes, object.ContentHash); err != nil {
		return "", err
	}
	return decryptArchive(ctx, tmpPath, c.encryptionKey, c.limits)
}

// Pull resolves a revision back to its nearest snapshot, verifies and applies
// each encrypted delta in order, then atomically swaps the reconstructed
// profile. Historical selections are restored on the server after the local
// swap has made the verified profile active.
func (c *SyncClient) Pull(ctx context.Context, revisionID, destinationDir string) (Revision, error) {
	if err := validateID(revisionID); err != nil {
		return Revision{}, err
	}
	return c.pull(ctx, revisionID, destinationDir)
}

// PullCurrent restores the cloud's current revision as read under the
// synchronization lease, so a commit racing this call cannot make the device
// select, and restore, an older revision. It returns ErrNoCloudRevision when
// the profile was never committed.
//
// A directory that already derives from the current revision is left alone:
// it can only differ by local changes made since, which the next Push
// uploads on top of that revision.
func (c *SyncClient) PullCurrent(ctx context.Context, destinationDir string) (Revision, error) {
	return c.pull(ctx, "", destinationDir)
}

func (c *SyncClient) pull(ctx context.Context, revisionID, destinationDir string) (revision Revision, err error) {
	if strings.TrimSpace(destinationDir) == "" {
		return Revision{}, errors.New("destination directory is required")
	}
	followCurrent := revisionID == ""
	var state localSyncState
	var hasState bool
	if followCurrent {
		state, hasState = c.loadBaselineState(destinationDir)
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
	syncCtx, stopRenewal := c.keepLeaseRenewed(ctx, grant)
	renewalActive := true
	defer func() {
		if !renewalActive {
			return
		}
		renewalErr := stopRenewal()
		if renewalErr != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = renewalErr
		}
	}()
	cloudProfile, err := c.getProfile(syncCtx)
	if err != nil {
		return Revision{}, err
	}
	if followCurrent {
		if cloudProfile.CurrentRevisionID == "" {
			return Revision{}, ErrNoCloudRevision
		}
		revisionID = cloudProfile.CurrentRevisionID
		if hasState {
			baseline, adoptedPending := state.baselineFor(revisionID)
			if baseline.RevisionID == revisionID && localProfileHasFiles(destinationDir) {
				if adoptedPending {
					if err := c.writeLocalState(destinationDir, baseline); err != nil {
						return Revision{}, err
					}
				}
				return Revision{ID: revisionID, WorkspaceID: c.workspaceID, ProfileID: c.profileID, Status: "committed"}, nil
			}
		}
	}
	chain, err := c.loadRevisionChain(syncCtx, revisionID, cloudProfile.CurrentRevisionID)
	if err != nil {
		return Revision{}, err
	}
	plan := chain[0]
	basePlan := chain[len(chain)-1]
	baseObject, _, err := c.validateArchivePlan(basePlan, basePlan.Revision.Status)
	if err != nil {
		return Revision{}, err
	}
	plainArchivePath, err := c.downloadPlainArchive(syncCtx, grant, basePlan, baseObject)
	if err != nil {
		return Revision{}, err
	}
	defer os.Remove(plainArchivePath)
	staging, err := extractArchiveToTemp(syncCtx, plainArchivePath, c.limits, destinationDir)
	if err != nil {
		return Revision{}, err
	}
	defer os.RemoveAll(staging)
	inventory, err := scanProfileDirectory(syncCtx, staging, c.limits)
	if err != nil {
		return Revision{}, err
	}
	for index := len(chain) - 2; index >= 0; index-- {
		deltaPlan := chain[index]
		object, _, hasObject, validateErr := c.validateDeltaPlan(deltaPlan, deltaPlan.Revision.Status)
		if validateErr != nil {
			return Revision{}, validateErr
		}
		if err := applyDeletedPaths(staging, deltaPlan.Manifest.DeletedPaths); err != nil {
			return Revision{}, err
		}
		if hasObject {
			plainDeltaPath, downloadErr := c.downloadPlainArchive(syncCtx, grant, deltaPlan, object)
			if downloadErr != nil {
				return Revision{}, downloadErr
			}
			deltaRoot, extractErr := extractArchiveToTemp(syncCtx, plainDeltaPath, c.limits, staging)
			_ = os.Remove(plainDeltaPath)
			if extractErr != nil {
				return Revision{}, extractErr
			}
			overlayErr := overlayDelta(syncCtx, deltaRoot, staging)
			_ = os.RemoveAll(deltaRoot)
			if overlayErr != nil {
				return Revision{}, overlayErr
			}
		}
		inventory, err = scanProfileDirectory(syncCtx, staging, c.limits)
		if err != nil {
			return Revision{}, err
		}
	}
	if err := stopRenewal(); err != nil {
		renewalActive = false
		return Revision{}, err
	}
	renewalActive = false
	if err := c.confirmLease(ctx, grant); err != nil {
		return Revision{}, err
	}
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
	if revisionID == cloudProfile.CurrentRevisionID {
		rollbackNeeded = false
		if err := swap.Commit(); err != nil {
			return Revision{}, err
		}
		if err := c.writeLocalState(destinationDir, localSyncState{
			RevisionID: plan.Revision.ID, ChainDepth: len(chain) - 1, Files: inventory,
		}); err != nil {
			return Revision{}, err
		}
		return plan.Revision, nil
	}
	getPath := c.profilePath("/revisions/" + url.PathEscape(revisionID))
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
	// A 2xx restore response means the remote selection may already be durable.
	// Keep the downloaded profile active even if the response body is malformed;
	// the retained backup then allows explicit operator recovery.
	rollbackNeeded = false
	if restored.Revision.ID != plan.Revision.ID || restored.Revision.WorkspaceID != c.workspaceID ||
		restored.Revision.ProfileID != c.profileID || restored.Revision.Status != "committed" {
		return Revision{}, errors.New("cloud returned an invalid restored profile revision")
	}
	// The server has now atomically selected this revision, so a failure to
	// remove the local backup must not put the device back on the old profile.
	if err := swap.Commit(); err != nil {
		return Revision{}, err
	}
	if err := c.writeLocalState(destinationDir, localSyncState{
		RevisionID: restored.Revision.ID, ChainDepth: len(chain) - 1, Files: inventory,
	}); err != nil {
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
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		name, err := safeArchivePath(rel)
		if err != nil {
			return err
		}
		if isTransientProfileEntry(name, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
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
		walkInfo, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		if files > limits.MaxFiles {
			return ErrArchiveLimit
		}
		input, err := os.Open(full)
		if err != nil {
			return err
		}
		stat, err := input.Stat()
		if err != nil {
			_ = input.Close()
			return err
		}
		if !stat.Mode().IsRegular() || !os.SameFile(walkInfo, stat) {
			_ = input.Close()
			return fmt.Errorf("profile entry changed while archiving %q", full)
		}
		if stat.Size() > limits.MaxFileBytes || total > limits.MaxBytes-stat.Size() {
			_ = input.Close()
			return ErrArchiveLimit
		}
		total += stat.Size()
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		out, err := zw.CreateHeader(header)
		if err != nil {
			_ = input.Close()
			return err
		}
		written, copyErr := io.Copy(out, &countingLimitReader{ctx: ctx, r: input, remaining: stat.Size()})
		postStat, statErr := input.Stat()
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != stat.Size() || postStat.Size() != stat.Size() || !postStat.ModTime().Equal(stat.ModTime()) {
			return fmt.Errorf("profile entry changed while archiving %q", full)
		}
		return nil
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
		if entry.FileInfo().IsDir() || isTransientProfilePath(name) {
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
	path := c.profilePath("/revisions")
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
		return Revision{}, ErrNoCloudRevision
	}
	sort.Slice(committed, func(i, j int) bool { return committed[i].Revision > committed[j].Revision })
	return committed[0], nil
}
