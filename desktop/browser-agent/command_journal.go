package browseragent

import (
	"encoding/json"
	"errors"
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
	root string
	mu   sync.Mutex
}
type commandReceipt struct {
	ID            string `json:"id"`
	Digest        string `json:"digest"`
	Status        string `json:"status"`
	ObservedState string `json:"observedState,omitempty"`
	FailureCode   string `json:"failureCode,omitempty"`
}

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
	return &CommandJournal{root: root}, nil
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
			return commandReceipt{}, true, errors.New("invalid command journal record")
		}
		file, readErr := os.Open(path)
		if readErr != nil {
			return commandReceipt{}, true, readErr
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 65537))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.ID != id || receipt.Digest != digest || (receipt.Status != "started" && receipt.Status != "completed" && receipt.Status != "failed") {
			return commandReceipt{}, true, errors.New("command journal identity or contents mismatch")
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
