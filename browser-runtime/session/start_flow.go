package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cdpclient "ant-chrome/browser-runtime/cdp"
	fingerprintloader "ant-chrome/browser-runtime/fingerprint-loader"
	"ant-chrome/browser-runtime/launcher"
	profileloader "ant-chrome/browser-runtime/profile-loader"
)

var (
	ErrFlowNotConfigured = errors.New("browser startup flow is not configured")
	ErrSessionExists     = errors.New("browser session already exists")
	ErrSessionNotFound   = errors.New("browser session not found")
)

type ProcessLauncher interface {
	StartProcess(context.Context, launcher.Config) (launcher.Process, error)
	Stop(int) error
}
type ProfileLoader interface {
	PrepareContext(context.Context, profileloader.Profile) (profileloader.Prepared, error)
}
type FingerprintLoader interface {
	Prepare(fingerprintloader.Template) (fingerprintloader.Runtime, error)
}
type CDPClient interface {
	Connect() error
	Close() error
}
type contextCDPClient interface {
	CDPClient
	ConnectContext(context.Context) error
}
type CDPFactory func(string) CDPClient

type StartSpec struct {
	InstanceID  string
	Profile     profileloader.Profile
	Fingerprint fingerprintloader.Template
	Launch      launcher.Config
}

type Option func(*StartFlow)

func WithManager(manager *Manager) Option {
	return func(f *StartFlow) {
		if manager != nil {
			f.manager = manager
		}
	}
}
func WithProfileLoader(loader ProfileLoader) Option {
	return func(f *StartFlow) {
		if loader != nil {
			f.profiles = loader
		}
	}
}
func WithFingerprintLoader(loader FingerprintLoader) Option {
	return func(f *StartFlow) {
		if loader != nil {
			f.fingerprints = loader
		}
	}
}
func WithLauncher(value ProcessLauncher) Option {
	return func(f *StartFlow) {
		if value != nil {
			f.launcher = value
		}
	}
}
func WithCDPFactory(factory CDPFactory) Option {
	return func(f *StartFlow) {
		if factory != nil {
			f.newCDP = factory
		}
	}
}

// WithCDPReadiness controls how long startup waits for Chromium's DevTools
// socket to become ready and the delay between attempts.
func WithCDPReadiness(timeout, backoff time.Duration) Option {
	return func(f *StartFlow) {
		if timeout > 0 {
			f.cdpTimeout = timeout
		}
		if backoff > 0 {
			f.cdpBackoff = backoff
		}
	}
}

// StartFlow owns the complete local process pipeline. It receives resolved
// proxy endpoints through launcher.Config and never resolves or switches a
// connector stack itself.
type StartFlow struct {
	manager      *Manager
	profiles     ProfileLoader
	fingerprints FingerprintLoader
	launcher     ProcessLauncher
	newCDP       CDPFactory

	mu         sync.Mutex
	specs      map[string]StartSpec
	starting   map[string]bool
	clients    map[string]CDPClient
	cdpTimeout time.Duration
	cdpBackoff time.Duration
}

func NewStartFlow(options ...Option) *StartFlow {
	f := &StartFlow{
		manager: New(), profiles: profileloader.New(), fingerprints: fingerprintloader.NewLoader(), launcher: launcher.New(),
		newCDP: func(endpoint string) CDPClient { return cdpclient.NewClient(endpoint) },
		specs:  make(map[string]StartSpec), starting: make(map[string]bool), clients: make(map[string]CDPClient),
		cdpTimeout: 10 * time.Second, cdpBackoff: 100 * time.Millisecond,
	}
	for _, option := range options {
		if option != nil {
			option(f)
		}
	}
	return f
}

// Configure registers a spec for the compatibility Start(instanceID) method.
func (f *StartFlow) Configure(spec StartSpec) error {
	id := strings.TrimSpace(spec.InstanceID)
	if id == "" {
		id = strings.TrimSpace(spec.Profile.ID)
	}
	if id == "" {
		return ErrInvalidSession
	}
	spec.InstanceID = id
	f.mu.Lock()
	f.specs[id] = spec
	f.mu.Unlock()
	return nil
}

func (f *StartFlow) Start(instanceID string) error {
	id := strings.TrimSpace(instanceID)
	f.mu.Lock()
	spec, ok := f.specs[id]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: instance %q", ErrFlowNotConfigured, id)
	}
	_, err := f.StartContext(context.Background(), spec)
	return err
}

func (f *StartFlow) StartContext(ctx context.Context, spec StartSpec) (Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	id := strings.TrimSpace(spec.InstanceID)
	if id == "" {
		id = strings.TrimSpace(spec.Profile.ID)
	}
	if id == "" {
		return Session{}, ErrInvalidSession
	}
	spec.InstanceID = id
	f.mu.Lock()
	if f.manager == nil {
		f.mu.Unlock()
		return Session{}, ErrFlowNotConfigured
	}
	if _, exists := f.manager.Get(id); exists || f.starting[id] {
		f.mu.Unlock()
		return Session{}, ErrSessionExists
	}
	f.starting[id] = true
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.starting, id); f.mu.Unlock() }()
	if f.profiles == nil || f.fingerprints == nil || f.launcher == nil || f.newCDP == nil {
		return Session{}, ErrFlowNotConfigured
	}
	profileInput := spec.Profile
	if strings.TrimSpace(profileInput.ID) == "" {
		profileInput.ID = id
	}
	prepared, err := f.profiles.PrepareContext(ctx, profileInput)
	if err != nil {
		return Session{}, fmt.Errorf("prepare profile: %w", err)
	}
	runtime, err := f.fingerprints.Prepare(spec.Fingerprint)
	if err != nil {
		return Session{}, fmt.Errorf("prepare fingerprint: %w", err)
	}
	config := spec.Launch
	config.ProfilePath = prepared.Path
	config.Args = append(append([]string(nil), config.Args...), runtime.Args...)
	process, err := f.launcher.StartProcess(ctx, config)
	if err != nil {
		return Session{}, fmt.Errorf("launch browser: %w", err)
	}
	if process.PID <= 0 || strings.TrimSpace(process.CDPEndpoint) == "" {
		_ = f.launcher.Stop(process.PID)
		return Session{}, errors.New("launch browser: invalid process metadata")
	}
	client := f.newCDP(process.CDPEndpoint)
	if client == nil {
		_ = f.launcher.Stop(process.PID)
		return Session{}, errors.New("connect CDP: nil client")
	}
	if err := f.connectCDP(ctx, client); err != nil {
		_ = client.Close()
		_ = f.launcher.Stop(process.PID)
		return Session{}, fmt.Errorf("connect CDP: %w", err)
	}
	session := Session{ID: id, PID: process.PID, CDP: process.CDPEndpoint, Status: "running", StartedAt: process.StartedAt}
	if err := f.manager.Upsert(session); err != nil {
		_ = client.Close()
		_ = f.launcher.Stop(process.PID)
		return Session{}, err
	}
	f.mu.Lock()
	f.clients[id] = client
	f.mu.Unlock()
	return session, nil
}

func (f *StartFlow) connectCDP(ctx context.Context, client CDPClient) error {
	f.mu.Lock()
	timeout, backoff := f.cdpTimeout, f.cdpBackoff
	f.mu.Unlock()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if backoff <= 0 {
		backoff = 100 * time.Millisecond
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		if err := readyCtx.Err(); err != nil {
			if last != nil {
				return fmt.Errorf("CDP readiness timeout: %w (last error: %v)", err, last)
			}
			return fmt.Errorf("CDP readiness timeout: %w", err)
		}
		var connectErr error
		if contextClient, ok := client.(contextCDPClient); ok {
			connectErr = contextClient.ConnectContext(readyCtx)
		} else {
			connectErr = client.Connect()
		}
		if connectErr == nil {
			return nil
		} else {
			last = connectErr
		}
		timer := time.NewTimer(backoff)
		select {
		case <-readyCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (f *StartFlow) Stop(instanceID string) error {
	return f.StopContext(context.Background(), instanceID)
}

func (f *StartFlow) StopContext(ctx context.Context, instanceID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id := strings.TrimSpace(instanceID)
	if f.manager == nil {
		return ErrFlowNotConfigured
	}
	session, ok := f.manager.Get(id)
	if !ok {
		return ErrSessionNotFound
	}
	f.manager.SetStatus(id, "stopping")
	f.mu.Lock()
	client := f.clients[id]
	f.mu.Unlock()
	var first error
	if client != nil {
		if err := client.Close(); err != nil {
			first = fmt.Errorf("close CDP: %w", err)
		}
	}
	if err := f.launcher.Stop(session.PID); err != nil && !errors.Is(err, launcher.ErrProcessNotFound) && first == nil {
		first = fmt.Errorf("stop browser: %w", err)
	}
	if first != nil {
		f.manager.SetStatus(id, "stop_failed")
		return first
	}
	f.manager.Delete(id)
	f.mu.Lock()
	delete(f.clients, id)
	f.mu.Unlock()
	return nil
}
