-- v017: make trusted notification publication safely idempotent.
ALTER TABLE notifications ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX notifications_idempotency_uq
    ON notifications (workspace_id, recipient_user_id, idempotency_key)
    WHERE idempotency_key <> '';
