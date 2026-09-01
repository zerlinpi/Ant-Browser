package session

// StartFlow describes the enterprise browser startup pipeline.
// Instance -> Profile -> Fingerprint -> Chromium -> CDP

type StartFlow struct{}

func NewStartFlow() *StartFlow {
	return &StartFlow{}
}

func (s *StartFlow) Start(instanceID string) error {
	// TODO:
	// 1. Load instance configuration
	// 2. Load profile
	// 3. Apply fingerprint template
	// 4. Launch Chromium
	// 5. Create CDP session
	return nil
}
