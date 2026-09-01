package model

// Account represents a cross-border commerce account.
type Account struct {
	ID                  uint64
	WorkspaceID         uint64
	Platform            string
	Username            string
	Email               string
	ProxyID             uint64
	BrowserInstanceID   uint64
	Status              string
	RiskLevel           string
	Notes               string
}
