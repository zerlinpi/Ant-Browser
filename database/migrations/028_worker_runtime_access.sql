-- v028: let the ant_worker role run the queue paths it was built for.
--
-- 1. Tenant RLS helpers run with their owner's privileges.
--    021 made cloud_current_workspace_id() and cloud_current_organization_id()
--    read workspaces and organizations so tenant policies fail closed for
--    suspended tenants. As invoker-rights functions they then required SELECT
--    on those tables from every role that evaluates a tenant policy.
--    ant_worker deliberately has no such grant, so every worker query on an
--    RLS-protected table (task claim, schedule dispatch, proxy health) failed
--    with "permission denied for table workspaces". As SECURITY DEFINER
--    functions with a pinned search_path they reveal only whether the
--    workspace or organization already named in the caller's own transaction
--    setting is active.
--
-- 2. Workflow target checks take their row locks as a dedicated role.
--    Scheduled dispatch and the tasks_validate_workflow_insert trigger lock
--    the workflow, its pinned version and the target instance FOR SHARE so
--    publication, archiving or deletion cannot race the enqueue. Row locks
--    require UPDATE privilege, which ant_worker must not have on these
--    tables. Following 023, the checks run as a NOLOGIN, BYPASSRLS role that
--    no login role is a member of. Both functions validate exactly the
--    workspace of the row being processed; the dispatch check additionally
--    requires an existing schedule that pins the same target.

CREATE OR REPLACE FUNCTION cloud_current_workspace_id()
RETURNS UUID
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT w.id
    FROM public.workspaces w
    JOIN public.organizations o ON o.id = w.organization_id
    WHERE w.id = NULLIF(current_setting('app.current_workspace_id', true), '')::uuid
      AND w.status = 'active' AND o.status = 'active';
$$;

CREATE OR REPLACE FUNCTION cloud_current_organization_id()
RETURNS UUID
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT o.id
    FROM public.organizations o
    WHERE o.id = NULLIF(current_setting('app.current_organization_id', true), '')::uuid
      AND o.status = 'active';
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_workflow_guard') THEN
        CREATE ROLE ant_workflow_guard NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_workflow_guard;
GRANT SELECT ON schedules TO ant_workflow_guard;
-- UPDATE is required only to take FOR SHARE row locks; the guard functions
-- never modify these tables.
GRANT SELECT, UPDATE ON workflows, workflow_versions, browser_instances TO ant_workflow_guard;

ALTER FUNCTION validate_workflow_task_insert() SECURITY DEFINER;
ALTER FUNCTION validate_workflow_task_insert() SET search_path = pg_catalog, public;
REVOKE ALL ON FUNCTION validate_workflow_task_insert() FROM PUBLIC;
ALTER FUNCTION validate_workflow_task_insert() OWNER TO ant_workflow_guard;

CREATE OR REPLACE FUNCTION schedule_target_executable(
    p_workspace_id UUID,
    p_schedule_id UUID,
    p_workflow_id UUID,
    p_workflow_version_id UUID,
    p_instance_id UUID
)
RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    PERFORM 1 FROM public.schedules s
    WHERE s.workspace_id = p_workspace_id AND s.id = p_schedule_id
      AND s.workflow_id = p_workflow_id AND s.workflow_version_id = p_workflow_version_id
      AND s.instance_id = p_instance_id;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    PERFORM 1
    FROM public.workflows w
    JOIN public.workflow_versions v
      ON v.workspace_id = w.workspace_id AND v.workflow_id = w.id AND v.id = p_workflow_version_id
    JOIN public.browser_instances i
      ON i.workspace_id = w.workspace_id AND i.id = p_instance_id AND i.deleted_at IS NULL
    WHERE w.workspace_id = p_workspace_id AND w.id = p_workflow_id AND w.status = 'published'
      AND w.published_version_id = v.id
      AND v.definition->>'engine' IN ('playwright', 'cdp')
    FOR SHARE OF w, v, i;
    RETURN FOUND;
END
$$;

REVOKE ALL ON FUNCTION schedule_target_executable(UUID, UUID, UUID, UUID, UUID) FROM PUBLIC;
ALTER FUNCTION schedule_target_executable(UUID, UUID, UUID, UUID, UUID) OWNER TO ant_workflow_guard;
GRANT EXECUTE ON FUNCTION schedule_target_executable(UUID, UUID, UUID, UUID, UUID) TO ant_worker;
