package runtime

// Injector connects fingerprint templates with browser runtime.
type Injector struct{}

func NewInjector() *Injector {
	return &Injector{}
}

// Inject will generate runtime patches for Chromium.
func (i *Injector) Inject(templateID string) error {
	// TODO:
	// 1. Load fingerprint template
	// 2. Generate JS patches
	// 3. Attach to browser runtime
	return nil
}
