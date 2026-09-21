package billingservice_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
)

func TestDefaultCatalogHasCommercialTiers(t *testing.T) {
	plans := billingservice.DefaultPlanCatalog(time.Now())
	if len(plans) != 3 {
		t.Fatalf("got %d plans", len(plans))
	}
	if plans[0].Code != billingservice.PlanFree || plans[1].Code != billingservice.PlanProfessional || plans[2].Code != billingservice.PlanEnterprise {
		t.Fatalf("unexpected catalog order: %#v", plans)
	}
	for _, p := range plans {
		if len(p.Entitlements) < 6 {
			t.Fatalf("%s missing quota entitlements", p.Code)
		}
	}
}

func TestUsageReserveIsIdempotentAndQuotaSafeConcurrently(t *testing.T) {
	store := memory.New()
	limit := int64(10)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertEntitlement(context.Background(), billingservice.Entitlement{OrganizationID: "org", Code: billingservice.EntitlementAutomationRuns, Limit: &limit, FeatureEnabled: true, ValidFrom: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	svc := billingservice.NewWithClock(store, func() time.Time { return now })
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan billingservice.UsageReservation, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		key := "request-" + string(rune('a'+i))
		go func() {
			defer wg.Done()
			r, err := svc.ReserveUsage(ctx, billingservice.ReserveUsageInput{OrganizationID: "org", MetricCode: billingservice.EntitlementAutomationRuns, Amount: 6, IdempotencyKey: key, Now: now})
			if err != nil {
				errs <- err
			} else {
				results <- r
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	var reservations []billingservice.UsageReservation
	for r := range results {
		reservations = append(reservations, r)
	}
	if len(reservations) != 1 || len(errs) != 1 || !errors.Is(<-errs, billingservice.ErrQuotaExceeded) {
		t.Fatalf("idempotency/quota contract failed: reservations=%d errors=%d", len(reservations), len(errs))
	}
	r := reservations[0]
	replay, err := svc.ReserveUsage(ctx, billingservice.ReserveUsageInput{OrganizationID: "org", MetricCode: billingservice.EntitlementAutomationRuns, Amount: 6, IdempotencyKey: r.IdempotencyKey, Now: now})
	if err != nil || replay.ID != r.ID {
		t.Fatalf("idempotent replay: %#v %v", replay, err)
	}
	if _, err := svc.CommitUsage(ctx, "org", r.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if committed, err := svc.CommitUsage(ctx, "org", r.ID, now.Add(2*time.Minute)); err != nil || committed.Status != "committed" {
		t.Fatalf("commit replay: %#v %v", committed, err)
	}
}

func TestUsageQuotaIsOrganizationScopedAcrossWorkspacesAndFailsClosed(t *testing.T) {
	store := memory.New()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	svc := billingservice.NewWithClock(store, func() time.Time { return now })
	input := billingservice.ReserveUsageInput{OrganizationID: "org", WorkspaceID: "workspace-a", MetricCode: billingservice.EntitlementAPICalls, Amount: 1, IdempotencyKey: "a", Now: now}
	if _, err := svc.ReserveUsage(context.Background(), input); !errors.Is(err, billingservice.ErrQuotaExceeded) {
		t.Fatalf("missing entitlement must fail closed: %v", err)
	}
	limit := int64(2)
	if err := store.UpsertEntitlement(context.Background(), billingservice.Entitlement{OrganizationID: "org", Code: billingservice.EntitlementAPICalls, Limit: &limit, FeatureEnabled: true, ValidFrom: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReserveUsage(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.WorkspaceID, input.IdempotencyKey = "workspace-b", "b"
	if _, err := svc.ReserveUsage(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.WorkspaceID, input.IdempotencyKey = "workspace-c", "c"
	if _, err := svc.ReserveUsage(context.Background(), input); !errors.Is(err, billingservice.ErrQuotaExceeded) {
		t.Fatalf("workspace bypassed org quota: %v", err)
	}
}

func TestLicenseScopeGraceAndRevocation(t *testing.T) {
	store := memory.New()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	svc := billingservice.NewWithClock(store, func() time.Time { return now })
	grace := now.Add(24 * time.Hour)
	expiry := now.Add(time.Hour)
	token := "0123456789abcdef0123456789abcdef"
	a, err := svc.ActivateLicense(context.Background(), "org", "workspace", "device", token, &expiry, &grace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateLicense(context.Background(), "org", "other", "device", token, now); !errors.Is(err, billingservice.ErrLicenseScope) {
		t.Fatalf("scope error: %v", err)
	}
	if _, err := svc.ValidateLicense(context.Background(), "org", "workspace", "device", token, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("grace should validate: %v", err)
	}
	if err := svc.RevokeLicense(context.Background(), "org", a.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateLicense(context.Background(), "org", "workspace", "device", token, now); !errors.Is(err, billingservice.ErrLicenseRevoked) {
		t.Fatalf("revoke not enforced: %v", err)
	}
}
