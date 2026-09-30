-- v032: finish the proxy health check path for the ant_worker role.
--
-- 1. The worker publishes notifications without reading them.
--    The worker publishes notifications in two places: the proxy health
--    handler when a probe reports an unreachable exit, and the task worker
--    when a task fails terminally. Both go through CreateNotification, which
--    inserted into notifications and notification_deliveries and read
--    notification_preferences directly. ant_worker has no privileges on
--    these tables (018 limits it to queue processing), so every publish as
--    ant_worker failed with "permission denied for table notifications": a
--    failed health check was retried as notification_publish_failed until
--    its retries ran out, and terminal task failures were never announced.
--
--    Granting ant_worker the privileges those statements need would let it
--    read every notification (titles, bodies and security payloads) of any
--    workspace it scopes into, and rewrite delivery state. Instead,
--    publish_notification() runs exactly those statements as the NOLOGIN
--    role ant_notification_publisher, which no login role is a member of.
--    That role is NOBYPASSRLS, so the tenant policies still apply inside the
--    function with the caller's app.current_workspace_id: a call can only
--    write rows of the workspace its own transaction is scoped to. The
--    stored row is returned only when it matches the request; a replayed
--    idempotency key with a different event, title, body or payload raises
--    notification_idempotency_conflict without revealing the stored
--    content. ant_control_plane uses the same function, so both runtimes
--    share one code path; it keeps its direct table privileges for reading
--    and marking notifications.
--
-- 2. Dead-lettered health check tasks finalize their check.
--    014's trigger sync_proxy_health_task_terminal() marks a proxy health
--    check failed when its task is cancelled, fails or is dead-lettered. It
--    ran with the privileges and RLS context of the updater. When a lease
--    expired after the last allowed attempt (a lost worker), the worker's
--    next claim dead-letters the task under the task_claim system operation,
--    which has no workspace scope; the trigger's UPDATE then matched no row
--    under the proxy_health_checks policy and the check stayed queued
--    forever. The trigger only updates the check whose request_id is the
--    updated task, in that task's own workspace, and trigger functions cannot
--    be called directly, so it now runs as the NOLOGIN, BYPASSRLS role
--    ant_proxy_health_sync (the pattern of 023 and 028), which may only read
--    and update proxy_health_checks.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_notification_publisher') THEN
        CREATE ROLE ant_notification_publisher NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_notification_publisher;
GRANT SELECT, INSERT ON notifications TO ant_notification_publisher;
-- The idempotent upsert sets id to itself so RETURNING yields the stored row.
GRANT UPDATE (id) ON notifications TO ant_notification_publisher;
GRANT SELECT, INSERT ON notification_deliveries TO ant_notification_publisher;
GRANT UPDATE (status, attempts, sent_at, next_attempt_at, last_error_code, updated_at)
    ON notification_deliveries TO ant_notification_publisher;
GRANT SELECT ON notification_preferences TO ant_notification_publisher;

CREATE OR REPLACE FUNCTION publish_notification(
    p_id UUID,
    p_workspace_id UUID,
    p_recipient_user_id UUID,
    p_event_type TEXT,
    p_title TEXT,
    p_body TEXT,
    p_payload JSONB,
    p_created_at TIMESTAMPTZ,
    p_idempotency_key TEXT
)
RETURNS SETOF public.notifications
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    stored public.notifications%ROWTYPE;
BEGIN
    INSERT INTO public.notifications AS n
        (id, workspace_id, recipient_user_id, event_type, title, body, payload, created_at, idempotency_key)
    VALUES (p_id, p_workspace_id, p_recipient_user_id, p_event_type, p_title, p_body, p_payload, p_created_at, p_idempotency_key)
    ON CONFLICT (workspace_id, recipient_user_id, idempotency_key) WHERE idempotency_key <> ''
    DO UPDATE SET id = n.id
    RETURNING n.* INTO stored;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    IF stored.event_type IS DISTINCT FROM p_event_type OR stored.title IS DISTINCT FROM p_title
       OR stored.body IS DISTINCT FROM p_body OR stored.payload IS DISTINCT FROM p_payload THEN
        RAISE EXCEPTION USING ERRCODE = 'P0001',
            MESSAGE = 'notification idempotency key was used for a different notification',
            CONSTRAINT = 'notification_idempotency_conflict';
    END IF;

    -- The notification row itself is the completed in-app delivery.
    -- WebSocket delivery defaults on unless the recipient disabled it;
    -- email is opt-in.
    INSERT INTO public.notification_deliveries AS d
        (id, workspace_id, notification_id, channel, status, attempts, sent_at, created_at, updated_at)
    VALUES (gen_random_uuid(), p_workspace_id, stored.id, 'in_app', 'sent', 1, stored.created_at, stored.created_at, stored.created_at)
    ON CONFLICT (workspace_id, notification_id, channel) DO UPDATE
    SET status = 'sent', attempts = GREATEST(d.attempts, 1),
        sent_at = COALESCE(d.sent_at, EXCLUDED.sent_at),
        next_attempt_at = NULL, last_error_code = '', updated_at = EXCLUDED.updated_at;

    INSERT INTO public.notification_deliveries
        (id, workspace_id, notification_id, channel, status, attempts, next_attempt_at, created_at, updated_at)
    SELECT gen_random_uuid(), p_workspace_id, stored.id, 'websocket', 'pending', 0,
           stored.created_at, stored.created_at, stored.created_at
    WHERE COALESCE((
        SELECT preference.enabled FROM public.notification_preferences preference
        WHERE preference.workspace_id = p_workspace_id AND preference.user_id = stored.recipient_user_id
          AND preference.channel = 'websocket' AND preference.event_type IN (stored.event_type, '*')
        ORDER BY (preference.event_type = stored.event_type) DESC LIMIT 1
    ), true)
    ON CONFLICT (workspace_id, notification_id, channel) DO NOTHING;

    INSERT INTO public.notification_deliveries
        (id, workspace_id, notification_id, channel, status, attempts, next_attempt_at, created_at, updated_at)
    SELECT gen_random_uuid(), p_workspace_id, stored.id, 'email', 'pending', 0,
           stored.created_at, stored.created_at, stored.created_at
    WHERE COALESCE((
        SELECT preference.enabled FROM public.notification_preferences preference
        WHERE preference.workspace_id = p_workspace_id AND preference.user_id = stored.recipient_user_id
          AND preference.channel = 'email' AND preference.event_type IN (stored.event_type, '*')
        ORDER BY (preference.event_type = stored.event_type) DESC LIMIT 1
    ), false)
    ON CONFLICT (workspace_id, notification_id, channel) DO NOTHING;

    RETURN NEXT stored;
END
$$;

REVOKE ALL ON FUNCTION publish_notification(UUID, UUID, UUID, TEXT, TEXT, TEXT, JSONB, TIMESTAMPTZ, TEXT) FROM PUBLIC;
ALTER FUNCTION publish_notification(UUID, UUID, UUID, TEXT, TEXT, TEXT, JSONB, TIMESTAMPTZ, TEXT) OWNER TO ant_notification_publisher;
GRANT EXECUTE ON FUNCTION publish_notification(UUID, UUID, UUID, TEXT, TEXT, TEXT, JSONB, TIMESTAMPTZ, TEXT)
    TO ant_control_plane, ant_worker;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ant_proxy_health_sync') THEN
        CREATE ROLE ant_proxy_health_sync NOLOGIN NOSUPERUSER BYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO ant_proxy_health_sync;
GRANT SELECT, UPDATE ON proxy_health_checks TO ant_proxy_health_sync;

ALTER FUNCTION sync_proxy_health_task_terminal() SECURITY DEFINER;
ALTER FUNCTION sync_proxy_health_task_terminal() SET search_path = pg_catalog, public;
REVOKE ALL ON FUNCTION sync_proxy_health_task_terminal() FROM PUBLIC;
ALTER FUNCTION sync_proxy_health_task_terminal() OWNER TO ant_proxy_health_sync;
