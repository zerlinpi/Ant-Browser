-- v026: authoritative profile storage ledger used by profile revision commits
-- and conflict promotion (server/platform/postgres/profile_sync.go,
-- applyProfileStorageQuota). The commit path already read and wrote these
-- tables, but no migration created them, so every PostgreSQL commit failed
-- closed with "profile storage quota exceeded". Because no commit could have
-- succeeded before this migration, there is no existing current file set to
-- backfill.
--
-- profile_storage_usage holds one row per organization: the bytes currently
-- referenced by every profile's current revision. profile_storage_files is the
-- materialized current file set per profile, so replacing a file charges only
-- the size delta. Both are organization-scoped like entitlements; commits set
-- the workspace's organization in the tenant scope before touching them.

CREATE TABLE profile_storage_usage (
    organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE profile_storage_files (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL,
    profile_id UUID NOT NULL,
    path TEXT NOT NULL CHECK (length(path) BETWEEN 1 AND 1024),
    object_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    revision_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, profile_id, path),
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, revision_id)
        REFERENCES profile_revisions(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX profile_storage_files_workspace_profile_idx
    ON profile_storage_files (workspace_id, profile_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON profile_storage_usage, profile_storage_files TO ant_control_plane;

ALTER TABLE profile_storage_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_storage_usage FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_storage_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_storage_files FORCE ROW LEVEL SECURITY;

CREATE POLICY profile_storage_usage_tenant_policy ON profile_storage_usage
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());

-- A file row must belong to both the organization in scope and a workspace of
-- that organization, so a forged workspace_id cannot be charged to another
-- tenant's ledger.
CREATE POLICY profile_storage_files_tenant_policy ON profile_storage_files
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (
        organization_id = cloud_current_organization_id()
        AND EXISTS (
            SELECT 1 FROM workspaces w
            WHERE w.id = workspace_id AND w.organization_id = profile_storage_files.organization_id
        )
    );
