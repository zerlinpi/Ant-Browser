package deviceservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound = errors.New("device not found")
	ErrRevoked  = errors.New("device revoked")
)

type Device struct {
	ID           string                 `json:"id"`
	WorkspaceID  string                 `json:"workspaceId"`
	UserID       string                 `json:"userId"`
	Name         string                 `json:"name"`
	Platform     string                 `json:"platform"`
	AgentVersion string                 `json:"agentVersion"`
	Capabilities map[string]interface{} `json:"capabilities"`
	Status       string                 `json:"status"`
	LastSeenAt   *time.Time             `json:"lastSeenAt,omitempty"`
	CreatedAt    time.Time              `json:"createdAt"`
	UpdatedAt    time.Time              `json:"updatedAt"`
	RevokedAt    *time.Time             `json:"revokedAt,omitempty"`
}

type Credential struct {
	ID         string     `json:"id"`
	DeviceID   string     `json:"deviceId"`
	SecretHash string     `json:"-"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type Repository interface {
	CreateDevice(context.Context, Device, Credential) error
	ListDevices(context.Context, string) ([]Device, error)
	FindDevice(context.Context, string) (Device, error)
	Heartbeat(context.Context, string, string, map[string]interface{}, time.Time) (Device, error)
	RevokeDevice(context.Context, string, string, time.Time) error
	AuthenticateDevice(context.Context, string, string) (Device, error)
}

type OpaqueTokenFactory func() (raw, hash string, err error)

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	newSecret  OpaqueTokenFactory
	authorizer Authorizer
	now        func() time.Time
}

type RegisterInput struct {
	WorkspaceID  string                 `json:"workspaceId"`
	Name         string                 `json:"name"`
	Platform     string                 `json:"platform"`
	AgentVersion string                 `json:"agentVersion"`
	Capabilities map[string]interface{} `json:"capabilities"`
}

type Registration struct {
	Device     Device `json:"device"`
	Credential string `json:"credential"`
}

func New(repository Repository, newSecret OpaqueTokenFactory, authorizer Authorizer) *Service {
	return &Service{repository: repository, newSecret: newSecret, authorizer: authorizer, now: time.Now}
}

func (s *Service) Register(ctx context.Context, userID string, input RegisterInput) (Registration, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		return Registration{}, errors.New("workspaceId is required")
	}
	if err := s.authorizer.Require(ctx, input.WorkspaceID, userID, memberservice.PermissionWorkspaceRead); err != nil {
		return Registration{}, err
	}
	name := strings.TrimSpace(input.Name)
	platform := strings.ToLower(strings.TrimSpace(input.Platform))
	if name == "" || len([]rune(name)) > 120 {
		return Registration{}, errors.New("valid device name is required")
	}
	if platform != "windows" && platform != "linux" && platform != "darwin" {
		return Registration{}, errors.New("platform must be windows, linux, or darwin")
	}
	raw, hash, err := s.newSecret()
	if err != nil {
		return Registration{}, err
	}
	now := s.now().UTC()
	device := Device{
		ID: uuid.NewString(), WorkspaceID: input.WorkspaceID, UserID: userID, Name: name, Platform: platform,
		AgentVersion: strings.TrimSpace(input.AgentVersion), Capabilities: input.Capabilities,
		Status: "offline", CreatedAt: now, UpdatedAt: now,
	}
	credential := Credential{ID: uuid.NewString(), DeviceID: device.ID, SecretHash: hash, CreatedAt: now}
	if err := s.repository.CreateDevice(ctx, device, credential); err != nil {
		return Registration{}, err
	}
	return Registration{Device: device, Credential: raw}, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]Device, error) {
	return s.repository.ListDevices(ctx, userID)
}

func (s *Service) Heartbeat(ctx context.Context, deviceID, version string, capabilities map[string]interface{}) (Device, error) {
	return s.repository.Heartbeat(ctx, deviceID, strings.TrimSpace(version), capabilities, s.now().UTC())
}

func (s *Service) Revoke(ctx context.Context, userID, deviceID string) error {
	return s.repository.RevokeDevice(ctx, userID, deviceID, s.now().UTC())
}

func (s *Service) Authenticate(ctx context.Context, deviceID, credentialHash string) (Device, error) {
	device, err := s.repository.AuthenticateDevice(ctx, deviceID, credentialHash)
	if err != nil || device.RevokedAt != nil {
		return Device{}, ErrRevoked
	}
	return device, nil
}
