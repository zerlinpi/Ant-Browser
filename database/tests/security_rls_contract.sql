DO $$
DECLARE
    missing_force_rls TEXT;
    bypass_role TEXT;
BEGIN
    SELECT string_agg(required.table_name, ', ' ORDER BY required.table_name)
    INTO missing_force_rls
    FROM (VALUES
        ('accounts'), ('audit_events'), ('browser_instances'),
        ('browser_profiles'), ('notifications'), ('proxies'), ('schedules'),
        ('tasks'), ('workflows'), ('subscriptions'), ('entitlements')
    ) AS required(table_name)
    LEFT JOIN pg_class c ON c.relname = required.table_name
    LEFT JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
    WHERE c.oid IS NULL OR n.oid IS NULL OR NOT c.relrowsecurity OR NOT c.relforcerowsecurity;

    IF missing_force_rls IS NOT NULL THEN
        RAISE EXCEPTION 'tables missing FORCE RLS: %', missing_force_rls;
    END IF;

    SELECT string_agg(rolname, ', ' ORDER BY rolname)
    INTO bypass_role
    FROM pg_roles
    WHERE rolname IN ('ant_control_plane', 'ant_worker') AND rolbypassrls;

    IF bypass_role IS NOT NULL THEN
        RAISE EXCEPTION 'runtime roles unexpectedly BYPASSRLS: %', bypass_role;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public'
          AND tablename = 'audit_events'
          AND policyname = 'audit_events_tenant_policy'
    ) THEN
        RAISE EXCEPTION 'audit_events tenant policy is missing';
    END IF;

    IF NOT has_function_privilege(
        'ant_worker',
        'claim_notification_deliveries(text,text[],integer,integer)',
        'EXECUTE'
    ) THEN
        RAISE EXCEPTION 'worker cannot execute notification delivery claim function';
    END IF;
    IF has_function_privilege(
        'ant_control_plane',
        'claim_notification_deliveries(text,text[],integer,integer)',
        'EXECUTE'
    ) THEN
        RAISE EXCEPTION 'control-plane unexpectedly has cross-tenant notification claim access';
    END IF;
END
$$;
