// Package billingservice contains the tenant-scoped billing, entitlement and
// license domain. Payment providers and webhook ingestion intentionally do
// not live here; this package only evaluates state that has already been
// recorded by an explicitly trusted control-plane workflow.
package billingservice

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound            = errors.New("billing resource not found")
	ErrInvalidInput        = errors.New("billing input is invalid")
	ErrQuotaExceeded       = errors.New("billing quota exceeded")
	ErrIdempotencyConflict = errors.New("billing idempotency key conflicts with an existing operation")
	ErrStateConflict       = errors.New("billing state transition conflict")
	ErrLicenseInvalid      = errors.New("license is invalid")
	ErrLicenseRevoked      = errors.New("license is revoked")
	ErrLicenseExpired      = errors.New("license is expired")
	ErrLicenseScope        = errors.New("license scope does not match the request")
)

const (
	PlanFree         = "free"
	PlanProfessional = "professional"
	PlanEnterprise   = "enterprise"

	EntitlementInstances      = "instances"
	EntitlementTeamMembers    = "team_members"
	EntitlementAutomationRuns = "automation_runs"
	EntitlementStorageBytes   = "storage_bytes"
	EntitlementAPICalls       = "api_calls"
	EntitlementCommercial     = "commercial_license"
)

type Plan struct {
	ID              string            `json:"id"`
	Code            string            `json:"code"`
	Name            string            `json:"name"`
	Currency        string            `json:"currency"`
	AmountMinor     int64             `json:"amountMinor"`
	BillingInterval string            `json:"billingInterval"`
	Active          bool              `json:"active"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	Entitlements    []PlanEntitlement `json:"entitlements,omitempty"`
}

type PlanEntitlement struct {
	PlanID         string            `json:"planId"`
	Code           string            `json:"code"`
	Limit          *int64            `json:"limit,omitempty"`
	FeatureEnabled bool              `json:"featureEnabled"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type Subscription struct {
	ID                      string    `json:"id"`
	OrganizationID          string    `json:"organizationId"`
	PlanID                  string    `json:"planId"`
	Status                  string    `json:"status"`
	Provider                string    `json:"provider"`
	ProviderSubscriptionRef string    `json:"providerSubscriptionRef"`
	CurrentPeriodStart      time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd        time.Time `json:"currentPeriodEnd"`
	CancelAtPeriodEnd       bool      `json:"cancelAtPeriodEnd"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

type Entitlement struct {
	ID                   string            `json:"id"`
	OrganizationID       string            `json:"organizationId"`
	Code                 string            `json:"code"`
	SourceSubscriptionID string            `json:"sourceSubscriptionId,omitempty"`
	Limit                *int64            `json:"limit,omitempty"`
	FeatureEnabled       bool              `json:"featureEnabled"`
	Consumed             int64             `json:"consumed"`
	ValidFrom            time.Time         `json:"validFrom"`
	ValidUntil           *time.Time        `json:"validUntil,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

type UsageCounter struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	WorkspaceID    string    `json:"workspaceId,omitempty"`
	MetricCode     string    `json:"metricCode"`
	PeriodStart    time.Time `json:"periodStart"`
	PeriodEnd      time.Time `json:"periodEnd"`
	Consumed       int64     `json:"consumed"`
	Reserved       int64     `json:"reserved"`
	Version        int64     `json:"version"`
}

type UsageReservation struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	WorkspaceID    string    `json:"workspaceId,omitempty"`
	MetricCode     string    `json:"metricCode"`
	Amount         int64     `json:"amount"`
	Status         string    `json:"status"`
	IdempotencyKey string    `json:"idempotencyKey"`
	ExpiresAt      time.Time `json:"expiresAt"`
	CreatedAt      time.Time `json:"createdAt"`
	// Period fields are returned by adapters so a delayed commit remains tied
	// to the period in which it was reserved.
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
}

type ReleaseChannel struct {
	ID          string                 `json:"id"`
	Code        string                 `json:"code"`
	Version     string                 `json:"version"`
	Manifest    map[string]interface{} `json:"manifest"`
	PublishedAt time.Time              `json:"publishedAt"`
	RevokedAt   *time.Time             `json:"revokedAt,omitempty"`
}

type LicenseActivation struct {
	ID                string            `json:"id"`
	OrganizationID    string            `json:"organizationId"`
	WorkspaceID       string            `json:"workspaceId,omitempty"`
	DeviceID          string            `json:"deviceId,omitempty"`
	ReleaseChannelID  string            `json:"releaseChannelId,omitempty"`
	Status            string            `json:"status"`
	ActivatedAt       time.Time         `json:"activatedAt"`
	LastRefreshAt     *time.Time        `json:"lastRefreshAt,omitempty"`
	ExpiresAt         *time.Time        `json:"expiresAt,omitempty"`
	OfflineGraceUntil *time.Time        `json:"offlineGraceUntil,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

type ReserveUsageInput struct {
	OrganizationID string
	WorkspaceID    string
	MetricCode     string
	Amount         int64
	IdempotencyKey string
	ExpiresAt      time.Time
	Now            time.Time
}

type Repository interface {
	ListPlans(context.Context) ([]Plan, error)
	FindPlanByCode(context.Context, string) (Plan, error)
	FindCurrentSubscription(context.Context, string) (Subscription, error)
	ListEntitlements(context.Context, string) ([]Entitlement, error)
	FindEntitlement(context.Context, string, string) (Entitlement, error)
	ReserveUsage(context.Context, ReserveUsageInput, *int64) (UsageReservation, error)
	CommitUsage(context.Context, string, string, time.Time) (UsageReservation, error)
	ReleaseUsage(context.Context, string, string, time.Time) (UsageReservation, error)
	ActivateLicense(context.Context, LicenseActivation, []byte) (LicenseActivation, error)
	FindLicenseActivation(context.Context, string, []byte) (LicenseActivation, error)
	RevokeLicense(context.Context, string, string, time.Time) error
	ListReleaseChannels(context.Context) ([]ReleaseChannel, error)
	FindReleaseChannel(context.Context, string) (ReleaseChannel, error)
}

// Authorizer is intentionally organization-scoped. Implementations must
// resolve actor membership from the organization (and may additionally check
// a workspace when the request carries one); callers must never treat an org
// path parameter as proof of access.
type Authorizer interface {
	RequireOrganization(context.Context, string, string, memberservice.Permission) error
}

// SeedRepository is implemented by adapters for trusted bootstrap/admin code.
// It is deliberately separate from Repository so request handlers cannot
// create commercial state accidentally.
type SeedRepository interface {
	CreatePlan(context.Context, Plan) error
	UpsertPlanEntitlement(context.Context, PlanEntitlement) error
	CreateSubscription(context.Context, Subscription) error
	UpsertEntitlement(context.Context, Entitlement) error
	CreateReleaseChannel(context.Context, ReleaseChannel) error
}

type Service struct {
	repository Repository
	now        func() time.Time
	authorizer Authorizer
}

func New(repository Repository) *Service { return &Service{repository: repository, now: time.Now} }

// NewWithClock is useful for deterministic expiry and period-boundary tests.
func NewWithClock(repository Repository, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{repository: repository, now: clock}
}

// NewAuthorized creates the request-facing service. New remains available for
// trusted workers/bootstrap code that already established tenant scope.
func NewAuthorized(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, now: time.Now, authorizer: authorizer}
}

func (s *Service) authorize(ctx context.Context, organizationID, actorID string, permission memberservice.Permission) error {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(actorID) == "" || s.authorizer == nil {
		return fmt.Errorf("%w: organization, actor and authorizer are required", ErrInvalidInput)
	}
	return s.authorizer.RequireOrganization(ctx, strings.TrimSpace(organizationID), strings.TrimSpace(actorID), permission)
}

func (s *Service) CurrentSubscriptionFor(ctx context.Context, actorID, organizationID string) (Subscription, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingRead); err != nil {
		return Subscription{}, err
	}
	return s.CurrentSubscription(ctx, organizationID)
}
func (s *Service) EntitlementsFor(ctx context.Context, actorID, organizationID string) ([]Entitlement, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingRead); err != nil {
		return nil, err
	}
	return s.Entitlements(ctx, organizationID)
}
func (s *Service) ReserveUsageFor(ctx context.Context, actorID string, input ReserveUsageInput) (UsageReservation, error) {
	if err := s.authorize(ctx, input.OrganizationID, actorID, memberservice.PermissionBillingManage); err != nil {
		return UsageReservation{}, err
	}
	return s.ReserveUsage(ctx, input)
}
func (s *Service) CommitUsageFor(ctx context.Context, actorID, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingManage); err != nil {
		return UsageReservation{}, err
	}
	return s.CommitUsage(ctx, organizationID, reservationID, now)
}
func (s *Service) ReleaseUsageFor(ctx context.Context, actorID, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingManage); err != nil {
		return UsageReservation{}, err
	}
	return s.ReleaseUsage(ctx, organizationID, reservationID, now)
}
func (s *Service) ActivateLicenseFor(ctx context.Context, actorID, organizationID, workspaceID, deviceID, token string, expiresAt, graceUntil *time.Time, releaseChannelID string, metadata map[string]string) (LicenseActivation, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingManage); err != nil {
		return LicenseActivation{}, err
	}
	return s.ActivateLicense(ctx, organizationID, workspaceID, deviceID, token, expiresAt, graceUntil, releaseChannelID, metadata)
}
func (s *Service) ValidateLicenseFor(ctx context.Context, actorID, organizationID, workspaceID, deviceID, token string, now time.Time) (LicenseActivation, error) {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingRead); err != nil {
		return LicenseActivation{}, err
	}
	return s.ValidateLicense(ctx, organizationID, workspaceID, deviceID, token, now)
}
func (s *Service) RevokeLicenseFor(ctx context.Context, actorID, organizationID, activationID string, now time.Time) error {
	if err := s.authorize(ctx, organizationID, actorID, memberservice.PermissionBillingManage); err != nil {
		return err
	}
	return s.RevokeLicense(ctx, organizationID, activationID, now)
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) { return s.repository.ListPlans(ctx) }
func (s *Service) Plan(ctx context.Context, code string) (Plan, error) {
	return s.repository.FindPlanByCode(ctx, normalize(code))
}
func (s *Service) CurrentSubscription(ctx context.Context, organizationID string) (Subscription, error) {
	if strings.TrimSpace(organizationID) == "" {
		return Subscription{}, fmt.Errorf("%w: organizationId is required", ErrInvalidInput)
	}
	return s.repository.FindCurrentSubscription(ctx, strings.TrimSpace(organizationID))
}
func (s *Service) Entitlements(ctx context.Context, organizationID string) ([]Entitlement, error) {
	if strings.TrimSpace(organizationID) == "" {
		return nil, fmt.Errorf("%w: organizationId is required", ErrInvalidInput)
	}
	return s.repository.ListEntitlements(ctx, strings.TrimSpace(organizationID))
}
func (s *Service) Entitlement(ctx context.Context, organizationID, code string) (Entitlement, error) {
	if strings.TrimSpace(organizationID) == "" || normalize(code) == "" {
		return Entitlement{}, fmt.Errorf("%w: organizationId and code are required", ErrInvalidInput)
	}
	return s.repository.FindEntitlement(ctx, strings.TrimSpace(organizationID), normalize(code))
}

func (s *Service) ReserveUsage(ctx context.Context, input ReserveUsageInput) (UsageReservation, error) {
	input.OrganizationID, input.WorkspaceID, input.MetricCode, input.IdempotencyKey = strings.TrimSpace(input.OrganizationID), strings.TrimSpace(input.WorkspaceID), normalize(input.MetricCode), strings.TrimSpace(input.IdempotencyKey)
	if input.OrganizationID == "" || input.MetricCode == "" || input.IdempotencyKey == "" || input.Amount <= 0 {
		return UsageReservation{}, fmt.Errorf("%w: organizationId, metricCode, positive amount and idempotencyKey are required", ErrInvalidInput)
	}
	if input.Now.IsZero() {
		input.Now = s.now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	if input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.Now.Add(15 * time.Minute)
	}
	if !input.ExpiresAt.After(input.Now) {
		return UsageReservation{}, fmt.Errorf("%w: expiresAt must be in the future", ErrInvalidInput)
	}
	entitlement, err := s.repository.FindEntitlement(ctx, input.OrganizationID, input.MetricCode)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UsageReservation{}, err
	}
	if errors.Is(err, ErrNotFound) || !entitlement.FeatureEnabled {
		return UsageReservation{}, ErrQuotaExceeded
	}
	var limit *int64
	limit = entitlement.Limit
	reservation, err := s.repository.ReserveUsage(ctx, input, limit)
	if errors.Is(err, ErrQuotaExceeded) {
		return UsageReservation{}, ErrQuotaExceeded
	}
	return reservation, err
}

func (s *Service) CommitUsage(ctx context.Context, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	return s.transitionUsage(ctx, organizationID, reservationID, now, true)
}

func (s *Service) Reserve(ctx context.Context, input ReserveUsageInput) (UsageReservation, error) {
	return s.ReserveUsage(ctx, input)
}
func (s *Service) Commit(ctx context.Context, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	return s.CommitUsage(ctx, organizationID, reservationID, now)
}
func (s *Service) Release(ctx context.Context, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	return s.ReleaseUsage(ctx, organizationID, reservationID, now)
}
func (s *Service) ReleaseUsage(ctx context.Context, organizationID, reservationID string, now time.Time) (UsageReservation, error) {
	return s.transitionUsage(ctx, organizationID, reservationID, now, false)
}
func (s *Service) transitionUsage(ctx context.Context, organizationID, reservationID string, now time.Time, commit bool) (UsageReservation, error) {
	organizationID, reservationID = strings.TrimSpace(organizationID), strings.TrimSpace(reservationID)
	if organizationID == "" || reservationID == "" {
		return UsageReservation{}, fmt.Errorf("%w: organizationId and reservationId are required", ErrInvalidInput)
	}
	if now.IsZero() {
		now = s.now()
	}
	if commit {
		return s.repository.CommitUsage(ctx, organizationID, reservationID, now.UTC())
	}
	return s.repository.ReleaseUsage(ctx, organizationID, reservationID, now.UTC())
}

func HashLicenseToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func (s *Service) ActivateLicense(ctx context.Context, organizationID, workspaceID, deviceID, token string, expiresAt, graceUntil *time.Time, releaseChannelID string, metadata map[string]string) (LicenseActivation, error) {
	organizationID, workspaceID, deviceID, token = strings.TrimSpace(organizationID), strings.TrimSpace(workspaceID), strings.TrimSpace(deviceID), strings.TrimSpace(token)
	if organizationID == "" || token == "" || (workspaceID == "") != (deviceID == "") {
		return LicenseActivation{}, fmt.Errorf("%w: organizationId/token and paired workspaceId/deviceId are required", ErrInvalidInput)
	}
	now := s.now().UTC()
	if expiresAt != nil {
		expires := expiresAt.UTC()
		expiresAt = &expires
		if !expires.After(now) {
			return LicenseActivation{}, fmt.Errorf("%w: expiresAt must be in the future", ErrInvalidInput)
		}
	}
	if graceUntil != nil {
		grace := graceUntil.UTC()
		graceUntil = &grace
		if expiresAt == nil || !grace.After(*expiresAt) || grace.Sub(*expiresAt) > 30*24*time.Hour {
			return LicenseActivation{}, fmt.Errorf("%w: offline grace must follow expiry and be at most 30 days", ErrInvalidInput)
		}
	}
	if len(token) < 32 || len(token) > 4096 || len(metadata) > 64 {
		return LicenseActivation{}, fmt.Errorf("%w: license token or metadata is invalid", ErrInvalidInput)
	}
	activation := LicenseActivation{ID: newID(), OrganizationID: organizationID, WorkspaceID: workspaceID, DeviceID: deviceID, ReleaseChannelID: strings.TrimSpace(releaseChannelID), Status: "active", ActivatedAt: now, ExpiresAt: expiresAt, OfflineGraceUntil: graceUntil, Metadata: cloneMap(metadata)}
	return s.repository.ActivateLicense(ctx, activation, HashLicenseToken(token))
}

func (s *Service) ValidateLicense(ctx context.Context, organizationID, workspaceID, deviceID, token string, now time.Time) (LicenseActivation, error) {
	organizationID, workspaceID, deviceID = strings.TrimSpace(organizationID), strings.TrimSpace(workspaceID), strings.TrimSpace(deviceID)
	if organizationID == "" || strings.TrimSpace(token) == "" {
		return LicenseActivation{}, fmt.Errorf("%w: organizationId and token are required", ErrInvalidInput)
	}
	if now.IsZero() {
		now = s.now()
	}
	now = now.UTC()
	activation, err := s.repository.FindLicenseActivation(ctx, organizationID, HashLicenseToken(token))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return LicenseActivation{}, ErrLicenseInvalid
		}
		return LicenseActivation{}, err
	}
	if activation.WorkspaceID != workspaceID || activation.DeviceID != deviceID {
		return LicenseActivation{}, ErrLicenseScope
	}
	if activation.Status == "revoked" {
		return LicenseActivation{}, ErrLicenseRevoked
	}
	if activation.ExpiresAt != nil && !activation.ExpiresAt.After(now) {
		if activation.OfflineGraceUntil == nil || !activation.OfflineGraceUntil.After(now) {
			return LicenseActivation{}, ErrLicenseExpired
		}
		activation.Status = "grace"
	}
	if activation.Status == "expired" {
		return LicenseActivation{}, ErrLicenseExpired
	}
	return activation, nil
}

func (s *Service) RevokeLicense(ctx context.Context, organizationID, activationID string, now time.Time) error {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(activationID) == "" {
		return fmt.Errorf("%w: organizationId and activationId are required", ErrInvalidInput)
	}
	if now.IsZero() {
		now = s.now()
	}
	return s.repository.RevokeLicense(ctx, strings.TrimSpace(organizationID), strings.TrimSpace(activationID), now.UTC())
}

func (s *Service) Activate(ctx context.Context, organizationID, workspaceID, deviceID, token string, expiresAt, graceUntil *time.Time, releaseChannelID string, metadata map[string]string) (LicenseActivation, error) {
	return s.ActivateLicense(ctx, organizationID, workspaceID, deviceID, token, expiresAt, graceUntil, releaseChannelID, metadata)
}
func (s *Service) Validate(ctx context.Context, organizationID, workspaceID, deviceID, token string, now time.Time) (LicenseActivation, error) {
	return s.ValidateLicense(ctx, organizationID, workspaceID, deviceID, token, now)
}
func (s *Service) Revoke(ctx context.Context, organizationID, activationID string, now time.Time) error {
	return s.RevokeLicense(ctx, organizationID, activationID, now)
}

func (s *Service) ListReleaseChannels(ctx context.Context) ([]ReleaseChannel, error) {
	return s.repository.ListReleaseChannels(ctx)
}
func (s *Service) ReleaseChannel(ctx context.Context, code string) (ReleaseChannel, error) {
	return s.repository.FindReleaseChannel(ctx, normalize(code))
}

func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func newID() string                 { return uuid.NewString() }
func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

// DefaultPlanCatalog is the product baseline. A nil limit means unlimited;
// the catalog is data, not a payment processor, and may be replaced by a
// trusted seed operation for a deployment.
func DefaultPlanCatalog(now time.Time) []Plan {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	limit := func(v int64) *int64 { return &v }
	plans := []struct {
		code, name string
		amount     int64
		interval   string
		values     map[string]*int64
	}{
		{PlanFree, "Free", 0, "none", map[string]*int64{EntitlementInstances: limit(3), EntitlementTeamMembers: limit(2), EntitlementAutomationRuns: limit(1000), EntitlementStorageBytes: limit(1 << 30), EntitlementAPICalls: limit(10000), EntitlementCommercial: nil}},
		{PlanProfessional, "Professional", 4900, "month", map[string]*int64{EntitlementInstances: limit(25), EntitlementTeamMembers: limit(10), EntitlementAutomationRuns: limit(25000), EntitlementStorageBytes: limit(50 << 30), EntitlementAPICalls: limit(500000), EntitlementCommercial: limit(1)}},
		{PlanEnterprise, "Enterprise", 0, "none", map[string]*int64{EntitlementInstances: nil, EntitlementTeamMembers: nil, EntitlementAutomationRuns: nil, EntitlementStorageBytes: nil, EntitlementAPICalls: nil, EntitlementCommercial: limit(1)}},
	}
	out := make([]Plan, 0, len(plans))
	for _, p := range plans {
		plan := Plan{ID: defaultPlanID(p.code), Code: p.code, Name: p.name, Currency: "USD", AmountMinor: p.amount, BillingInterval: p.interval, Active: true, CreatedAt: now, UpdatedAt: now}
		codes := make([]string, 0, len(p.values))
		for code := range p.values {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			value := p.values[code]
			enabled := !(p.code == PlanFree && code == EntitlementCommercial)
			plan.Entitlements = append(plan.Entitlements, PlanEntitlement{PlanID: plan.ID, Code: code, Limit: value, FeatureEnabled: enabled})
		}
		out = append(out, plan)
	}
	return out
}

func defaultPlanID(code string) string {
	switch code {
	case PlanFree:
		return "00000000-0000-4000-8000-000000000001"
	case PlanProfessional:
		return "00000000-0000-4000-8000-000000000002"
	case PlanEnterprise:
		return "00000000-0000-4000-8000-000000000003"
	default:
		return uuid.NewSHA1(uuid.Nil, []byte("ant-browser/plan/"+code)).String()
	}
}
