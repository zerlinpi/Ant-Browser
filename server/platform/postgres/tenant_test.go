package postgres

import (
	"context"
	"testing"
)

func TestValidateTenantScope(t *testing.T) {
	if err := validateTenantScope(TenantScope{WorkspaceID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid workspace scope was accepted")
	}
	if err := validateTenantScope(TenantScope{SystemOperation: "task_claim"}); err != nil {
		t.Fatalf("worker scope rejected: %v", err)
	}
	if err := validateTenantScope(TenantScope{}); err == nil {
		t.Fatal("empty tenant scope was accepted")
	}
	if err := validateTenantScope(TenantScope{UserID: "00000000-0000-0000-0000-000000000002"}); err != nil {
		t.Fatalf("user-only scope rejected: %v", err)
	}
	if err := validateTenantScope(TenantScope{SystemOperation: "arbitrary_bypass"}); err == nil {
		t.Fatal("arbitrary system operation was accepted")
	}
	if err := validateTenantScope(TenantScope{UserID: "00000000-0000-0000-0000-000000000002", SystemOperation: "admin_read"}); err != nil {
		t.Fatalf("authenticated admin scope rejected: %v", err)
	}
	if err := validateTenantScope(TenantScope{SystemOperation: "admin_read"}); err == nil {
		t.Fatal("anonymous admin scope was accepted")
	}
	if err := validateTenantScope(TenantScope{UserID: "00000000-0000-0000-0000-000000000002", WorkspaceID: "00000000-0000-0000-0000-000000000001", SystemOperation: "admin_read"}); err == nil {
		t.Fatal("tenant-bound admin scope was accepted")
	}
	if err := validateTenantScope(TenantScope{WorkspaceID: "00000000-0000-0000-0000-000000000001", SystemOperation: "task_claim"}); err == nil {
		t.Fatal("mixed tenant and system scope was accepted")
	}
}

func TestWithTenantScopeMergesIdentityAndRejectsConflicts(t *testing.T) {
	ctx := WithTenantScope(context.Background(), TenantScope{
		WorkspaceID: "00000000-0000-0000-0000-000000000001",
		UserID:      "00000000-0000-0000-0000-000000000002",
	})
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: "00000000-0000-0000-0000-000000000001"})
	scope, ok := tenantScopeFromContext(ctx)
	if !ok || scope.UserID == "" || validateTenantScope(scope) != nil {
		t.Fatalf("merged scope = %+v", scope)
	}

	conflicting := WithTenantScope(ctx, TenantScope{WorkspaceID: "00000000-0000-0000-0000-000000000003"})
	scope, _ = tenantScopeFromContext(conflicting)
	if err := validateTenantScope(scope); err == nil {
		t.Fatal("conflicting workspace scope was accepted")
	}
}

func TestWithTenantScopeIsRequestLocal(t *testing.T) {
	base := context.Background()
	scoped := WithTenantScope(base, TenantScope{WorkspaceID: "00000000-0000-0000-0000-000000000001"})
	if _, ok := tenantScopeFromContext(base); ok {
		t.Fatal("tenant scope leaked to parent context")
	}
	scope, ok := tenantScopeFromContext(scoped)
	if !ok || scope.WorkspaceID == "" {
		t.Fatal("tenant scope was not attached to child context")
	}
}
