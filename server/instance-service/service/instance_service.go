package service

// InstanceService manages browser runtime instances.
// It connects cloud instances with desktop clients.

type InstanceService struct{}

func NewInstanceService() *InstanceService {
	return &InstanceService{}
}

func (s *InstanceService) Create(profileID string, workspaceID string) error {
	// TODO:
	// create instance record
	// assign profile
	// notify desktop client
	return nil
}

func (s *InstanceService) Heartbeat(instanceID string) error {
	// TODO: update online status
	return nil
}
