package profile

// Loader manages browser profile loading.
// It will bridge cloud profile sync data with local Chromium runtime.

type Profile struct {
	ID string
	Path string
	Version string
}

type Loader struct{}

func New() *Loader {
	return &Loader{}
}

func (l *Loader) Load(profile Profile) error {
	// TODO:
	// 1. Validate profile
	// 2. Restore cookies/localStorage/indexedDB
	// 3. Prepare Chromium user-data-dir
	return nil
}
