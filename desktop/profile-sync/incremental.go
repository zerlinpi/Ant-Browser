package profilesync

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	localStateSchema  = "ant-profile-sync-state/v1"
	maxStateBytes     = 16 << 20
	maxDeltaChainSize = 32
)

type fileSignature struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// localSyncState is the device's record of the cloud revision its profile
// directory was last synchronized with. It lives beside the directory, never
// inside it, so it is not uploaded.
type localSyncState struct {
	SchemaVersion string `json:"schemaVersion"`
	ProfileID     string `json:"profileId"`
	// ProfileDirectory binds the state to one local directory. Rebinding the
	// cloud profile to another local profile must not reuse this baseline.
	ProfileDirectory string `json:"profileDirectory,omitempty"`
	// RevisionID is empty only when the device has never synchronized but
	// uploaded a conflict snapshot (PendingRevisionID).
	RevisionID string                   `json:"revisionId"`
	ChainDepth int                      `json:"chainDepth"`
	Files      map[string]fileSignature `json:"files"`
	// PendingRevisionID/PendingFiles describe a snapshot uploaded for an open
	// conflict. If the conflict is resolved with keep_local the cloud
	// promotes exactly that revision, and it becomes this device's baseline
	// instead of producing a second conflict.
	PendingRevisionID string                   `json:"pendingRevisionId,omitempty"`
	PendingFiles      map[string]fileSignature `json:"pendingFiles,omitempty"`
}

// baselineFor returns the committed revision the local directory derives
// from, given the cloud's current revision. A conflict snapshot the cloud
// promoted (keep_local) is that baseline. One it did not promote means the
// directory holds changes the cloud has not accepted, so there is no usable
// baseline: the next push opens a new conflict and a pull replaces the
// directory instead of keeping those changes.
func (s localSyncState) baselineFor(currentRevisionID string) (baseline localSyncState, adoptedPending bool) {
	if s.PendingRevisionID == "" {
		return localSyncState{RevisionID: s.RevisionID, ChainDepth: s.ChainDepth, Files: s.Files}, false
	}
	if s.PendingRevisionID == currentRevisionID {
		return localSyncState{RevisionID: s.PendingRevisionID, Files: s.PendingFiles}, true
	}
	return localSyncState{}, false
}

var errProfileFileFound = errors.New("profile file found")

// localProfileHasFiles reports whether dir is a real directory holding at
// least one synchronized file, so a wiped profile is never mistaken for one
// that already matches the cloud.
func localProfileHasFiles(dir string) bool {
	root, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return false
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	err = filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == root {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if isTransientProfileEntry(filepath.ToSlash(rel), entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			return errProfileFileFound
		}
		return nil
	})
	return errors.Is(err, errProfileFileFound)
}

func (c *SyncClient) statePath(profileDir string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(profileDir))
	if err != nil || root == "" {
		return "", errors.New("profile state path is invalid")
	}
	return filepath.Join(filepath.Dir(root), ".ant-profile-sync-"+c.profileID+".json"), nil
}

func (c *SyncClient) loadLocalState(profileDir string) (localSyncState, bool, error) {
	statePath, err := c.statePath(profileDir)
	if err != nil {
		return localSyncState{}, false, err
	}
	info, err := os.Lstat(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return localSyncState{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxStateBytes {
		return localSyncState{}, false, errors.New("local profile sync state is invalid")
	}
	file, err := os.Open(statePath)
	if err != nil {
		return localSyncState{}, false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	var state localSyncState
	if err := decoder.Decode(&state); err != nil || decoder.Decode(new(any)) != io.EOF {
		return localSyncState{}, false, errors.New("local profile sync state is corrupt")
	}
	if err := c.validateLocalState(state); err != nil {
		return localSyncState{}, false, err
	}
	if state.ProfileDirectory != "" {
		root, err := filepath.Abs(strings.TrimSpace(profileDir))
		if err != nil {
			return localSyncState{}, false, errors.New("profile state path is invalid")
		}
		if !sameProfileDirectory(state.ProfileDirectory, root) {
			// The cloud profile was rebound to another local directory; the
			// recorded baseline describes different files.
			return localSyncState{}, false, nil
		}
	}
	return state, true, nil
}

// loadBaselineState is loadLocalState for synchronization decisions. An
// unreadable state counts as no baseline: a pull then replaces the directory
// with the cloud's current revision, and a push opens a conflict instead of
// overwriting cloud data. Either way the state is rewritten afterwards, so a
// damaged file cannot block synchronization permanently.
func (c *SyncClient) loadBaselineState(profileDir string) (localSyncState, bool) {
	state, found, err := c.loadLocalState(profileDir)
	if err != nil {
		return localSyncState{}, false
	}
	return state, found
}

func sameProfileDirectory(recorded, current string) bool {
	recorded, current = filepath.Clean(recorded), filepath.Clean(current)
	if filepath.Separator == '\\' {
		return strings.EqualFold(recorded, current)
	}
	return recorded == current
}

func (c *SyncClient) validateLocalState(state localSyncState) error {
	if state.SchemaVersion != localStateSchema || state.ProfileID != c.profileID ||
		state.ChainDepth < 0 || state.ChainDepth > maxDeltaChainSize || len(state.ProfileDirectory) > 4096 ||
		strings.ContainsRune(state.ProfileDirectory, 0) {
		return errors.New("local profile sync state identity is invalid")
	}
	if state.RevisionID == "" {
		if state.PendingRevisionID == "" || state.ChainDepth != 0 || len(state.Files) != 0 {
			return errors.New("local profile sync state identity is invalid")
		}
	} else if validateID(state.RevisionID) != nil {
		return errors.New("local profile sync state identity is invalid")
	}
	if state.PendingRevisionID == "" {
		if len(state.PendingFiles) != 0 {
			return errors.New("local profile sync state identity is invalid")
		}
	} else if validateID(state.PendingRevisionID) != nil || state.PendingRevisionID == state.RevisionID {
		return errors.New("local profile sync state identity is invalid")
	}
	if err := c.validateInventory(state.Files); err != nil {
		return err
	}
	return c.validateInventory(state.PendingFiles)
}

func (c *SyncClient) validateInventory(files map[string]fileSignature) error {
	if len(files) > c.limits.MaxFiles {
		return errors.New("local profile sync state contents are invalid")
	}
	var total int64
	for name, signature := range files {
		clean, err := safeArchivePath(name)
		if err != nil || clean != name || signature.Size < 0 || signature.Size > c.limits.MaxFileBytes ||
			!validSHA256(signature.SHA256) || total > c.limits.MaxBytes-signature.Size {
			return errors.New("local profile sync state contents are invalid")
		}
		total += signature.Size
	}
	return nil
}

func (c *SyncClient) writeLocalState(profileDir string, state localSyncState) error {
	state.SchemaVersion = localStateSchema
	state.ProfileID = c.profileID
	root, err := filepath.Abs(strings.TrimSpace(profileDir))
	if err != nil || root == "" {
		return errors.New("profile state path is invalid")
	}
	state.ProfileDirectory = root
	if err := c.validateLocalState(state); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode local profile sync state: %w", err)
	}
	if len(payload)+1 > maxStateBytes {
		return errors.New("local profile sync state exceeds the supported limit")
	}
	statePath, err := c.statePath(profileDir)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(statePath); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local profile sync state target is unsafe")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	file, err := os.CreateTemp(filepath.Dir(statePath), ".ant-profile-sync-state-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(append(payload, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary, statePath)
	}
	if err != nil {
		return fmt.Errorf("persist local profile sync state: %w", err)
	}
	return nil
}

func scanProfileDirectory(ctx context.Context, sourceDir string, limits Limits) (map[string]fileSignature, error) {
	limits = limits.withDefaults()
	root, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("profile source must be a real directory")
	}
	files := make(map[string]fileSignature)
	var total int64
	err = filepath.WalkDir(root, func(full string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if full == root {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		name, err := safeArchivePath(rel)
		if err != nil {
			return err
		}
		if isTransientProfileEntry(name, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrSymlinkProfile
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported profile entry %q", full)
		}
		if len(files) >= limits.MaxFiles {
			return ErrArchiveLimit
		}
		walkInfo, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(full)
		if err != nil {
			return err
		}
		stat, err := input.Stat()
		if err != nil || !stat.Mode().IsRegular() || !os.SameFile(walkInfo, stat) {
			_ = input.Close()
			return fmt.Errorf("profile entry changed while scanning %q", full)
		}
		if stat.Size() > limits.MaxFileBytes || total > limits.MaxBytes-stat.Size() {
			_ = input.Close()
			return ErrArchiveLimit
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, &countingLimitReader{ctx: ctx, r: input, remaining: stat.Size()})
		postStat, statErr := input.Stat()
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != stat.Size() || postStat.Size() != stat.Size() || !postStat.ModTime().Equal(stat.ModTime()) {
			return fmt.Errorf("profile entry changed while scanning %q", full)
		}
		total += stat.Size()
		files[name] = fileSignature{Size: stat.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func scanArchiveInventory(ctx context.Context, archivePath string, limits Limits) (map[string]fileSignature, error) {
	limits = limits.withDefaults()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if len(reader.File) == 0 {
		return nil, ErrEmptyArchive
	}
	if len(reader.File) > limits.MaxFiles {
		return nil, ErrArchiveLimit
	}
	files := make(map[string]fileSignature, len(reader.File))
	var total int64
	for _, item := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clean, err := safeArchivePath(item.Name)
		if err != nil || clean != item.Name || item.FileInfo().IsDir() || item.Mode()&os.ModeSymlink != 0 || !item.Mode().IsRegular() {
			return nil, ErrUnsafeArchivePath
		}
		if _, exists := files[clean]; exists {
			return nil, ErrUnsafeArchivePath
		}
		size := int64(item.UncompressedSize64)
		if size < 0 || size > limits.MaxFileBytes || total > limits.MaxBytes-size {
			return nil, ErrArchiveLimit
		}
		input, err := item.Open()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(hash, &countingLimitReader{ctx: ctx, r: input, remaining: size})
		closeErr := input.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if written != size {
			return nil, errors.New("profile archive entry size changed")
		}
		total += size
		files[clean] = fileSignature{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	return files, nil
}

func profileChanges(current, baseline map[string]fileSignature) (changed, deleted []string) {
	for name, signature := range current {
		if previous, exists := baseline[name]; !exists || previous != signature {
			changed = append(changed, name)
		}
	}
	for name := range baseline {
		if _, exists := current[name]; !exists {
			deleted = append(deleted, name)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}

func createDeltaArchive(ctx context.Context, sourceDir string, changed []string, expected map[string]fileSignature, limits Limits) (string, archiveSummary, error) {
	limits = limits.withDefaults()
	if len(changed) == 0 {
		return "", archiveSummary{}, ErrEmptyArchive
	}
	root, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", archiveSummary{}, err
	}
	file, err := os.CreateTemp("", "ant-profile-delta-*.zip")
	if err != nil {
		return "", archiveSummary{}, err
	}
	archivePath := file.Name()
	cleanup := func(e error) (string, archiveSummary, error) {
		_ = file.Close()
		_ = os.Remove(archivePath)
		return "", archiveSummary{}, e
	}
	limited := &limitedWriter{w: file, max: limits.MaxArchiveBytes}
	writer := zip.NewWriter(limited)
	for _, name := range changed {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return cleanup(err)
		}
		clean, err := safeArchivePath(name)
		if err != nil || clean != name {
			_ = writer.Close()
			return cleanup(ErrUnsafeArchivePath)
		}
		signature, exists := expected[name]
		if !exists {
			_ = writer.Close()
			return cleanup(errors.New("delta file is missing from the current profile inventory"))
		}
		full := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != signature.Size {
			_ = writer.Close()
			return cleanup(fmt.Errorf("profile entry changed while creating delta %q", full))
		}
		input, err := os.Open(full)
		if err != nil {
			_ = writer.Close()
			return cleanup(err)
		}
		stat, err := input.Stat()
		if err != nil || !os.SameFile(info, stat) || stat.Size() != signature.Size {
			_ = input.Close()
			_ = writer.Close()
			return cleanup(fmt.Errorf("profile entry changed while creating delta %q", full))
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		output, err := writer.CreateHeader(header)
		if err != nil {
			_ = input.Close()
			_ = writer.Close()
			return cleanup(err)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(output, hash), &countingLimitReader{ctx: ctx, r: input, remaining: stat.Size()})
		postStat, statErr := input.Stat()
		closeErr := input.Close()
		if copyErr != nil || statErr != nil || closeErr != nil || written != stat.Size() || postStat.Size() != stat.Size() ||
			!postStat.ModTime().Equal(stat.ModTime()) || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), signature.SHA256) {
			_ = writer.Close()
			return cleanup(fmt.Errorf("profile entry changed while creating delta %q", full))
		}
	}
	if err := writer.Close(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		return cleanup(err)
	}
	info, err := os.Stat(archivePath)
	if err != nil || info.Size() > limits.MaxArchiveBytes {
		if err == nil {
			err = ErrArchiveLimit
		}
		return cleanup(err)
	}
	hash, err := fileSHA256(archivePath)
	if err != nil {
		return cleanup(err)
	}
	return archivePath, archiveSummary{Size: info.Size(), Hash: hash}, nil
}

func applyDeletedPaths(root string, deleted []string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, name := range deleted {
		clean, err := safeArchivePath(name)
		if err != nil || clean != name {
			return ErrUnsafeArchivePath
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafeArchivePath
		}
		if err := os.Remove(target); err != nil {
			return err
		}
		for parent := filepath.Dir(target); parent != root && strings.HasPrefix(parent+string(os.PathSeparator), root+string(os.PathSeparator)); parent = filepath.Dir(parent) {
			if err := os.Remove(parent); err != nil {
				break
			}
		}
	}
	return nil
}

func overlayDelta(ctx context.Context, deltaRoot, targetRoot string) error {
	deltaRoot, err := filepath.Abs(deltaRoot)
	if err != nil {
		return err
	}
	targetRoot, err = filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	return filepath.WalkDir(deltaRoot, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if source == deltaRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrSymlinkProfile
		}
		rel, err := filepath.Rel(deltaRoot, source)
		if err != nil {
			return err
		}
		name, err := safeArchivePath(rel)
		if err != nil {
			return err
		}
		target := filepath.Join(targetRoot, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return ErrUnsafeArchivePath
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		output, err := os.CreateTemp(filepath.Dir(target), ".ant-profile-overlay-*")
		if err != nil {
			_ = input.Close()
			return err
		}
		temporary := output.Name()
		copyErr := func() error {
			defer os.Remove(temporary)
			if err := output.Chmod(0o600); err != nil {
				return err
			}
			if _, err := io.Copy(output, &contextReader{ctx: ctx, reader: input}); err != nil {
				return err
			}
			if err := output.Sync(); err != nil {
				return err
			}
			if err := output.Close(); err != nil {
				return err
			}
			return os.Rename(temporary, target)
		}()
		inputCloseErr := input.Close()
		if copyErr != nil {
			_ = output.Close()
			return copyErr
		}
		return inputCloseErr
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
