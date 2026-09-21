-- v018: make RLS effective for runtime roles and give the worker only the
-- narrowly-scoped cross-workspace operation it needs to claim queue work.
-- The migration role/table owner is intentionally not a runtime role.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_control_plane') THEN
        CREATE ROLE ant_control_plane NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_worker') THEN
        CREATE ROLE ant_worker NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_device_authenticator') THEN
        -- This role cannot log in or be assumed by either runtime. It owns one
        -- narrowly-shaped SECURITY DEFINER function so device credentials can
        -- be verified before a workspace is known without a broad RLS bypass.
        CREATE ROLE ant_device_authenticator NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_control_plane, ant_worker, ant_device_authenticator;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ant_control_plane;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ant_control_plane;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ant_control_plane;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO ant_control_plane;

-- Worker access is limited to queue processing and device authentication. It
-- cannot read arbitrary tenant data, even when app.system_operation is set.
GRANT SELECT, INSERT, UPDATE, DELETE ON tasks, task_runs, task_attempts TO ant_worker;
GRANT SELECT ON browser_instances, devices, workflows, workflow_versions TO ant_worker;
GRANT SELECT, UPDATE ON schedules TO ant_worker;
GRANT SELECT, UPDATE ON proxies, proxy_health_checks TO ant_worker;
GRANT INSERT ON proxy_health_samples TO ant_worker;
GRANT SELECT ON secret_envelopes TO ant_worker;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ant_worker;

GRANT SELECT ON devices, device_credentials TO ant_device_authenticator;

CREATE OR REPLACE FUNCTION authenticate_device(p_device_id UUID, p_credential_hash TEXT)
RETURNS TABLE (
    id UUID, workspace_id UUID, user_id UUID, name TEXT, platform TEXT,
    agent_version TEXT, capabilities JSONB, status TEXT,
    last_seen_at TIMESTAMPTZ, created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT d.id, d.workspace_id, d.user_id, d.name, d.platform,
           d.agent_version, d.capabilities, d.status, d.last_seen_at,
           d.created_at, d.updated_at, d.revoked_at
    FROM public.devices d
    JOIN public.device_credentials dc ON dc.device_id = d.id
    WHERE d.id = p_device_id
      AND dc.credential_hash = decode(p_credential_hash, 'hex')
      AND d.revoked_at IS NULL AND dc.revoked_at IS NULL
      AND (dc.expires_at IS NULL OR dc.expires_at > now())
    ORDER BY dc.created_at DESC
    LIMIT 1
$$;
REVOKE ALL ON FUNCTION authenticate_device(UUID, TEXT) FROM PUBLIC;
ALTER FUNCTION authenticate_device(UUID, TEXT) OWNER TO ant_device_authenticator;
GRANT EXECUTE ON FUNCTION authenticate_device(UUID, TEXT) TO ant_control_plane;

ALTER TABLE devices FORCE ROW LEVEL SECURITY;
ALTER TABLE device_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE browser_instances FORCE ROW LEVEL SECURITY;
ALTER TABLE fingerprint_templates FORCE ROW LEVEL SECURITY;
ALTER TABLE workflow_versions FORCE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;
ALTER TABLE task_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE task_attempts FORCE ROW LEVEL SECURITY;
ALTER TABLE instance_sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE instance_commands FORCE ROW LEVEL SECURITY;
ALTER TABLE instance_events FORCE ROW LEVEL SECURITY;
ALTER TABLE browser_profiles FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_revisions FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_manifests FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_objects FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_sync_leases FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_conflicts FORCE ROW LEVEL SECURITY;
ALTER TABLE profile_restore_events FORCE ROW LEVEL SECURITY;
ALTER TABLE accounts FORCE ROW LEVEL SECURITY;
ALTER TABLE account_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE account_secret_access_events FORCE ROW LEVEL SECURITY;
ALTER TABLE proxies FORCE ROW LEVEL SECURITY;
ALTER TABLE proxy_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE proxy_assignments FORCE ROW LEVEL SECURITY;
ALTER TABLE proxy_health_samples FORCE ROW LEVEL SECURITY;
ALTER TABLE account_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE proxy_health_checks FORCE ROW LEVEL SECURITY;
ALTER TABLE secret_envelopes FORCE ROW LEVEL SECURITY;
ALTER TABLE workflows FORCE ROW LEVEL SECURITY;
ALTER TABLE workflow_permissions FORCE ROW LEVEL SECURITY;
ALTER TABLE schedules FORCE ROW LEVEL SECURITY;
ALTER TABLE artifacts FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_preferences FORCE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
ALTER TABLE notification_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE analytics_events FORCE ROW LEVEL SECURITY;
ALTER TABLE metric_rollups FORCE ROW LEVEL SECURITY;
ALTER TABLE risk_events FORCE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE entitlements FORCE ROW LEVEL SECURITY;
ALTER TABLE usage_counters FORCE ROW LEVEL SECURITY;
ALTER TABLE usage_reservations FORCE ROW LEVEL SECURITY;
ALTER TABLE license_activations FORCE ROW LEVEL SECURITY;

CREATE OR REPLACE FUNCTION cloud_current_user_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_user_id', true), '')::uuid;
$$;

-- User-facing device management can span the workspaces in which a user has
-- registered devices. Pre-workspace credential verification goes only
-- through authenticate_device(); there is no general device_auth policy.
DROP POLICY devices_tenant_policy ON devices;
CREATE POLICY devices_tenant_policy ON devices
    USING (
        workspace_id = cloud_current_workspace_id()
        OR user_id = cloud_current_user_id()
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR user_id = cloud_current_user_id()
    );
DROP POLICY device_credentials_tenant_policy ON device_credentials;
CREATE POLICY device_credentials_tenant_policy ON device_credentials
    USING (
        EXISTS (
            SELECT 1 FROM devices d
            WHERE d.id = device_id
              AND (d.workspace_id = cloud_current_workspace_id() OR d.user_id = cloud_current_user_id())
        )
    )
    WITH CHECK (EXISTS (
        SELECT 1 FROM devices d
        WHERE d.id = device_id
          AND (d.workspace_id = cloud_current_workspace_id() OR d.user_id = cloud_current_user_id())
    ));

-- Queue claim is cross-workspace by design, but only ant_worker can invoke it.
-- All state transitions still use the workspace tenant setting.
DROP POLICY tasks_tenant_policy ON tasks;
CREATE POLICY tasks_tenant_policy ON tasks
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) IN ('task_claim', 'schedule_dispatch'))
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) IN ('task_claim', 'schedule_dispatch'))
    );
DROP POLICY task_runs_tenant_policy ON task_runs;
CREATE POLICY task_runs_tenant_policy ON task_runs
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'task_claim')
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'task_claim')
    );
DROP POLICY task_attempts_tenant_policy ON task_attempts;
CREATE POLICY task_attempts_tenant_policy ON task_attempts
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'task_claim')
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'task_claim')
    );

DROP POLICY browser_instances_tenant_policy ON browser_instances;
CREATE POLICY browser_instances_tenant_policy ON browser_instances
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) IN ('task_claim', 'schedule_dispatch'))
    )
    WITH CHECK (workspace_id = cloud_current_workspace_id());
DROP POLICY workflows_tenant_policy ON workflows;
CREATE POLICY workflows_tenant_policy ON workflows
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'schedule_dispatch')
    )
    WITH CHECK (workspace_id = cloud_current_workspace_id());
DROP POLICY workflow_versions_tenant_policy ON workflow_versions;
CREATE POLICY workflow_versions_tenant_policy ON workflow_versions
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) IN ('task_claim', 'schedule_dispatch'))
    )
    WITH CHECK (workspace_id = cloud_current_workspace_id());

-- The scheduler is a worker-only path. It may lease due schedules and create
-- the resulting task, but cannot read or mutate arbitrary tenant resources.
DROP POLICY schedules_tenant_policy ON schedules;
CREATE POLICY schedules_tenant_policy ON schedules
    USING (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'schedule_dispatch')
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        OR (current_user = 'ant_worker' AND current_setting('app.system_operation', true) = 'schedule_dispatch')
    );
