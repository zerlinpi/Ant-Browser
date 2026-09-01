package model

// CloudProfile represents a synchronized browser profile.
type CloudProfile struct {
	ID string
	WorkspaceID string
	Name string
	FingerprintID string
	OwnerID uint
}

// ProfileVersion tracks profile snapshots.
type ProfileVersion struct {
	ID string
	ProfileID string
	Version int
	Hash string
	CreatedAt string
}
