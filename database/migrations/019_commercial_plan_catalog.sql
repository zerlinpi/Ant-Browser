-- v019: deterministic commercial plan catalog and fail-closed Free
-- provisioning for every organization.

ALTER TABLE entitlements
    ADD COLUMN feature_enabled BOOLEAN NOT NULL DEFAULT true;

CREATE UNIQUE INDEX usage_counters_org_metric_period_uq
    ON usage_counters (organization_id, metric_code, period_start)
    WHERE workspace_id IS NULL;

INSERT INTO plans (code,name,currency,amount_minor,billing_interval,active)
VALUES
    ('free','Free','USD',0,'none',true),
    ('professional','Professional','USD',4900,'month',true),
    ('enterprise','Enterprise','USD',0,'none',true)
ON CONFLICT (code) DO UPDATE SET
    name=EXCLUDED.name, currency=EXCLUDED.currency,
    amount_minor=EXCLUDED.amount_minor,
    billing_interval=EXCLUDED.billing_interval, active=true;

WITH catalog(plan_code,entitlement_code,limit_value,feature_enabled) AS (VALUES
    ('free','instances',3,true),
    ('free','team_members',2,true),
    ('free','automation_runs',1000,true),
    ('free','storage_bytes',1073741824,true),
    ('free','api_calls',10000,true),
    ('free','commercial_license',NULL,false),
    ('professional','instances',25,true),
    ('professional','team_members',10,true),
    ('professional','automation_runs',25000,true),
    ('professional','storage_bytes',53687091200,true),
    ('professional','api_calls',500000,true),
    ('professional','commercial_license',1,true),
    ('enterprise','instances',NULL,true),
    ('enterprise','team_members',NULL,true),
    ('enterprise','automation_runs',NULL,true),
    ('enterprise','storage_bytes',NULL,true),
    ('enterprise','api_calls',NULL,true),
    ('enterprise','commercial_license',1,true)
)
INSERT INTO plan_entitlements (plan_id,entitlement_code,limit_value,feature_enabled)
SELECT p.id,c.entitlement_code,c.limit_value,c.feature_enabled
FROM catalog c JOIN plans p ON p.code=c.plan_code
ON CONFLICT (plan_id,entitlement_code) DO UPDATE SET
    limit_value=EXCLUDED.limit_value,
    feature_enabled=EXCLUDED.feature_enabled;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_billing_enforcer') THEN
        CREATE ROLE ant_billing_enforcer NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_billing_enforcer;
GRANT SELECT ON plans, plan_entitlements, subscriptions TO ant_billing_enforcer;
GRANT INSERT ON subscriptions, entitlements TO ant_billing_enforcer;

CREATE OR REPLACE FUNCTION provision_organization_billing(p_organization_id UUID)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    selected_subscription UUID;
    selected_plan UUID;
BEGIN
    SELECT s.id, s.plan_id
    INTO selected_subscription, selected_plan
    FROM public.subscriptions s
    WHERE s.organization_id = p_organization_id
      AND s.status IN ('trialing','active','past_due')
    ORDER BY s.updated_at DESC
    LIMIT 1;

    IF selected_subscription IS NULL THEN
        selected_subscription := gen_random_uuid();
        SELECT p.id INTO selected_plan
        FROM public.plans p
        WHERE p.code = 'free' AND p.active
        LIMIT 1;
        IF selected_plan IS NULL THEN
            RAISE EXCEPTION 'free billing plan is not provisioned';
        END IF;
        INSERT INTO public.subscriptions (
            id,organization_id,plan_id,status,provider,
            provider_subscription_ref,current_period_start,current_period_end
        ) VALUES (
            selected_subscription,p_organization_id,selected_plan,'active','internal',
            'free:' || p_organization_id::text,now(),now()+interval '100 years'
        );
    END IF;

    INSERT INTO public.entitlements (
        id,organization_id,entitlement_code,source_subscription_id,
        value_limit,feature_enabled,valid_from,metadata
    )
    SELECT gen_random_uuid(),p_organization_id,pe.entitlement_code,
           selected_subscription,pe.limit_value,pe.feature_enabled,now(),pe.metadata
    FROM public.plan_entitlements pe
    WHERE pe.plan_id = selected_plan
    ON CONFLICT (organization_id,entitlement_code) DO NOTHING;
END
$$;

-- Backfill while the migration owner still has direct EXECUTE. Runtime
-- provisioning below is performed only by the dedicated trigger owner.
SELECT provision_organization_billing(id) FROM organizations;
REVOKE ALL ON FUNCTION provision_organization_billing(UUID) FROM PUBLIC;

CREATE OR REPLACE FUNCTION provision_organization_billing_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    PERFORM public.provision_organization_billing(NEW.id);
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION provision_organization_billing_trigger() FROM PUBLIC;
ALTER FUNCTION provision_organization_billing(UUID) OWNER TO ant_billing_enforcer;
ALTER FUNCTION provision_organization_billing_trigger() OWNER TO ant_billing_enforcer;

DROP TRIGGER IF EXISTS organizations_provision_billing ON organizations;
CREATE TRIGGER organizations_provision_billing
    AFTER INSERT ON organizations
    FOR EACH ROW EXECUTE FUNCTION provision_organization_billing_trigger();
