package hardware

// Profile represents hardware fingerprint settings.
type Profile struct {
	CPU         int
	Memory      int
	GPU         string
	ScreenWidth int
	ScreenHeight int
}

func DefaultProfile() Profile {
	return Profile{}
}
