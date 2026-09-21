CREATE TABLE IF NOT EXISTS proxy_configs (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    name VARCHAR(120) NOT NULL,
    protocol VARCHAR(32) NOT NULL,
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    username VARCHAR(255),
    has_credentials BOOLEAN NOT NULL DEFAULT FALSE,
    secret_ref VARCHAR(255),
    connector_type VARCHAR(16) NOT NULL,
    kernel VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    version BIGINT NOT NULL DEFAULT 1,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_configs_workspace_name_active
    ON proxy_configs (workspace_id, lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS proxy_assignments (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    target_id UUID NOT NULL,
    target_type VARCHAR(32) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS proxy_assignments_target_active
    ON proxy_assignments (workspace_id, target_type, target_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS proxy_health_checks (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    proxy_id UUID NOT NULL,
    request_id UUID NOT NULL,
    connector_type VARCHAR(16) NOT NULL,
    kernel VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL,
    ip VARCHAR(64),
    latency_ms BIGINT,
    error_code VARCHAR(64),
    error_message TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS proxy_health_checks_proxy_created
    ON proxy_health_checks (workspace_id, proxy_id, created_at DESC);
