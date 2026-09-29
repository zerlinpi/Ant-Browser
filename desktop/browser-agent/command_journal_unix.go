//go:build !windows

package browseragent

import (
	"errors"
	"os"
	"syscall"
)

func secureJournalRoot(path string) error {
	if err := os.Chmod(path, 0700); err != nil {
		return errors.New("command journal permissions could not be restricted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("command journal directory permissions are unsafe")
	}
	return nil
}

func syncJournalDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func lockJournalFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockJournalFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
