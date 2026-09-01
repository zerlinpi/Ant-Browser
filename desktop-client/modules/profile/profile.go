package profile

// Profile represents an isolated browser environment.
// It is the core asset managed by the enterprise client.
//
// Account -> Profile -> Browser Instance

type Profile struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Platform string `json:"platform"`
	DataPath string `json:"data_path"`
	ProxyID string `json:"proxy_id"`
	FingerprintID string `json:"fingerprint_id"`
	Status string `json:"status"`
}

// ProfileManager controls local browser environments.
type ProfileManager struct {
	profiles map[string]Profile
}

func NewProfileManager() *ProfileManager {
	return &ProfileManager{
		profiles: make(map[string]Profile),
	}
}

func (m *ProfileManager) Add(profile Profile) {
	m.profiles[profile.ID] = profile
}

func (m *ProfileManager) Get(id string) (Profile, bool) {
	p, ok := m.profiles[id]
	return p, ok
}
