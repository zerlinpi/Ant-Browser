-- v007: plans, subscriptions, usage, commercial entitlements and licenses.

CREATE TABLE plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    amount_minor BIGINT NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    billing_interval TEXT NOT NULL DEFAULT 'month'
        CHECK (billing_interval IN ('month', 'year', 'one_time', 'none')),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE plan_entitlements (
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    entitlement_code TEXT NOT NULL,
    limit_value BIGINT,
    feature_enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (plan_id, entitlement_code),
    CHECK (limit_value IS NULL OR limit_value >= 0)
);

CREATE TABLE subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES plans(id),
    status TEXT NOT NULL DEFAULT 'trialing'
        CHECK (status IN ('trialing', 'active', 'past_due', 'cancelled', 'expired')),
    provider TEXT NOT NULL DEFAULT 'internal',
    provider_subscription_ref TEXT NOT NULL DEFAULT '',
    current_period_start TIMESTAMPTZ NOT NULL,
    current_period_end TIMESTAMPTZ NOT NULL,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (current_period_end > current_period_start),
    UNIQUE (organization_id, id)
);

CREATE UNIQUE INDEX subscriptions_one_current_uq
    ON subscriptions (organization_id)
    WHERE status IN ('trialing', 'active', 'past_due');

CREATE TABLE subscription_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    provider_event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    UNIQUE (subscription_id, provider_event_id)
);

CREATE TABLE entitlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    entitlement_code TEXT NOT NULL,
    source_subscription_id UUID REFERENCES subscriptions(id) ON DELETE SET NULL,
    value_limit BIGINT,
    consumed_value BIGINT NOT NULL DEFAULT 0 CHECK (consumed_value >= 0),
    valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (organization_id, entitlement_code),
    CHECK (value_limit IS NULL OR value_limit >= 0),
    CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE TABLE usage_counters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE,
    metric_code TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    consumed_value BIGINT NOT NULL DEFAULT 0 CHECK (consumed_value >= 0),
    reserved_value BIGINT NOT NULL DEFAULT 0 CHECK (reserved_value >= 0),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (organization_id, workspace_id, metric_code, period_start),
    FOREIGN KEY (organization_id, workspace_id)
        REFERENCES workspaces(organization_id, id) ON DELETE CASCADE,
    CHECK (period_end > period_start)
);

CREATE TABLE usage_reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE,
    metric_code TEXT NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    status TEXT NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'committed', 'released', 'expired')),
    idempotency_key TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, idempotency_key),
    FOREIGN KEY (organization_id, workspace_id)
        REFERENCES workspaces(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE release_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    manifest JSONB NOT NULL,
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE TABLE license_activations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_id UUID,
    device_id UUID,
    release_channel_id UUID REFERENCES release_channels(id),
    license_token_hash BYTEA NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'grace', 'revoked', 'expired')),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_refresh_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    offline_grace_until TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    FOREIGN KEY (organization_id, workspace_id)
        REFERENCES workspaces(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, device_id)
        REFERENCES devices(workspace_id, id) ON DELETE SET NULL,
    CHECK ((workspace_id IS NULL) = (device_id IS NULL))
);

CREATE INDEX entitlements_active_idx ON entitlements (organization_id, entitlement_code, valid_until);
CREATE INDEX license_activations_org_idx ON license_activations (organization_id, status);

CREATE TRIGGER plans_set_updated_at BEFORE UPDATE ON plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER subscriptions_set_updated_at BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
