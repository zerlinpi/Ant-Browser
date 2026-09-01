package canvas

// Generator creates deterministic canvas fingerprint configuration.
// The runtime injector will apply this configuration to Chromium.
type Generator struct{}

func NewGenerator() *Generator {
	return &Generator{}
}

func (g *Generator) Generate(seed string) string {
	return seed
}
