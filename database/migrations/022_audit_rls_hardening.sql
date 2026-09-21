-- v022: audit rows carry sensitive cross-tenant activity and must not remain
-- the one analytics surface outside FORCE ROW LEVEL SECURITY.

ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS audit_events_tenant_policy ON audit_events;
CREATE POLICY audit_events_tenant_policy ON audit_events
    USING (
        workspace_id = cloud_current_workspace_id()
        OR organization_id = cloud_current_organization_id()
        OR (
            current_user = 'ant_control_plane'
            AND current_setting('app.system_operation', true) = 'admin_read'
            AND EXISTS (
                SELECT 1
                FROM platform_admins pa
                JOIN users u ON u.id = pa.user_id
                WHERE pa.user_id = cloud_current_user_id()
                  AND pa.role = 'platform_admin'
                  AND pa.status = 'active'
                  AND u.status = 'active'
                  AND u.deleted_at IS NULL
            )
        )
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR organization_id = cloud_current_organization_id()
        OR (
            current_user = 'ant_control_plane'
            AND current_setting('app.system_operation', true) = 'admin_audit'
            AND actor_user_id = cloud_current_user_id()
        )
    );

COMMENT ON POLICY audit_events_tenant_policy ON audit_events IS
    'Workspace/org reads are tenant scoped; platform reads require an active admin; admin audit writes are actor-bound.';
