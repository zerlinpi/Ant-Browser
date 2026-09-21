// Package profile prepares an isolated Chromium user-data directory and
// provides a narrow hook for restoring a cloud profile revision.
package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrInvalidProfile = errors.New("invalid browser profile")

type Profile struct {
	ID      string
	Path    string
	Version string
}

type Prepared struct {
	ID      string
	Path    string
	Version string
}

// Restorer materializes a verified, decrypted profile revision. The cloud
// sync client implements this interface; Loader never knows storage secrets.
type Restorer interface {
	Restore(context.Context, Profile, string) error
}

type Option func(*Loader)

func WithRoot(root string) Option {
	return func(loader *Loader) { loader.root = strings.TrimSpace(root) }
}

func WithRestorer(restorer Restorer) Option {
	return func(loader *Loader) { loader.restorer = restorer }
}

type Loader struct {
	mu       sync.RWMutex
	root     string
	restorer Restorer
	loaded   map[string]Prepared
}

func New(options ...Option) *Loader {
	loader := &Loader{loaded: make(map[string]Prepared)}
	for _, option := range options {
		if option != nil {
			option(loader)
		}
	}
	return loader
}

// Load retains the initial scaffold API. PrepareContext returns the resolved
// directory needed by a launcher.
func (l *Loader) Load(input Profile) error {
	_, err := l.PrepareContext(context.Background(), input)
	return err
}

func (l *Loader) PrepareContext(ctx context.Context, input Profile) (Prepared, error) {
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	input.ID, input.Path, input.Version = strings.TrimSpace(input.ID), strings.TrimSpace(input.Path), strings.TrimSpace(input.Version)
	if input.ID == "" || len(input.ID) > 128 || strings.ContainsAny(input.ID, "\x00\r\n/\\") {
		return Prepared{}, fmt.Errorf("%w: id is required and cannot contain path separators", ErrInvalidProfile)
	}
	if len(input.Version) > 128 || strings.ContainsAny(input.Version, "\x00\r\n") {
		return Prepared{}, fmt.Errorf("%w: version is invalid", ErrInvalidProfile)
	}
	resolved, err := l.resolvePath(input)
	if err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("prepare profile directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return Prepared{}, fmt.Errorf("%w: profile path is not a directory", ErrInvalidProfile)
	}
	input.Path = resolved
	if input.Version != "" && l.restorer != nil {
		if err := l.restorer.Restore(ctx, input, resolved); err != nil {
			return Prepared{}, fmt.Errorf("restore profile revision %q: %w", input.Version, err)
		}
	}
	prepared := Prepared{ID: input.ID, Path: resolved, Version: input.Version}
	l.mu.Lock()
	l.loaded[input.ID] = prepared
	l.mu.Unlock()
	return prepared, nil
}

func (l *Loader) Prepared(id string) (Prepared, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prepared, ok := l.loaded[strings.TrimSpace(id)]
	return prepared, ok
}

func (l *Loader) resolvePath(input Profile) (string, error) {
	root := strings.TrimSpace(l.root)
	requested := input.Path
	if requested == "" {
		if root == "" {
			return "", fmt.Errorf("%w: path or loader root is required", ErrInvalidProfile)
		}
		requested = filepath.Join(root, input.ID)
	}
	resolved, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path: %v", ErrInvalidProfile, err)
	}
	if root == "" {
		return filepath.Clean(resolved), nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root: %v", ErrInvalidProfile, err)
	}
	// Reject lexical escapes before creating anything. Besides being cheaper,
	// this avoids creating a rejected path outside the configured root.
	requestedRelative, relErr := filepath.Rel(root, resolved)
	if relErr != nil || requestedRelative == ".." || strings.HasPrefix(requestedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(requestedRelative) {
		return "", fmt.Errorf("%w: profile path escapes the configured root", ErrInvalidProfile)
	}
	// Canonicalize both sides after creating the root. A lexical Rel check is
	// not sufficient when a profile directory (or one of its parents) is a
	// symlink pointing outside the tenant's profile root.
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("%w: create profile root: %v", ErrInvalidProfile, err)
	}
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve profile root: %v", ErrInvalidProfile, err)
	}
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		return "", fmt.Errorf("%w: create profile path: %v", ErrInvalidProfile, err)
	}
	canonicalResolved, err := canonicalPath(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: resolve profile path: %v", ErrInvalidProfile, err)
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalResolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%w: profile path escapes the configured root", ErrInvalidProfile)
	}
	return filepath.Clean(canonicalResolved), nil
}

// canonicalPath resolves a path only when one of its existing components is a
// symlink. Some restricted Windows volumes reject EvalSymlinks even for a
// normal directory; avoiding that call in the common case keeps profile setup
// portable while preserving the escape check for links.
func canonicalPath(path string) (string, error) {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	rest = strings.TrimLeft(rest, `\\/`)
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return filepath.EvalSymlinks(clean)
		}
	}
	return clean, nil
}
