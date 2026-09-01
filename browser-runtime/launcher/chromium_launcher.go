package launcher

// ChromiumLauncher abstracts Chromium process lifecycle.
// Existing Ant-Browser chrome logic can be connected here without replacement.

type Config struct {
	Executable string
	ProfilePath string
	Proxy string
	RemoteDebugPort int
	Args []string
}

type Launcher struct{}

func New() *Launcher {
	return &Launcher{}
}

func (l *Launcher) Start(cfg Config) error {
	// TODO:
	// 1. Build chromium arguments
	// 2. Load profile
	// 3. Start process
	// 4. Expose CDP endpoint
	return nil
}

func (l *Launcher) Stop(pid int) error {
	return nil
}
