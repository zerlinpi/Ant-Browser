-- v008: opt-in-safe PostgreSQL RLS foundations for workspace/org-scoped rows.
-- The application must set LOCAL app.current_workspace_id and/or
-- app.current_organization_id after authenticating a request. The migration
-- role/table owner bypasses RLS by PostgreSQL design; production must use a
-- separate application role with no BYPASSRLS privilege.

CREATE OR REPLACE FUNCTION cloud_current_workspace_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_workspace_id', true), '')::uuid;
$$;

CREATE OR REPLACE FUNCTION cloud_current_organization_id()
RETURNS UUID
LANGUAGE sql
STABLE
AS $$
    SELECT NULLIF(current_setting('app.current_organization_id', true), '')::uuid;
$$;

ALTER TABLE devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE browser_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE instance_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE instance_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE instance_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE browser_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_manifests ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_sync_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_conflicts ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_restore_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_secret_access_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxies ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxy_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxy_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxy_health_samples ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE metric_rollups ENABLE ROW LEVEL SECURITY;
ALTER TABLE risk_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY devices_tenant_policy ON devices
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY device_credentials_tenant_policy ON device_credentials
    USING (EXISTS (
        SELECT 1 FROM devices d
        WHERE d.id = device_id AND d.workspace_id = cloud_current_workspace_id()
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM devices d
        WHERE d.id = device_id AND d.workspace_id = cloud_current_workspace_id()
    ));
CREATE POLICY browser_instances_tenant_policy ON browser_instances
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY instance_sessions_tenant_policy ON instance_sessions
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY instance_commands_tenant_policy ON instance_commands
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY instance_events_tenant_policy ON instance_events
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY browser_profiles_tenant_policy ON browser_profiles
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY profile_revisions_tenant_policy ON profile_revisions
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY profile_manifests_tenant_policy ON profile_manifests
    USING (EXISTS (SELECT 1 FROM profile_revisions r WHERE r.id = revision_id))
    WITH CHECK (EXISTS (SELECT 1 FROM profile_revisions r WHERE r.id = revision_id));
CREATE POLICY profile_objects_tenant_policy ON profile_objects
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY profile_sync_leases_tenant_policy ON profile_sync_leases
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY profile_conflicts_tenant_policy ON profile_conflicts
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY profile_restore_events_tenant_policy ON profile_restore_events
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY accounts_tenant_policy ON accounts
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY account_secrets_tenant_policy ON account_secrets
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY account_secret_access_events_tenant_policy ON account_secret_access_events
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY proxies_tenant_policy ON proxies
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY proxy_credentials_tenant_policy ON proxy_credentials
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY proxy_assignments_tenant_policy ON proxy_assignments
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY proxy_health_samples_tenant_policy ON proxy_health_samples
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY workflows_tenant_policy ON workflows
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY workflow_versions_tenant_policy ON workflow_versions
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY workflow_permissions_tenant_policy ON workflow_permissions
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY schedules_tenant_policy ON schedules
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY tasks_tenant_policy ON tasks
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY task_runs_tenant_policy ON task_runs
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY task_attempts_tenant_policy ON task_attempts
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY artifacts_tenant_policy ON artifacts
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY notification_preferences_tenant_policy ON notification_preferences
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY notifications_tenant_policy ON notifications
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY notification_deliveries_tenant_policy ON notification_deliveries
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY analytics_events_tenant_policy ON analytics_events
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY metric_rollups_tenant_policy ON metric_rollups
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
CREATE POLICY risk_events_tenant_policy ON risk_events
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());

ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE license_activations ENABLE ROW LEVEL SECURITY;

CREATE POLICY subscriptions_tenant_policy ON subscriptions
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());
CREATE POLICY entitlements_tenant_policy ON entitlements
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());
CREATE POLICY usage_counters_tenant_policy ON usage_counters
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());
CREATE POLICY usage_reservations_tenant_policy ON usage_reservations
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());
CREATE POLICY license_activations_tenant_policy ON license_activations
    USING (organization_id = cloud_current_organization_id())
    WITH CHECK (organization_id = cloud_current_organization_id());
