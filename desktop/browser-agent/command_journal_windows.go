//go:build windows

package browseragent

// Go cannot portably fsync a Windows directory handle. The individual file is
// flushed before the same-directory rename; NTFS rename provides the atomic
// replacement boundary used by the journal.
func syncJournalDirectory(string) error { return nil }

// Windows privacy is inherited from the application data directory ACL. We
// still reject reparse/symlink roots in NewCommandJournal before this hook.
func secureJournalRoot(string) error { return nil }
