package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

func (s *Store) CreateNotification(ctx context.Context, item notificationservice.Notification, idempotencyKey string) (notificationservice.Notification, error) {
	payload, err := json.Marshal(item.Payload)
	if err != nil {
		return notificationservice.Notification{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return notificationservice.Notification{}, err
	}
	defer rollback(ctx, tx)
	var existing notificationservice.Notification
	var existingPayload []byte
	err = tx.QueryRow(ctx, `INSERT INTO notifications
		(id,workspace_id,recipient_user_id,event_type,title,body,payload,created_at,idempotency_key)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8,$9)
		ON CONFLICT (workspace_id,recipient_user_id,idempotency_key) WHERE idempotency_key <> ''
		DO UPDATE SET id=notifications.id
		RETURNING id::text,workspace_id::text,recipient_user_id::text,event_type,title,body,payload,read_at,created_at`,
		item.ID, item.WorkspaceID, item.RecipientUserID, item.EventType, item.Title, item.Body, payload, item.CreatedAt, idempotencyKey,
	).Scan(&existing.ID, &existing.WorkspaceID, &existing.RecipientUserID, &existing.EventType, &existing.Title, &existing.Body, &existingPayload, &existing.ReadAt, &existing.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	if isForeignKeyViolation(err) {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	if err != nil {
		return notificationservice.Notification{}, err
	}
	_ = json.Unmarshal(existingPayload, &existing.Payload)
	if existing.EventType != item.EventType || existing.Title != item.Title || existing.Body != item.Body || payloadJSON(existing.Payload) != payloadJSON(item.Payload) {
		return notificationservice.Notification{}, notificationservice.ErrConflict
	}
	// The notification row itself is the completed in-app delivery. WebSocket
	// delivery defaults on unless the recipient disabled it; email is opt-in.
	if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries
		(id,workspace_id,notification_id,channel,status,attempts,sent_at,created_at,updated_at)
		VALUES (gen_random_uuid(),$1::uuid,$2::uuid,'in_app','sent',1,$3,$3,$3)
		ON CONFLICT (workspace_id,notification_id,channel) DO UPDATE
		SET status='sent',attempts=GREATEST(notification_deliveries.attempts,1),
			sent_at=COALESCE(notification_deliveries.sent_at,EXCLUDED.sent_at),
			next_attempt_at=NULL,last_error_code='',updated_at=EXCLUDED.updated_at`,
		item.WorkspaceID, existing.ID, existing.CreatedAt); err != nil {
		return notificationservice.Notification{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries
		(id,workspace_id,notification_id,channel,status,attempts,next_attempt_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1::uuid,$2::uuid,'websocket','pending',0,$4,$4,$4
		WHERE COALESCE((
			SELECT preference.enabled FROM notification_preferences preference
			WHERE preference.workspace_id=$1::uuid AND preference.user_id=$3::uuid
			  AND preference.channel='websocket' AND preference.event_type IN ($5,'*')
			ORDER BY (preference.event_type=$5) DESC LIMIT 1
		),true)
		ON CONFLICT (workspace_id,notification_id,channel) DO NOTHING`,
		item.WorkspaceID, existing.ID, existing.RecipientUserID, existing.CreatedAt, existing.EventType); err != nil {
		return notificationservice.Notification{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries
		(id,workspace_id,notification_id,channel,status,attempts,next_attempt_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1::uuid,$2::uuid,'email','pending',0,$4,$4,$4
		WHERE COALESCE((
			SELECT preference.enabled FROM notification_preferences preference
			WHERE preference.workspace_id=$1::uuid AND preference.user_id=$3::uuid
			  AND preference.channel='email' AND preference.event_type IN ($5,'*')
			ORDER BY (preference.event_type=$5) DESC LIMIT 1
		),false)
		ON CONFLICT (workspace_id,notification_id,channel) DO NOTHING`,
		item.WorkspaceID, existing.ID, existing.RecipientUserID, existing.CreatedAt, existing.EventType); err != nil {
		return notificationservice.Notification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return notificationservice.Notification{}, err
	}
	return existing, nil
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

func payloadJSON(payload map[string]interface{}) string {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
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
