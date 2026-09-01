package webrtc

// Fingerprint stores WebRTC runtime configuration.
type Fingerprint struct {
	ICEPolicy string
	NetworkType string
	Enabled bool
}

func New(policy string, network string) Fingerprint {
	return Fingerprint{
		ICEPolicy: policy,
		NetworkType: network,
		Enabled: true,
	}
}
