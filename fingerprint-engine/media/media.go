package media

// MediaFingerprint describes available media device profile.
type MediaFingerprint struct {
	Camera bool
	Microphone bool
	Speaker bool
}

func Default() MediaFingerprint {
	return MediaFingerprint{
		Camera: true,
		Microphone: true,
		Speaker: true,
	}
}
