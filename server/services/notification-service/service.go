// Package notificationservice provides tenant- and recipient-scoped in-app
// notifications. Creation is intentionally an internal service operation;
// the gateway does not expose an arbitrary notification creation endpoint.
package notificationservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound    = errors.New("notification not found")
	ErrConflict    = errors.New("notification idempotency conflict")
	ErrInvalid     = errors.New("notification is invalid")
	ErrPreferences = errors.New("notification preferences are unavailable")
)

const ChannelInApp = "in_app"

type Notification struct {
	ID              string                 `json:"id"`
	WorkspaceID     string                 `json:"workspaceId"`
	RecipientUserID string                 `json:"recipientUserId"`
	EventType       string                 `json:"eventType"`
	Title           string                 `json:"title"`
	Body            string                 `json:"body"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
	ReadAt          *time.Time             `json:"readAt,omitempty"`
	CreatedAt       time.Time              `json:"createdAt"`
}

type NotificationPreference struct {
	WorkspaceID string    `json:"workspaceId"`
	UserID      string    `json:"userId"`
	Channel     string    `json:"channel"`
	EventType   string    `json:"eventType"`
	Enabled     bool      `json:"enabled"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CreateInput is consumed by trusted event producers, not by user-facing
// HTTP handlers. IdempotencyKey must identify the source event.
type CreateInput struct {
	WorkspaceID     string                 `json:"workspaceId"`
	RecipientUserID string                 `json:"recipientUserId"`
	EventType       string                 `json:"eventType"`
	Title           string                 `json:"title"`
	Body            string                 `json:"body"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
	IdempotencyKey  string                 `json:"-"`
}

type ListInput struct {
	Limit      int
	Offset     int
	UnreadOnly bool
}

type Page struct {
	Items       []Notification `json:"items"`
	NextOffset  int            `json:"nextOffset,omitempty"`
	UnreadCount int            `json:"unreadCount"`
}

type Repository interface {
	CreateNotification(context.Context, Notification, string) (Notification, error)
	ListNotifications(context.Context, string, string, int, int, bool) ([]Notification, error)
	CountUnreadNotifications(context.Context, string, string) (int, error)
	MarkNotificationRead(context.Context, string, string, string, time.Time) (Notification, error)
	MarkAllNotificationsRead(context.Context, string, string, time.Time) (int, error)
	ListNotificationPreferences(context.Context, string, string) ([]NotificationPreference, error)
	UpsertNotificationPreferences(context.Context, string, string, []NotificationPreference, time.Time) ([]NotificationPreference, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now}
}

// Create persists a notification from a trusted producer. It is deliberately
// not registered as a gateway route, so ordinary users cannot forge events.
func (s *Service) Create(ctx context.Context, input CreateInput) (Notification, error) {
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	recipientID := strings.TrimSpace(input.RecipientUserID)
	eventType := strings.TrimSpace(input.EventType)
	key := strings.TrimSpace(input.IdempotencyKey)
	if workspaceID == "" || recipientID == "" || eventType == "" || strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Body) == "" {
		return Notification{}, errors.New("workspaceId, recipientUserId, eventType, title and body are required")
	}
	if key == "" || len(key) > 200 {
		return Notification{}, errors.New("notification idempotency key is required")
	}
	now := s.now().UTC()
	n := Notification{ID: uuid.NewString(), WorkspaceID: workspaceID, RecipientUserID: recipientID,
		EventType: eventType, Title: strings.TrimSpace(input.Title), Body: strings.TrimSpace(input.Body),
		Payload: clonePayload(input.Payload), CreatedAt: now}
	return s.repository.CreateNotification(ctx, n, key)
}

// Publish is the domain-oriented alias used by event producers.
func (s *Service) Publish(ctx context.Context, input CreateInput) (Notification, error) {
	return s.Create(ctx, input)
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string, input ListInput) (Page, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return Page{}, err
	}
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 50
	}
	if input.Offset < 0 {
		input.Offset = 0
	}
	items, err := s.repository.ListNotifications(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), input.Limit, input.Offset, input.UnreadOnly)
	if err != nil {
		return Page{}, err
	}
	unread, err := s.repository.CountUnreadNotifications(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID))
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: items, UnreadCount: unread}
	if len(items) == input.Limit {
		page.NextOffset = input.Offset + len(items)
	}
	return page, nil
}

func (s *Service) ListNotifications(ctx context.Context, actorID, workspaceID string, input ListInput) (Page, error) {
	return s.List(ctx, actorID, workspaceID, input)
}

func (s *Service) UnreadCount(ctx context.Context, actorID, workspaceID string) (int, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return 0, err
	}
	return s.repository.CountUnreadNotifications(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID))
}

// Authorize validates a recipient-scoped streaming connection without
// performing an unrelated notification query.
func (s *Service) Authorize(ctx context.Context, actorID, workspaceID string) error {
	return s.require(ctx, actorID, workspaceID)
}

func (s *Service) MarkRead(ctx context.Context, actorID, workspaceID, notificationID string) (Notification, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return Notification{}, err
	}
	if strings.TrimSpace(notificationID) == "" {
		return Notification{}, ErrNotFound
	}
	return s.repository.MarkNotificationRead(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), strings.TrimSpace(notificationID), s.now().UTC())
}

func (s *Service) MarkAllRead(ctx context.Context, actorID, workspaceID string) (int, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return 0, err
	}
	return s.repository.MarkAllNotificationsRead(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), s.now().UTC())
}

func (s *Service) Preferences(ctx context.Context, actorID, workspaceID string) ([]NotificationPreference, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	return s.repository.ListNotificationPreferences(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID))
}

func (s *Service) GetPreferences(ctx context.Context, actorID, workspaceID string) ([]NotificationPreference, error) {
	return s.Preferences(ctx, actorID, workspaceID)
}

func (s *Service) UpdatePreferences(ctx context.Context, actorID, workspaceID string, preferences []NotificationPreference) ([]NotificationPreference, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	if len(preferences) > 100 {
		return nil, errors.New("at most 100 notification preferences are allowed")
	}
	for i := range preferences {
		preferences[i].WorkspaceID = strings.TrimSpace(workspaceID)
		preferences[i].UserID = strings.TrimSpace(actorID)
		preferences[i].Channel = strings.ToLower(strings.TrimSpace(preferences[i].Channel))
		preferences[i].EventType = strings.TrimSpace(preferences[i].EventType)
		if preferences[i].Channel != ChannelInApp && preferences[i].Channel != "email" && preferences[i].Channel != "websocket" && preferences[i].Channel != "push" {
			return nil, errors.New("notification channel is invalid")
		}
		if preferences[i].EventType == "" {
			return nil, errors.New("notification eventType is required")
		}
		preferences[i].UpdatedAt = s.now().UTC()
	}
	return s.repository.UpsertNotificationPreferences(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), preferences, s.now().UTC())
}

func (s *Service) require(ctx context.Context, actorID, workspaceID string) error {
	actorID, workspaceID = strings.TrimSpace(actorID), strings.TrimSpace(workspaceID)
	if actorID == "" || workspaceID == "" {
		return errors.New("workspaceId and actorId are required")
	}
	if s.authorizer == nil {
		return errors.New("authorizer is not configured")
	}
	return s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionWorkspaceRead)
}

func clonePayload(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
