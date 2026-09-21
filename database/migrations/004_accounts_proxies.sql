-- v004: workspace accounts, encrypted secret metadata, proxies and health.

CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform TEXT NOT NULL,
    external_identifier TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    profile_id UUID,
    browser_instance_id UUID,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'risk', 'locked', 'deleted')),
    risk_level TEXT NOT NULL DEFAULT 'unknown'
        CHECK (risk_level IN ('unknown', 'low', 'medium', 'high', 'critical')),
    notes TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, platform, external_identifier),
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id),
    FOREIGN KEY (workspace_id, browser_instance_id)
        REFERENCES browser_instances(workspace_id, id)
);

CREATE TABLE account_secrets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    account_id UUID NOT NULL,
    secret_type TEXT NOT NULL CHECK (secret_type IN ('password', 'cookie', 'totp_seed', 'api_key', 'oauth_token')),
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    encryption_key_ref TEXT NOT NULL,
    algorithm TEXT NOT NULL DEFAULT 'envelope-aes-gcm',
    key_version TEXT NOT NULL,
    secret_fingerprint BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    UNIQUE (workspace_id, account_id, secret_type),
    FOREIGN KEY (workspace_id, account_id)
        REFERENCES accounts(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE account_secret_access_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    account_id UUID NOT NULL,
    secret_type TEXT NOT NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_device_id UUID,
    purpose TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('issued', 'denied', 'rotated', 'revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, account_id)
        REFERENCES accounts(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, actor_device_id)
        REFERENCES devices(workspace_id, id)
);

CREATE TABLE proxies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    country_code TEXT NOT NULL DEFAULT '',
    connector_type TEXT NOT NULL CHECK (connector_type IN ('xray', 'mihomo')),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'unhealthy', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE proxy_credentials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    username_ciphertext BYTEA,
    password_ciphertext BYTEA,
    nonce BYTEA,
    encryption_key_ref TEXT,
    key_version TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, proxy_id)
        REFERENCES proxies(workspace_id, id) ON DELETE CASCADE,
    UNIQUE (workspace_id, proxy_id)
);

CREATE TABLE proxy_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    profile_id UUID,
    account_id UUID,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, proxy_id) REFERENCES proxies(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, profile_id) REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts(workspace_id, id) ON DELETE CASCADE,
    CHECK ((profile_id IS NOT NULL) OR (account_id IS NOT NULL))
);

CREATE UNIQUE INDEX proxy_assignments_profile_uq
    ON proxy_assignments (workspace_id, profile_id) WHERE profile_id IS NOT NULL;
CREATE UNIQUE INDEX proxy_assignments_account_uq
    ON proxy_assignments (workspace_id, account_id) WHERE account_id IS NOT NULL;

CREATE TABLE proxy_health_samples (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    connector_type TEXT NOT NULL CHECK (connector_type IN ('xray', 'mihomo')),
    success BOOLEAN NOT NULL,
    latency_ms INTEGER CHECK (latency_ms IS NULL OR latency_ms >= 0),
    public_ip INET,
    error_code TEXT NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, proxy_id) REFERENCES proxies(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX proxy_health_time_idx ON proxy_health_samples (workspace_id, proxy_id, checked_at DESC);
CREATE INDEX accounts_workspace_status_idx ON accounts (workspace_id, status, risk_level);

CREATE TRIGGER accounts_set_updated_at BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER proxies_set_updated_at BEFORE UPDATE ON proxies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
