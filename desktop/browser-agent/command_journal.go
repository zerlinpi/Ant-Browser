package browseragent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

// CommandJournal records intent before any browser side effect. A started
// record after a crash is ambiguous, never permission to repeat a restart.
// The trusted desktop supplies a private directory; credentials are not stored.
type CommandJournal struct {
	root     string
	lockFile *os.File
	mu       sync.Mutex
	closed   bool
}
type commandReceipt struct {
	ID            string `json:"id"`
	Digest        string `json:"digest"`
	Status        string `json:"status"`
	ObservedState string `json:"observedState,omitempty"`
	FailureCode   string `json:"failureCode,omitempty"`
}

// errJournalRecordConflict marks an existing record that cannot be trusted
// for this dispatch: its digest differs (the command ID was reused with other
// content) or it is unreadable or corrupt. The command must never execute,
// but the journal itself remains usable for other commands.
var errJournalRecordConflict = errors.New("command journal record conflicts with the dispatched command")

func NewCommandJournal(root string) (*CommandJournal, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("command journal path must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("command journal unavailable")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("command journal must be a real directory")
	}
	if err := secureJournalRoot(root); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, ".agent.lock")
	if info, statErr := os.Lstat(lockPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("command journal lock must be a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, errors.New("command journal lock is unavailable")
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("command journal lock is unavailable")
	}
	if err := lockFile.Chmod(0600); err != nil {
		_ = lockFile.Close()
		return nil, errors.New("command journal lock permissions could not be restricted")
	}
	if err := lockJournalFile(lockFile); err != nil {
		_ = lockFile.Close()
		return nil, errors.New("command journal is already in use")
	}
	return &CommandJournal{root: root, lockFile: lockFile}, nil
}

// Close releases the process-wide ownership of this command journal. Only one
// desktop process may consume cloud commands for a device at a time; without
// this lock, a second process could misclassify an in-flight command as a
// crash-recovery record while the first process is still executing it.
func (j *CommandJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if j.lockFile == nil {
		return nil
	}
	unlockErr := unlockJournalFile(j.lockFile)
	closeErr := j.lockFile.Close()
	j.lockFile = nil
	if unlockErr != nil {
		return errors.New("command journal lock could not be released")
	}
	if closeErr != nil {
		return errors.New("command journal lock could not be closed")
	}
	return nil
}

func (j *CommandJournal) path(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", errors.New("invalid command identity")
	}
	return filepath.Join(j.root, id+".json"), nil
}

func (j *CommandJournal) begin(id, digest string) (commandReceipt, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return commandReceipt{}, false, errors.New("command journal is closed")
	}
	path, err := j.path(id)
	if err != nil {
		return commandReceipt{}, false, err
	}
	receipt := commandReceipt{ID: id, Digest: digest, Status: "started"}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		// Do not follow a substituted symlink or silently discard corrupt state.
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return commandReceipt{}, true, fmt.Errorf("%w: invalid record", errJournalRecordConflict)
		}
		file, readErr := os.Open(path)
		if readErr != nil {
			return commandReceipt{}, true, fmt.Errorf("%w: unreadable record", errJournalRecordConflict)
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 65537))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.ID != id || receipt.Digest != digest || (receipt.Status != "started" && receipt.Status != "completed" && receipt.Status != "failed") {
			return commandReceipt{}, true, fmt.Errorf("%w: identity or contents mismatch", errJournalRecordConflict)
		}
		return receipt, true, nil
	}
	if err != nil {
		return commandReceipt{}, false, errors.New("command journal intent could not be created")
	}
	err = json.NewEncoder(file).Encode(receipt)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return commandReceipt{}, false, errors.New("command journal intent could not be persisted")
	}
	if err := syncJournalDirectory(j.root); err != nil {
		return commandReceipt{}, false, errors.New("command journal directory could not be synchronized")
	}
	return receipt, false, nil
}

func (j *CommandJournal) finish(receipt commandReceipt) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("command journal is closed")
	}
	path, err := j.path(receipt.ID)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(j.root, ".receipt-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		err = json.NewEncoder(file).Encode(receipt)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, path)
	}
	if err == nil {
		err = syncJournalDirectory(j.root)
	}
	if err != nil {
		return errors.New("command result could not be persisted")
	}
	return nil
}
