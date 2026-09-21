package notificationservice

import (
	"context"
	"errors"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	"testing"
	"time"
)

// notificationTestRepo is intentionally small and exercises the service's
// scope arguments; adapter-level tests cover SQL predicates separately.
type notificationTestRepo struct {
	items map[string]Notification
	keys  map[string]string
}

func (r *notificationTestRepo) CreateNotification(_ context.Context, item Notification, key string) (Notification, error) {
	scope := item.WorkspaceID + ":" + item.RecipientUserID + ":" + key
	if id := r.keys[scope]; id != "" {
		current := r.items[id]
		if current.Title != item.Title {
			return Notification{}, ErrConflict
		}
		return current, nil
	}
	r.keys[scope] = item.ID
	r.items[item.ID] = item
	return item, nil
}
func (r *notificationTestRepo) ListNotifications(_ context.Context, ws, recipient string, limit, offset int, unread bool) ([]Notification, error) {
	result := make([]Notification, 0)
	for _, item := range r.items {
		if item.WorkspaceID == ws && item.RecipientUserID == recipient && (!unread || item.ReadAt == nil) {
			result = append(result, item)
		}
	}
	if offset >= len(result) {
		return result[:0], nil
	}
	end := offset + limit
	if end > len(result) {
		end = len(result)
	}
	return result[offset:end], nil
}
func (r *notificationTestRepo) CountUnreadNotifications(_ context.Context, ws, recipient string) (int, error) {
	count := 0
	for _, item := range r.items {
		if item.WorkspaceID == ws && item.RecipientUserID == recipient && item.ReadAt == nil {
			count++
		}
	}
	return count, nil
}
func (r *notificationTestRepo) MarkNotificationRead(_ context.Context, ws, recipient, id string, now time.Time) (Notification, error) {
	item, ok := r.items[id]
	if !ok || item.WorkspaceID != ws || item.RecipientUserID != recipient {
		return Notification{}, ErrNotFound
	}
	if item.ReadAt == nil {
		item.ReadAt = &now
		r.items[id] = item
	}
	return item, nil
}
func (r *notificationTestRepo) MarkAllNotificationsRead(_ context.Context, ws, recipient string, now time.Time) (int, error) {
	count := 0
	for id, item := range r.items {
		if item.WorkspaceID == ws && item.RecipientUserID == recipient && item.ReadAt == nil {
			item.ReadAt = &now
			r.items[id] = item
			count++
		}
	}
	return count, nil
}
func (r *notificationTestRepo) ListNotificationPreferences(context.Context, string, string) ([]NotificationPreference, error) {
	return []NotificationPreference{}, nil
}
func (r *notificationTestRepo) UpsertNotificationPreferences(context.Context, string, string, []NotificationPreference, time.Time) ([]NotificationPreference, error) {
	return nil, errors.New("not implemented")
}

type allowNotifications struct{}

func (allowNotifications) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

func TestServiceScopesReadsAndIdempotency(t *testing.T) {
	repo := &notificationTestRepo{items: map[string]Notification{}, keys: map[string]string{}}
	svc := New(repo, allowNotifications{})
	first, err := svc.Publish(context.Background(), CreateInput{WorkspaceID: "workspace-a", RecipientUserID: "user-a", EventType: "risk", Title: "Risk", Body: "Review", IdempotencyKey: "risk-1"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Publish(context.Background(), CreateInput{WorkspaceID: "workspace-a", RecipientUserID: "user-a", EventType: "risk", Title: "Risk", Body: "Review", IdempotencyKey: "risk-1"})
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotency replay = %+v, %v", replay, err)
	}
	if _, err := svc.Publish(context.Background(), CreateInput{WorkspaceID: "workspace-a", RecipientUserID: "user-a", EventType: "risk", Title: "Changed", Body: "Review", IdempotencyKey: "risk-1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("content reuse error = %v", err)
	}
	other, err := svc.List(context.Background(), "user-b", "workspace-a", ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("cross-recipient notification leaked: %+v", other.Items)
	}
	read, err := svc.MarkRead(context.Background(), "user-a", "workspace-a", first.ID)
	if err != nil || read.ReadAt == nil {
		t.Fatalf("mark read = %+v, %v", read, err)
	}
	if _, err := svc.MarkRead(context.Background(), "user-b", "workspace-a", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-recipient mark read error = %v", err)
	}
}
