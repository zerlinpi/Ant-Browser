-- v023: atomically enforce organization-wide instance and team-seat limits.
-- Trigger functions run under a dedicated non-login role. Locking the active
-- entitlement row serializes the count-and-insert decision per organization
-- and resource without granting runtime roles an RLS bypass.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_billing_enforcer') THEN
        CREATE ROLE ant_billing_enforcer NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_billing_enforcer;
GRANT SELECT ON organizations, workspaces, workspace_members,
                browser_instances, entitlements TO ant_billing_enforcer;
GRANT UPDATE ON entitlements TO ant_billing_enforcer;

CREATE OR REPLACE FUNCTION enforce_browser_instance_entitlement()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    selected_organization UUID;
    configured_limit BIGINT;
    feature_allowed BOOLEAN;
    current_usage BIGINT;
BEGIN
    IF NEW.deleted_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF OLD.deleted_at IS NULL AND OLD.workspace_id = NEW.workspace_id THEN
            RETURN NEW;
        END IF;
    END IF;

    SELECT w.organization_id INTO selected_organization
    FROM public.workspaces w WHERE w.id = NEW.workspace_id;
    IF selected_organization IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT e.value_limit, e.feature_enabled
    INTO configured_limit, feature_allowed
    FROM public.entitlements e
    WHERE e.organization_id = selected_organization
      AND e.entitlement_code = 'instances'
      AND e.valid_from <= clock_timestamp()
      AND (e.valid_until IS NULL OR e.valid_until > clock_timestamp())
    FOR UPDATE;

    IF NOT FOUND OR NOT feature_allowed THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001',
            MESSAGE = 'billing quota exceeded: instances entitlement unavailable',
            CONSTRAINT = 'billing_instances_quota';
    END IF;
    IF configured_limit IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT count(*) INTO current_usage
    FROM public.browser_instances instance
    JOIN public.workspaces workspace ON workspace.id = instance.workspace_id
    WHERE workspace.organization_id = selected_organization
      AND instance.deleted_at IS NULL
      AND instance.id <> NEW.id;
    IF current_usage + 1 > configured_limit THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001',
            MESSAGE = 'billing quota exceeded: instances',
            CONSTRAINT = 'billing_instances_quota';
    END IF;
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION enforce_team_member_entitlement()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    selected_organization UUID;
    configured_limit BIGINT;
    feature_allowed BOOLEAN;
    current_usage BIGINT;
BEGIN
    IF NEW.status <> 'active' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF OLD.status = 'active'
           AND OLD.workspace_id = NEW.workspace_id
           AND OLD.user_id = NEW.user_id THEN
            RETURN NEW;
        END IF;
    END IF;

    SELECT w.organization_id INTO selected_organization
    FROM public.workspaces w WHERE w.id = NEW.workspace_id;
    IF selected_organization IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT e.value_limit, e.feature_enabled
    INTO configured_limit, feature_allowed
    FROM public.entitlements e
    WHERE e.organization_id = selected_organization
      AND e.entitlement_code = 'team_members'
      AND e.valid_from <= clock_timestamp()
      AND (e.valid_until IS NULL OR e.valid_until > clock_timestamp())
    FOR UPDATE;

    IF NOT FOUND OR NOT feature_allowed THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001',
            MESSAGE = 'billing quota exceeded: team members entitlement unavailable',
            CONSTRAINT = 'billing_team_members_quota';
    END IF;
    IF configured_limit IS NULL THEN
        RETURN NEW;
    END IF;

    -- One user consumes one organization seat even when they belong to more
    -- than one workspace in that organization.
    IF EXISTS (
        SELECT 1
        FROM public.workspace_members member
        JOIN public.workspaces workspace ON workspace.id = member.workspace_id
        WHERE workspace.organization_id = selected_organization
          AND member.user_id = NEW.user_id AND member.status = 'active'
          AND NOT (member.workspace_id = NEW.workspace_id AND member.user_id = NEW.user_id)
    ) THEN
        RETURN NEW;
    END IF;

    SELECT count(DISTINCT member.user_id) INTO current_usage
    FROM public.workspace_members member
    JOIN public.workspaces workspace ON workspace.id = member.workspace_id
    WHERE workspace.organization_id = selected_organization
      AND member.status = 'active'
      AND NOT (member.workspace_id = NEW.workspace_id AND member.user_id = NEW.user_id);
    IF current_usage + 1 > configured_limit THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001',
            MESSAGE = 'billing quota exceeded: team members',
            CONSTRAINT = 'billing_team_members_quota';
    END IF;
    RETURN NEW;
END
$$;

REVOKE ALL ON FUNCTION enforce_browser_instance_entitlement() FROM PUBLIC;
REVOKE ALL ON FUNCTION enforce_team_member_entitlement() FROM PUBLIC;
ALTER FUNCTION enforce_browser_instance_entitlement() OWNER TO ant_billing_enforcer;
ALTER FUNCTION enforce_team_member_entitlement() OWNER TO ant_billing_enforcer;

DROP TRIGGER IF EXISTS browser_instances_entitlement_guard ON browser_instances;
CREATE TRIGGER browser_instances_entitlement_guard
    BEFORE INSERT OR UPDATE OF workspace_id, deleted_at ON browser_instances
    FOR EACH ROW EXECUTE FUNCTION enforce_browser_instance_entitlement();

DROP TRIGGER IF EXISTS workspace_members_entitlement_guard ON workspace_members;
CREATE TRIGGER workspace_members_entitlement_guard
    BEFORE INSERT OR UPDATE OF workspace_id, user_id, status ON workspace_members
    FOR EACH ROW EXECUTE FUNCTION enforce_team_member_entitlement();
