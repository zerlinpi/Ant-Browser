//go:build windows

package browseragent

import (
	"os"

	"golang.org/x/sys/windows"
)

// Go cannot portably fsync a Windows directory handle. The individual file is
// flushed before the same-directory rename; NTFS rename provides the atomic
// replacement boundary used by the journal.
func syncJournalDirectory(string) error { return nil }

// Windows privacy is inherited from the application data directory ACL. We
// still reject reparse/symlink roots in NewCommandJournal before this hook.
func secureJournalRoot(string) error { return nil }

func lockJournalFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{},
	)
}

func unlockJournalFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
