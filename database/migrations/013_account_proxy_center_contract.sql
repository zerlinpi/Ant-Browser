-- v013: production Account Center and Proxy Center lifecycle contracts.
-- This migration extends the v004 tables in place so existing account/proxy
-- data and foreign keys remain authoritative.

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS external_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS username TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_status_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_status_check CHECK (
    status IN (
        'pending', 'active', 'suspended', 'disabled',
        'verification_required', 'error',
        'paused', 'risk', 'locked', 'deleted'
    )
);
ALTER TABLE accounts ADD CONSTRAINT accounts_version_positive_check CHECK (version >= 1);
CREATE INDEX accounts_workspace_live_idx
    ON accounts (workspace_id, platform, updated_at DESC)
    WHERE deleted_at IS NULL;

ALTER TABLE account_secrets
    ADD COLUMN IF NOT EXISTS encrypted_dek BYTEA,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE account_secrets ADD CONSTRAINT account_secrets_version_positive_check CHECK (version >= 1);

CREATE TABLE account_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    account_id UUID NOT NULL,
    binding_type TEXT NOT NULL CHECK (binding_type IN ('profile', 'browser_instance', 'proxy')),
    profile_id UUID,
    browser_instance_id UUID,
    proxy_id UUID,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, account_id, binding_type),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, account_id)
        REFERENCES accounts(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, profile_id)
        REFERENCES browser_profiles(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, browser_instance_id)
        REFERENCES browser_instances(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, proxy_id)
        REFERENCES proxies(workspace_id, id) ON DELETE CASCADE,
    CHECK (
        (binding_type = 'profile' AND profile_id IS NOT NULL AND browser_instance_id IS NULL AND proxy_id IS NULL)
        OR (binding_type = 'browser_instance' AND profile_id IS NULL AND browser_instance_id IS NOT NULL AND proxy_id IS NULL)
        OR (binding_type = 'proxy' AND profile_id IS NULL AND browser_instance_id IS NULL AND proxy_id IS NOT NULL)
    )
);
CREATE INDEX account_bindings_account_idx
    ON account_bindings (workspace_id, account_id, status, updated_at DESC);
CREATE TRIGGER account_bindings_set_updated_at BEFORE UPDATE ON account_bindings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS username TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS has_credentials BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS secret_ref TEXT,
    ADD COLUMN IF NOT EXISTS kernel TEXT,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

UPDATE proxies
SET kernel = CASE
    WHEN protocol = 'direct' THEN 'direct'
    WHEN connector_type = 'mihomo' THEN 'mihomo'
    WHEN protocol IN ('hysteria', 'hysteria2', 'tuic', 'anytls') THEN 'sing-box'
    ELSE 'xray'
END
WHERE kernel IS NULL;

ALTER TABLE proxies ALTER COLUMN kernel SET NOT NULL;
ALTER TABLE proxies DROP CONSTRAINT IF EXISTS proxies_port_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_port_check
    CHECK ((protocol = 'direct' AND port = 0) OR port BETWEEN 1 AND 65535);
ALTER TABLE proxies DROP CONSTRAINT IF EXISTS proxies_status_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_status_check
    CHECK (status IN ('active', 'disabled', 'paused', 'unhealthy', 'deleted'));
ALTER TABLE proxies ADD CONSTRAINT proxies_kernel_check
    CHECK (kernel IN ('direct', 'xray', 'sing-box', 'mihomo'));
ALTER TABLE proxies ADD CONSTRAINT proxies_version_positive_check CHECK (version >= 1);
CREATE INDEX proxies_workspace_live_idx
    ON proxies (workspace_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;

ALTER TABLE proxy_credentials
    ADD COLUMN IF NOT EXISTS credential_ciphertext BYTEA,
    ADD COLUMN IF NOT EXISTS encrypted_dek BYTEA,
    ADD COLUMN IF NOT EXISTS algorithm TEXT NOT NULL DEFAULT 'envelope-aes-256-gcm',
    ADD COLUMN IF NOT EXISTS secret_fingerprint BYTEA,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

-- Generic secret envelopes allow a credential to be written before its
-- subject row is committed while retaining tenant scoping and encryption at
-- rest. Orphans are safe ciphertext and are removed by service compensation.
CREATE TABLE secret_envelopes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('proxy_credentials')),
    subject_id UUID NOT NULL,
    algorithm TEXT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    encrypted_dek BYTEA NOT NULL,
    encryption_key_ref TEXT NOT NULL,
    key_version TEXT NOT NULL,
    secret_fingerprint BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id)
);
CREATE INDEX secret_envelopes_subject_idx
    ON secret_envelopes (workspace_id, purpose, subject_id)
    WHERE revoked_at IS NULL;

ALTER TABLE proxy_assignments
    ADD COLUMN IF NOT EXISTS browser_instance_id UUID,
    ADD COLUMN IF NOT EXISTS target_type TEXT,
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE proxy_assignments ADD CONSTRAINT proxy_assignments_browser_instance_fk
    FOREIGN KEY (workspace_id, browser_instance_id)
    REFERENCES browser_instances(workspace_id, id) ON DELETE CASCADE;

UPDATE proxy_assignments
SET target_type = CASE
    WHEN profile_id IS NOT NULL THEN 'profile'
    WHEN account_id IS NOT NULL THEN 'account'
    WHEN browser_instance_id IS NOT NULL THEN 'browser_instance'
END
WHERE target_type IS NULL;

ALTER TABLE proxy_assignments ALTER COLUMN target_type SET NOT NULL;
ALTER TABLE proxy_assignments DROP CONSTRAINT IF EXISTS proxy_assignments_check;
ALTER TABLE proxy_assignments ADD CONSTRAINT proxy_assignments_target_check CHECK (
    (target_type = 'profile' AND profile_id IS NOT NULL AND account_id IS NULL AND browser_instance_id IS NULL)
    OR (target_type = 'account' AND profile_id IS NULL AND account_id IS NOT NULL AND browser_instance_id IS NULL)
    OR (target_type = 'browser_instance' AND profile_id IS NULL AND account_id IS NULL AND browser_instance_id IS NOT NULL)
);
ALTER TABLE proxy_assignments ADD CONSTRAINT proxy_assignments_version_positive_check CHECK (version >= 1);
DROP INDEX IF EXISTS proxy_assignments_profile_uq;
DROP INDEX IF EXISTS proxy_assignments_account_uq;
CREATE UNIQUE INDEX proxy_assignments_profile_uq
    ON proxy_assignments (workspace_id, profile_id)
    WHERE profile_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX proxy_assignments_account_uq
    ON proxy_assignments (workspace_id, account_id)
    WHERE account_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX proxy_assignments_instance_uq
    ON proxy_assignments (workspace_id, browser_instance_id)
    WHERE browser_instance_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX proxy_assignments_live_target_uq
    ON proxy_assignments (workspace_id, target_type, COALESCE(profile_id, account_id, browser_instance_id))
    WHERE deleted_at IS NULL;
CREATE INDEX proxy_assignments_proxy_live_idx
    ON proxy_assignments (workspace_id, proxy_id)
    WHERE deleted_at IS NULL;
CREATE TRIGGER proxy_assignments_set_updated_at BEFORE UPDATE ON proxy_assignments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE proxy_health_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    request_id UUID NOT NULL,
    connector_type TEXT NOT NULL CHECK (connector_type IN ('xray', 'mihomo')),
    kernel TEXT NOT NULL CHECK (kernel IN ('direct', 'xray', 'sing-box', 'mihomo')),
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    ip INET,
    latency_ms BIGINT CHECK (latency_ms IS NULL OR latency_ms >= 0),
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, request_id),
    FOREIGN KEY (workspace_id, proxy_id)
        REFERENCES proxies(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX proxy_health_checks_proxy_time_idx
    ON proxy_health_checks (workspace_id, proxy_id, created_at DESC);

ALTER TABLE account_bindings ENABLE ROW LEVEL SECURITY;
CREATE POLICY account_bindings_tenant_policy ON account_bindings
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());

ALTER TABLE proxy_health_checks ENABLE ROW LEVEL SECURITY;
CREATE POLICY proxy_health_checks_tenant_policy ON proxy_health_checks
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());

ALTER TABLE secret_envelopes ENABLE ROW LEVEL SECURITY;
CREATE POLICY secret_envelopes_tenant_policy ON secret_envelopes
    USING (workspace_id = cloud_current_workspace_id())
    WITH CHECK (workspace_id = cloud_current_workspace_id());
