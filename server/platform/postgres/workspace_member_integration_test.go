package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// TestMemberLifecycleAndCredentialRotationAgainstPostgres mirrors the memory
// store tests against PostgreSQL. Like TestMigrationsAgainstPostgres it is
// opt-in through ANT_TEST_DATABASE_URL (a disposable database).
func TestMemberLifecycleAndCredentialRotationAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsPath); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	newUser := func(label string) authservice.User {
		user := authservice.User{
			ID: uuid.NewString(), Email: uuid.NewString() + "@" + label + ".test", PasswordHash: "test",
			DisplayName: "User " + label, Status: "active", CreatedAt: now, UpdatedAt: now,
		}
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
		return user
	}
	owner, member := newUser("owner"), newUser("member")
	suffix := uuid.NewString()[:8]
	organization := workspaceservice.Organization{ID: uuid.NewString(), Name: "Members", Slug: "org-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	workspace := workspaceservice.Workspace{ID: uuid.NewString(), OrganizationID: organization.ID, Name: "Members", Slug: "ws-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateOrganizationWorkspace(ctx, organization, workspace, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: owner.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Request-equivalent tenant scopes, as set by the gateway.
	ownerScope := postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: workspace.ID, UserID: owner.ID})
	memberScope := postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: workspace.ID, UserID: member.ID})
	addMember := func(role memberservice.Role) error {
		joined := time.Now().UTC().Truncate(time.Microsecond)
		return store.AddMember(ownerScope, workspaceservice.Membership{
			ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: member.ID, Role: role, Status: "active", JoinedAt: joined, UpdatedAt: joined,
		})
	}
	if err := addMember(memberservice.RoleOperator); err != nil {
		t.Fatal(err)
	}

	workspaces, err := store.ListWorkspaces(memberScope, member.ID)
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != workspace.ID || workspaces[0].Role != memberservice.RoleOperator {
		t.Fatalf("member workspaces = %+v, %v", workspaces, err)
	}
	members, err := store.ListMembers(ownerScope, workspace.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	for _, listed := range members {
		want := owner
		if listed.UserID == member.ID {
			want = member
		}
		if listed.Email != want.Email || listed.DisplayName != want.DisplayName {
			t.Fatalf("member profile = %+v, want %+v", listed, want)
		}
	}

	createDevice := func(scope context.Context, userID string) (deviceservice.Device, string) {
		raw, hash, err := security.NewOpaqueToken()
		if err != nil {
			t.Fatal(err)
		}
		device := deviceservice.Device{ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: userID, Name: "Agent", Platform: "linux", Status: "offline", CreatedAt: now, UpdatedAt: now}
		if err := store.CreateDevice(scope, device, deviceservice.Credential{ID: uuid.NewString(), DeviceID: device.ID, SecretHash: hash, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		return device, raw
	}
	memberDevice, memberSecret := createDevice(memberScope, member.ID)
	ownerDevice, ownerSecret := createDevice(ownerScope, owner.ID)
	if _, err := store.AuthenticateDevice(ctx, memberDevice.ID, security.HashOpaqueToken(memberSecret)); err != nil {
		t.Fatalf("member device credential rejected: %v", err)
	}

	updated, err := store.UpdateMemberRole(ownerScope, workspace.ID, member.ID, memberservice.RoleViewer)
	if err != nil || updated.Role != memberservice.RoleViewer || updated.Email != member.Email {
		t.Fatalf("role change = %+v, %v", updated, err)
	}
	if _, err := store.UpdateMemberRole(ownerScope, workspace.ID, owner.ID, memberservice.RoleAdmin); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner demoted: %v", err)
	}
	if _, err := store.RemoveMember(ownerScope, workspace.ID, owner.ID, now); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner removed: %v", err)
	}

	removal, err := store.RemoveMember(ownerScope, workspace.ID, member.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if removal.Membership.Status != "removed" || len(removal.RevokedDeviceIDs) != 1 || removal.RevokedDeviceIDs[0] != memberDevice.ID {
		t.Fatalf("removal = %+v", removal)
	}
	if _, err := store.AuthenticateDevice(ctx, memberDevice.ID, security.HashOpaqueToken(memberSecret)); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("removed member's device still authenticates: %v", err)
	}
	if _, err := store.AuthenticateDevice(ctx, ownerDevice.ID, security.HashOpaqueToken(ownerSecret)); err != nil {
		t.Fatalf("owner device affected by member removal: %v", err)
	}
	if membership, err := store.FindMembership(ownerScope, workspace.ID, member.ID); err != nil || membership.Status != "removed" {
		t.Fatalf("removed membership = %+v, %v", membership, err)
	}
	if _, err := store.RemoveMember(ownerScope, workspace.ID, member.ID, now); !errors.Is(err, workspaceservice.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	if err := addMember(memberservice.RoleViewer); err != nil {
		t.Fatalf("re-adding removed member: %v", err)
	}
	if membership, err := store.FindMembership(ownerScope, workspace.ID, member.ID); err != nil || membership.Status != "active" || membership.Role != memberservice.RoleViewer {
		t.Fatalf("re-added membership = %+v, %v", membership, err)
	}
	if err := addMember(memberservice.RoleViewer); !errors.Is(err, workspaceservice.ErrMemberExists) {
		t.Fatalf("duplicate active membership: %v", err)
	}

	// Rotation: only the registering user, never for a revoked device, and
	// the previous credential stops authenticating at commit.
	ownerDevices := postgres.WithTenantScope(ctx, postgres.TenantScope{UserID: owner.ID})
	memberDevices := postgres.WithTenantScope(ctx, postgres.TenantScope{UserID: member.ID})
	raw, hash, err := security.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	replacement := deviceservice.Credential{ID: uuid.NewString(), DeviceID: ownerDevice.ID, SecretHash: hash, CreatedAt: now}
	if _, err := store.RotateDeviceCredential(memberDevices, member.ID, ownerDevice.ID, replacement, now); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("another user rotated the device: %v", err)
	}
	if _, err := store.RotateDeviceCredential(ownerDevices, owner.ID, ownerDevice.ID, replacement, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateDevice(ctx, ownerDevice.ID, security.HashOpaqueToken(ownerSecret)); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("previous credential still authenticates: %v", err)
	}
	if _, err := store.AuthenticateDevice(ctx, ownerDevice.ID, security.HashOpaqueToken(raw)); err != nil {
		t.Fatalf("rotated credential rejected: %v", err)
	}
	memberReplacement := deviceservice.Credential{ID: uuid.NewString(), DeviceID: memberDevice.ID, SecretHash: hash + "00", CreatedAt: now}
	if _, err := store.RotateDeviceCredential(memberDevices, member.ID, memberDevice.ID, memberReplacement, now); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("revoked device rotated: %v", err)
	}
}
