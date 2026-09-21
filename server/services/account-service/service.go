// Package accountservice contains the tenant-scoped Account Center domain.
//
// Secrets deliberately do not appear on Account.  Callers provide an
// envelope-encrypted value (or use an EnvelopeCrypto implementation before
// calling PutSecret); repositories persist only the envelope metadata.
package accountservice

import (
	"context"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound        = errors.New("account not found")
	ErrVersionConflict = errors.New("account version conflict")
	ErrSecretRequired  = errors.New("encrypted secret envelope is required")
	ErrUnsupported     = errors.New("account repository operation is unsupported")
)

type Platform string

const (
	PlatformAmazon   Platform = "amazon"
	PlatformShopify  Platform = "shopify"
	PlatformTikTok   Platform = "tiktok"
	PlatformFacebook Platform = "facebook"
	PlatformGoogle   Platform = "google"
	PlatformEBay     Platform = "ebay"
	PlatformEbay     Platform = PlatformEBay // compatibility alias
)

func (p Platform) Valid() bool {
	switch p {
	case PlatformAmazon, PlatformShopify, PlatformTikTok, PlatformFacebook, PlatformGoogle, PlatformEBay:
		return true
	default:
		return false
	}
}

const (
	StatusPending            = "pending"
	StatusActive             = "active"
	StatusSuspended          = "suspended"
	StatusDisabled           = "disabled"
	StatusVerificationNeeded = "verification_required"
	StatusError              = "error"
	// Legacy persistence-compatible lifecycle values.
	StatusPaused  = "paused"
	StatusRisk    = "risk"
	StatusLocked  = "locked"
	StatusDeleted = "deleted"
	RiskUnknown   = "unknown"
	RiskLow       = "low"
	RiskMedium    = "medium"
	RiskHigh      = "high"
	RiskCritical  = "critical"
)

type Account struct {
	ID          string            `json:"id"`
	WorkspaceID string            `json:"workspaceId"`
	Platform    Platform          `json:"platform"`
	Name        string            `json:"name"`
	Identifier  string            `json:"identifier"`
	ExternalID  string            `json:"externalId,omitempty"`
	Username    string            `json:"username,omitempty"`
	Email       string            `json:"email,omitempty"`
	Region      string            `json:"region,omitempty"`
	Status      string            `json:"status"`
	RiskLevel   string            `json:"riskLevel"`
	Notes       string            `json:"notes,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Version     int64             `json:"version"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	DeletedAt   *time.Time        `json:"deletedAt,omitempty"`
	// DisplayName and ExternalIdentifier are compatibility aliases used by the
	// persistence schema. Service responses populate both pairs.
	DisplayName        string `json:"displayName,omitempty"`
	ExternalIdentifier string `json:"externalIdentifier,omitempty"`
	ProfileID          string `json:"profileId,omitempty"`
	BrowserInstanceID  string `json:"browserInstanceId,omitempty"`
	CreatedBy          string `json:"createdBy,omitempty"`
}

// SecretEnvelope is safe to persist and return as metadata. Ciphertext must
// already be produced by an envelope encryption system; plaintext is never a
// field in this type.
type SecretEnvelope struct {
	Algorithm    string `json:"algorithm"`
	Ciphertext   string `json:"-"`
	Nonce        string `json:"-"`
	KeyReference string `json:"-"`
	KeyVersion   string `json:"keyVersion"`
	EncryptedDEK string `json:"-"`
	Fingerprint  string `json:"fingerprint,omitempty"`
}

type AccountSecret struct {
	ID          string         `json:"id"`
	AccountID   string         `json:"accountId"`
	WorkspaceID string         `json:"workspaceId"`
	Kind        string         `json:"kind"`
	SecretType  string         `json:"secretType,omitempty"`
	Envelope    SecretEnvelope `json:"envelope"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	Version     int64          `json:"version"`
}

type AccountBinding struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"accountId"`
	WorkspaceID string    `json:"workspaceId"`
	BindingType string    `json:"bindingType"`
	TargetID    string    `json:"targetId"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type RiskEvent struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"accountId"`
	WorkspaceID string    `json:"workspaceId"`
	Level       string    `json:"level"`
	Code        string    `json:"code"`
	Description string    `json:"description,omitempty"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
}

type CreateInput struct {
	Platform           string            `json:"platform"`
	Name               string            `json:"name"`
	Identifier         string            `json:"identifier"`
	ExternalID         string            `json:"externalId,omitempty"`
	Username           string            `json:"username,omitempty"`
	Email              string            `json:"email,omitempty"`
	Region             string            `json:"region,omitempty"`
	Status             string            `json:"status,omitempty"`
	RiskLevel          string            `json:"riskLevel,omitempty"`
	Notes              string            `json:"notes,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DisplayName        string            `json:"displayName,omitempty"`
	ExternalIdentifier string            `json:"externalIdentifier,omitempty"`
	ProfileID          string            `json:"profileId,omitempty"`
	BrowserInstanceID  string            `json:"browserInstanceId,omitempty"`
	CreatedBy          string            `json:"createdBy,omitempty"`
}

type UpdateInput struct {
	Name               string            `json:"name"`
	Identifier         string            `json:"identifier"`
	ExternalID         string            `json:"externalId,omitempty"`
	Username           string            `json:"username,omitempty"`
	Email              string            `json:"email,omitempty"`
	Region             string            `json:"region,omitempty"`
	Status             string            `json:"status,omitempty"`
	RiskLevel          string            `json:"riskLevel,omitempty"`
	Notes              string            `json:"notes,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DisplayName        string            `json:"displayName,omitempty"`
	ExternalIdentifier string            `json:"externalIdentifier,omitempty"`
	ProfileID          string            `json:"profileId,omitempty"`
	BrowserInstanceID  string            `json:"browserInstanceId,omitempty"`
	CreatedBy          string            `json:"createdBy,omitempty"`
}

// EnvelopeCrypto is intentionally a provider boundary. Production wiring can
// use KMS/Vault; the Account Center itself has no key material or crypto code.
type EnvelopeCrypto interface {
	Encrypt(context.Context, []byte, []byte) (SecretEnvelope, error)
	Decrypt(context.Context, SecretEnvelope, []byte) ([]byte, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

// Repository is the required account persistence surface. Optional
// capabilities are represented by the narrower interfaces below, allowing
// read-only adapters and incremental deployments.
type Repository interface {
	CreateAccount(context.Context, Account) error
	FindAccount(context.Context, string, string) (Account, error)
	ListAccounts(context.Context, string) ([]Account, error)
	UpdateAccount(context.Context, Account, int64) (Account, error)
	DeleteAccount(context.Context, string, string, int64, time.Time) error
}

type SecretRepository interface {
	PutAccountSecret(context.Context, AccountSecret) error
	ListAccountSecrets(context.Context, string, string) ([]AccountSecret, error)
	DeleteAccountSecret(context.Context, string, string, string) error
}

type BindingRepository interface {
	UpsertAccountBinding(context.Context, AccountBinding) (AccountBinding, error)
	ListAccountBindings(context.Context, string, string) ([]AccountBinding, error)
	DeleteAccountBinding(context.Context, string, string, string) error
}

type RiskEventRepository interface {
	CreateRiskEvent(context.Context, RiskEvent) error
	ListRiskEvents(context.Context, string, string) ([]RiskEvent, error)
}

type Service struct {
	repository Repository
	authorizer Authorizer
	crypto     EnvelopeCrypto
	now        func() time.Time
}

// AccountService is kept as a domain-oriented name for callers migrating
// from the legacy account-service package.
type AccountService = Service

func New(repository Repository, authorizer Authorizer, crypto ...EnvelopeCrypto) *Service {
	s := &Service{repository: repository, authorizer: authorizer, now: time.Now}
	if len(crypto) > 0 {
		s.crypto = crypto[0]
	}
	return s
}

// NewService is an explicit constructor alias for dependency-wiring code.
func NewService(repository Repository, authorizer Authorizer, crypto ...EnvelopeCrypto) *Service {
	return New(repository, authorizer, crypto...)
}

func (s *Service) Create(ctx context.Context, actorID, workspaceID string, input CreateInput) (Account, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return Account{}, err
	}
	account, err := normalizeCreate(workspaceID, input)
	if err != nil {
		return Account{}, err
	}
	now := s.now().UTC()
	account.ID, account.Version, account.CreatedAt, account.UpdatedAt = uuid.NewString(), 1, now, now
	account.CreatedBy = actorID
	if err := s.repository.CreateAccount(ctx, account); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (s *Service) CreateAccount(ctx context.Context, actorID, workspaceID string, input CreateInput) (Account, error) {
	return s.Create(ctx, actorID, workspaceID, input)
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]Account, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountRead); err != nil {
		return nil, err
	}
	return s.repository.ListAccounts(ctx, strings.TrimSpace(workspaceID))
}

func (s *Service) ListAccounts(ctx context.Context, actorID, workspaceID string) ([]Account, error) {
	return s.List(ctx, actorID, workspaceID)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, accountID string) (Account, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountRead); err != nil {
		return Account{}, err
	}
	return s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
}

func (s *Service) GetAccount(ctx context.Context, actorID, workspaceID, accountID string) (Account, error) {
	return s.Get(ctx, actorID, workspaceID, accountID)
}

func (s *Service) Update(ctx context.Context, actorID, workspaceID, accountID string, expectedVersion int64, input UpdateInput) (Account, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return Account{}, err
	}
	if expectedVersion < 1 {
		return Account{}, errors.New("expectedVersion must be positive")
	}
	current, err := s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
	if err != nil {
		return Account{}, err
	}
	updated, err := normalizeUpdate(current, input)
	if err != nil {
		return Account{}, err
	}
	updated.UpdatedAt = s.now().UTC()
	return s.repository.UpdateAccount(ctx, updated, expectedVersion)
}

func (s *Service) UpdateAccount(ctx context.Context, actorID, workspaceID, accountID string, expectedVersion int64, input UpdateInput) (Account, error) {
	return s.Update(ctx, actorID, workspaceID, accountID, expectedVersion, input)
}

func (s *Service) Delete(ctx context.Context, actorID, workspaceID, accountID string, expectedVersion int64) error {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return errors.New("expectedVersion must be positive")
	}
	return s.repository.DeleteAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID), expectedVersion, s.now().UTC())
}

func (s *Service) DeleteAccount(ctx context.Context, actorID, workspaceID, accountID string, expectedVersion int64) error {
	return s.Delete(ctx, actorID, workspaceID, accountID, expectedVersion)
}

func (s *Service) SetStatus(ctx context.Context, actorID, workspaceID, accountID, status string, expectedVersion int64) (Account, error) {
	account, err := s.GetForManage(ctx, actorID, workspaceID, accountID)
	if err != nil {
		return Account{}, err
	}
	return s.Update(ctx, actorID, workspaceID, accountID, expectedVersion, UpdateInput{Name: account.Name, Identifier: account.Identifier, ExternalID: account.ExternalID, Username: account.Username, Email: account.Email, Region: account.Region, Status: status, RiskLevel: account.RiskLevel, Notes: account.Notes, Metadata: account.Metadata})
}

func (s *Service) SetAccountStatus(ctx context.Context, actorID, workspaceID, accountID, status string, expectedVersion int64) (Account, error) {
	return s.SetStatus(ctx, actorID, workspaceID, accountID, status, expectedVersion)
}

func (s *Service) SetRiskLevel(ctx context.Context, actorID, workspaceID, accountID, risk string, expectedVersion int64) (Account, error) {
	account, err := s.GetForManage(ctx, actorID, workspaceID, accountID)
	if err != nil {
		return Account{}, err
	}
	return s.Update(ctx, actorID, workspaceID, accountID, expectedVersion, UpdateInput{Name: account.Name, Identifier: account.Identifier, ExternalID: account.ExternalID, Username: account.Username, Email: account.Email, Region: account.Region, Status: account.Status, RiskLevel: risk, Notes: account.Notes, Metadata: account.Metadata})
}

func (s *Service) SetAccountRiskLevel(ctx context.Context, actorID, workspaceID, accountID, risk string, expectedVersion int64) (Account, error) {
	return s.SetRiskLevel(ctx, actorID, workspaceID, accountID, risk, expectedVersion)
}

// GetForManage is useful to handlers that need a fresh version before a
// mutation; it still authorizes as account.manage.
func (s *Service) GetForManage(ctx context.Context, actorID, workspaceID, accountID string) (Account, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return Account{}, err
	}
	return s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
}

func (s *Service) PutSecret(ctx context.Context, actorID, workspaceID, accountID, kind string, envelope SecretEnvelope) (AccountSecret, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return AccountSecret{}, err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "password", "cookie", "totp_seed", "api_key", "oauth_token":
	default:
		return AccountSecret{}, errors.New("secret kind is invalid")
	}
	if err := validateEnvelope(envelope); err != nil {
		return AccountSecret{}, err
	}
	if _, err := s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID)); err != nil {
		return AccountSecret{}, err
	}
	repository, ok := s.repository.(SecretRepository)
	if !ok {
		return AccountSecret{}, ErrUnsupported
	}
	now := s.now().UTC()
	secret := AccountSecret{ID: uuid.NewString(), AccountID: strings.TrimSpace(accountID), WorkspaceID: strings.TrimSpace(workspaceID), Kind: strings.TrimSpace(kind), SecretType: strings.TrimSpace(kind), Envelope: envelope, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := repository.PutAccountSecret(ctx, secret); err != nil {
		return AccountSecret{}, err
	}
	return secret, nil
}

func (s *Service) StoreSecret(ctx context.Context, actorID, workspaceID, accountID, kind string, plaintext []byte, aad []byte) (AccountSecret, error) {
	// Authorize before invoking the crypto provider so unauthorized requests
	// cannot trigger encryption work or provider-side observations.
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return AccountSecret{}, err
	}
	if s.crypto == nil {
		return AccountSecret{}, errors.New("envelope crypto provider is not configured")
	}
	if len(plaintext) == 0 {
		return AccountSecret{}, ErrSecretRequired
	}
	envelope, err := s.crypto.Encrypt(ctx, plaintext, aad)
	if err != nil {
		return AccountSecret{}, err
	}
	return s.PutSecret(ctx, actorID, workspaceID, accountID, kind, envelope)
}

func (s *Service) ListSecrets(ctx context.Context, actorID, workspaceID, accountID string) ([]AccountSecret, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountRead); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(SecretRepository)
	if !ok {
		return nil, ErrUnsupported
	}
	return repository.ListAccountSecrets(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
}

// DeleteSecret revokes an account secret after checking account-management
// authorization. Secret material is never returned by this operation.
func (s *Service) DeleteSecret(ctx context.Context, actorID, workspaceID, accountID, secretID string) error {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return err
	}
	repository, ok := s.repository.(SecretRepository)
	if !ok {
		return ErrUnsupported
	}
	return repository.DeleteAccountSecret(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID), strings.TrimSpace(secretID))
}

func (s *Service) Bind(ctx context.Context, actorID, workspaceID, accountID, bindingType, targetID string) (AccountBinding, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return AccountBinding{}, err
	}
	if strings.TrimSpace(bindingType) == "" || strings.TrimSpace(targetID) == "" {
		return AccountBinding{}, errors.New("bindingType and targetId are required")
	}
	repository, ok := s.repository.(BindingRepository)
	if !ok {
		return AccountBinding{}, ErrUnsupported
	}
	if _, err := s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID)); err != nil {
		return AccountBinding{}, err
	}
	now := s.now().UTC()
	return repository.UpsertAccountBinding(ctx, AccountBinding{ID: uuid.NewString(), AccountID: strings.TrimSpace(accountID), WorkspaceID: strings.TrimSpace(workspaceID), BindingType: strings.TrimSpace(bindingType), TargetID: strings.TrimSpace(targetID), Status: "active", CreatedAt: now, UpdatedAt: now})
}

func (s *Service) ListBindings(ctx context.Context, actorID, workspaceID, accountID string) ([]AccountBinding, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountRead); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(BindingRepository)
	if !ok {
		return nil, ErrUnsupported
	}
	return repository.ListAccountBindings(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
}

// Unbind removes an active account binding after checking account-management
// authorization.
func (s *Service) Unbind(ctx context.Context, actorID, workspaceID, accountID, bindingID string) error {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return err
	}
	repository, ok := s.repository.(BindingRepository)
	if !ok {
		return ErrUnsupported
	}
	return repository.DeleteAccountBinding(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID), strings.TrimSpace(bindingID))
}

func (s *Service) RecordRiskEvent(ctx context.Context, actorID, workspaceID, accountID, level, code, description string) (RiskEvent, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountManage); err != nil {
		return RiskEvent{}, err
	}
	level = normalizeRisk(level)
	if !validRisk(level) || strings.TrimSpace(code) == "" {
		return RiskEvent{}, errors.New("valid risk level and code are required")
	}
	repository, ok := s.repository.(RiskEventRepository)
	if !ok {
		return RiskEvent{}, ErrUnsupported
	}
	if _, err := s.repository.FindAccount(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID)); err != nil {
		return RiskEvent{}, err
	}
	event := RiskEvent{ID: uuid.NewString(), AccountID: strings.TrimSpace(accountID), WorkspaceID: strings.TrimSpace(workspaceID), Level: level, Code: strings.TrimSpace(code), Description: strings.TrimSpace(description), CreatedBy: actorID, CreatedAt: s.now().UTC()}
	if err := repository.CreateRiskEvent(ctx, event); err != nil {
		return RiskEvent{}, err
	}
	return event, nil
}

// ListRiskEvents returns the tenant-scoped risk-event history.
func (s *Service) ListRiskEvents(ctx context.Context, actorID, workspaceID, accountID string) ([]RiskEvent, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionAccountRead); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(RiskEventRepository)
	if !ok {
		return nil, ErrUnsupported
	}
	return repository.ListRiskEvents(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(accountID))
}

func (s *Service) require(ctx context.Context, workspaceID, actorID string, permission memberservice.Permission) error {
	workspaceID, actorID = strings.TrimSpace(workspaceID), strings.TrimSpace(actorID)
	if workspaceID == "" || actorID == "" {
		return errors.New("workspaceId and actorId are required")
	}
	if s.authorizer == nil {
		return errors.New("authorizer is not configured")
	}
	return s.authorizer.Require(ctx, workspaceID, actorID, permission)
}

func normalizeCreate(workspaceID string, input CreateInput) (Account, error) {
	platform := Platform(strings.ToLower(strings.TrimSpace(input.Platform)))
	if !platform.Valid() {
		return Account{}, errors.New("platform must be amazon, shopify, tiktok, facebook, google, or ebay")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.TrimSpace(input.DisplayName)
	}
	identifier := strings.TrimSpace(input.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(input.ExternalIdentifier)
	}
	account := Account{WorkspaceID: strings.TrimSpace(workspaceID), Platform: platform, Name: name, Identifier: identifier, ExternalID: strings.TrimSpace(input.ExternalID), Username: strings.TrimSpace(input.Username), Email: strings.TrimSpace(input.Email), Region: strings.TrimSpace(input.Region), Status: strings.TrimSpace(input.Status), RiskLevel: normalizeRisk(input.RiskLevel), Notes: strings.TrimSpace(input.Notes), Metadata: cloneMetadata(input.Metadata), ProfileID: strings.TrimSpace(input.ProfileID), BrowserInstanceID: strings.TrimSpace(input.BrowserInstanceID)}
	if account.Name == "" || len([]rune(account.Name)) > 120 {
		return Account{}, errors.New("valid account name is required")
	}
	if account.Identifier == "" {
		account.Identifier = account.ExternalID
		if account.Identifier == "" {
			account.Identifier = account.Username
		}
		if account.Identifier == "" {
			account.Identifier = account.Email
		}
	}
	if account.Identifier == "" || len([]rune(account.Identifier)) > 255 {
		return Account{}, errors.New("account identifier is required")
	}
	if account.Email != "" {
		if _, err := mail.ParseAddress(account.Email); err != nil {
			return Account{}, errors.New("email is invalid")
		}
	}
	if account.Platform == PlatformShopify && strings.Contains(account.Identifier, "://") {
		parsed, err := url.Parse(account.Identifier)
		if err != nil || parsed.Host == "" {
			return Account{}, errors.New("shopify identifier must be a valid store URL")
		}
	}
	if account.Status == "" {
		account.Status = StatusPending
	}
	if !validStatus(account.Status) {
		return Account{}, errors.New("invalid account status")
	}
	if !validRisk(account.RiskLevel) {
		return Account{}, errors.New("invalid account risk level")
	}
	account.DisplayName, account.ExternalIdentifier = account.Name, account.Identifier
	return account, nil
}

// ValidateCreateInput validates platform identity and lifecycle fields without
// persisting anything. It is shared by HTTP handlers and import workflows.
func ValidateCreateInput(workspaceID string, input CreateInput) error {
	_, err := normalizeCreate(workspaceID, input)
	return err
}

func normalizeUpdate(current Account, input UpdateInput) (Account, error) {
	if strings.TrimSpace(input.Name) == "" {
		input.Name = input.DisplayName
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = current.Name
	}
	if strings.TrimSpace(input.Identifier) == "" {
		input.Identifier = input.ExternalIdentifier
	}
	if strings.TrimSpace(input.Identifier) == "" {
		input.Identifier = current.Identifier
	}
	account := current
	account.Name, account.Identifier, account.ExternalID, account.Username, account.Email, account.Region, account.Notes = strings.TrimSpace(input.Name), strings.TrimSpace(input.Identifier), strings.TrimSpace(input.ExternalID), strings.TrimSpace(input.Username), strings.TrimSpace(input.Email), strings.TrimSpace(input.Region), strings.TrimSpace(input.Notes)
	account.Metadata = cloneMetadata(input.Metadata)
	if input.ProfileID != "" {
		account.ProfileID = strings.TrimSpace(input.ProfileID)
	}
	if input.BrowserInstanceID != "" {
		account.BrowserInstanceID = strings.TrimSpace(input.BrowserInstanceID)
	}
	if account.Name == "" || len([]rune(account.Name)) > 120 || account.Identifier == "" {
		return Account{}, errors.New("valid account name and identifier are required")
	}
	if account.Email != "" {
		if _, err := mail.ParseAddress(account.Email); err != nil {
			return Account{}, errors.New("email is invalid")
		}
	}
	if input.Status == "" {
		account.Status = current.Status
	} else {
		account.Status = strings.ToLower(strings.TrimSpace(input.Status))
	}
	if !validStatus(account.Status) {
		return Account{}, errors.New("invalid account status")
	}
	if input.RiskLevel == "" {
		account.RiskLevel = current.RiskLevel
	} else {
		account.RiskLevel = normalizeRisk(input.RiskLevel)
	}
	if !validRisk(account.RiskLevel) {
		return Account{}, errors.New("invalid account risk level")
	}
	account.DisplayName, account.ExternalIdentifier = account.Name, account.Identifier
	return account, nil
}

func validateEnvelope(envelope SecretEnvelope) error {
	if strings.TrimSpace(envelope.Algorithm) == "" || strings.TrimSpace(envelope.Ciphertext) == "" || strings.TrimSpace(envelope.KeyReference) == "" || strings.TrimSpace(envelope.KeyVersion) == "" {
		return ErrSecretRequired
	}
	return nil
}
func validStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case StatusPending, StatusActive, StatusSuspended, StatusDisabled, StatusVerificationNeeded, StatusError, StatusPaused, StatusRisk, StatusLocked, StatusDeleted:
		return true
	}
	return false
}
func validRisk(value string) bool {
	switch normalizeRisk(value) {
	case RiskUnknown, RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}
func normalizeRisk(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return RiskUnknown
	}
	return value
}
func cloneMetadata(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
