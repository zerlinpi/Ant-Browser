package browserinstanceservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound                 = errors.New("browser instance not found")
	ErrVersionConflict          = errors.New("browser instance version conflict")
	ErrStateConflict            = errors.New("browser instance state conflict")
	ErrInvalidInput             = errors.New("invalid browser instance input")
	ErrInvalidAction            = errors.New("invalid browser instance action")
	ErrIdempotencyConflict      = errors.New("idempotency key already used for another command")
	ErrInvalidCommandTransition = errors.New("invalid command status transition")
)

type BrowserInstance struct {
	ID                    string     `json:"id"`
	WorkspaceID           string     `json:"workspaceId"`
	Name                  string     `json:"name"`
	Platform              string     `json:"platform"`
	FingerprintTemplateID string     `json:"fingerprintTemplateId,omitempty"`
	ProxyAssignmentID     string     `json:"proxyAssignmentId,omitempty"`
	ProfileID             string     `json:"profileId,omitempty"`
	DesiredState          string     `json:"desiredState"`
	ObservedState         string     `json:"observedState"`
	AssignedDeviceID      string     `json:"assignedDeviceId,omitempty"`
	CurrentRevision       int64      `json:"currentRevision"`
	Version               int64      `json:"version"`
	Tags                  []string   `json:"tags"`
	LastSeenAt            *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	DeletedAt             *time.Time `json:"deletedAt,omitempty"`
}

type Command struct {
	ID              string                 `json:"id"`
	WorkspaceID     string                 `json:"workspaceId"`
	InstanceID      string                 `json:"instanceId"`
	DeviceID        string                 `json:"deviceId,omitempty"`
	Action          string                 `json:"action"`
	IdempotencyKey  string                 `json:"idempotencyKey"`
	ExpectedVersion int64                  `json:"expectedVersion"`
	Status          string                 `json:"status"`
	Payload         map[string]interface{} `json:"payload"`
	Deadline        time.Time              `json:"deadline"`
	CreatedBy       string                 `json:"createdBy"`
	CreatedAt       time.Time              `json:"createdAt"`
	AcknowledgedAt  *time.Time             `json:"acknowledgedAt,omitempty"`
	CompletedAt     *time.Time             `json:"completedAt,omitempty"`
	FailureCode     string                 `json:"failureCode,omitempty"`
	FailureMessage  string                 `json:"failureMessage,omitempty"`
}

type Repository interface {
	FindDevice(context.Context, string) (deviceservice.Device, error)
	CreateInstance(context.Context, BrowserInstance) error
	FindInstance(context.Context, string, string) (BrowserInstance, error)
	ListInstances(context.Context, string) ([]BrowserInstance, error)
	UpdateInstance(context.Context, BrowserInstance, int64) (BrowserInstance, error)
	SoftDeleteInstance(context.Context, string, string, int64, time.Time) error
	CreateCommand(context.Context, Command, string) (Command, BrowserInstance, error)
	FindCommandByIdempotencyKey(context.Context, string, string) (Command, error)
	ExpireCommands(context.Context, string, string, time.Time) error
	ListPendingCommands(context.Context, string, string) ([]Command, error)
	TransitionCommand(context.Context, string, string, string, string, string, string, time.Time) (Command, error)
	UpdateObservedState(context.Context, string, string, string, string, map[string]interface{}, time.Time) (BrowserInstance, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
}

type CreateInput struct {
	Name                  string   `json:"name"`
	Platform              string   `json:"platform"`
	ProfileID             string   `json:"profileId,omitempty"`
	FingerprintTemplateID string   `json:"fingerprintTemplateId,omitempty"`
	ProxyAssignmentID     string   `json:"proxyAssignmentId,omitempty"`
	AssignedDeviceID      string   `json:"assignedDeviceId,omitempty"`
	Tags                  []string `json:"tags,omitempty"`
}

type UpdateInput struct {
	Name                  *string   `json:"name,omitempty"`
	ProfileID             *string   `json:"profileId,omitempty"`
	FingerprintTemplateID *string   `json:"fingerprintTemplateId,omitempty"`
	ProxyAssignmentID     *string   `json:"proxyAssignmentId,omitempty"`
	AssignedDeviceID      *string   `json:"assignedDeviceId,omitempty"`
	Tags                  *[]string `json:"tags,omitempty"`
}

type CloneInput struct {
	Name             string  `json:"name,omitempty"`
	AssignedDeviceID *string `json:"assignedDeviceId,omitempty"`
}

type CommandInput struct {
	Action          string                 `json:"action"`
	ExpectedVersion int64                  `json:"expectedVersion"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
}

type AgentCommandEventInput struct {
	Status         string
	FailureCode    string
	FailureMessage string
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now}
}

func (s *Service) Create(ctx context.Context, actorID, workspaceID string, input CreateInput) (BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceCreate); err != nil {
		return BrowserInstance{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return BrowserInstance{}, errors.New("valid instance name is required")
	}
	now := s.now().UTC()
	instance := BrowserInstance{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Name: name,
		Platform:              strings.ToLower(strings.TrimSpace(input.Platform)),
		FingerprintTemplateID: strings.TrimSpace(input.FingerprintTemplateID),
		ProxyAssignmentID:     strings.TrimSpace(input.ProxyAssignmentID), ProfileID: strings.TrimSpace(input.ProfileID),
		DesiredState: "stopped", ObservedState: "offline", AssignedDeviceID: strings.TrimSpace(input.AssignedDeviceID),
		Version: 1, Tags: normalizeTags(input.Tags), CreatedAt: now, UpdatedAt: now,
	}
	if instance.Platform == "" {
		instance.Platform = "chromium"
	}
	if err := s.repository.CreateInstance(ctx, instance); err != nil {
		return BrowserInstance{}, err
	}
	return instance, nil
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceRead); err != nil {
		return nil, err
	}
	return s.repository.ListInstances(ctx, workspaceID)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, instanceID string) (BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceRead); err != nil {
		return BrowserInstance{}, err
	}
	return s.repository.FindInstance(ctx, workspaceID, instanceID)
}

func (s *Service) Update(ctx context.Context, actorID, workspaceID, instanceID string, expectedVersion int64, input UpdateInput) (BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceUpdate); err != nil {
		return BrowserInstance{}, err
	}
	if expectedVersion < 1 {
		return BrowserInstance{}, ErrInvalidInput
	}
	instance, err := s.repository.FindInstance(ctx, workspaceID, instanceID)
	if err != nil {
		return BrowserInstance{}, err
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 120 {
			return BrowserInstance{}, ErrInvalidInput
		}
		instance.Name = name
	}
	if input.ProfileID != nil {
		if instance.isRuntimeActive() {
			return BrowserInstance{}, ErrStateConflict
		}
		instance.ProfileID = strings.TrimSpace(*input.ProfileID)
	}
	if input.FingerprintTemplateID != nil {
		if instance.isRuntimeActive() {
			return BrowserInstance{}, ErrStateConflict
		}
		instance.FingerprintTemplateID = strings.TrimSpace(*input.FingerprintTemplateID)
	}
	if input.ProxyAssignmentID != nil {
		if instance.isRuntimeActive() {
			return BrowserInstance{}, ErrStateConflict
		}
		instance.ProxyAssignmentID = strings.TrimSpace(*input.ProxyAssignmentID)
	}
	if input.AssignedDeviceID != nil {
		if instance.isRuntimeActive() {
			return BrowserInstance{}, ErrStateConflict
		}
		instance.AssignedDeviceID = strings.TrimSpace(*input.AssignedDeviceID)
	}
	if input.Tags != nil {
		instance.Tags = normalizeTags(*input.Tags)
	}
	instance.UpdatedAt = s.now().UTC()
	return s.repository.UpdateInstance(ctx, instance, expectedVersion)
}

func (s *Service) Delete(ctx context.Context, actorID, workspaceID, instanceID string, expectedVersion int64) error {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceDelete); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return ErrInvalidInput
	}
	instance, err := s.repository.FindInstance(ctx, workspaceID, instanceID)
	if err != nil {
		return err
	}
	if instance.isRuntimeActive() || instance.DesiredState == "running" || instance.DesiredState == "starting" || instance.DesiredState == "migrating" {
		return ErrStateConflict
	}
	return s.repository.SoftDeleteInstance(ctx, workspaceID, instanceID, expectedVersion, s.now().UTC())
}

func (s *Service) Clone(ctx context.Context, actorID, workspaceID, instanceID string, input CloneInput) (BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceCreate); err != nil {
		return BrowserInstance{}, err
	}
	source, err := s.repository.FindInstance(ctx, workspaceID, instanceID)
	if err != nil {
		return BrowserInstance{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = source.Name + " Copy"
	}
	assignedDeviceID := source.AssignedDeviceID
	if input.AssignedDeviceID != nil {
		assignedDeviceID = strings.TrimSpace(*input.AssignedDeviceID)
	}
	return s.Create(ctx, actorID, workspaceID, CreateInput{
		Name:     name,
		Platform: source.Platform,
		// A profile fork/assignment fork requires a cross-service transaction.
		// Keep clones config-only rather than sharing mutable runtime references.
		ProfileID:             "",
		FingerprintTemplateID: source.FingerprintTemplateID,
		ProxyAssignmentID:     "",
		AssignedDeviceID:      assignedDeviceID,
		Tags:                  append([]string(nil), source.Tags...),
	})
}

func (s *Service) RequestCommand(ctx context.Context, actorID, workspaceID, instanceID, idempotencyKey string, input CommandInput) (Command, BrowserInstance, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionInstanceOperate); err != nil {
		return Command{}, BrowserInstance{}, err
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if input.ExpectedVersion < 1 {
		return Command{}, BrowserInstance{}, ErrInvalidInput
	}
	desiredState := ""
	targetDeviceID := ""
	switch action {
	case "instance.start":
		desiredState = "running"
	case "instance.stop":
		desiredState = "stopped"
	case "instance.restart":
		desiredState = "running"
	case "instance.migrate":
		targetDeviceID, _ = input.Payload["targetDeviceId"].(string)
		targetDeviceID = strings.TrimSpace(targetDeviceID)
		if _, err := uuid.Parse(targetDeviceID); err != nil {
			return Command{}, BrowserInstance{}, ErrInvalidInput
		}
		input.Payload["targetDeviceId"] = targetDeviceID
		desiredState = "migrating"
	default:
		return Command{}, BrowserInstance{}, ErrInvalidAction
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		return Command{}, BrowserInstance{}, errors.New("Idempotency-Key is required")
	}
	if existing, err := s.repository.FindCommandByIdempotencyKey(ctx, workspaceID, idempotencyKey); err == nil {
		if !existing.MatchesRequest(instanceID, action, input.ExpectedVersion, input.Payload) {
			return Command{}, BrowserInstance{}, ErrIdempotencyConflict
		}
		instance, findErr := s.repository.FindInstance(ctx, workspaceID, instanceID)
		return existing, instance, findErr
	} else if !errors.Is(err, ErrNotFound) {
		return Command{}, BrowserInstance{}, err
	}
	if (action == "instance.migrate" && len(input.Payload) != 1) || (action != "instance.migrate" && len(input.Payload) != 0) {
		return Command{}, BrowserInstance{}, ErrInvalidInput
	}
	instance, err := s.repository.FindInstance(ctx, workspaceID, instanceID)
	if err != nil {
		return Command{}, BrowserInstance{}, err
	}
	if strings.TrimSpace(instance.AssignedDeviceID) == "" {
		return Command{}, BrowserInstance{}, ErrInvalidInput
	}
	sourceDevice, findErr := s.repository.FindDevice(ctx, instance.AssignedDeviceID)
	if findErr != nil || sourceDevice.WorkspaceID != workspaceID || sourceDevice.RevokedAt != nil {
		return Command{}, BrowserInstance{}, ErrInvalidInput
	}
	if action == "instance.migrate" {
		if targetDeviceID == instance.AssignedDeviceID {
			return Command{}, BrowserInstance{}, ErrInvalidInput
		}
		targetDevice, targetErr := s.repository.FindDevice(ctx, targetDeviceID)
		if targetErr != nil || targetDevice.WorkspaceID != workspaceID || targetDevice.RevokedAt != nil {
			return Command{}, BrowserInstance{}, ErrInvalidInput
		}
	}
	now := s.now().UTC()
	command := Command{
		ID: uuid.NewString(), WorkspaceID: workspaceID, InstanceID: instanceID,
		DeviceID: instance.AssignedDeviceID, Action: action, IdempotencyKey: idempotencyKey,
		ExpectedVersion: input.ExpectedVersion, Status: "pending", Payload: input.Payload,
		Deadline: now.Add(2 * time.Minute), CreatedBy: actorID, CreatedAt: now,
	}
	return s.repository.CreateCommand(ctx, command, desiredState)
}

func (s *Service) PendingCommands(ctx context.Context, workspaceID, deviceID string) ([]Command, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(deviceID) == "" {
		return nil, errors.New("workspaceId and deviceId are required")
	}
	if err := s.repository.ExpireCommands(ctx, workspaceID, deviceID, s.now().UTC()); err != nil {
		return nil, err
	}
	return s.repository.ListPendingCommands(ctx, workspaceID, deviceID)
}

func (s *Service) ApplyAgentCommandEvent(ctx context.Context, workspaceID, deviceID, commandID string, input AgentCommandEventInput) (Command, error) {
	status := strings.ToLower(strings.TrimSpace(input.Status))
	switch status {
	case "accepted", "running", "completed", "failed":
	default:
		return Command{}, errors.New("invalid command status")
	}
	return s.repository.TransitionCommand(
		ctx, workspaceID, deviceID, commandID, status,
		strings.TrimSpace(input.FailureCode), strings.TrimSpace(input.FailureMessage), s.now().UTC(),
	)
}

func (s *Service) ApplyObservedState(ctx context.Context, workspaceID, deviceID, instanceID, state string, payload map[string]interface{}) (BrowserInstance, error) {
	state = strings.ToLower(strings.TrimSpace(state))
	switch state {
	case "offline", "starting", "running", "stopping", "failed":
	default:
		return BrowserInstance{}, errors.New("invalid observed state")
	}
	return s.repository.UpdateObservedState(ctx, workspaceID, deviceID, instanceID, state, payload, s.now().UTC())
}

func IsTerminalCommandStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "expired"
}

func CanTransitionCommand(from, to string) bool {
	if from == to {
		return true
	}
	if IsTerminalCommandStatus(from) {
		return false
	}
	switch from {
	case "pending", "queued":
		return to == "accepted" || to == "running" || to == "completed" || to == "failed"
	case "accepted":
		return to == "running" || to == "completed" || to == "failed"
	case "running":
		return to == "completed" || to == "failed"
	default:
		return false
	}
}

func normalizeTags(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > 40 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 20 {
			break
		}
	}
	return result
}

func (instance BrowserInstance) isRuntimeActive() bool {
	// Desired state fences the interval between command submission and the
	// agent's first observation, when the browser can already be starting.
	if instance.DesiredState == "running" || instance.DesiredState == "migrating" {
		return true
	}
	switch instance.ObservedState {
	case "running", "starting", "stopping", "migrating":
		return true
	default:
		return false
	}
}

// MatchesRequest compares the canonical JSON payload too: changing a migration
// destination must not silently replay a command addressed to another device.
func (command Command) MatchesRequest(instanceID, action string, version int64, payload map[string]interface{}) bool {
	if command.InstanceID != instanceID || command.Action != action || command.ExpectedVersion != version {
		return false
	}
	if len(command.Payload) == 0 && len(payload) == 0 {
		return true
	}
	previous, previousErr := json.Marshal(command.Payload)
	next, nextErr := json.Marshal(payload)
	return previousErr == nil && nextErr == nil && string(previous) == string(next)
}
