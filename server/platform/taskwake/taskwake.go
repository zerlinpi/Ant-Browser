package taskwake

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Signal struct {
	WorkspaceID string    `json:"workspaceId"`
	TaskID      string    `json:"taskId"`
	TaskType    string    `json:"taskType"`
	QueuedAt    time.Time `json:"queuedAt"`
}

type Handler func(Signal)

type Bus interface {
	Ping(context.Context) error
	Notify(context.Context, Signal) error
	Subscribe(context.Context, Handler) error
	Close() error
}

type Disabled struct{}

func NewDisabled() Disabled                               { return Disabled{} }
func (Disabled) Ping(context.Context) error               { return nil }
func (Disabled) Notify(context.Context, Signal) error     { return nil }
func (Disabled) Subscribe(context.Context, Handler) error { return nil }
func (Disabled) Close() error                             { return nil }

type Memory struct {
	mu          sync.RWMutex
	subscribers map[int]Handler
	nextID      int
	closed      chan struct{}
	closeOnce   sync.Once
}

func NewMemory() *Memory {
	return &Memory{subscribers: make(map[int]Handler), closed: make(chan struct{})}
}

func (m *Memory) Ping(context.Context) error {
	select {
	case <-m.closed:
		return errors.New("task wake bus is closed")
	default:
		return nil
	}
}

func (m *Memory) Notify(_ context.Context, signal Signal) error {
	m.mu.RLock()
	handlers := make([]Handler, 0, len(m.subscribers))
	for _, handler := range m.subscribers {
		handlers = append(handlers, handler)
	}
	m.mu.RUnlock()
	for _, handler := range handlers {
		handler(signal)
	}
	return nil
}

func (m *Memory) Subscribe(ctx context.Context, handler Handler) error {
	if handler == nil {
		return errors.New("task wake handler is required")
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

func (m *Memory) Close() error {
	m.closeOnce.Do(func() { close(m.closed) })
	return nil
}
