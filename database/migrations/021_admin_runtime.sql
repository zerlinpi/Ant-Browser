-- v021: platform-admin control plane. This role is intentionally separate
-- from organization owners and workspace members.
-- There is deliberately no automatic seed from an owner row. The first
-- administrator must be inserted by a reviewed, one-time bootstrap job using
-- the migration connection; subsequent grants go through admin-service.

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_status_check;
ALTER TABLE organizations ADD CONSTRAINT organizations_status_check
    CHECK (status IN ('active', 'suspended', 'deleted'));
CREATE INDEX IF NOT EXISTS organizations_status_idx ON organizations (status, created_at DESC);

-- Tenant RLS predicates must stop resolving suspended organizations. This
-- keeps resource tables fail-closed without adding a status join to every
-- existing policy.
CREATE OR REPLACE FUNCTION cloud_current_workspace_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
    SELECT w.id
    FROM workspaces w
    JOIN organizations o ON o.id = w.organization_id
    WHERE w.id = NULLIF(current_setting('app.current_workspace_id', true), '')::uuid
      AND w.status = 'active' AND o.status = 'active';
$$;

CREATE OR REPLACE FUNCTION cloud_current_organization_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
    SELECT o.id
    FROM organizations o
    WHERE o.id = NULLIF(current_setting('app.current_organization_id', true), '')::uuid
      AND o.status = 'active';
$$;

CREATE TABLE IF NOT EXISTS platform_admins (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    role TEXT NOT NULL DEFAULT 'platform_admin'
        CHECK (role = 'platform_admin'),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'revoked')),
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS platform_admins_active_idx
    ON platform_admins (status) WHERE status = 'active';

GRANT SELECT, INSERT, UPDATE ON platform_admins TO ant_control_plane;

CREATE TRIGGER platform_admins_set_updated_at BEFORE UPDATE ON platform_admins
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- This is the only broad cross-tenant table surface introduced by this
-- migration. The normal control-plane role must opt into one of these fixed
-- operation names through Store.WithTenant; callers cannot set it via HTTP.
ALTER TABLE platform_admins ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_admins FORCE ROW LEVEL SECURITY;
CREATE POLICY platform_admin_system_policy ON platform_admins
    USING (
        current_user = 'ant_control_plane'
        AND current_setting('app.system_operation', true) IN ('admin_read', 'admin_mutation', 'admin_audit')
    )
    WITH CHECK (
        current_user = 'ant_control_plane'
        AND current_setting('app.system_operation', true) IN ('admin_mutation', 'admin_audit')
    );

COMMENT ON TABLE platform_admins IS
    'Separate platform-admin authority; never inferred from organization/workspace RBAC.';
