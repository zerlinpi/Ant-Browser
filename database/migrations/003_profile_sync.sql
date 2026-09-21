-- v003: encrypted, immutable profile revisions and synchronization leases.

CREATE TABLE browser_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 255),
    fingerprint_template_id UUID,
    current_revision_id UUID,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE profile_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    profile_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    base_revision_id UUID,
    content_hash BYTEA NOT NULL,
    status TEXT NOT NULL DEFAULT 'committed'
        CHECK (status IN ('uploading', 'committed', 'superseded', 'corrupt')),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, profile_id, revision),
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, base_revision_id)
        REFERENCES profile_revisions(workspace_id, id)
);

CREATE TABLE profile_manifests (
    revision_id UUID PRIMARY KEY REFERENCES profile_revisions(id) ON DELETE CASCADE,
    schema_version TEXT NOT NULL,
    file_count INTEGER NOT NULL CHECK (file_count >= 0),
    total_bytes BIGINT NOT NULL CHECK (total_bytes >= 0),
    manifest_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE profile_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    object_key TEXT NOT NULL,
    content_hash BYTEA NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    storage_backend TEXT NOT NULL DEFAULT 's3',
    encrypted BOOLEAN NOT NULL DEFAULT true,
    encryption_key_ref TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, object_key),
    FOREIGN KEY (workspace_id, revision_id)
        REFERENCES profile_revisions(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE profile_sync_leases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    profile_id UUID NOT NULL,
    holder_device_id UUID NOT NULL,
    lease_token_hash BYTEA NOT NULL UNIQUE,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, holder_device_id)
        REFERENCES devices(workspace_id, id)
);

CREATE UNIQUE INDEX profile_sync_leases_active_uq
    ON profile_sync_leases (workspace_id, profile_id)
    WHERE released_at IS NULL;

CREATE TABLE profile_conflicts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    profile_id UUID NOT NULL,
    local_revision_id UUID NOT NULL,
    remote_revision_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'resolved', 'discarded')),
    resolution TEXT NOT NULL DEFAULT '',
    resolved_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, local_revision_id)
        REFERENCES profile_revisions(workspace_id, id),
    FOREIGN KEY (workspace_id, remote_revision_id)
        REFERENCES profile_revisions(workspace_id, id),
    CHECK (local_revision_id <> remote_revision_id)
);

CREATE TABLE profile_restore_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    profile_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    device_id UUID,
    outcome TEXT NOT NULL CHECK (outcome IN ('started', 'succeeded', 'failed')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, revision_id)
        REFERENCES profile_revisions(workspace_id, id),
    FOREIGN KEY (workspace_id, device_id)
        REFERENCES devices(workspace_id, id)
);

ALTER TABLE browser_profiles
    ADD CONSTRAINT browser_profiles_current_revision_fk
    FOREIGN KEY (workspace_id, current_revision_id)
    REFERENCES profile_revisions(workspace_id, id);

ALTER TABLE browser_instances
    ADD CONSTRAINT browser_instances_profile_tenant_fk
    FOREIGN KEY (workspace_id, profile_id)
    REFERENCES browser_profiles(workspace_id, id);

CREATE INDEX profile_revisions_history_idx
    ON profile_revisions (workspace_id, profile_id, revision DESC);
CREATE INDEX profile_conflicts_open_idx
    ON profile_conflicts (workspace_id, profile_id, created_at DESC)
    WHERE status = 'open';

CREATE TRIGGER browser_profiles_set_updated_at BEFORE UPDATE ON browser_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
