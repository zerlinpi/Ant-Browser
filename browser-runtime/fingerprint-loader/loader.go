// Package fingerprintloader validates and prepares fingerprint templates.
package fingerprintloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidTemplate = errors.New("invalid fingerprint template")
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	chromiumArgPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

const maxTemplateBytes = 16 * 1024

type Template struct {
	ID     string                 `json:"id"`
	Config map[string]interface{} `json:"config"`
}

// Runtime is the immutable, sanitized representation passed to an injector.
type Runtime struct {
	ID     string                 `json:"id"`
	Config map[string]interface{} `json:"config"`
	Args   []string               `json:"args"`
}

type Injector interface {
	Apply(context.Context, Runtime) error
}
type Option func(*Loader)

func WithInjector(injector Injector) Option { return func(l *Loader) { l.injector = injector } }
func WithMaxTemplateBytes(limit int) Option {
	return func(l *Loader) {
		if limit > 0 {
			l.maxBytes = limit
		}
	}
}

type Loader struct {
	mu       sync.RWMutex
	injector Injector
	maxBytes int
	loaded   map[string]Runtime
}

func NewLoader(options ...Option) *Loader {
	l := &Loader{maxBytes: maxTemplateBytes, loaded: make(map[string]Runtime)}
	for _, option := range options {
		if option != nil {
			option(l)
		}
	}
	return l
}

// Load validates and applies a template. Prepared data can be retrieved with Runtime.
func (l *Loader) Load(template Template) error {
	runtime, err := l.Prepare(template)
	if err != nil {
		return err
	}
	if l.injector != nil {
		if err := l.injector.Apply(context.Background(), runtime); err != nil {
			return fmt.Errorf("apply fingerprint template %q: %w", runtime.ID, err)
		}
	}
	l.mu.Lock()
	l.loaded[runtime.ID] = runtime
	l.mu.Unlock()
	return nil
}

func (l *Loader) Prepare(template Template) (Runtime, error) {
	id := strings.TrimSpace(template.ID)
	if !idPattern.MatchString(id) {
		return Runtime{}, fmt.Errorf("%w: id must be a safe non-empty identifier", ErrInvalidTemplate)
	}
	if template.Config == nil {
		return Runtime{}, fmt.Errorf("%w: config is required", ErrInvalidTemplate)
	}
	clone, err := cloneConfig(template.Config, 0)
	if err != nil {
		return Runtime{}, err
	}
	encoded, err := json.Marshal(clone)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: config is not JSON-safe: %v", ErrInvalidTemplate, err)
	}
	l.mu.RLock()
	limit := l.maxBytes
	l.mu.RUnlock()
	if len(encoded) > limit {
		return Runtime{}, fmt.Errorf("%w: config exceeds %d bytes", ErrInvalidTemplate, limit)
	}
	args, err := buildArgs(clone)
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{ID: id, Config: clone, Args: args}, nil
}

func (l *Loader) Runtime(id string) (Runtime, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	r, ok := l.loaded[strings.TrimSpace(id)]
	if !ok {
		return Runtime{}, false
	}
	r.Config = cloneMap(r.Config)
	r.Args = append([]string(nil), r.Args...)
	return r, true
}

func cloneConfig(value map[string]interface{}, depth int) (map[string]interface{}, error) {
	if depth > 8 {
		return nil, fmt.Errorf("%w: config nesting is too deep", ErrInvalidTemplate)
	}
	result := make(map[string]interface{}, len(value))
	for key, item := range value {
		if key == "" || len(key) > 128 || hasControlCharacters(key) {
			return nil, fmt.Errorf("%w: config key is invalid", ErrInvalidTemplate)
		}
		clean, err := cloneValue(item, depth+1)
		if err != nil {
			return nil, err
		}
		result[key] = clean
	}
	return result, nil
}
func cloneMap(value map[string]interface{}) map[string]interface{} {
	result, _ := cloneConfig(value, 0)
	return result
}

func cloneValue(value interface{}, depth int) (interface{}, error) {
	if depth > 8 {
		return nil, fmt.Errorf("%w: config nesting is too deep", ErrInvalidTemplate)
	}
	switch typed := value.(type) {
	case nil, bool:
		return typed, nil
	case string:
		if hasControlCharacters(typed) {
			return nil, fmt.Errorf("%w: config string contains control characters", ErrInvalidTemplate)
		}
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, fmt.Errorf("%w: config number is not finite", ErrInvalidTemplate)
		}
		return typed, nil
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return nil, fmt.Errorf("%w: config number is not finite", ErrInvalidTemplate)
		}
		return typed, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed, nil
	case map[string]interface{}:
		return cloneConfig(typed, depth+1)
	case []interface{}:
		items := make([]interface{}, len(typed))
		for i, item := range typed {
			clean, err := cloneValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			items[i] = clean
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%w: config contains unsupported value %T", ErrInvalidTemplate, value)
	}
}

// Only explicitly namespaced values become Chromium arguments. Arbitrary
// config is retained for injectors but is never copied to a process command.
func buildArgs(config map[string]interface{}) ([]string, error) {
	var args []string
	keys := make([]string, 0, len(config))
	for key := range config {
		if !strings.HasPrefix(key, "chromium.") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]string, len(keys))
	for _, key := range keys {
		value := config[key]
		name := strings.TrimPrefix(key, "chromium.")
		text, ok := value.(string)
		canonicalName := strings.ToLower(name)
		if !chromiumArgPattern.MatchString(name) || isLauncherOwnedArgument(name) || !ok || strings.TrimSpace(text) == "" || hasControlCharacters(text) {
			return nil, fmt.Errorf("%w: chromium argument %q is invalid", ErrInvalidTemplate, key)
		}
		if previous, exists := seen[canonicalName]; exists {
			return nil, fmt.Errorf("%w: chromium arguments %q and %q collide", ErrInvalidTemplate, previous, key)
		}
		seen[canonicalName] = key
		args = append(args, "--"+name+"="+text)
	}
	return args, nil
}

func hasControlCharacters(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

func isLauncherOwnedArgument(name string) bool {
	switch strings.ToLower(name) {
	case "user-data-dir", "remote-debugging-address", "remote-debugging-port", "remote-debugging-pipe", "proxy-server", "no-proxy-server":
		return true
	default:
		return false
	}
}
