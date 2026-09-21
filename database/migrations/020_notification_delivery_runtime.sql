-- v020: durable email/WebSocket notification delivery with worker leases.
-- Cross-tenant claims are exposed only through narrowly-shaped SECURITY
-- DEFINER functions; the worker never receives direct access to user records.

ALTER TABLE notification_deliveries
    ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '',
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD CONSTRAINT notification_delivery_lease_shape CHECK (
        (lease_owner = '' AND lease_expires_at IS NULL)
        OR (lease_owner <> '' AND lease_expires_at IS NOT NULL)
    );

CREATE INDEX notification_deliveries_ready_idx
    ON notification_deliveries (COALESCE(next_attempt_at, created_at), created_at, id)
    WHERE status IN ('pending', 'failed') AND attempts < 8;

-- In-app persistence itself is the durable delivery. Repair rows created by
-- earlier application versions so the outbox contains only external channels.
UPDATE notification_deliveries
SET status = 'sent', attempts = GREATEST(attempts, 1),
    sent_at = COALESCE(sent_at, created_at), next_attempt_at = NULL,
    last_error_code = '', updated_at = now()
WHERE channel = 'in_app' AND status IN ('pending', 'failed');

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_notification_dispatcher') THEN
        CREATE ROLE ant_notification_dispatcher NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_notification_dispatcher;
GRANT SELECT ON notifications, users TO ant_notification_dispatcher;
GRANT SELECT, UPDATE ON notification_deliveries TO ant_notification_dispatcher;

CREATE OR REPLACE FUNCTION claim_notification_deliveries(
    p_worker_id TEXT,
    p_channels TEXT[],
    p_limit INTEGER,
    p_lease_seconds INTEGER
)
RETURNS TABLE (
    id UUID,
    workspace_id UUID,
    notification_id UUID,
    recipient_user_id UUID,
    recipient_email TEXT,
    channel TEXT,
    event_type TEXT,
    title TEXT,
    body TEXT,
    payload JSONB,
    created_at TIMESTAMPTZ,
    attempts INTEGER,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF btrim(COALESCE(p_worker_id, '')) = '' OR length(p_worker_id) > 200 THEN
        RAISE EXCEPTION 'invalid notification worker ID';
    END IF;
    IF p_limit < 1 OR p_limit > 32 THEN
        RAISE EXCEPTION 'invalid notification claim limit';
    END IF;
    IF p_lease_seconds < 10 OR p_lease_seconds > 1800 THEN
        RAISE EXCEPTION 'invalid notification lease duration';
    END IF;
    IF COALESCE(cardinality(p_channels), 0) = 0
       OR NOT p_channels <@ ARRAY['email', 'websocket']::TEXT[] THEN
        RAISE EXCEPTION 'invalid notification delivery channels';
    END IF;

    RETURN QUERY
    WITH candidates AS (
        SELECT d.id
        FROM public.notification_deliveries d
        WHERE d.channel = ANY(p_channels)
          AND d.status IN ('pending', 'failed')
          AND d.attempts < 8
          AND COALESCE(d.next_attempt_at, d.created_at) <= clock_timestamp()
          AND (d.lease_expires_at IS NULL OR d.lease_expires_at <= clock_timestamp())
        ORDER BY COALESCE(d.next_attempt_at, d.created_at), d.created_at, d.id
        FOR UPDATE OF d SKIP LOCKED
        LIMIT p_limit
    ), claimed AS (
        UPDATE public.notification_deliveries d
        SET status = 'pending', attempts = d.attempts + 1,
            lease_owner = p_worker_id,
            lease_expires_at = clock_timestamp() + make_interval(secs => p_lease_seconds),
            last_error_code = '', updated_at = clock_timestamp()
        FROM candidates c
        WHERE d.id = c.id
        RETURNING d.id, d.workspace_id, d.notification_id, d.channel,
                  d.attempts, d.lease_owner, d.lease_expires_at
    )
    SELECT c.id, c.workspace_id, c.notification_id, n.recipient_user_id,
           u.email, c.channel, n.event_type, n.title, n.body, n.payload,
           n.created_at, c.attempts, c.lease_owner, c.lease_expires_at
    FROM claimed c
    JOIN public.notifications n
      ON n.workspace_id = c.workspace_id AND n.id = c.notification_id
    JOIN public.users u ON u.id = n.recipient_user_id
    ORDER BY n.created_at, c.id;
END
$$;

CREATE OR REPLACE FUNCTION complete_notification_delivery(
    p_delivery_id UUID,
    p_workspace_id UUID,
    p_worker_id TEXT,
    p_finished_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    affected INTEGER;
BEGIN
    UPDATE public.notification_deliveries d
    SET status = 'sent', sent_at = p_finished_at, next_attempt_at = NULL,
        last_error_code = '', lease_owner = '', lease_expires_at = NULL,
        updated_at = p_finished_at
    WHERE d.id = p_delivery_id AND d.workspace_id = p_workspace_id
      AND d.lease_owner = p_worker_id AND d.lease_expires_at > p_finished_at
      AND d.status = 'pending';
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected = 1;
END
$$;

CREATE OR REPLACE FUNCTION fail_notification_delivery(
    p_delivery_id UUID,
    p_workspace_id UUID,
    p_worker_id TEXT,
    p_error_code TEXT,
    p_discard BOOLEAN,
    p_retry_at TIMESTAMPTZ,
    p_finished_at TIMESTAMPTZ
)
RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    affected INTEGER;
BEGIN
    UPDATE public.notification_deliveries d
    SET status = CASE WHEN p_discard THEN 'discarded' ELSE 'failed' END,
        next_attempt_at = CASE WHEN p_discard THEN NULL ELSE p_retry_at END,
        last_error_code = left(COALESCE(NULLIF(btrim(p_error_code), ''), 'delivery_failed'), 100),
        lease_owner = '', lease_expires_at = NULL, updated_at = p_finished_at
    WHERE d.id = p_delivery_id AND d.workspace_id = p_workspace_id
      AND d.lease_owner = p_worker_id AND d.lease_expires_at > p_finished_at
      AND d.status = 'pending';
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected = 1;
END
$$;

REVOKE ALL ON FUNCTION claim_notification_deliveries(TEXT, TEXT[], INTEGER, INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION complete_notification_delivery(UUID, UUID, TEXT, TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION fail_notification_delivery(UUID, UUID, TEXT, TEXT, BOOLEAN, TIMESTAMPTZ, TIMESTAMPTZ) FROM PUBLIC;

ALTER FUNCTION claim_notification_deliveries(TEXT, TEXT[], INTEGER, INTEGER) OWNER TO ant_notification_dispatcher;
ALTER FUNCTION complete_notification_delivery(UUID, UUID, TEXT, TIMESTAMPTZ) OWNER TO ant_notification_dispatcher;
ALTER FUNCTION fail_notification_delivery(UUID, UUID, TEXT, TEXT, BOOLEAN, TIMESTAMPTZ, TIMESTAMPTZ) OWNER TO ant_notification_dispatcher;

GRANT EXECUTE ON FUNCTION claim_notification_deliveries(TEXT, TEXT[], INTEGER, INTEGER) TO ant_worker;
GRANT EXECUTE ON FUNCTION complete_notification_delivery(UUID, UUID, TEXT, TIMESTAMPTZ) TO ant_worker;
GRANT EXECUTE ON FUNCTION fail_notification_delivery(UUID, UUID, TEXT, TEXT, BOOLEAN, TIMESTAMPTZ, TIMESTAMPTZ) TO ant_worker;
