package audio

// AudioFingerprint defines AudioContext fingerprint configuration.
// Runtime injection will consume this configuration later.
type AudioFingerprint struct {
	Seed string
	Noise float64
}
