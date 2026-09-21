-- v005: versioned automation definitions, queue state, worker leases and artifacts.

CREATE TABLE workflows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE workflow_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    dsl_schema_version TEXT NOT NULL,
    definition JSONB NOT NULL,
    content_hash BYTEA NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, workflow_id, version),
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflows(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE workflow_permissions (
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    role_id UUID NOT NULL REFERENCES roles(id),
    can_execute BOOLEAN NOT NULL DEFAULT false,
    can_edit BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (workspace_id, workflow_id, role_id),
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflows(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    workflow_version_id UUID NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    enabled BOOLEAN NOT NULL DEFAULT true,
    next_run_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflows(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, workflow_version_id) REFERENCES workflow_versions(workspace_id, id)
);

CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    workflow_id UUID,
    workflow_version_id UUID,
    requested_by UUID REFERENCES users(id) ON DELETE SET NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'leased', 'running', 'succeeded', 'failed', 'cancelled', 'dead_letter')),
    priority INTEGER NOT NULL DEFAULT 0,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    retry_limit INTEGER NOT NULL DEFAULT 3 CHECK (retry_limit BETWEEN 0 AND 100),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflows(workspace_id, id),
    FOREIGN KEY (workspace_id, workflow_version_id) REFERENCES workflow_versions(workspace_id, id)
);

CREATE INDEX tasks_dispatch_idx ON tasks (status, priority DESC, available_at)
    WHERE status IN ('queued', 'leased', 'running');

CREATE TABLE task_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    task_id UUID NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE task_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    task_run_id UUID NOT NULL,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    worker_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('leased', 'running', 'succeeded', 'failed', 'expired')),
    lease_expires_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    error_code TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (workspace_id, task_run_id) REFERENCES task_runs(workspace_id, id) ON DELETE CASCADE,
    UNIQUE (workspace_id, task_run_id, attempt)
);

CREATE TABLE artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    task_run_id UUID,
    object_key TEXT NOT NULL,
    content_hash BYTEA NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    storage_backend TEXT NOT NULL DEFAULT 's3',
    encryption_key_ref TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, object_key),
    FOREIGN KEY (workspace_id, task_run_id) REFERENCES task_runs(workspace_id, id) ON DELETE SET NULL
);

CREATE TRIGGER workflows_set_updated_at BEFORE UPDATE ON workflows
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER schedules_set_updated_at BEFORE UPDATE ON schedules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER tasks_set_updated_at BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
