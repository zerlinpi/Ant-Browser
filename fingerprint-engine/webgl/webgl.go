package webgl

// Profile describes GPU/WebGL fingerprint settings.
type Profile struct {
	Vendor   string
	Renderer string
}

func DefaultProfile() Profile {
	return Profile{}
}
