-- v011: commercial fingerprint templates and additional profile-sync state.

CREATE TABLE fingerprint_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 255),
    mode TEXT NOT NULL CHECK (mode IN ('seeded', 'fixed', 'custom')),
    browser_family TEXT NOT NULL DEFAULT 'chromium',
    browser_major INTEGER NOT NULL CHECK (browser_major > 0),
    platform TEXT NOT NULL CHECK (platform IN ('windows', 'linux', 'macos')),
    seed BIGINT NOT NULL CHECK (seed > 0),
    locale TEXT NOT NULL,
    timezone TEXT NOT NULL,
    runtime_args JSONB NOT NULL DEFAULT '[]'::jsonb,
    configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

ALTER TABLE browser_profiles ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'syncing', 'conflict', 'archived', 'deleted'));
ALTER TABLE profile_revisions ADD COLUMN device_id UUID;
ALTER TABLE profile_revisions ADD COLUMN committed_at TIMESTAMPTZ;
ALTER TABLE profile_sync_leases ADD COLUMN renewed_at TIMESTAMPTZ;

ALTER TABLE browser_profiles
    ADD CONSTRAINT browser_profiles_fingerprint_tenant_fk
    FOREIGN KEY (workspace_id, fingerprint_template_id)
    REFERENCES fingerprint_templates(workspace_id, id);
ALTER TABLE browser_instances
    ADD CONSTRAINT browser_instances_fingerprint_tenant_fk
    FOREIGN KEY (workspace_id, fingerprint_template_id)
    REFERENCES fingerprint_templates(workspace_id, id);
ALTER TABLE profile_revisions
    ADD CONSTRAINT profile_revisions_device_tenant_fk
    FOREIGN KEY (workspace_id, device_id)
    REFERENCES devices(workspace_id, id);

CREATE INDEX fingerprint_templates_workspace_idx
    ON fingerprint_templates (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX profile_sync_leases_expiry_idx
    ON profile_sync_leases (expires_at)
    WHERE released_at IS NULL;

CREATE TRIGGER fingerprint_templates_set_updated_at BEFORE UPDATE ON fingerprint_templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE fingerprint_templates ENABLE ROW LEVEL SECURITY;
CREATE POLICY fingerprint_templates_tenant_policy ON fingerprint_templates
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
