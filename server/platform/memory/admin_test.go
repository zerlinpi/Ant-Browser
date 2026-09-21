package memory

import (
	"context"
	"testing"
	"time"

	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestAdminServiceUsesSeparatePlatformRoleAndAudits(t *testing.T) {
	store := New()
	now := time.Now().UTC()
	ctx := context.Background()
	actor := authservice.User{ID: "admin-user", Email: "admin@example.com", Status: "active", CreatedAt: now, UpdatedAt: now}
	target := authservice.User{ID: "target-user", Email: "target@example.com", Status: "active", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	if err := store.CreateUser(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	store.SeedPlatformAdmin(adminservice.PlatformAdmin{UserID: actor.ID, Role: adminservice.PlatformAdminRole, Status: "active"})
	svc := adminservice.New(store)
	granted, err := svc.GrantPlatformAdmin(ctx, actor.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if granted.Role != adminservice.PlatformAdminRole {
		t.Fatalf("unexpected role: %q", granted.Role)
	}
	session := authservice.Session{ID: "target-session", UserID: target.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	refresh := authservice.RefreshToken{ID: "target-refresh", SessionID: session.ID, TokenHash: "target-hash", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.SaveSession(ctx, session, refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetUserStatus(ctx, actor.ID, target.ID, adminservice.UserStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if active, err := store.SessionActive(ctx, target.ID, session.ID); err != nil || active {
		t.Fatalf("suspended user session remained active: %v %v", active, err)
	}
	page, err := svc.ListUsers(ctx, actor.ID, adminservice.ListOptions{Status: adminservice.UserStatusSuspended})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != target.ID {
		t.Fatalf("unexpected page: %+v", page)
	}
	events := store.AdminAuditEvents()
	if len(events) < 3 {
		t.Fatalf("expected audit events, got %d", len(events))
	}
	if _, err := svc.SetUserStatus(ctx, target.ID, actor.ID, adminservice.UserStatusSuspended); err == nil {
		t.Fatal("non-platform admin should be denied")
	}
}

func TestAdminMemoryOrganizationAndWorkspacePaging(t *testing.T) {
	store := New()
	now := time.Now().UTC()
	ctx := context.Background()
	actor := authservice.User{ID: "actor", Email: "actor@example.com", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, actor); err != nil {
		t.Fatal(err)
	}
	store.SeedPlatformAdmin(adminservice.PlatformAdmin{UserID: actor.ID, Role: adminservice.PlatformAdminRole, Status: "active"})
	org := workspaceservice.Organization{ID: "org", Name: "Acme", Slug: "acme", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	ws := workspaceservice.Workspace{ID: "ws", OrganizationID: org.ID, Name: "Main", Slug: "main", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	mem := workspaceservice.Membership{ID: "member", WorkspaceID: ws.ID, UserID: actor.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now}
	if err := store.CreateOrganizationWorkspace(ctx, org, ws, mem); err != nil {
		t.Fatal(err)
	}
	svc := adminservice.New(store)
	orgs, err := svc.ListOrganizations(ctx, actor.ID, adminservice.ListOptions{})
	if err != nil || orgs.Total != 1 {
		t.Fatalf("organizations: %+v %v", orgs, err)
	}
	workspaces, err := svc.ListWorkspaces(ctx, actor.ID, adminservice.ListOptions{})
	if err != nil || workspaces.Total != 1 {
		t.Fatalf("workspaces: %+v %v", workspaces, err)
	}
	if _, err := svc.DisableOrganization(ctx, actor.ID, org.ID, "security review"); err != nil {
		t.Fatal(err)
	}
	workspaceSvc := workspaceservice.New(store)
	if _, err := workspaceSvc.Get(ctx, actor.ID, ws.ID); err == nil {
		t.Fatal("suspended organization must block workspace access")
	}
}
