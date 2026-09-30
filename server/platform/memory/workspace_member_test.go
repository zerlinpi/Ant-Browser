package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// memberStoreFixture builds two workspaces, both owned by "owner", with
// "member" active in both and holding devices in each.
func memberStoreFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	store := New()
	now := time.Now().UTC().Add(-time.Minute)
	for _, id := range []string{"owner", "member"} {
		if err := store.CreateUser(ctx, authservice.User{ID: id, Email: id + "@example.com", DisplayName: "Display " + id, Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, workspaceID := range []string{"ws-a", "ws-b"} {
		organization := workspaceservice.Organization{ID: "org-" + workspaceID, Name: workspaceID, Slug: workspaceID, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
		workspace := workspaceservice.Workspace{ID: workspaceID, OrganizationID: organization.ID, Name: workspaceID, Slug: workspaceID, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
		owner := workspaceservice.Membership{ID: "owner-" + workspaceID, WorkspaceID: workspaceID, UserID: "owner", Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now}
		if err := store.CreateOrganizationWorkspace(ctx, organization, workspace, owner); err != nil {
			t.Fatal(err)
		}
		member := workspaceservice.Membership{ID: "member-" + workspaceID, WorkspaceID: workspaceID, UserID: "member", Role: memberservice.RoleOperator, Status: "active", JoinedAt: now.Add(time.Second), UpdatedAt: now}
		if err := store.AddMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	for _, device := range []struct{ id, workspaceID, userID string }{
		{"device-a2", "ws-a", "member"}, {"device-a1", "ws-a", "member"},
		{"device-b1", "ws-b", "member"}, {"device-owner", "ws-a", "owner"},
	} {
		if err := store.CreateDevice(ctx,
			deviceservice.Device{ID: device.id, WorkspaceID: device.workspaceID, UserID: device.userID, Name: device.id, Platform: "linux", Status: "offline", CreatedAt: now, UpdatedAt: now},
			deviceservice.Credential{ID: "credential-" + device.id, DeviceID: device.id, SecretHash: "secret-" + device.id, CreatedAt: now},
		); err != nil {
			t.Fatal(err)
		}
	}
	return store, now
}

func TestRemoveMemberRevokesOnlyThatMembersWorkspaceDevices(t *testing.T) {
	ctx := context.Background()
	store, _ := memberStoreFixture(t)
	removedAt := time.Now().UTC()

	removal, err := store.RemoveMember(ctx, "ws-a", "member", removedAt)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Membership.Status != "removed" || removal.Membership.UserID != "member" {
		t.Fatalf("unexpected removed membership: %+v", removal.Membership)
	}
	if len(removal.RevokedDeviceIDs) != 2 || removal.RevokedDeviceIDs[0] != "device-a1" || removal.RevokedDeviceIDs[1] != "device-a2" {
		t.Fatalf("revoked devices = %v, want sorted [device-a1 device-a2]", removal.RevokedDeviceIDs)
	}
	for _, id := range []string{"device-a1", "device-a2"} {
		device := store.devices[id]
		if device.Status != "revoked" || device.RevokedAt == nil || !device.RevokedAt.Equal(removedAt) {
			t.Fatalf("%s was not revoked: %+v", id, device)
		}
		if credential := store.deviceCredentials[id]; credential.RevokedAt == nil {
			t.Fatalf("%s credential was not revoked", id)
		}
		if _, err := store.AuthenticateDevice(ctx, id, "secret-"+id); !errors.Is(err, deviceservice.ErrRevoked) {
			t.Fatalf("%s still authenticates: %v", id, err)
		}
	}
	for _, id := range []string{"device-b1", "device-owner"} {
		if _, err := store.AuthenticateDevice(ctx, id, "secret-"+id); err != nil {
			t.Fatalf("%s outside the removal was affected: %v", id, err)
		}
	}

	// The membership row is retained as removed, invisible to listings, and
	// authorizes nothing; the member's other workspace is untouched.
	if membership, err := store.FindMembership(ctx, "ws-a", "member"); err != nil || membership.Status != "removed" {
		t.Fatalf("removed membership = %+v, %v", membership, err)
	}
	members, err := store.ListMembers(ctx, "ws-a")
	if err != nil || len(members) != 1 || members[0].UserID != "owner" {
		t.Fatalf("members after removal = %+v, %v", members, err)
	}
	workspaces, err := store.ListWorkspaces(ctx, "member")
	if err != nil || len(workspaces) != 1 || workspaces[0].ID != "ws-b" || workspaces[0].Role != memberservice.RoleOperator {
		t.Fatalf("member workspaces after removal = %+v, %v", workspaces, err)
	}
	if _, err := store.RemoveMember(ctx, "ws-a", "member", removedAt); !errors.Is(err, workspaceservice.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	if _, err := store.UpdateMemberRole(ctx, "ws-a", "member", memberservice.RoleAdmin); !errors.Is(err, workspaceservice.ErrNotFound) {
		t.Fatalf("role change on removed member: %v", err)
	}

	// Re-adding re-activates the row; revoked devices stay revoked.
	if err := store.AddMember(ctx, workspaceservice.Membership{ID: "member-a-again", WorkspaceID: "ws-a", UserID: "member", Role: memberservice.RoleViewer, Status: "active", JoinedAt: removedAt, UpdatedAt: removedAt}); err != nil {
		t.Fatalf("re-adding removed member: %v", err)
	}
	if membership, _ := store.FindMembership(ctx, "ws-a", "member"); membership.Status != "active" || membership.Role != memberservice.RoleViewer || membership.ID != "member-a-again" {
		t.Fatalf("re-added membership = %+v", membership)
	}
	if _, err := store.AuthenticateDevice(ctx, "device-a1", "secret-device-a1"); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("re-adding restored a revoked device: %v", err)
	}
	if err := store.AddMember(ctx, workspaceservice.Membership{ID: "dup", WorkspaceID: "ws-a", UserID: "member", Role: memberservice.RoleViewer, Status: "active", JoinedAt: removedAt, UpdatedAt: removedAt}); !errors.Is(err, workspaceservice.ErrMemberExists) {
		t.Fatalf("duplicate active membership: %v", err)
	}
}

func TestRemoveMemberKeepsTheLastOwner(t *testing.T) {
	ctx := context.Background()
	store, _ := memberStoreFixture(t)
	if _, err := store.RemoveMember(ctx, "ws-a", "owner", time.Now().UTC()); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner removed: %v", err)
	}
	if _, err := store.UpdateMemberRole(ctx, "ws-a", "owner", memberservice.RoleAdmin); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner demoted: %v", err)
	}
	if membership := store.memberships[membershipKey("ws-a", "owner")]; membership.Status != "active" || membership.Role != memberservice.RoleOwner {
		t.Fatalf("failed removal changed the owner: %+v", membership)
	}
	if _, err := store.AuthenticateDevice(ctx, "device-owner", "secret-device-owner"); err != nil {
		t.Fatalf("failed removal revoked a device: %v", err)
	}
}

func TestMemberProfilesAndWorkspaceRolesAreProjectionsOnly(t *testing.T) {
	ctx := context.Background()
	store, _ := memberStoreFixture(t)
	members, err := store.ListMembers(ctx, "ws-a")
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	if members[0].UserID != "owner" || members[0].Email != "owner@example.com" || members[0].DisplayName != "Display owner" ||
		members[1].UserID != "member" || members[1].Email != "member@example.com" {
		t.Fatalf("member profiles = %+v", members)
	}
	updated, err := store.UpdateMemberRole(ctx, "ws-a", "member", memberservice.RoleManager)
	if err != nil || updated.Role != memberservice.RoleManager || updated.Email != "member@example.com" {
		t.Fatalf("updated membership = %+v, %v", updated, err)
	}
	for key, membership := range store.memberships {
		if membership.Email != "" || membership.DisplayName != "" {
			t.Fatalf("membership %s persisted profile fields: %+v", key, membership)
		}
	}
	workspaces, err := store.ListWorkspaces(ctx, "owner")
	if err != nil || len(workspaces) != 2 || workspaces[0].Role != memberservice.RoleOwner || workspaces[1].Role != memberservice.RoleOwner {
		t.Fatalf("owner workspaces = %+v, %v", workspaces, err)
	}
	for id, workspace := range store.workspaces {
		if workspace.Role != "" {
			t.Fatalf("workspace %s persisted a role: %+v", id, workspace)
		}
	}
}

func TestRotateDeviceCredentialReplacesTheSecret(t *testing.T) {
	ctx := context.Background()
	store, now := memberStoreFixture(t)
	replacement := deviceservice.Credential{ID: "rotated", DeviceID: "device-a1", SecretHash: "rotated-secret", CreatedAt: now}
	if _, err := store.RotateDeviceCredential(ctx, "owner", "device-a1", replacement, now); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("another user rotated the device: %v", err)
	}
	device, err := store.RotateDeviceCredential(ctx, "member", "device-a1", replacement, now)
	if err != nil || device.ID != "device-a1" {
		t.Fatalf("rotate = %+v, %v", device, err)
	}
	if _, err := store.AuthenticateDevice(ctx, "device-a1", "secret-device-a1"); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("old secret still authenticates: %v", err)
	}
	if _, err := store.AuthenticateDevice(ctx, "device-a1", "rotated-secret"); err != nil {
		t.Fatalf("new secret rejected: %v", err)
	}
	if err := store.RevokeDevice(ctx, "member", "device-a1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateDeviceCredential(ctx, "member", "device-a1", replacement, now); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("revoked device rotated: %v", err)
	}
}
