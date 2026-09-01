package service

import "errors"

// SyncManager handles browser profile cloud synchronization.
type SyncManager struct{}

func NewSyncManager() *SyncManager {
	return &SyncManager{}
}

func (s *SyncManager) UploadProfile(profileID string) error {
	if profileID == "" {
		return errors.New("profile id required")
	}
	return nil
}

func (s *SyncManager) DownloadProfile(profileID string) error {
	if profileID == "" {
		return errors.New("profile id required")
	}
	return nil
}
