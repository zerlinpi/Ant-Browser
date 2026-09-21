package accountservice

import (
	"context"
	"errors"
	"testing"
	"time"

	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

type testAuthorizer struct{ permissions []memberservice.Permission }

func (a *testAuthorizer) Require(_ context.Context, _, _ string, permission memberservice.Permission) error {
	a.permissions = append(a.permissions, permission)
	return nil
}

type testRepository struct {
	accounts map[string]Account
	secrets  map[string]AccountSecret
	bindings map[string]AccountBinding
	events   map[string]RiskEvent
}

func newTestRepository() *testRepository {
	return &testRepository{accounts: map[string]Account{}, secrets: map[string]AccountSecret{}, bindings: map[string]AccountBinding{}, events: map[string]RiskEvent{}}
}
func (r *testRepository) CreateAccount(_ context.Context, account Account) error {
	r.accounts[account.ID] = account
	return nil
}
func (r *testRepository) FindAccount(_ context.Context, workspaceID, accountID string) (Account, error) {
	account, ok := r.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return Account{}, ErrNotFound
	}
	return account, nil
}
func (r *testRepository) ListAccounts(_ context.Context, workspaceID string) ([]Account, error) {
	items := []Account{}
	for _, account := range r.accounts {
		if account.WorkspaceID == workspaceID && account.DeletedAt == nil {
			items = append(items, account)
		}
	}
	return items, nil
}
func (r *testRepository) UpdateAccount(_ context.Context, account Account, expected int64) (Account, error) {
	current, ok := r.accounts[account.ID]
	if !ok || current.WorkspaceID != account.WorkspaceID {
		return Account{}, ErrNotFound
	}
	if current.Version != expected {
		return Account{}, ErrVersionConflict
	}
	account.Version++
	r.accounts[account.ID] = account
	return account, nil
}
func (r *testRepository) DeleteAccount(_ context.Context, workspaceID, accountID string, expected int64, now time.Time) error {
	current, err := r.FindAccount(context.Background(), workspaceID, accountID)
	if err != nil {
		return err
	}
	if current.Version != expected {
		return ErrVersionConflict
	}
	current.DeletedAt = &now
	current.Version++
	r.accounts[accountID] = current
	return nil
}
func (r *testRepository) PutAccountSecret(_ context.Context, secret AccountSecret) error {
	r.secrets[secret.ID] = secret
	return nil
}
func (r *testRepository) ListAccountSecrets(_ context.Context, workspaceID, accountID string) ([]AccountSecret, error) {
	items := []AccountSecret{}
	for _, secret := range r.secrets {
		if secret.WorkspaceID == workspaceID && secret.AccountID == accountID {
			items = append(items, secret)
		}
	}
	return items, nil
}
func (r *testRepository) DeleteAccountSecret(_ context.Context, _, _, id string) error {
	delete(r.secrets, id)
	return nil
}
func (r *testRepository) UpsertAccountBinding(_ context.Context, binding AccountBinding) (AccountBinding, error) {
	r.bindings[binding.ID] = binding
	return binding, nil
}
func (r *testRepository) ListAccountBindings(_ context.Context, _, _ string) ([]AccountBinding, error) {
	items := []AccountBinding{}
	for _, binding := range r.bindings {
		items = append(items, binding)
	}
	return items, nil
}
func (r *testRepository) DeleteAccountBinding(_ context.Context, _, _, id string) error {
	delete(r.bindings, id)
	return nil
}
func (r *testRepository) CreateRiskEvent(_ context.Context, event RiskEvent) error {
	r.events[event.ID] = event
	return nil
}
func (r *testRepository) ListRiskEvents(_ context.Context, _, _ string) ([]RiskEvent, error) {
	items := []RiskEvent{}
	for _, event := range r.events {
		items = append(items, event)
	}
	return items, nil
}

func TestCreateValidatesCommercialPlatformsAndDefaultsLifecycle(t *testing.T) {
	for _, platform := range []string{"Amazon", "Shopify", "TikTok", "Facebook", "Google", "eBay"} {
		repository, authorizer := newTestRepository(), &testAuthorizer{}
		account, err := New(repository, authorizer).Create(context.Background(), "actor", "tenant-a", CreateInput{Platform: platform, Name: "Store", Identifier: "store-1"})
		if err != nil {
			t.Fatalf("platform %s: %v", platform, err)
		}
		if account.Platform != Platform(platformToLower(platform)) || account.Status != StatusPending || account.RiskLevel != RiskUnknown || account.Version != 1 {
			t.Fatalf("unexpected account for %s: %+v", platform, account)
		}
	}
	if _, err := New(newTestRepository(), &testAuthorizer{}).Create(context.Background(), "actor", "tenant-a", CreateInput{Platform: "etsy", Name: "Store", Identifier: "x"}); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestUpdateUsesTenantAndOptimisticVersion(t *testing.T) {
	repository, authorizer := newTestRepository(), &testAuthorizer{}
	service := New(repository, authorizer)
	account, err := service.Create(context.Background(), "actor", "tenant-a", CreateInput{Platform: "google", Name: "Ads", Identifier: "ads@example.com", Email: "ads@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), "actor", "tenant-a", account.ID, 99, UpdateInput{Name: "new", Identifier: "ads@example.com"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v, want version conflict", err)
	}
	if _, err := service.Get(context.Background(), "actor", "tenant-b", account.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant lookup got %v", err)
	}
	updated, err := service.Update(context.Background(), "actor", "tenant-a", account.ID, 1, UpdateInput{Name: "New Ads", Identifier: "ads@example.com", Status: StatusActive, RiskLevel: RiskLow})
	if err != nil || updated.Version != 2 || updated.Status != StatusActive {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
}

func TestSecretsRequireEnvelopeAndNeverExposePlaintext(t *testing.T) {
	repository, authorizer := newTestRepository(), &testAuthorizer{}
	service := New(repository, authorizer)
	account, err := service.Create(context.Background(), "actor", "tenant-a", CreateInput{Platform: "amazon", Name: "Seller", Identifier: "seller"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutSecret(context.Background(), "actor", "tenant-a", account.ID, "password", SecretEnvelope{}); !errors.Is(err, ErrSecretRequired) {
		t.Fatalf("got %v, want envelope validation", err)
	}
	secret, err := service.PutSecret(context.Background(), "actor", "tenant-a", account.ID, "password", SecretEnvelope{Algorithm: "AES-GCM", Ciphertext: "ciphertext-only", KeyReference: "kms/account", KeyVersion: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Envelope.Ciphertext != "ciphertext-only" || len(repository.secrets) != 1 {
		t.Fatalf("secret was not stored as envelope metadata: %+v", secret)
	}
}

func platformToLower(value string) string {
	if value == "eBay" {
		return "ebay"
	}
	return map[string]string{"Amazon": "amazon", "Shopify": "shopify", "TikTok": "tiktok", "Facebook": "facebook", "Google": "google"}[value]
}
