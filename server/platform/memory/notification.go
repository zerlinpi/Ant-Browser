package memory

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

func notificationIdempotencyKey(workspaceID, recipientID, key string) string {
	return workspaceID + "\x00" + recipientID + "\x00" + key
}

func notificationPreferenceKey(workspaceID, userID, channel, eventType string) string {
	return workspaceID + "\x00" + userID + "\x00" + channel + "\x00" + eventType
}

func cloneNotification(item notificationservice.Notification) notificationservice.Notification {
	item.Payload = cloneNotificationPayload(item.Payload)
	if item.ReadAt != nil {
		readAt := *item.ReadAt
		item.ReadAt = &readAt
	}
	return item
}

func cloneNotificationPayload(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (s *Store) CreateNotification(_ context.Context, item notificationservice.Notification, idempotencyKey string) (notificationservice.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := notificationIdempotencyKey(item.WorkspaceID, item.RecipientUserID, idempotencyKey)
	if existingID, ok := s.notificationIdempotency[key]; ok {
		existing := s.notifications[existingID]
		if existing.EventType != item.EventType || existing.Title != item.Title || existing.Body != item.Body || payloadJSON(existing.Payload) != payloadJSON(item.Payload) {
			return notificationservice.Notification{}, notificationservice.ErrConflict
		}
		return cloneNotification(existing), nil
	}
	if _, ok := s.workspaces[item.WorkspaceID]; !ok {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	membership, ok := s.memberships[membershipKey(item.WorkspaceID, item.RecipientUserID)]
	if !ok || membership.Status != "active" {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	s.notifications[item.ID] = cloneNotification(item)
	s.notificationIdempotency[key] = item.ID
	return cloneNotification(item), nil
}

func (s *Store) ListNotifications(_ context.Context, workspaceID, recipientID string, limit, offset int, unreadOnly bool) ([]notificationservice.Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]notificationservice.Notification, 0)
	for _, item := range s.notifications {
		if item.WorkspaceID != workspaceID || item.RecipientUserID != recipientID || (unreadOnly && item.ReadAt != nil) {
			continue
		}
		items = append(items, cloneNotification(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if offset >= len(items) {
		return []notificationservice.Notification{}, nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end], nil
}

func (s *Store) CountUnreadNotifications(_ context.Context, workspaceID, recipientID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, item := range s.notifications {
		if item.WorkspaceID == workspaceID && item.RecipientUserID == recipientID && item.ReadAt == nil {
			count++
		}
	}
	return count, nil
}

func (s *Store) MarkNotificationRead(_ context.Context, workspaceID, recipientID, notificationID string, now time.Time) (notificationservice.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.notifications[notificationID]
	if !ok || item.WorkspaceID != workspaceID || item.RecipientUserID != recipientID {
		return notificationservice.Notification{}, notificationservice.ErrNotFound
	}
	if item.ReadAt == nil {
		item.ReadAt = &now
		s.notifications[notificationID] = cloneNotification(item)
	}
	return cloneNotification(item), nil
}

func (s *Store) MarkAllNotificationsRead(_ context.Context, workspaceID, recipientID string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, item := range s.notifications {
		if item.WorkspaceID == workspaceID && item.RecipientUserID == recipientID && item.ReadAt == nil {
			item.ReadAt = &now
			s.notifications[id] = cloneNotification(item)
			count++
		}
	}
	return count, nil
}

func (s *Store) ListNotificationPreferences(_ context.Context, workspaceID, userID string) ([]notificationservice.NotificationPreference, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]notificationservice.NotificationPreference, 0)
	for _, item := range s.notificationPreferences {
		if item.WorkspaceID == workspaceID && item.UserID == userID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Channel == items[j].Channel {
			return items[i].EventType < items[j].EventType
		}
		return items[i].Channel < items[j].Channel
	})
	return items, nil
}

func (s *Store) UpsertNotificationPreferences(_ context.Context, workspaceID, userID string, preferences []notificationservice.NotificationPreference, now time.Time) ([]notificationservice.NotificationPreference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.memberships[membershipKey(workspaceID, userID)]
	if !ok || membership.Status != "active" {
		return nil, notificationservice.ErrNotFound
	}
	for _, item := range preferences {
		item.WorkspaceID, item.UserID, item.UpdatedAt = workspaceID, userID, now
		s.notificationPreferences[notificationPreferenceKey(workspaceID, userID, item.Channel, item.EventType)] = item
	}
	items := make([]notificationservice.NotificationPreference, 0)
	for _, item := range s.notificationPreferences {
		if item.WorkspaceID == workspaceID && item.UserID == userID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Channel+items[i].EventType < items[j].Channel+items[j].EventType })
	return items, nil
}

func payloadJSON(payload map[string]interface{}) string {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
