package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
)

const cloudProfileColumns = `
	p.id::text, p.workspace_id::text, COALESCE(p.owner_user_id::text, ''),
	p.name, COALESCE(p.fingerprint_template_id::text, ''),
	COALESCE(p.current_revision_id::text, ''), p.status, p.version,
	p.created_at, p.updated_at, p.deleted_at`

const profileRevisionColumns = `
	r.id::text, r.workspace_id::text, r.profile_id::text, r.revision,
	COALESCE(r.base_revision_id::text, ''), r.content_hash, r.status,
	COALESCE(r.device_id::text, ''), COALESCE(r.created_by::text, ''),
	r.created_at, r.committed_at`

const profileConflictColumns = `
	c.id::text, c.workspace_id::text, c.profile_id::text,
	c.local_revision_id::text, c.remote_revision_id::text,
	c.status, c.resolution, COALESCE(c.resolved_by::text, ''),
	c.created_at, c.resolved_at`

const profileObjectColumns = `
	o.id::text, o.workspace_id::text, o.revision_id::text, o.object_key,
	o.content_hash, o.size_bytes, o.storage_backend, o.encrypted,
	o.encryption_key_ref, o.content_type, o.created_at`

func (s *Store) CreateCloudProfile(ctx context.Context, profile profilesyncservice.Profile) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO browser_profiles (
			id, workspace_id, owner_user_id, name, fingerprint_template_id,
			current_revision_id, status, version, created_at, updated_at, deleted_at
		) VALUES (
			$1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, '')::uuid,
			NULLIF($6, '')::uuid, $7, $8, $9, $10, $11
		)
	`, profile.ID, profile.WorkspaceID, profile.OwnerUserID, profile.Name,
		profile.FingerprintTemplateID, profile.CurrentRevisionID, profile.Status,
		profile.Version, profile.CreatedAt, profile.UpdatedAt, profile.DeletedAt)
	if isUniqueViolationOn(err, profileNameIndex) {
		return profilesyncservice.ErrNameConflict
	}
	if isForeignKeyViolation(err) {
		return profilesyncservice.ErrNotFound
	}
	return err
}

func (s *Store) FindCloudProfile(ctx context.Context, workspaceID, profileID string) (profilesyncservice.Profile, error) {
	return scanCloudProfile(s.pool.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
	`, workspaceID, profileID))
}

func (s *Store) ListCloudProfiles(ctx context.Context, workspaceID string) ([]profilesyncservice.Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.deleted_at IS NULL
		ORDER BY p.created_at, p.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]profilesyncservice.Profile, 0)
	for rows.Next() {
		profile, scanErr := scanCloudProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, profile)
	}
	return items, rows.Err()
}

func (s *Store) AcquireProfileLease(ctx context.Context, lease profilesyncservice.Lease, hash string, reclaimOwn bool, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if _, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, lease.WorkspaceID, lease.ProfileID)); err != nil {
		return err
	}
	if open, err := openProfileConflict(ctx, tx, lease.WorkspaceID, lease.ProfileID); err != nil {
		return err
	} else if open {
		return profilesyncservice.ErrConflictUnresolved
	}
	if reclaimOwn {
		// The device lost its token (for example it crashed mid-sync). Any
		// operation still using the old token fails its lease check, so
		// replacing the lease cannot commit a half-finished transfer.
		if _, err := tx.Exec(ctx, `
			UPDATE profile_sync_leases
			SET released_at = $4
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND holder_device_id = $3::uuid AND released_at IS NULL
		`, lease.WorkspaceID, lease.ProfileID, lease.HolderDeviceID, now); err != nil {
			return err
		}
	}
	var deviceExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM devices
			WHERE workspace_id = $1::uuid AND id = $2::uuid AND revoked_at IS NULL
		)
	`, lease.WorkspaceID, lease.HolderDeviceID).Scan(&deviceExists); err != nil {
		return err
	}
	if !deviceExists {
		return profilesyncservice.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profile_sync_leases
		SET released_at = $3
		WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
		  AND released_at IS NULL AND expires_at <= $3
	`, lease.WorkspaceID, lease.ProfileID, now); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_sync_leases
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND released_at IS NULL AND expires_at > $3
		)
	`, lease.WorkspaceID, lease.ProfileID, now).Scan(&active); err != nil {
		return err
	}
	if active {
		return profilesyncservice.ErrLeaseHeld
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO profile_sync_leases (
			id, workspace_id, profile_id, holder_device_id, lease_token_hash,
			acquired_at, renewed_at, expires_at, released_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, decode($5, 'hex'), $6, $7, $8, $9)
	`, lease.ID, lease.WorkspaceID, lease.ProfileID, lease.HolderDeviceID,
		hash, lease.AcquiredAt, lease.RenewedAt, lease.ExpiresAt, lease.ReleasedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return profilesyncservice.ErrLeaseHeld
		}
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE browser_profiles
		SET status = 'syncing', updated_at = $3, version = version + 1
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, lease.WorkspaceID, lease.ProfileID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ValidateProfileLease(ctx context.Context, workspaceID, profileID, deviceID, hash string, now time.Time) error {
	if _, err := s.FindCloudProfile(ctx, workspaceID, profileID); err != nil {
		return err
	}
	var valid bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_sync_leases
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND holder_device_id = $3::uuid
			  AND lease_token_hash = decode($4, 'hex')
			  AND released_at IS NULL AND expires_at > $5
		)
	`, workspaceID, profileID, deviceID, hash, now).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return profilesyncservice.ErrLeaseInvalid
	}
	return nil
}

func (s *Store) RenewProfileLease(ctx context.Context, workspaceID, profileID, deviceID, hash string, now, expiresAt time.Time) (profilesyncservice.Lease, error) {
	return scanProfileLease(s.pool.QueryRow(ctx, `
		UPDATE profile_sync_leases AS l
		SET renewed_at = $6, expires_at = $7
		WHERE l.workspace_id = $1::uuid AND l.profile_id = $2::uuid
		  AND l.holder_device_id = $3::uuid
		  AND l.lease_token_hash = decode($4, 'hex')
		  AND l.released_at IS NULL AND l.expires_at > $5
		RETURNING l.id::text, l.workspace_id::text, l.profile_id::text,
		          l.holder_device_id::text, l.acquired_at, l.renewed_at,
		          l.expires_at, l.released_at
	`, workspaceID, profileID, deviceID, hash, now, now, expiresAt))
}

func (s *Store) ReleaseProfileLease(ctx context.Context, workspaceID, profileID, deviceID, hash string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	tag, err := tx.Exec(ctx, `
		UPDATE profile_sync_leases
		SET released_at = $6
		WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
		  AND holder_device_id = $3::uuid AND lease_token_hash = decode($4, 'hex')
		  AND released_at IS NULL AND expires_at > $5
	`, workspaceID, profileID, deviceID, hash, now, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return profilesyncservice.ErrLeaseInvalid
	}
	_, err = tx.Exec(ctx, `
		UPDATE browser_profiles
		SET status = CASE WHEN status = 'conflict' THEN status ELSE 'active' END,
		    updated_at = $3, version = version + 1
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, profileID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginProfileRevision(
	ctx context.Context,
	revision profilesyncservice.Revision,
	manifest profilesyncservice.Manifest,
	objects []profilesyncservice.Object,
	leaseHash, conflictID string,
	now time.Time,
) (profilesyncservice.Revision, profilesyncservice.Profile, *profilesyncservice.Conflict, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: revision.WorkspaceID})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	defer rollback(ctx, tx)
	profile, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, revision.WorkspaceID, revision.ProfileID))
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	if err := requireProfileLease(ctx, tx, revision.WorkspaceID, revision.ProfileID, revision.DeviceID, leaseHash, now); err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	if open, err := openProfileConflict(ctx, tx, revision.WorkspaceID, revision.ProfileID); err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	} else if open {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrConflictUnresolved
	}
	if profile.CurrentRevisionID == "" && revision.BaseRevisionID != "" {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrRevisionConflict
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision), 0) + 1
		FROM profile_revisions
		WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
	`, revision.WorkspaceID, revision.ProfileID).Scan(&revision.Revision); err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	contentHash, err := hex.DecodeString(revision.ContentHash)
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO profile_revisions (
			id, workspace_id, profile_id, revision, base_revision_id,
			content_hash, status, device_id, created_by, created_at, committed_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, NULLIF($5, '')::uuid,
			$6, $7, NULLIF($8, '')::uuid, NULLIF($9, '')::uuid, $10, $11
		)
	`, revision.ID, revision.WorkspaceID, revision.ProfileID, revision.Revision,
		revision.BaseRevisionID, contentHash, revision.Status, revision.DeviceID,
		revision.CreatedBy, revision.CreatedAt, revision.CommittedAt)
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO profile_manifests (
			revision_id, schema_version, file_count, total_bytes, manifest_json, created_at
		) VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6)
	`, manifest.RevisionID, manifest.SchemaVersion, manifest.FileCount,
		manifest.TotalBytes, manifestJSON, manifest.CreatedAt)
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	if err := insertProfileObjects(ctx, tx, objects); err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	var conflict *profilesyncservice.Conflict
	status := "syncing"
	if profile.CurrentRevisionID != revision.BaseRevisionID {
		value := profilesyncservice.Conflict{
			ID: conflictID, WorkspaceID: revision.WorkspaceID, ProfileID: revision.ProfileID,
			LocalRevisionID: revision.ID, RemoteRevisionID: profile.CurrentRevisionID,
			Status: "open", CreatedAt: now,
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO profile_conflicts (
				id, workspace_id, profile_id, local_revision_id, remote_revision_id,
				status, resolution, resolved_by, created_at, resolved_at
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, NULL, $8, NULL)
		`, value.ID, value.WorkspaceID, value.ProfileID, value.LocalRevisionID,
			value.RemoteRevisionID, value.Status, value.Resolution, value.CreatedAt)
		if err != nil {
			return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
		}
		conflict = &value
		status = "conflict"
	}
	profile, err = scanCloudProfile(tx.QueryRow(ctx, `
		UPDATE browser_profiles AS p
		SET status = $3, updated_at = $4, version = p.version + 1
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid
		RETURNING `+cloudProfileColumns+`
	`, revision.WorkspaceID, revision.ProfileID, status, now))
	if err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, err
	}
	return revision, profile, conflict, nil
}

func (s *Store) LoadProfileRevisionPlan(ctx context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash string, now time.Time) (profilesyncservice.RevisionPlan, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	defer rollback(ctx, tx)
	if err := requireProfileLease(ctx, tx, workspaceID, profileID, deviceID, leaseHash, now); err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	revision, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid
		  AND r.id = $3::uuid AND r.status = 'uploading'
	`, workspaceID, profileID, revisionID))
	if err != nil {
		if errors.Is(err, profilesyncservice.ErrNotFound) {
			return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrRevisionState
		}
		return profilesyncservice.RevisionPlan{}, err
	}
	manifest, err := scanProfileManifest(tx.QueryRow(ctx, `
		SELECT manifest_json FROM profile_manifests WHERE revision_id = $1::uuid
	`, revisionID))
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	objects, err := listProfileObjects(ctx, tx, workspaceID, revisionID)
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	return profilesyncservice.RevisionPlan{Revision: revision, Manifest: manifest, Objects: objects}, nil
}

func (s *Store) LoadProfileRevisionSnapshot(ctx context.Context, workspaceID, profileID, revisionID string) (profilesyncservice.RevisionPlan, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	defer rollback(ctx, tx)
	if _, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
	`, workspaceID, profileID)); err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	revision, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid
		  AND r.id = $3::uuid AND r.status IN ('committed', 'superseded')
	`, workspaceID, profileID, revisionID))
	if err != nil {
		if errors.Is(err, profilesyncservice.ErrNotFound) {
			return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrRevisionState
		}
		return profilesyncservice.RevisionPlan{}, err
	}
	manifest, err := scanProfileManifest(tx.QueryRow(ctx, `
		SELECT manifest_json FROM profile_manifests WHERE revision_id = $1::uuid
	`, revisionID))
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	objects, err := listProfileObjects(ctx, tx, workspaceID, revisionID)
	if err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.RevisionPlan{}, err
	}
	return profilesyncservice.RevisionPlan{Revision: revision, Manifest: manifest, Objects: objects}, nil
}

// withProfileOrganizationScope adds the workspace's organization to the
// tenant scope. The storage entitlement and the profile storage ledger are
// organization-scoped under RLS, while profile routes carry only a workspace
// scope; without the organization the quota check would see no entitlement
// and fail closed for every commit.
func (s *Store) withProfileOrganizationScope(ctx context.Context, workspaceID string) (context.Context, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	var organizationID string
	if err := s.pool.QueryRow(ctx, `
		SELECT organization_id::text FROM workspaces WHERE id = $1::uuid
	`, workspaceID).Scan(&organizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ctx, profilesyncservice.ErrNotFound
		}
		return ctx, err
	}
	return WithTenantScope(ctx, TenantScope{OrganizationID: organizationID}), nil
}

func (s *Store) CommitProfileRevision(ctx context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash string, now time.Time) (profilesyncservice.Profile, profilesyncservice.Revision, error) {
	ctx, err := s.withProfileOrganizationScope(ctx, workspaceID)
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	defer rollback(ctx, tx)
	profile, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, workspaceID, profileID))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if err := requireProfileLease(ctx, tx, workspaceID, profileID, deviceID, leaseHash, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	revision, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid
		  AND r.id = $3::uuid
		FOR UPDATE OF r
	`, workspaceID, profileID, revisionID))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if revision.Status != "uploading" {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	var openConflict bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_conflicts
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND local_revision_id = $3::uuid AND status = 'open'
		)
	`, workspaceID, profileID, revisionID).Scan(&openConflict); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if openConflict {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionConflict
	}
	if revision.BaseRevisionID != profile.CurrentRevisionID {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	if err := applyProfileStorageQuota(ctx, tx, profile, revision, revisionID, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if profile.CurrentRevisionID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE profile_revisions SET status = 'superseded'
			WHERE workspace_id = $1::uuid AND id = $2::uuid AND status = 'committed'
		`, workspaceID, profile.CurrentRevisionID); err != nil {
			return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
		}
	}
	revision, err = scanProfileRevision(tx.QueryRow(ctx, `
		UPDATE profile_revisions AS r
		SET status = 'committed', committed_at = $4
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid AND r.id = $3::uuid
		RETURNING `+profileRevisionColumns+`
	`, workspaceID, profileID, revisionID, now))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	profile, err = scanCloudProfile(tx.QueryRow(ctx, `
		UPDATE browser_profiles AS p
		SET current_revision_id = $3::uuid, status = 'active',
		    updated_at = $4, version = p.version + 1
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid
		RETURNING `+cloudProfileColumns+`
	`, workspaceID, profileID, revisionID, now))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE profile_sync_leases
		SET released_at = $6
		WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
		  AND holder_device_id = $3::uuid AND lease_token_hash = decode($4, 'hex')
		  AND released_at IS NULL AND expires_at > $5
	`, workspaceID, profileID, deviceID, leaseHash, now, now)
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if tag.RowsAffected() != 1 {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrLeaseInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	return profile, revision, nil
}

func (s *Store) RestoreProfileRevision(ctx context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash, actorID string, now time.Time) (profilesyncservice.Profile, profilesyncservice.Revision, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	defer rollback(ctx, tx)
	profile, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, workspaceID, profileID))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if err := requireProfileLease(ctx, tx, workspaceID, profileID, deviceID, leaseHash, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if profile.CurrentRevisionID == revisionID {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	var openConflict bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_conflicts
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid AND status = 'open'
		)
	`, workspaceID, profileID).Scan(&openConflict); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if openConflict {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionConflict
	}
	revision, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid
		  AND r.id = $3::uuid AND r.status IN ('committed', 'superseded')
		FOR UPDATE OF r
	`, workspaceID, profileID, revisionID))
	if err != nil {
		if errors.Is(err, profilesyncservice.ErrNotFound) {
			return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
		}
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if profile.CurrentRevisionID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE profile_revisions SET status = 'superseded'
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND id = $3::uuid AND status = 'committed'
		`, workspaceID, profileID, profile.CurrentRevisionID); err != nil {
			return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
		}
	}
	revision, err = scanProfileRevision(tx.QueryRow(ctx, `
		UPDATE profile_revisions AS r SET status = 'committed'
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid AND r.id = $3::uuid
		RETURNING `+profileRevisionColumns+`
	`, workspaceID, profileID, revisionID))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	profile, err = scanCloudProfile(tx.QueryRow(ctx, `
		UPDATE browser_profiles AS p
		SET current_revision_id = $3::uuid, status = 'active',
		    updated_at = $4, version = p.version + 1
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid
		RETURNING `+cloudProfileColumns+`
	`, workspaceID, profileID, revisionID, now))
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	leaseTag, err := tx.Exec(ctx, `
		UPDATE profile_sync_leases SET released_at = $6
		WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
		  AND holder_device_id = $3::uuid AND lease_token_hash = decode($4, 'hex')
		  AND released_at IS NULL AND expires_at > $5
	`, workspaceID, profileID, deviceID, leaseHash, now, now)
	if err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if leaseTag.RowsAffected() != 1 {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrLeaseInvalid
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO profile_restore_events (
			workspace_id, profile_id, revision_id, device_id, outcome, created_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'succeeded', $5)
	`, workspaceID, profileID, revisionID, deviceID, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			workspace_id, actor_user_id, actor_device_id, action,
			resource_type, resource_id, outcome, metadata, created_at
		) VALUES (
			$1::uuid, NULLIF($2, '')::uuid, $3::uuid, 'profile.revision.restore',
			'browser_profile', $4::uuid, 'success',
			jsonb_build_object('revisionId', $5::text), $6
		)
	`, workspaceID, actorID, deviceID, profileID, revisionID, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	return profile, revision, nil
}

func (s *Store) ListProfileRevisions(ctx context.Context, workspaceID, profileID string) ([]profilesyncservice.Revision, error) {
	if _, err := s.FindCloudProfile(ctx, workspaceID, profileID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid
		ORDER BY r.revision DESC
	`, workspaceID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]profilesyncservice.Revision, 0)
	for rows.Next() {
		revision, scanErr := scanProfileRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, revision)
	}
	return items, rows.Err()
}

func (s *Store) ListProfileConflicts(ctx context.Context, workspaceID, profileID string) ([]profilesyncservice.Conflict, error) {
	if _, err := s.FindCloudProfile(ctx, workspaceID, profileID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+profileConflictColumns+` FROM profile_conflicts c
		WHERE c.workspace_id = $1::uuid AND c.profile_id = $2::uuid
		ORDER BY c.created_at DESC, c.id DESC
	`, workspaceID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]profilesyncservice.Conflict, 0)
	for rows.Next() {
		conflict, scanErr := scanProfileConflict(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, conflict)
	}
	return items, rows.Err()
}

func (s *Store) ResolveProfileConflict(ctx context.Context, workspaceID, profileID, conflictID, resolution, actorID string, now time.Time) (profilesyncservice.Conflict, error) {
	// keep_local charges the promoted snapshot to the organization ledger.
	ctx, err := s.withProfileOrganizationScope(ctx, workspaceID)
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	defer rollback(ctx, tx)
	profile, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
		FOR UPDATE OF p
	`, workspaceID, profileID))
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	conflict, err := scanProfileConflict(tx.QueryRow(ctx, `
		SELECT `+profileConflictColumns+` FROM profile_conflicts c
		WHERE c.workspace_id = $1::uuid AND c.profile_id = $2::uuid AND c.id = $3::uuid
		FOR UPDATE OF c
	`, workspaceID, profileID, conflictID))
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	if conflict.Status != "open" {
		return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
	}
	local, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid AND r.id = $3::uuid
		FOR UPDATE OF r
	`, workspaceID, profileID, conflict.LocalRevisionID))
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	switch resolution {
	case "keep_local":
		// The service verified the local snapshot's objects before this
		// transaction. Promotion also requires that nothing newer was
		// committed and that no device is mid-transfer under a lease.
		if local.Status != "uploading" || profile.CurrentRevisionID != conflict.RemoteRevisionID {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
		}
		manifest, err := scanProfileManifest(tx.QueryRow(ctx, `
			SELECT manifest_json FROM profile_manifests WHERE revision_id = $1::uuid
		`, local.ID))
		if err != nil {
			return profilesyncservice.Conflict{}, err
		}
		if manifest.Mode != "snapshot" {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
		}
		var leased bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM profile_sync_leases
				WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
				  AND released_at IS NULL AND expires_at > $3
			)
		`, workspaceID, profileID, now).Scan(&leased); err != nil {
			return profilesyncservice.Conflict{}, err
		}
		if leased {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrLeaseHeld
		}
		if err := applyProfileStorageQuota(ctx, tx, profile, local, local.ID, now); err != nil {
			return profilesyncservice.Conflict{}, err
		}
		if profile.CurrentRevisionID != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE profile_revisions SET status = 'superseded'
				WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
				  AND id = $3::uuid AND status = 'committed'
			`, workspaceID, profileID, profile.CurrentRevisionID); err != nil {
				return profilesyncservice.Conflict{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE profile_revisions SET status = 'committed', committed_at = $4
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid AND id = $3::uuid
		`, workspaceID, profileID, local.ID, now); err != nil {
			return profilesyncservice.Conflict{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE browser_profiles SET current_revision_id = $3::uuid
			WHERE workspace_id = $1::uuid AND id = $2::uuid
		`, workspaceID, profileID, local.ID); err != nil {
			return profilesyncservice.Conflict{}, err
		}
	case "keep_remote":
		// An interrupted upload may never have produced objects; discarding
		// the local revision is always safe because it was never current.
		if local.Status == "uploading" {
			if _, err := tx.Exec(ctx, `
				UPDATE profile_revisions SET status = 'superseded'
				WHERE workspace_id = $1::uuid AND profile_id = $2::uuid AND id = $3::uuid
			`, workspaceID, profileID, local.ID); err != nil {
				return profilesyncservice.Conflict{}, err
			}
		}
	default:
		return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
	}
	conflict, err = scanProfileConflict(tx.QueryRow(ctx, `
		UPDATE profile_conflicts AS c
		SET status = 'resolved', resolution = $4,
		    resolved_by = NULLIF($5, '')::uuid, resolved_at = $6
		WHERE c.workspace_id = $1::uuid AND c.profile_id = $2::uuid AND c.id = $3::uuid
		RETURNING `+profileConflictColumns+`
	`, workspaceID, profileID, conflictID, resolution, actorID, now))
	if err != nil {
		return profilesyncservice.Conflict{}, err
	}
	// Legacy data may hold several open conflicts; the profile stays blocked
	// until every one of them is resolved.
	if _, err := tx.Exec(ctx, `
		UPDATE browser_profiles AS p
		SET status = CASE WHEN EXISTS (
		        SELECT 1 FROM profile_conflicts c
		        WHERE c.workspace_id = p.workspace_id AND c.profile_id = p.id AND c.status = 'open'
		    ) THEN 'conflict' ELSE 'active' END,
		    updated_at = $3, version = p.version + 1
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid
	`, workspaceID, profileID, now); err != nil {
		return profilesyncservice.Conflict{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			workspace_id, actor_user_id, action, resource_type, resource_id, outcome, metadata, created_at
		) VALUES (
			$1::uuid, NULLIF($2, '')::uuid, 'profile.conflict.resolve', 'browser_profile', $3::uuid, 'success',
			jsonb_build_object('conflictId', $4::text, 'resolution', $5::text,
			                   'localRevisionId', $6::text, 'remoteRevisionId', $7::text), $8
		)
	`, workspaceID, actorID, profileID, conflict.ID, resolution,
		conflict.LocalRevisionID, conflict.RemoteRevisionID, now); err != nil {
		return profilesyncservice.Conflict{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.Conflict{}, err
	}
	return conflict, nil
}

func (s *Store) LoadProfileConflictPlan(ctx context.Context, workspaceID, profileID, conflictID string) (profilesyncservice.Conflict, profilesyncservice.RevisionPlan, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	defer rollback(ctx, tx)
	if _, err := scanCloudProfile(tx.QueryRow(ctx, `
		SELECT `+cloudProfileColumns+` FROM browser_profiles p
		WHERE p.workspace_id = $1::uuid AND p.id = $2::uuid AND p.deleted_at IS NULL
	`, workspaceID, profileID)); err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	conflict, err := scanProfileConflict(tx.QueryRow(ctx, `
		SELECT `+profileConflictColumns+` FROM profile_conflicts c
		WHERE c.workspace_id = $1::uuid AND c.profile_id = $2::uuid AND c.id = $3::uuid
	`, workspaceID, profileID, conflictID))
	if err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	revision, err := scanProfileRevision(tx.QueryRow(ctx, `
		SELECT `+profileRevisionColumns+` FROM profile_revisions r
		WHERE r.workspace_id = $1::uuid AND r.profile_id = $2::uuid AND r.id = $3::uuid
	`, workspaceID, profileID, conflict.LocalRevisionID))
	if err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	manifest, err := scanProfileManifest(tx.QueryRow(ctx, `
		SELECT manifest_json FROM profile_manifests WHERE revision_id = $1::uuid
	`, revision.ID))
	if err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	objects, err := listProfileObjects(ctx, tx, workspaceID, revision.ID)
	if err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, err
	}
	return conflict, profilesyncservice.RevisionPlan{Revision: revision, Manifest: manifest, Objects: objects}, nil
}

// insertProfileObjects writes a revision's object rows in one statement.
// COPY is not usable here: PostgreSQL rejects COPY FROM on tables with row
// level security for non-bypass roles, and profile_objects is FORCE RLS, so
// the runtime role must go through INSERT and its WITH CHECK policy.
func insertProfileObjects(ctx context.Context, tx pgx.Tx, objects []profilesyncservice.Object) error {
	if len(objects) == 0 {
		return nil
	}
	var (
		ids, workspaces, revisions, keys, backends, keyRefs, contentTypes = make([]string, 0, len(objects)), make([]string, 0, len(objects)), make([]string, 0, len(objects)), make([]string, 0, len(objects)), make([]string, 0, len(objects)), make([]string, 0, len(objects)), make([]string, 0, len(objects))
		hashes                                                            = make([][]byte, 0, len(objects))
		sizes                                                             = make([]int64, 0, len(objects))
		encrypted                                                         = make([]bool, 0, len(objects))
		created                                                           = make([]time.Time, 0, len(objects))
	)
	for _, object := range objects {
		decodedHash, err := hex.DecodeString(object.ContentHash)
		if err != nil {
			return err
		}
		ids = append(ids, object.ID)
		workspaces = append(workspaces, object.WorkspaceID)
		revisions = append(revisions, object.RevisionID)
		keys = append(keys, object.ObjectKey)
		hashes = append(hashes, decodedHash)
		sizes = append(sizes, object.SizeBytes)
		backends = append(backends, object.StorageBackend)
		encrypted = append(encrypted, object.Encrypted)
		keyRefs = append(keyRefs, object.EncryptionKeyRef)
		contentTypes = append(contentTypes, object.ContentType)
		created = append(created, object.CreatedAt)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO profile_objects (
			id, workspace_id, revision_id, object_key, content_hash, size_bytes,
			storage_backend, encrypted, encryption_key_ref, content_type, created_at
		)
		SELECT o.id::uuid, o.workspace_id::uuid, o.revision_id::uuid, o.object_key, o.content_hash,
		       o.size_bytes, o.storage_backend, o.encrypted, o.encryption_key_ref, o.content_type, o.created_at
		FROM unnest(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::bytea[], $6::bigint[],
			$7::text[], $8::boolean[], $9::text[], $10::text[], $11::timestamptz[]
		) AS o(id, workspace_id, revision_id, object_key, content_hash, size_bytes,
		       storage_backend, encrypted, encryption_key_ref, content_type, created_at)
	`, ids, workspaces, revisions, keys, hashes, sizes, backends, encrypted, keyRefs, contentTypes, created)
	return err
}

// openProfileConflict reports whether the profile has any open conflict.
func openProfileConflict(ctx context.Context, tx pgx.Tx, workspaceID, profileID string) (bool, error) {
	var open bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_conflicts
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid AND status = 'open'
		)
	`, workspaceID, profileID).Scan(&open)
	return open, err
}

func requireProfileLease(ctx context.Context, tx pgx.Tx, workspaceID, profileID, deviceID, hash string, now time.Time) error {
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM profile_sync_leases
			WHERE workspace_id = $1::uuid AND profile_id = $2::uuid
			  AND holder_device_id = $3::uuid
			  AND lease_token_hash = decode($4, 'hex')
			  AND released_at IS NULL AND expires_at > $5
		)
	`, workspaceID, profileID, deviceID, hash, now).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return profilesyncservice.ErrLeaseInvalid
	}
	return nil
}

type profileStorageFileState struct {
	WorkspaceID string
	ObjectKey   string
	SizeBytes   int64
	RevisionID  string
}

// applyProfileStorageQuota performs the storage check and materializes the
// profile's current file set in the same serializable transaction as the
// revision promotion. The organization ledger row is locked, so commits from
// separate workspaces cannot oversubscribe one organization's entitlement.
func applyProfileStorageQuota(ctx context.Context, tx pgx.Tx, profile profilesyncservice.Profile, revision profilesyncservice.Revision, revisionID string, now time.Time) error {
	var organizationID string
	if err := tx.QueryRow(ctx, `
		SELECT organization_id::text FROM workspaces
		WHERE id = $1::uuid
	`, profile.WorkspaceID).Scan(&organizationID); err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	var limit *int64
	var enabled bool
	err := tx.QueryRow(ctx, `
		SELECT value_limit, feature_enabled
		FROM entitlements
		WHERE organization_id = $1::uuid AND entitlement_code = 'storage_bytes'
		  AND valid_from <= $2
		  AND (valid_until IS NULL OR valid_until > $2)
		FOR UPDATE
	`, organizationID, now).Scan(&limit, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || !enabled {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO profile_storage_usage (organization_id, used_bytes, version, updated_at)
		VALUES ($1::uuid, 0, 1, $2)
		ON CONFLICT (organization_id) DO NOTHING
	`, organizationID, now)
	if err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	var used int64
	if err := tx.QueryRow(ctx, `
		SELECT used_bytes FROM profile_storage_usage
		WHERE organization_id = $1::uuid FOR UPDATE
	`, organizationID).Scan(&used); err != nil || used < 0 {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	current := make(map[string]profileStorageFileState)
	rows, err := tx.Query(ctx, `
		SELECT path, workspace_id::text, object_key, size_bytes, revision_id::text
		FROM profile_storage_files
		WHERE organization_id = $1::uuid AND profile_id = $2::uuid
		FOR UPDATE
	`, organizationID, profile.ID)
	if err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	for rows.Next() {
		var path string
		var state profileStorageFileState
		if err := rows.Scan(&path, &state.WorkspaceID, &state.ObjectKey, &state.SizeBytes, &state.RevisionID); err != nil || state.SizeBytes < 0 {
			rows.Close()
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		current[path] = state
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	rows.Close()
	if profile.CurrentRevisionID != "" && len(current) == 0 {
		// A current revision without materialized files means the ledger was
		// not backfilled or has been corrupted. Charging from zero would fail
		// open, so reject the commit.
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	manifest, err := scanProfileManifest(tx.QueryRow(ctx, `
		SELECT manifest_json FROM profile_manifests WHERE revision_id = $1::uuid
	`, revisionID))
	if err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	next := make(map[string]profileStorageFileState, len(current)+len(manifest.Files))
	if manifest.Mode == "incremental" {
		if revision.BaseRevisionID == "" {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		for path, state := range current {
			next[path] = state
		}
	} else if manifest.Mode != "snapshot" {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	for _, deleted := range manifest.DeletedPaths {
		delete(next, deleted)
	}
	for _, file := range manifest.Files {
		if file.SizeBytes < 0 {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		next[file.Path] = profileStorageFileState{
			WorkspaceID: profile.WorkspaceID, ObjectKey: file.ObjectKey,
			SizeBytes: file.SizeBytes, RevisionID: revisionID,
		}
	}
	currentBytes, nextBytes, err := profileStorageBytes(current, next)
	if err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	delta := nextBytes - currentBytes
	if delta < 0 && -delta > used || delta > 0 && used > (int64(^uint64(0)>>1))-delta {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	result := used + delta
	if limit != nil && result > *limit {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profile_storage_usage
		SET used_bytes = $2, version = version + 1, updated_at = $3
		WHERE organization_id = $1::uuid
	`, organizationID, result, now); err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	if _, err := tx.Exec(ctx, `DELETE FROM profile_storage_files WHERE organization_id = $1::uuid AND profile_id = $2::uuid`, organizationID, profile.ID); err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	for path, state := range next {
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_storage_files (
				organization_id, workspace_id, profile_id, path, object_key,
				size_bytes, revision_id, updated_at
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::uuid, $8)
		`, organizationID, state.WorkspaceID, profile.ID, path, state.ObjectKey,
			state.SizeBytes, state.RevisionID, now); err != nil {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
	}
	// Keep the billing read model aligned with the authoritative ledger.
	if _, err := tx.Exec(ctx, `
		UPDATE entitlements SET consumed_value = $2
		WHERE organization_id = $1::uuid AND entitlement_code = 'storage_bytes'
	`, organizationID, result); err != nil {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	return nil
}

func profileStorageBytes(current, next map[string]profileStorageFileState) (int64, int64, error) {
	var currentBytes, nextBytes int64
	for _, state := range current {
		if state.SizeBytes < 0 || currentBytes > (int64(^uint64(0)>>1))-state.SizeBytes {
			return 0, 0, errors.New("profile storage byte overflow")
		}
		currentBytes += state.SizeBytes
	}
	for _, state := range next {
		if state.SizeBytes < 0 || nextBytes > (int64(^uint64(0)>>1))-state.SizeBytes {
			return 0, 0, errors.New("profile storage byte overflow")
		}
		nextBytes += state.SizeBytes
	}
	return currentBytes, nextBytes, nil
}

func listProfileObjects(ctx context.Context, tx pgx.Tx, workspaceID, revisionID string) ([]profilesyncservice.Object, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+profileObjectColumns+` FROM profile_objects o
		WHERE o.workspace_id = $1::uuid AND o.revision_id = $2::uuid
		ORDER BY o.object_key
	`, workspaceID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]profilesyncservice.Object, 0)
	for rows.Next() {
		object, scanErr := scanProfileObject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, object)
	}
	return items, rows.Err()
}

func scanCloudProfile(row scanner) (profilesyncservice.Profile, error) {
	var profile profilesyncservice.Profile
	if err := row.Scan(
		&profile.ID, &profile.WorkspaceID, &profile.OwnerUserID, &profile.Name,
		&profile.FingerprintTemplateID, &profile.CurrentRevisionID, &profile.Status,
		&profile.Version, &profile.CreatedAt, &profile.UpdatedAt, &profile.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Profile{}, profilesyncservice.ErrNotFound
		}
		return profilesyncservice.Profile{}, err
	}
	return profile, nil
}

func scanProfileRevision(row scanner) (profilesyncservice.Revision, error) {
	var revision profilesyncservice.Revision
	var contentHash []byte
	if err := row.Scan(
		&revision.ID, &revision.WorkspaceID, &revision.ProfileID, &revision.Revision,
		&revision.BaseRevisionID, &contentHash, &revision.Status, &revision.DeviceID,
		&revision.CreatedBy, &revision.CreatedAt, &revision.CommittedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Revision{}, profilesyncservice.ErrNotFound
		}
		return profilesyncservice.Revision{}, err
	}
	revision.ContentHash = hex.EncodeToString(contentHash)
	return revision, nil
}

func scanProfileManifest(row scanner) (profilesyncservice.Manifest, error) {
	var encoded []byte
	if err := row.Scan(&encoded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Manifest{}, profilesyncservice.ErrNotFound
		}
		return profilesyncservice.Manifest{}, err
	}
	var manifest profilesyncservice.Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return profilesyncservice.Manifest{}, err
	}
	return manifest, nil
}

func scanProfileObject(row scanner) (profilesyncservice.Object, error) {
	var object profilesyncservice.Object
	var contentHash []byte
	if err := row.Scan(
		&object.ID, &object.WorkspaceID, &object.RevisionID, &object.ObjectKey,
		&contentHash, &object.SizeBytes, &object.StorageBackend, &object.Encrypted,
		&object.EncryptionKeyRef, &object.ContentType, &object.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Object{}, profilesyncservice.ErrNotFound
		}
		return profilesyncservice.Object{}, err
	}
	object.ContentHash = hex.EncodeToString(contentHash)
	return object, nil
}

func scanProfileLease(row scanner) (profilesyncservice.Lease, error) {
	var lease profilesyncservice.Lease
	if err := row.Scan(
		&lease.ID, &lease.WorkspaceID, &lease.ProfileID, &lease.HolderDeviceID,
		&lease.AcquiredAt, &lease.RenewedAt, &lease.ExpiresAt, &lease.ReleasedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Lease{}, profilesyncservice.ErrLeaseInvalid
		}
		return profilesyncservice.Lease{}, err
	}
	return lease, nil
}

func scanProfileConflict(row scanner) (profilesyncservice.Conflict, error) {
	var conflict profilesyncservice.Conflict
	if err := row.Scan(
		&conflict.ID, &conflict.WorkspaceID, &conflict.ProfileID,
		&conflict.LocalRevisionID, &conflict.RemoteRevisionID, &conflict.Status,
		&conflict.Resolution, &conflict.ResolvedBy, &conflict.CreatedAt, &conflict.ResolvedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrNotFound
		}
		return profilesyncservice.Conflict{}, err
	}
	return conflict, nil
}
