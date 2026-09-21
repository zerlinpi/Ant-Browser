package session

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrInvalidSession = errors.New("invalid browser session")

type Session struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	CDP       string    `json:"cdp"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt,omitempty"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func New() *Manager { return &Manager{sessions: make(map[string]Session)} }

// Create preserves the scaffold API and atomically replaces an existing ID.
func (m *Manager) Create(s Session) { _ = m.Upsert(s) }

func (m *Manager) Upsert(s Session) error {
	s.ID = strings.TrimSpace(s.ID)
	s.Status = strings.TrimSpace(s.Status)
	if s.ID == "" || len(s.ID) > 128 || strings.ContainsAny(s.ID, "\x00\r\n") {
		return ErrInvalidSession
	}
	if s.PID < 0 || len(s.CDP) > 2048 || strings.ContainsAny(s.CDP, "\x00\r\n") {
		return ErrInvalidSession
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (m *Manager) Get(id string) (Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[strings.TrimSpace(id)]
	return s, ok
}

func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.TrimSpace(id)
	if _, ok := m.sessions[key]; !ok {
		return false
	}
	delete(m.sessions, key)
	return true
}

func (m *Manager) List() []Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

func (m *Manager) SetStatus(id, status string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[strings.TrimSpace(id)]
	if !ok {
		return false
	}
	s.Status = strings.TrimSpace(status)
	m.sessions[s.ID] = s
	return true
}
