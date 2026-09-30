package profilesyncservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound          = errors.New("cloud profile not found")
	ErrVersionConflict   = errors.New("cloud profile version conflict")
	ErrLeaseHeld         = errors.New("cloud profile is leased by another device")
	ErrLeaseInvalid      = errors.New("profile sync lease is invalid or expired")
	ErrRevisionConflict  = errors.New("profile revision conflicts with the current cloud revision")
	ErrRevisionState     = errors.New("profile revision state conflict")
	ErrObjectUnavailable = errors.New("profile object is unavailable or does not match its metadata")
	ErrDeviceScope       = errors.New("profile operation is outside the authenticated device scope")
	// ErrConflictUnresolved blocks new leases and revisions while a profile
	// has an open conflict, so no device can pull, push or restore state that
	// a pending keep-local/keep-remote decision would contradict.
	ErrConflictUnresolved = errors.New("profile has an unresolved synchronization conflict")
	// ErrNameConflict: cloud profile names are unique per workspace among
	// live profiles, compared case-insensitively.
	ErrNameConflict = errors.New("cloud profile name already exists")
	// ErrStorageQuotaExceeded is returned when the organization's current
	// profile footprint would exceed its storage_bytes entitlement. Storage is
	// tracked separately from period-based billing usage because replacing a
	// file must charge only the resulting delta.
	ErrStorageQuotaExceeded = errors.New("profile storage quota exceeded")
)

const (
	manifestSchemaVersion       = "ant-profile/v2"
	maxProfileFiles             = 10_000
	maxProfileBytes       int64 = 20 << 30
	maxProfileFile        int64 = 4 << 30
)

type Profile struct {
	ID                    string     `json:"id"`
	WorkspaceID           string     `json:"workspaceId"`
	OwnerUserID           string     `json:"ownerUserId,omitempty"`
	Name                  string     `json:"name"`
	FingerprintTemplateID string     `json:"fingerprintTemplateId,omitempty"`
	CurrentRevisionID     string     `json:"currentRevisionId,omitempty"`
	Status                string     `json:"status"`
	Version               int64      `json:"version"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	DeletedAt             *time.Time `json:"deletedAt,omitempty"`
}

type Revision struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspaceId"`
	ProfileID      string     `json:"profileId"`
	Revision       int64      `json:"revision"`
	BaseRevisionID string     `json:"baseRevisionId,omitempty"`
	ContentHash    string     `json:"contentHash"`
	Status         string     `json:"status"`
	DeviceID       string     `json:"deviceId,omitempty"`
	CreatedBy      string     `json:"createdBy,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	CommittedAt    *time.Time `json:"committedAt,omitempty"`
}

type Manifest struct {
	RevisionID    string         `json:"revisionId"`
	SchemaVersion string         `json:"schemaVersion"`
	Mode          string         `json:"mode"`
	FileCount     int            `json:"fileCount"`
	TotalBytes    int64          `json:"totalBytes"`
	Files         []ManifestFile `json:"files"`
	DeletedPaths  []string       `json:"deletedPaths,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
}

type ManifestFile struct {
	Path             string `json:"path"`
	ObjectKey        string `json:"objectKey"`
	CiphertextSHA256 string `json:"ciphertextSha256"`
	SizeBytes        int64  `json:"sizeBytes"`
	ContentType      string `json:"contentType"`
}

type Object struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspaceId"`
	RevisionID       string    `json:"revisionId"`
	ObjectKey        string    `json:"objectKey"`
	ContentHash      string    `json:"contentHash"`
	SizeBytes        int64     `json:"sizeBytes"`
	StorageBackend   string    `json:"storageBackend"`
	Encrypted        bool      `json:"encrypted"`
	EncryptionKeyRef string    `json:"encryptionKeyRef"`
	ContentType      string    `json:"contentType"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Lease struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspaceId"`
	ProfileID      string     `json:"profileId"`
	HolderDeviceID string     `json:"holderDeviceId"`
	AcquiredAt     time.Time  `json:"acquiredAt"`
	RenewedAt      *time.Time `json:"renewedAt,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	ReleasedAt     *time.Time `json:"releasedAt,omitempty"`
}

type LeaseGrant struct {
	Lease Lease  `json:"lease"`
	Token string `json:"token"`
}

type Conflict struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspaceId"`
	ProfileID        string     `json:"profileId"`
	LocalRevisionID  string     `json:"localRevisionId"`
	RemoteRevisionID string     `json:"remoteRevisionId"`
	Status           string     `json:"status"`
	Resolution       string     `json:"resolution,omitempty"`
	ResolvedBy       string     `json:"resolvedBy,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

type RevisionPlan struct {
	Revision Revision  `json:"revision"`
	Manifest Manifest  `json:"manifest"`
	Objects  []Object  `json:"objects"`
	Conflict *Conflict `json:"conflict,omitempty"`
}

type Repository interface {
	CreateCloudProfile(context.Context, Profile) error
	FindCloudProfile(context.Context, string, string) (Profile, error)
	ListCloudProfiles(context.Context, string) ([]Profile, error)
	// AcquireProfileLease grants lease unless another holder has an active
	// lease or the profile has an open conflict. With reclaimOwn, an active
	// lease already held by the same device is released and replaced, so a
	// device that crashed mid-sync does not wait for its own lease to expire.
	AcquireProfileLease(ctx context.Context, lease Lease, tokenHash string, reclaimOwn bool, now time.Time) error
	ValidateProfileLease(context.Context, string, string, string, string, time.Time) error
	RenewProfileLease(context.Context, string, string, string, string, time.Time, time.Time) (Lease, error)
	ReleaseProfileLease(context.Context, string, string, string, string, time.Time) error
	BeginProfileRevision(context.Context, Revision, Manifest, []Object, string, string, time.Time) (Revision, Profile, *Conflict, error)
	LoadProfileRevisionPlan(context.Context, string, string, string, string, string, time.Time) (RevisionPlan, error)
	LoadProfileRevisionSnapshot(context.Context, string, string, string) (RevisionPlan, error)
	CommitProfileRevision(context.Context, string, string, string, string, string, time.Time) (Profile, Revision, error)
	RestoreProfileRevision(context.Context, string, string, string, string, string, string, time.Time) (Profile, Revision, error)
	ListProfileRevisions(context.Context, string, string) ([]Revision, error)
	ListProfileConflicts(context.Context, string, string) ([]Conflict, error)
	// LoadProfileConflictPlan returns a conflict with the plan (manifest and
	// objects) of its local revision, whatever that revision's status.
	LoadProfileConflictPlan(ctx context.Context, workspaceID, profileID, conflictID string) (Conflict, RevisionPlan, error)
	// ResolveProfileConflict applies a resolution atomically. keep_local
	// promotes the local snapshot revision to current (its objects must have
	// been verified by the caller); keep_remote discards the local revision.
	ResolveProfileConflict(ctx context.Context, workspaceID, profileID, conflictID, resolution, actorID string, now time.Time) (Conflict, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type TokenFactory func() (raw, hash string, err error)

type ObjectStorage interface {
	Verify(context.Context, Object) error
	PresignUpload(context.Context, Object, time.Duration) (string, error)
	PresignDownload(context.Context, Object, time.Duration) (string, error)
}

type Service struct {
	repository       Repository
	authorizer       Authorizer
	newToken         TokenFactory
	objectStorage    ObjectStorage
	encryptionKeyRef string
	storageBackend   string
	now              func() time.Time
}

type CreateProfileInput struct {
	Name                  string `json:"name"`
	FingerprintTemplateID string `json:"fingerprintTemplateId,omitempty"`
}

type AcquireLeaseInput struct {
	DeviceID   string `json:"deviceId"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

type LeaseTokenInput struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

type BeginRevisionInput struct {
	DeviceID       string      `json:"deviceId"`
	LeaseToken     string      `json:"leaseToken"`
	BaseRevisionID string      `json:"baseRevisionId,omitempty"`
	Mode           string      `json:"mode,omitempty"`
	Files          []FileInput `json:"files"`
	DeletedPaths   []string    `json:"deletedPaths,omitempty"`
}

type FileInput struct {
	Path             string `json:"path"`
	CiphertextSHA256 string `json:"ciphertextSha256"`
	SizeBytes        int64  `json:"sizeBytes"`
	ContentType      string `json:"contentType,omitempty"`
}

type ResolveConflictInput struct {
	Resolution string `json:"resolution"`
}

type UploadGrant struct {
	ObjectID  string            `json:"objectId"`
	ObjectKey string            `json:"objectKey"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type DownloadGrant struct {
	ObjectID  string            `json:"objectId"`
	ObjectKey string            `json:"objectKey"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

func New(
	repository Repository,
	authorizer Authorizer,
	newToken TokenFactory,
	objectStorage ObjectStorage,
	encryptionKeyRef, storageBackend string,
) *Service {
	return &Service{
		repository: repository, authorizer: authorizer, newToken: newToken, objectStorage: objectStorage,
		encryptionKeyRef: strings.TrimSpace(encryptionKeyRef), storageBackend: strings.TrimSpace(storageBackend), now: time.Now,
	}
}

func (s *Service) CreateProfile(ctx context.Context, actorID, workspaceID string, input CreateProfileInput) (Profile, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileManage); err != nil {
		return Profile{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return Profile{}, errors.New("valid profile name is required")
	}
	now := s.now().UTC()
	profile := Profile{
		ID: uuid.NewString(), WorkspaceID: workspaceID, OwnerUserID: actorID,
		Name: name, FingerprintTemplateID: strings.TrimSpace(input.FingerprintTemplateID),
		Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repository.CreateCloudProfile(ctx, profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *Service) ListProfiles(ctx context.Context, actorID, workspaceID string) ([]Profile, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return nil, err
	}
	return s.repository.ListCloudProfiles(ctx, workspaceID)
}

func (s *Service) GetProfile(ctx context.Context, actorID, workspaceID, profileID string) (Profile, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return Profile{}, err
	}
	return s.repository.FindCloudProfile(ctx, workspaceID, profileID)
}

func (s *Service) AcquireLease(ctx context.Context, actorID, workspaceID, profileID string, input AcquireLeaseInput) (LeaseGrant, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return LeaseGrant{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return LeaseGrant{}, err
	}
	deviceID := strings.TrimSpace(input.DeviceID)
	if deviceID == "" {
		return LeaseGrant{}, errors.New("deviceId is required")
	}
	ttl := 10 * time.Minute
	if input.TTLSeconds != 0 {
		ttl = time.Duration(input.TTLSeconds) * time.Second
	}
	if ttl < time.Minute || ttl > 30*time.Minute {
		return LeaseGrant{}, errors.New("ttlSeconds must be between 60 and 1800")
	}
	raw, hash, err := s.newToken()
	if err != nil {
		return LeaseGrant{}, err
	}
	now := s.now().UTC()
	lease := Lease{
		ID: uuid.NewString(), WorkspaceID: workspaceID, ProfileID: profileID,
		HolderDeviceID: deviceID, AcquiredAt: now, ExpiresAt: now.Add(ttl),
	}
	// Only the device itself may replace its own active lease (for example
	// after crashing mid-sync). A workspace user naming a device ID must wait
	// for expiry, so it cannot preempt that device's in-flight transfer.
	reclaimOwn := memberservice.AuthorizedDeviceID(ctx) == deviceID
	if err := s.repository.AcquireProfileLease(ctx, lease, hash, reclaimOwn, now); err != nil {
		return LeaseGrant{}, err
	}
	return LeaseGrant{Lease: lease, Token: raw}, nil
}

func (s *Service) RenewLease(ctx context.Context, actorID, workspaceID, profileID string, input LeaseTokenInput, ttl time.Duration) (Lease, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return Lease{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return Lease{}, err
	}
	if ttl < time.Minute || ttl > 30*time.Minute {
		return Lease{}, errors.New("lease TTL must be between one and thirty minutes")
	}
	now := s.now().UTC()
	return s.repository.RenewProfileLease(
		ctx, workspaceID, profileID, strings.TrimSpace(input.DeviceID), tokenHash(input.Token), now, now.Add(ttl),
	)
}

func (s *Service) ReleaseLease(ctx context.Context, actorID, workspaceID, profileID string, input LeaseTokenInput) error {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return err
	}
	return s.repository.ReleaseProfileLease(
		ctx, workspaceID, profileID, strings.TrimSpace(input.DeviceID), tokenHash(input.Token), s.now().UTC(),
	)
}

func (s *Service) BeginRevision(ctx context.Context, actorID, workspaceID, profileID string, input BeginRevisionInput) (RevisionPlan, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return RevisionPlan{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return RevisionPlan{}, err
	}
	if s.encryptionKeyRef == "" || s.storageBackend == "" {
		return RevisionPlan{}, errors.New("profile object storage is not configured")
	}
	now := s.now().UTC()
	revisionID := uuid.NewString()
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "snapshot"
	}
	if mode != "snapshot" && mode != "incremental" {
		return RevisionPlan{}, errors.New("profile revision mode must be snapshot or incremental")
	}
	if mode == "incremental" && strings.TrimSpace(input.BaseRevisionID) == "" {
		return RevisionPlan{}, errors.New("baseRevisionId is required for an incremental profile revision")
	}
	manifest, objects, contentHash, err := s.buildManifest(
		workspaceID, profileID, revisionID, mode, input.Files, input.DeletedPaths, now,
	)
	if err != nil {
		return RevisionPlan{}, err
	}
	revision := Revision{
		ID: revisionID, WorkspaceID: workspaceID, ProfileID: profileID,
		BaseRevisionID: strings.TrimSpace(input.BaseRevisionID), ContentHash: contentHash,
		Status: "uploading", DeviceID: strings.TrimSpace(input.DeviceID), CreatedBy: actorID, CreatedAt: now,
	}
	revision, profile, conflict, err := s.repository.BeginProfileRevision(
		ctx, revision, manifest, objects, tokenHash(input.LeaseToken), uuid.NewString(), now,
	)
	if err != nil {
		return RevisionPlan{}, err
	}
	_ = profile
	return RevisionPlan{Revision: revision, Manifest: manifest, Objects: objects, Conflict: conflict}, nil
}

func (s *Service) CommitRevision(ctx context.Context, actorID, workspaceID, profileID, revisionID string, input LeaseTokenInput) (Profile, Revision, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return Profile{}, Revision{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return Profile{}, Revision{}, err
	}
	if s.objectStorage == nil {
		return Profile{}, Revision{}, errors.New("profile object storage is not configured")
	}
	now := s.now().UTC()
	hash := tokenHash(input.Token)
	plan, err := s.repository.LoadProfileRevisionPlan(
		ctx, workspaceID, profileID, revisionID, strings.TrimSpace(input.DeviceID), hash, now,
	)
	if err != nil {
		return Profile{}, Revision{}, err
	}
	for _, object := range plan.Objects {
		if err := s.objectStorage.Verify(ctx, object); err != nil {
			return Profile{}, Revision{}, ErrObjectUnavailable
		}
	}
	return s.repository.CommitProfileRevision(
		ctx, workspaceID, profileID, revisionID, strings.TrimSpace(input.DeviceID), hash, s.now().UTC(),
	)
}

func (s *Service) PrepareObjectUpload(ctx context.Context, actorID, workspaceID, profileID, revisionID, objectID string, input LeaseTokenInput) (UploadGrant, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return UploadGrant{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return UploadGrant{}, err
	}
	if s.objectStorage == nil {
		return UploadGrant{}, errors.New("profile object storage is not configured")
	}
	now := s.now().UTC()
	plan, err := s.repository.LoadProfileRevisionPlan(
		ctx, workspaceID, profileID, revisionID, strings.TrimSpace(input.DeviceID), tokenHash(input.Token), now,
	)
	if err != nil {
		return UploadGrant{}, err
	}
	var selected *Object
	for index := range plan.Objects {
		if plan.Objects[index].ID == objectID {
			selected = &plan.Objects[index]
			break
		}
	}
	if selected == nil {
		return UploadGrant{}, ErrNotFound
	}
	ttl := 15 * time.Minute
	url, err := s.objectStorage.PresignUpload(ctx, *selected, ttl)
	if err != nil {
		return UploadGrant{}, err
	}
	return UploadGrant{
		ObjectID: selected.ID, ObjectKey: selected.ObjectKey, Method: "PUT", URL: url,
		Headers: map[string]string{"Content-Type": selected.ContentType}, ExpiresAt: now.Add(ttl),
	}, nil
}

func (s *Service) GetRevision(ctx context.Context, actorID, workspaceID, profileID, revisionID string) (RevisionPlan, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return RevisionPlan{}, err
	}
	return s.repository.LoadProfileRevisionSnapshot(ctx, workspaceID, profileID, revisionID)
}

func (s *Service) PrepareObjectDownload(ctx context.Context, actorID, workspaceID, profileID, revisionID, objectID string) (DownloadGrant, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return DownloadGrant{}, err
	}
	if s.objectStorage == nil {
		return DownloadGrant{}, errors.New("profile object storage is not configured")
	}
	plan, err := s.repository.LoadProfileRevisionSnapshot(ctx, workspaceID, profileID, revisionID)
	if err != nil {
		return DownloadGrant{}, err
	}
	var selected *Object
	for index := range plan.Objects {
		if plan.Objects[index].ID == objectID {
			selected = &plan.Objects[index]
			break
		}
	}
	if selected == nil {
		return DownloadGrant{}, ErrNotFound
	}
	now := s.now().UTC()
	ttl := 15 * time.Minute
	downloadURL, err := s.objectStorage.PresignDownload(ctx, *selected, ttl)
	if err != nil {
		return DownloadGrant{}, err
	}
	return DownloadGrant{
		ObjectID: selected.ID, ObjectKey: selected.ObjectKey, Method: "GET", URL: downloadURL,
		Headers: map[string]string{}, ExpiresAt: now.Add(ttl),
	}, nil
}

func (s *Service) RestoreRevision(ctx context.Context, actorID, workspaceID, profileID, revisionID string, input LeaseTokenInput) (Profile, Revision, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return Profile{}, Revision{}, err
	}
	if err := requireAuthorizedDevice(ctx, input.DeviceID); err != nil {
		return Profile{}, Revision{}, err
	}
	if s.objectStorage == nil {
		return Profile{}, Revision{}, errors.New("profile object storage is not configured")
	}
	deviceID := strings.TrimSpace(input.DeviceID)
	leaseHash := tokenHash(input.Token)
	now := s.now().UTC()
	if err := s.repository.ValidateProfileLease(ctx, workspaceID, profileID, deviceID, leaseHash, now); err != nil {
		return Profile{}, Revision{}, err
	}
	plan, err := s.repository.LoadProfileRevisionSnapshot(ctx, workspaceID, profileID, revisionID)
	if err != nil {
		return Profile{}, Revision{}, err
	}
	for _, object := range plan.Objects {
		if err := s.objectStorage.Verify(ctx, object); err != nil {
			return Profile{}, Revision{}, ErrObjectUnavailable
		}
	}
	return s.repository.RestoreProfileRevision(
		ctx, workspaceID, profileID, revisionID, deviceID, leaseHash, actorID, s.now().UTC(),
	)
}

func requireAuthorizedDevice(ctx context.Context, requestedDeviceID string) error {
	authorizedDeviceID := memberservice.AuthorizedDeviceID(ctx)
	if authorizedDeviceID != "" && strings.TrimSpace(requestedDeviceID) != authorizedDeviceID {
		return ErrDeviceScope
	}
	return nil
}

func (s *Service) Revisions(ctx context.Context, actorID, workspaceID, profileID string) ([]Revision, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return nil, err
	}
	return s.repository.ListProfileRevisions(ctx, workspaceID, profileID)
}

func (s *Service) Conflicts(ctx context.Context, actorID, workspaceID, profileID string) ([]Conflict, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileRead); err != nil {
		return nil, err
	}
	return s.repository.ListProfileConflicts(ctx, workspaceID, profileID)
}

func (s *Service) ResolveConflict(ctx context.Context, actorID, workspaceID, profileID, conflictID string, input ResolveConflictInput) (Conflict, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionProfileSync); err != nil {
		return Conflict{}, err
	}
	resolution := strings.ToLower(strings.TrimSpace(input.Resolution))
	if resolution != "keep_local" && resolution != "keep_remote" {
		return Conflict{}, errors.New("resolution must be keep_local or keep_remote")
	}
	if resolution == "keep_local" {
		// Promoting the local revision makes it the state every device
		// restores, so each of its objects must exist and match before the
		// conflict is resolved; an interrupted upload stays resolvable only
		// as keep_remote.
		if s.objectStorage == nil {
			return Conflict{}, errors.New("profile object storage is not configured")
		}
		conflict, plan, err := s.repository.LoadProfileConflictPlan(ctx, workspaceID, profileID, conflictID)
		if err != nil {
			return Conflict{}, err
		}
		if conflict.Status != "open" || plan.Revision.Status != "uploading" || plan.Manifest.Mode != "snapshot" || len(plan.Objects) == 0 {
			return Conflict{}, ErrRevisionState
		}
		for _, object := range plan.Objects {
			if err := s.objectStorage.Verify(ctx, object); err != nil {
				return Conflict{}, ErrObjectUnavailable
			}
		}
	}
	return s.repository.ResolveProfileConflict(
		ctx, workspaceID, profileID, conflictID, resolution, actorID, s.now().UTC(),
	)
}

func (s *Service) buildManifest(workspaceID, profileID, revisionID, mode string, inputs []FileInput, deletedInputs []string, now time.Time) (Manifest, []Object, string, error) {
	if len(inputs)+len(deletedInputs) == 0 || len(inputs)+len(deletedInputs) > maxProfileFiles {
		return Manifest{}, nil, "", errors.New("profile revision must contain between 1 and 10000 changes")
	}
	if mode == "snapshot" && len(inputs) == 0 {
		return Manifest{}, nil, "", errors.New("a profile snapshot must contain at least one file")
	}
	if mode == "snapshot" && len(deletedInputs) != 0 {
		return Manifest{}, nil, "", errors.New("deletedPaths are only valid for incremental profile revisions")
	}
	files := make([]ManifestFile, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	var totalBytes int64
	for _, input := range inputs {
		cleanPath, err := normalizeProfilePath(input.Path)
		if err != nil {
			return Manifest{}, nil, "", err
		}
		if _, exists := seen[cleanPath]; exists {
			return Manifest{}, nil, "", errors.New("profile manifest contains duplicate paths")
		}
		seen[cleanPath] = struct{}{}
		contentHash := strings.ToLower(strings.TrimSpace(input.CiphertextSHA256))
		if len(contentHash) != 64 {
			return Manifest{}, nil, "", errors.New("ciphertextSha256 must be a SHA-256 hex digest")
		}
		if _, err := hex.DecodeString(contentHash); err != nil {
			return Manifest{}, nil, "", errors.New("ciphertextSha256 must be a SHA-256 hex digest")
		}
		if input.SizeBytes < 0 || input.SizeBytes > maxProfileFile || totalBytes > maxProfileBytes-input.SizeBytes {
			return Manifest{}, nil, "", errors.New("profile object size exceeds the supported limit")
		}
		totalBytes += input.SizeBytes
		contentType := strings.TrimSpace(input.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if _, _, err := mime.ParseMediaType(contentType); err != nil || strings.ContainsAny(contentType, "\r\n") {
			return Manifest{}, nil, "", errors.New("profile object contentType is invalid")
		}
		pathHash := sha256.Sum256([]byte(cleanPath))
		objectKey := strings.Join([]string{
			"workspaces", workspaceID, "profiles", profileID, "revisions", revisionID,
			hex.EncodeToString(pathHash[:]) + "-" + contentHash[:16],
		}, "/")
		files = append(files, ManifestFile{
			Path: cleanPath, ObjectKey: objectKey, CiphertextSHA256: contentHash,
			SizeBytes: input.SizeBytes, ContentType: contentType,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	deletedPaths := make([]string, 0, len(deletedInputs))
	for _, deletedInput := range deletedInputs {
		cleanPath, err := normalizeProfilePath(deletedInput)
		if err != nil {
			return Manifest{}, nil, "", err
		}
		if _, exists := seen[cleanPath]; exists {
			return Manifest{}, nil, "", errors.New("profile manifest changes the same path more than once")
		}
		seen[cleanPath] = struct{}{}
		deletedPaths = append(deletedPaths, cleanPath)
	}
	sort.Strings(deletedPaths)
	manifest := Manifest{
		RevisionID: revisionID, SchemaVersion: manifestSchemaVersion, Mode: mode,
		FileCount: len(files), TotalBytes: totalBytes, Files: files,
		DeletedPaths: deletedPaths, CreatedAt: now,
	}
	canonical, err := json.Marshal(struct {
		SchemaVersion string         `json:"schemaVersion"`
		Mode          string         `json:"mode"`
		Files         []ManifestFile `json:"files"`
		DeletedPaths  []string       `json:"deletedPaths,omitempty"`
	}{SchemaVersion: manifest.SchemaVersion, Mode: manifest.Mode, Files: manifest.Files, DeletedPaths: manifest.DeletedPaths})
	if err != nil {
		return Manifest{}, nil, "", err
	}
	digest := sha256.Sum256(canonical)
	objects := make([]Object, 0, len(files))
	for _, file := range files {
		objects = append(objects, Object{
			ID: uuid.NewString(), WorkspaceID: workspaceID, RevisionID: revisionID,
			ObjectKey: file.ObjectKey, ContentHash: file.CiphertextSHA256, SizeBytes: file.SizeBytes,
			StorageBackend: s.storageBackend, Encrypted: true, EncryptionKeyRef: s.encryptionKeyRef,
			ContentType: file.ContentType, CreatedAt: now,
		})
	}
	return manifest, objects, hex.EncodeToString(digest[:]), nil
}

func normalizeProfilePath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) {
		return "", errors.New("profile manifest path is invalid")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("profile manifest path is invalid")
	}
	return cleaned, nil
}

func tokenHash(raw string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(digest[:])
}

type MetadataVerifier struct{}

func (MetadataVerifier) Verify(_ context.Context, object Object) error {
	if object.ObjectKey == "" || object.SizeBytes < 0 || len(object.ContentHash) != 64 || !object.Encrypted {
		return ErrObjectUnavailable
	}
	return nil
}

func (MetadataVerifier) PresignUpload(_ context.Context, object Object, _ time.Duration) (string, error) {
	if object.ObjectKey == "" {
		return "", ErrObjectUnavailable
	}
	return "memory://profile-objects/" + object.ID, nil
}

func (MetadataVerifier) PresignDownload(_ context.Context, object Object, _ time.Duration) (string, error) {
	if object.ObjectKey == "" {
		return "", ErrObjectUnavailable
	}
	return "memory://profile-objects/" + object.ID, nil
}
