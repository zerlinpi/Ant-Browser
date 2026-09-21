package realtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrPresenceNotFound = errors.New("device presence not found")

type DevicePresence struct {
	DeviceID     string    `json:"deviceId"`
	WorkspaceID  string    `json:"workspaceId"`
	NodeID       string    `json:"nodeId"`
	ConnectionID string    `json:"connectionId"`
	ConnectedAt  time.Time `json:"connectedAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

type CommandHandler func(deviceID string, payload []byte)

type UserNotification struct {
	WorkspaceID string
	UserID      string
	Payload     []byte
}

type NotificationHandler func(UserNotification)

type Bus interface {
	Ping(context.Context) error
	SetPresence(context.Context, DevicePresence, time.Duration) error
	RefreshPresence(context.Context, string, string, time.Time, time.Duration) error
	RemovePresence(context.Context, string, string) error
	Presence(context.Context, string) (DevicePresence, error)
	PublishCommand(context.Context, string, []byte) error
	SubscribeCommands(context.Context, CommandHandler) error
	PublishNotification(context.Context, UserNotification) error
	SubscribeNotifications(context.Context, NotificationHandler) error
	Close() error
}

type Disabled struct{}

func NewDisabled() Disabled { return Disabled{} }

func (Disabled) Ping(context.Context) error { return nil }
func (Disabled) SetPresence(context.Context, DevicePresence, time.Duration) error {
	return nil
}
func (Disabled) RefreshPresence(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}
func (Disabled) RemovePresence(context.Context, string, string) error { return nil }
func (Disabled) Presence(context.Context, string) (DevicePresence, error) {
	return DevicePresence{}, ErrPresenceNotFound
}
func (Disabled) PublishCommand(context.Context, string, []byte) error { return nil }
func (Disabled) SubscribeCommands(context.Context, CommandHandler) error {
	return nil
}
func (Disabled) PublishNotification(context.Context, UserNotification) error { return nil }
func (Disabled) SubscribeNotifications(context.Context, NotificationHandler) error {
	return nil
}
func (Disabled) Close() error { return nil }

type Memory struct {
	mu                      sync.RWMutex
	presences               map[string]memoryPresence
	subscribers             map[int]CommandHandler
	notificationSubscribers map[int]NotificationHandler
	nextID                  int
	closed                  chan struct{}
	closeOnce               sync.Once
}

type memoryPresence struct {
	value     DevicePresence
	expiresAt time.Time
}

func NewMemory() *Memory {
	return &Memory{
		presences: make(map[string]memoryPresence), subscribers: make(map[int]CommandHandler),
		notificationSubscribers: make(map[int]NotificationHandler),
		closed:                  make(chan struct{}),
	}
}

func (m *Memory) Ping(context.Context) error {
	select {
	case <-m.closed:
		return errors.New("realtime bus is closed")
	default:
		return nil
	}
}

func (m *Memory) SetPresence(_ context.Context, value DevicePresence, ttl time.Duration) error {
	m.mu.Lock()
	m.presences[value.DeviceID] = memoryPresence{value: value, expiresAt: time.Now().Add(ttl)}
	m.mu.Unlock()
	return nil
}

func (m *Memory) RefreshPresence(_ context.Context, deviceID, connectionID string, seenAt time.Time, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.presences[deviceID]
	if !ok || current.value.ConnectionID != connectionID || time.Now().After(current.expiresAt) {
		return ErrPresenceNotFound
	}
	current.value.LastSeenAt = seenAt
	current.expiresAt = time.Now().Add(ttl)
	m.presences[deviceID] = current
	return nil
}

func (m *Memory) RemovePresence(_ context.Context, deviceID, connectionID string) error {
	m.mu.Lock()
	if current, ok := m.presences[deviceID]; ok && current.value.ConnectionID == connectionID {
		delete(m.presences, deviceID)
	}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Presence(_ context.Context, deviceID string) (DevicePresence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.presences[deviceID]
	if !ok {
		return DevicePresence{}, ErrPresenceNotFound
	}
	if time.Now().After(current.expiresAt) {
		delete(m.presences, deviceID)
		return DevicePresence{}, ErrPresenceNotFound
	}
	return current.value, nil
}

func (m *Memory) PublishCommand(_ context.Context, deviceID string, payload []byte) error {
	m.mu.RLock()
	handlers := make([]CommandHandler, 0, len(m.subscribers))
	for _, handler := range m.subscribers {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		handler(deviceID, append([]byte(nil), payload...))
	}
	return nil
}

func (m *Memory) SubscribeCommands(ctx context.Context, handler CommandHandler) error {
	if handler == nil {
		return errors.New("command handler is required")
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.subscribers[id] = handler
	m.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-m.closed:
		}
		m.mu.Lock()
		delete(m.subscribers, id)
		m.mu.Unlock()
	}()
	return nil
}

func (m *Memory) PublishNotification(_ context.Context, value UserNotification) error {
	m.mu.RLock()
	handlers := make([]NotificationHandler, 0, len(m.notificationSubscribers))
	for _, handler := range m.notificationSubscribers {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		copyValue := value
		copyValue.Payload = append([]byte(nil), value.Payload...)
		handler(copyValue)
	}
	return nil
}

func (m *Memory) SubscribeNotifications(ctx context.Context, handler NotificationHandler) error {
	if handler == nil {
		return errors.New("notification handler is required")
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.notificationSubscribers[id] = handler
	m.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-m.closed:
		}
		m.mu.Lock()
		delete(m.notificationSubscribers, id)
		m.mu.Unlock()
	}()
	return nil
}

func (m *Memory) Close() error {
	m.closeOnce.Do(func() { close(m.closed) })
	return nil
}
