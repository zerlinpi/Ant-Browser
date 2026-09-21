-- v002: browser instances, desired/observed state, commands and events.

CREATE TABLE browser_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    profile_id UUID,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 255),
    platform TEXT NOT NULL DEFAULT '',
    assigned_device_id UUID,
    desired_state TEXT NOT NULL DEFAULT 'stopped'
        CHECK (desired_state IN ('stopped', 'running', 'paused', 'deleted')),
    observed_state TEXT NOT NULL DEFAULT 'offline'
        CHECK (observed_state IN ('offline', 'starting', 'running', 'stopping', 'failed')),
    observed_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE INDEX browser_instances_workspace_state_idx
    ON browser_instances (workspace_id, observed_state, updated_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX browser_instances_device_idx ON browser_instances (assigned_device_id)
    WHERE assigned_device_id IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE browser_instances
    ADD CONSTRAINT browser_instances_device_tenant_fk
    FOREIGN KEY (workspace_id, assigned_device_id)
    REFERENCES devices (workspace_id, id);

CREATE TABLE instance_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    device_id UUID,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('starting', 'running', 'stopping', 'stopped', 'failed')),
    cdp_endpoint_ref TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stopped_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    FOREIGN KEY (workspace_id, instance_id)
        REFERENCES browser_instances(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, device_id)
        REFERENCES devices(workspace_id, id)
);

CREATE INDEX instance_sessions_instance_time_idx
    ON instance_sessions (workspace_id, instance_id, started_at DESC);

CREATE TABLE instance_commands (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    device_id UUID,
    command_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    action TEXT NOT NULL,
    expected_version BIGINT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'accepted', 'running', 'completed', 'failed', 'cancelled', 'expired')),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    UNIQUE (workspace_id, command_id),
    UNIQUE (workspace_id, idempotency_key),
    FOREIGN KEY (workspace_id, instance_id)
        REFERENCES browser_instances(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, device_id)
        REFERENCES devices(workspace_id, id)
);

CREATE INDEX instance_commands_dispatch_idx
    ON instance_commands (workspace_id, status, created_at)
    WHERE status IN ('queued', 'accepted', 'running');

CREATE TABLE instance_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, instance_id, sequence),
    FOREIGN KEY (workspace_id, instance_id)
        REFERENCES browser_instances(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX instance_events_replay_idx
    ON instance_events (workspace_id, instance_id, sequence);

CREATE TRIGGER browser_instances_set_updated_at BEFORE UPDATE ON browser_instances
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
