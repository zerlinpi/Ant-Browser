package session

// Manager tracks browser runtime sessions.

type Session struct {
	ID string
	PID int
	CDP string
	Status string
}

type Manager struct {
	sessions map[string]Session
}

func New() *Manager {
	return &Manager{sessions: make(map[string]Session)}
}

func (m *Manager) Create(s Session) {
	m.sessions[s.ID] = s
}

func (m *Manager) Get(id string) (Session, bool) {
	s, ok := m.sessions[id]
	return s, ok
}
