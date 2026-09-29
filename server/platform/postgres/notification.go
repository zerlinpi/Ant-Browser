package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

// CreateNotification stores a notification with its in-app, WebSocket and
// email deliveries through publish_notification (migration 032). The
// function runs as a dedicated role, so ant_worker can publish without any
// privilege on the notification tables, while the tenant policies still pin
// every row to the workspace the caller's transaction is scoped to. A replay
// of an idempotency key returns the stored notification when it matches and
// ErrConflict otherwise.
func (s *Store) CreateNotification(ctx context.Context, item notificationservice.Notification, idempotencyKey string) (notificationservice.Notification, error) {
	payload, err := json.Marshal(item.Payload)
	if err != nil {
		return notificationservice.Notification{}, err
	}
	var stored notificationservice.Notification
	var storedPayload []byte
	err = s.pool.QueryRow(ctx, `SELECT id::text,workspace_id::text,recipient_user_id::text,event_type,title,body,payload,read_at,created_at
		FROM publish_notification($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8,$9)`,
		item.ID, item.WorkspaceID, item.RecipientUserID, item.EventType, item.Title, item.Body, payload, item.CreatedAt, idempotencyKey,
	).Scan(&stored.ID, &stored.WorkspaceID, &stored.RecipientUserID, &stored.EventType, &stored.Title, &stored.Body, &storedPayload, &stored.ReadAt, &stored.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isForeignKeyViolation(err):
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	case isNotificationConflict(err):
		return notificationservice.Notification{}, notificationservice.ErrConflict
	case err != nil:
		return notificationservice.Notification{}, err
	}
	_ = json.Unmarshal(storedPayload, &stored.Payload)
	return stored, nil
}

// isNotificationConflict reports publish_notification's idempotency error.
func isNotificationConflict(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "P0001" && pgError.ConstraintName == "notification_idempotency_conflict"
}

func (s *Store) ListNotifications(ctx context.Context, workspaceID, recipientID string, limit, offset int, unreadOnly bool) ([]notificationservice.Notification, error) {
	query := `SELECT id::text,workspace_id::text,recipient_user_id::text,event_type,title,body,payload,read_at,created_at
		FROM notifications WHERE workspace_id=$1::uuid AND recipient_user_id=$2::uuid`
	if unreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`
	rows, err := s.pool.Query(ctx, query, workspaceID, recipientID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]notificationservice.Notification, 0)
	for rows.Next() {
		item, scanErr := scanNotification(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CountUnreadNotifications(ctx context.Context, workspaceID, recipientID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1::uuid AND recipient_user_id=$2::uuid AND read_at IS NULL`, workspaceID, recipientID).Scan(&count)
	return count, err
}

func (s *Store) MarkNotificationRead(ctx context.Context, workspaceID, recipientID, notificationID string, now time.Time) (notificationservice.Notification, error) {
	var item notificationservice.Notification
	var payload []byte
	err := s.pool.QueryRow(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,$4)
		WHERE workspace_id=$1::uuid AND recipient_user_id=$2::uuid AND id=$3::uuid
		RETURNING id::text,workspace_id::text,recipient_user_id::text,event_type,title,body,payload,read_at,created_at`, workspaceID, recipientID, notificationID, now).
		Scan(&item.ID, &item.WorkspaceID, &item.RecipientUserID, &item.EventType, &item.Title, &item.Body, &payload, &item.ReadAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	if err != nil {
		return notificationservice.Notification{}, err
	}
	_ = json.Unmarshal(payload, &item.Payload)
	return item, nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, workspaceID, recipientID string, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at=$3 WHERE workspace_id=$1::uuid AND recipient_user_id=$2::uuid AND read_at IS NULL`, workspaceID, recipientID, now)
	return int(tag.RowsAffected()), err
}

func (s *Store) ListNotificationPreferences(ctx context.Context, workspaceID, userID string) ([]notificationservice.NotificationPreference, error) {
	rows, err := s.pool.Query(ctx, `SELECT workspace_id::text,user_id::text,channel,event_type,enabled,updated_at
		FROM notification_preferences WHERE workspace_id=$1::uuid AND user_id=$2::uuid ORDER BY channel,event_type`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]notificationservice.NotificationPreference, 0)
	for rows.Next() {
		var item notificationservice.NotificationPreference
		if err := rows.Scan(&item.WorkspaceID, &item.UserID, &item.Channel, &item.EventType, &item.Enabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertNotificationPreferences(ctx context.Context, workspaceID, userID string, preferences []notificationservice.NotificationPreference, now time.Time) ([]notificationservice.NotificationPreference, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	for _, item := range preferences {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_preferences
			(workspace_id,user_id,channel,event_type,enabled,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6)
			ON CONFLICT (workspace_id,user_id,channel,event_type) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=EXCLUDED.updated_at`,
			workspaceID, userID, item.Channel, item.EventType, item.Enabled, now); err != nil {
			if isForeignKeyViolation(err) {
				return nil, notificationservice.ErrNotFound
			}
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListNotificationPreferences(ctx, workspaceID, userID)
}

func scanNotification(row scanner) (notificationservice.Notification, error) {
	var item notificationservice.Notification
	var payload []byte
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.RecipientUserID, &item.EventType, &item.Title, &item.Body, &payload, &item.ReadAt, &item.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notificationservice.Notification{}, notificationservice.ErrNotFound
		}
		return notificationservice.Notification{}, err
	}
	_ = json.Unmarshal(payload, &item.Payload)
	return item, nil
}

func (s *Store) ClaimNotificationDeliveries(ctx context.Context, workerID string, channels []string, limit int, leaseTTL time.Duration, _ time.Time) ([]notificationservice.Delivery, error) {
	rows, err := s.pool.Query(ctx, `SELECT
		id::text,workspace_id::text,notification_id::text,recipient_user_id::text,
		recipient_email,channel,event_type,title,body,payload,created_at,
		attempts,lease_owner,lease_expires_at
		FROM claim_notification_deliveries($1,$2::text[],$3,$4)`,
		workerID, channels, limit, int(leaseTTL/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]notificationservice.Delivery, 0)
	for rows.Next() {
		var item notificationservice.Delivery
		var payload []byte
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.NotificationID,
			&item.RecipientUserID, &item.RecipientEmail, &item.Channel,
			&item.EventType, &item.Title, &item.Body, &payload,
			&item.CreatedAt, &item.Attempts, &item.LeaseOwner, &item.LeaseExpiresAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CompleteNotificationDelivery(ctx context.Context, item notificationservice.Delivery, finishedAt time.Time) error {
	var completed bool
	if err := s.pool.QueryRow(ctx, `SELECT complete_notification_delivery($1::uuid,$2::uuid,$3,$4)`,
		item.ID, item.WorkspaceID, item.LeaseOwner, finishedAt).Scan(&completed); err != nil {
		return err
	}
	if !completed {
		return notificationservice.ErrDeliveryLease
	}
	return nil
}

func (s *Store) FailNotificationDelivery(ctx context.Context, item notificationservice.Delivery, errorCode string, discard bool, retryAt, finishedAt time.Time) error {
	var failed bool
	if err := s.pool.QueryRow(ctx, `SELECT fail_notification_delivery($1::uuid,$2::uuid,$3,$4,$5,$6,$7)`,
		item.ID, item.WorkspaceID, item.LeaseOwner, errorCode, discard, retryAt, finishedAt).Scan(&failed); err != nil {
		return err
	}
	if !failed {
		return notificationservice.ErrDeliveryLease
	}
	return nil
}
