package navigator

// NavigatorFingerprint describes browser navigator characteristics.
type NavigatorFingerprint struct {
	UserAgent string
	Platform string
	Languages []string
	HardwareConcurrency int
	DeviceMemory int
}

func Default() NavigatorFingerprint {
	return NavigatorFingerprint{
		Platform: "Windows",
		Languages: []string{"en-US"},
		HardwareConcurrency: 8,
		DeviceMemory: 16,
	}
}
