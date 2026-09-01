package browser

// BrowserManager is the desktop client abstraction layer.
// It will connect enterprise accounts with local browser profiles.
//
// Flow:
// Account -> Profile -> Proxy -> Chromium Instance

type ProfileConfig struct {
	ID string
	Name string
	Path string
	Proxy string
}

type BrowserManager struct {
}

func NewBrowserManager() *BrowserManager {
	return &BrowserManager{}
}

// Start will be connected with the existing Ant-Browser launcher.
func (b *BrowserManager) Start(profile ProfileConfig) error {
	// TODO:
	// 1. Load profile data
	// 2. Apply proxy settings
	// 3. Launch Chromium
	// 4. Return CDP endpoint
	return nil
}
