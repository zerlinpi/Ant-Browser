-- v006: durable notifications, delivery attempts, analytics and risk events.

CREATE TABLE notification_preferences (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'websocket', 'push')),
    event_type TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id, channel, event_type),
    FOREIGN KEY (workspace_id, user_id)
        REFERENCES workspace_members(workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    recipient_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, recipient_user_id)
        REFERENCES workspace_members(workspace_id, user_id) ON DELETE CASCADE
);

CREATE INDEX notifications_recipient_idx
    ON notifications (workspace_id, recipient_user_id, created_at DESC);

CREATE TABLE notification_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    notification_id UUID NOT NULL,
    channel TEXT NOT NULL CHECK (channel IN ('in_app', 'email', 'websocket', 'push')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'failed', 'discarded')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, notification_id, channel),
    FOREIGN KEY (workspace_id, notification_id) REFERENCES notifications(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE analytics_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dimensions JSONB NOT NULL DEFAULT '{}'::jsonb,
    measurements JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_id TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (workspace_id, actor_user_id)
        REFERENCES workspace_members(workspace_id, user_id) ON DELETE SET NULL
);

CREATE INDEX analytics_events_workspace_time_idx
    ON analytics_events (workspace_id, occurred_at DESC);
CREATE INDEX analytics_events_type_time_idx
    ON analytics_events (workspace_id, event_type, occurred_at DESC);

CREATE TABLE metric_rollups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    metric_name TEXT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_width_seconds INTEGER NOT NULL CHECK (bucket_width_seconds > 0),
    dimensions JSONB NOT NULL DEFAULT '{}'::jsonb,
    value NUMERIC NOT NULL DEFAULT 0,
    sample_count BIGINT NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, metric_name, bucket_start, bucket_width_seconds, dimensions)
);

CREATE TABLE risk_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_id UUID,
    proxy_id UUID,
    severity TEXT NOT NULL CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    event_type TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, account_id) REFERENCES accounts(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, proxy_id) REFERENCES proxies(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX risk_events_open_idx ON risk_events (workspace_id, severity, created_at DESC)
    WHERE resolved_at IS NULL;
