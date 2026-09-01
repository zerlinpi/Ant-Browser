package session

import "time"

// BrowserSession represents a running browser instance.
// It connects account, profile and local Chromium process.
type BrowserSession struct {
	ID string

	AccountID string
	ProfileID string

	PID int
	CDPEndpoint string

	Status string

	CreatedAt time.Time
}

// SessionManager controls browser lifecycle records.
type SessionManager struct {
	sessions map[string]*BrowserSession
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*BrowserSession),
	}
}

func (m *SessionManager) Create(accountID string, profileID string) *BrowserSession {
	s := &BrowserSession{
		ID:        accountID + "-" + profileID,
		AccountID: accountID,
		ProfileID: profileID,
		Status:    "created",
		CreatedAt: time.Now(),
	}

	m.sessions[s.ID] = s

	return s
}

func (m *SessionManager) Get(id string) *BrowserSession {
	return m.sessions[id]
}
