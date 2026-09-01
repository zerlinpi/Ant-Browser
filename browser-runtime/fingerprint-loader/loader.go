package fingerprintloader

// Loader connects fingerprint templates with browser runtime.
// It keeps fingerprint generation separated from Chromium launching.

type Template struct {
	ID string
	Config map[string]interface{}
}

type Loader struct{}

func NewLoader() *Loader {
	return &Loader{}
}

func (l *Loader) Load(template Template) error {
	// TODO:
	// 1. Read fingerprint template
	// 2. Generate runtime configuration
	// 3. Pass configuration to injector
	return nil
}
