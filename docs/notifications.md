# Notifications

The control-plane notification inbox is tenant-scoped and recipient-scoped.
Every read, count, and preference query requires an authenticated workspace
member and applies both `workspace_id` and `recipient_user_id` predicates.

User routes:

- `GET /api/v1/workspaces/{workspaceID}/notifications?limit=50&offset=0&unreadOnly=true`
- `GET /api/v1/workspaces/{workspaceID}/notifications/unread-count`
- `POST /api/v1/workspaces/{workspaceID}/notifications/{notificationID}/read`
- `POST /api/v1/workspaces/{workspaceID}/notifications/read-all`
- `GET /api/v1/workspaces/{workspaceID}/notification-preferences`
- `PUT /api/v1/workspaces/{workspaceID}/notification-preferences`

Notification creation is an internal event-producer operation. It requires a
recipient, an event type, and a source `IdempotencyKey`; reusing that key with
different content returns a conflict. The gateway does not accept arbitrary
notification creation from users. Current trusted producers publish:

- `account.risk_event` when an account risk event is recorded;
- `security.event` after a successful account sign-in for each workspace the
  user belongs to;
- `task.failed` after a task reaches a terminal failure;
- `proxy.health_failed` when a proxy probe fails or its task reaches a
  terminal failure.

`PUT /notification-preferences` accepts `{ "preferences": [...] }`. Each item
contains `channel`, `eventType`, and `enabled`; the service overwrites tenant
and user identity from the authenticated request. Exact event preferences win
over the `*` wildcard. WebSocket defaults on and email defaults off when no
preference exists.

The in-app record is written transactionally with durable delivery rows.
WebSocket delivery is dispatched through the shared realtime bus and the
client authenticates with a short-lived socket ticket, never a bearer token in
the URL. Email delivery is enabled only when SMTP is configured and the
recipient opted in. Missing external senders fail closed and remain visible to
the delivery retry/dead-letter workflow; the system never reports a fake send.
Push remains schema-reserved and has no sender.
