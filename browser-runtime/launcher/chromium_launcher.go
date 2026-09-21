// Package launcher owns the process boundary for the standalone browser
// runtime. It deliberately accepts an already-resolved proxy endpoint: the
// xray+sing-box or Mihomo connector stack is selected by the desktop backend,
// never guessed or switched here.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidConfig   = errors.New("invalid chromium launch configuration")
	ErrProcessNotFound = errors.New("chromium process is not managed by this launcher")
)

// Config contains only process-level Chromium inputs. Proxy must be the
// endpoint produced by the currently selected connector stack; credentials
// must never be placed on the process command line.
type Config struct {
	Executable      string
	ProfilePath     string
	Proxy           string
	Direct          bool
	RemoteDebugPort int
	Args            []string
}

type Process struct {
	PID         int       `json:"pid"`
	CDPEndpoint string    `json:"cdpEndpoint"`
	StartedAt   time.Time `json:"startedAt"`
	Args        []string  `json:"-"`
}

type Launcher struct {
	mu        sync.RWMutex
	processes map[int]*exec.Cmd
	snapshots map[int]Process
}

func New() *Launcher {
	return &Launcher{processes: make(map[int]*exec.Cmd), snapshots: make(map[int]Process)}
}

// Start retains the original scaffold API. New callers should use
// StartProcess so they can register the PID and loopback CDP endpoint.
func (l *Launcher) Start(cfg Config) error {
	_, err := l.StartProcess(context.Background(), cfg)
	return err
}

func (l *Launcher) StartProcess(ctx context.Context, cfg Config) (Process, error) {
	if err := ctx.Err(); err != nil {
		return Process{}, err
	}
	normalized, args, err := normalize(cfg)
	if err != nil {
		return Process{}, err
	}
	cmd := exec.CommandContext(ctx, normalized.Executable, args...)
	cmd.Dir = normalized.ProfilePath
	if err := cmd.Start(); err != nil {
		return Process{}, fmt.Errorf("start chromium: %w", err)
	}
	snapshot := Process{
		PID:         cmd.Process.Pid,
		CDPEndpoint: "http://127.0.0.1:" + strconv.Itoa(normalized.RemoteDebugPort),
		StartedAt:   time.Now().UTC(),
		Args:        append([]string(nil), args...),
	}
	l.mu.Lock()
	l.processes[snapshot.PID] = cmd
	l.snapshots[snapshot.PID] = snapshot
	l.mu.Unlock()
	go l.reap(cmd)
	return snapshot, nil
}

// BuildArgs validates a config without starting Chromium. Supply a debug port
// when deterministic output is required; zero allocates a loopback port.
func BuildArgs(cfg Config) ([]string, error) {
	_, args, err := normalize(cfg)
	return args, err
}

func normalize(cfg Config) (Config, []string, error) {
	cfg.Executable = strings.TrimSpace(cfg.Executable)
	cfg.ProfilePath = strings.TrimSpace(cfg.ProfilePath)
	if cfg.Executable == "" || cfg.ProfilePath == "" {
		return Config{}, nil, fmt.Errorf("%w: executable and profile path are required", ErrInvalidConfig)
	}
	executable, err := filepath.Abs(cfg.Executable)
	if err != nil {
		return Config{}, nil, fmt.Errorf("%w: resolve executable: %v", ErrInvalidConfig, err)
	}
	info, err := os.Stat(executable)
	if err != nil || info.IsDir() {
		return Config{}, nil, fmt.Errorf("%w: executable is not a file", ErrInvalidConfig)
	}
	profilePath, err := filepath.Abs(cfg.ProfilePath)
	if err != nil {
		return Config{}, nil, fmt.Errorf("%w: resolve profile path: %v", ErrInvalidConfig, err)
	}
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return Config{}, nil, fmt.Errorf("%w: create profile path: %v", ErrInvalidConfig, err)
	}
	profileInfo, err := os.Stat(profilePath)
	if err != nil || !profileInfo.IsDir() {
		return Config{}, nil, fmt.Errorf("%w: profile path is not a directory", ErrInvalidConfig)
	}
	if cfg.Direct && strings.TrimSpace(cfg.Proxy) != "" {
		return Config{}, nil, fmt.Errorf("%w: direct and proxy modes are mutually exclusive", ErrInvalidConfig)
	}
	proxy, err := normalizeProxy(cfg.Proxy)
	if err != nil {
		return Config{}, nil, err
	}
	if cfg.RemoteDebugPort == 0 {
		cfg.RemoteDebugPort, err = reserveLoopbackPort()
		if err != nil {
			return Config{}, nil, fmt.Errorf("allocate CDP port: %w", err)
		}
	}
	if cfg.RemoteDebugPort < 1 || cfg.RemoteDebugPort > 65535 {
		return Config{}, nil, fmt.Errorf("%w: remote debug port is outside 1..65535", ErrInvalidConfig)
	}

	args := make([]string, 0, len(cfg.Args)+5)
	args = append(args,
		"--user-data-dir="+profilePath,
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port="+strconv.Itoa(cfg.RemoteDebugPort),
	)
	if cfg.Direct {
		args = append(args, "--no-proxy-server")
	} else if proxy != "" {
		args = append(args, "--proxy-server="+proxy)
	}
	for _, arg := range cfg.Args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if strings.ContainsAny(arg, "\x00\r\n") || isControlledArgument(arg) {
			return Config{}, nil, fmt.Errorf("%w: unsupported or duplicate argument %q", ErrInvalidConfig, arg)
		}
		args = append(args, arg)
	}
	cfg.Executable, cfg.ProfilePath, cfg.Proxy = executable, profilePath, proxy
	cfg.Args = append([]string(nil), args...)
	return cfg, args, nil
}

func normalizeProxy(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: proxy must be a credential-free endpoint", ErrInvalidConfig)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks4", "socks5":
	default:
		return "", fmt.Errorf("%w: Chromium proxy scheme %q is unsupported", ErrInvalidConfig, parsed.Scheme)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("%w: proxy port is invalid", ErrInvalidConfig)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	return parsed.String(), nil
}

func isControlledArgument(arg string) bool {
	name := strings.ToLower(strings.TrimSpace(strings.SplitN(arg, "=", 2)[0]))
	switch name {
	case "--user-data-dir", "--remote-debugging-address", "--remote-debugging-port", "--remote-debugging-pipe", "--proxy-server", "--no-proxy-server":
		return true
	}
	return false
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func (l *Launcher) Stop(pid int) error {
	l.mu.RLock()
	cmd, ok := l.processes[pid]
	l.mu.RUnlock()
	if !ok || cmd.Process == nil {
		return ErrProcessNotFound
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop chromium process %d: %w", pid, err)
	}
	return nil
}

func (l *Launcher) Process(pid int) (Process, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snapshot, ok := l.snapshots[pid]
	snapshot.Args = append([]string(nil), snapshot.Args...)
	return snapshot, ok
}

func (l *Launcher) reap(cmd *exec.Cmd) {
	_ = cmd.Wait()
	if cmd.Process == nil {
		return
	}
	l.mu.Lock()
	delete(l.processes, cmd.Process.Pid)
	delete(l.snapshots, cmd.Process.Pid)
	l.mu.Unlock()
}
