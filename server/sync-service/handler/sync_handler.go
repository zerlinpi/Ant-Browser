package handler

// SyncHandler exposes profile synchronization APIs.
type SyncHandler struct{}

func NewSyncHandler() *SyncHandler {
	return &SyncHandler{}
}

// UploadProfile receives profile packages from desktop clients.
func (h *SyncHandler) UploadProfile() {
	// TODO: validate workspace permission
	// TODO: calculate hash
	// TODO: create profile version
}

// DownloadProfile returns the requested profile package.
func (h *SyncHandler) DownloadProfile() {
	// TODO: fetch version and stream package
}
