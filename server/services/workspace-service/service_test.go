package workspaceservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type fixture struct {
	ctx       context.Context
	store     *memory.Store
	service   *workspaceservice.Service
	workspace workspaceservice.Workspace
	users     map[string]string // name -> user ID
}

// newFixture creates a workspace owned by "owner" and adds each named member
// with the given role. Seat limits are lifted so role combinations fit.
func newFixture(t *testing.T, members map[string]memberservice.Role) fixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	f := fixture{ctx: ctx, store: store, service: workspaceservice.New(store), users: map[string]string{}}
	createUser := func(name string) {
		id := uuid.NewString()
		if err := store.CreateUser(ctx, authservice.User{ID: id, Email: name + "@example.com", DisplayName: "User " + name, Status: "active"}); err != nil {
			t.Fatal(err)
		}
		f.users[name] = id
	}
	createUser("owner")
	workspace, err := f.service.Create(ctx, f.users["owner"], workspaceservice.CreateInput{Name: "Members"})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = workspace
	if err := store.UpsertEntitlement(ctx, billingservice.Entitlement{
		OrganizationID: workspace.OrganizationID, Code: billingservice.EntitlementTeamMembers,
		FeatureEnabled: true, ValidFrom: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	for name, role := range members {
		createUser(name)
		if role == memberservice.RoleOwner {
			// Ownership cannot be granted through the service; seed it the way
			// workspace creation does.
			now := time.Now().UTC()
			if err := store.AddMember(ctx, workspaceservice.Membership{
				ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: f.users[name],
				Role: role, Status: "active", JoinedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := f.service.AddMember(ctx, f.users["owner"], workspace.ID, f.users[name], role); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	return f
}

func (f fixture) id(name string) string { return f.users[name] }

func TestRequireDeniesInactiveAndRemovedMembers(t *testing.T) {
	f := newFixture(t, map[string]memberservice.Role{"operator": memberservice.RoleOperator})
	if err := f.service.Require(f.ctx, f.workspace.ID, f.id("operator"), memberservice.PermissionInstanceOperate); err != nil {
		t.Fatalf("active operator denied: %v", err)
	}

	invitedID := uuid.NewString()
	if err := f.store.CreateUser(f.ctx, authservice.User{ID: invitedID, Email: "invited@example.com", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.store.AddMember(f.ctx, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: f.workspace.ID, UserID: invitedID,
		Role: memberservice.RoleAdmin, Status: "invited", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Require(f.ctx, f.workspace.ID, invitedID, memberservice.PermissionWorkspaceRead); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("inactive (invited) member authorized: %v", err)
	}

	if _, err := f.service.RemoveMember(f.ctx, f.id("owner"), f.workspace.ID, f.id("operator")); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Require(f.ctx, f.workspace.ID, f.id("operator"), memberservice.PermissionWorkspaceRead); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("removed member authorized: %v", err)
	}
	if _, err := f.service.Get(f.ctx, f.id("operator"), f.workspace.ID); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("removed member read workspace: %v", err)
	}
	if items, err := f.service.List(f.ctx, f.id("operator")); err != nil || len(items) != 0 {
		t.Fatalf("removed member still lists workspace: %+v %v", items, err)
	}
}

func TestChangeRolePermissionAndOwnerRules(t *testing.T) {
	f := newFixture(t, map[string]memberservice.Role{
		"admin": memberservice.RoleAdmin, "manager": memberservice.RoleManager,
		"viewer": memberservice.RoleViewer, "coowner": memberservice.RoleOwner,
	})
	ws := f.workspace.ID

	if _, err := f.service.ChangeRole(f.ctx, f.id("manager"), ws, f.id("viewer"), memberservice.RoleOperator); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("manager without member.manage changed a role: %v", err)
	}
	updated, err := f.service.ChangeRole(f.ctx, f.id("admin"), ws, f.id("viewer"), memberservice.RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != memberservice.RoleOperator || updated.UserID != f.id("viewer") || updated.Email != "viewer@example.com" || updated.DisplayName != "User viewer" {
		t.Fatalf("unexpected updated membership: %+v", updated)
	}
	if _, err := f.service.ChangeRole(f.ctx, f.id("admin"), ws, f.id("coowner"), memberservice.RoleAdmin); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("admin modified an owner: %v", err)
	}
	for _, role := range []memberservice.Role{memberservice.RoleOwner, "superuser", ""} {
		if _, err := f.service.ChangeRole(f.ctx, f.id("owner"), ws, f.id("admin"), role); !errors.Is(err, workspaceservice.ErrInvalidRole) {
			t.Fatalf("role %q accepted: %v", role, err)
		}
	}
	for _, target := range []string{uuid.NewString(), "not-a-uuid"} {
		if _, err := f.service.ChangeRole(f.ctx, f.id("owner"), ws, target, memberservice.RoleViewer); !errors.Is(err, workspaceservice.ErrNotFound) {
			t.Fatalf("non-member %q: %v", target, err)
		}
	}

	// An owner may demote another owner while one owner remains.
	if _, err := f.service.ChangeRole(f.ctx, f.id("owner"), ws, f.id("coowner"), memberservice.RoleAdmin); err != nil {
		t.Fatalf("owner could not demote co-owner: %v", err)
	}
	if _, err := f.service.ChangeRole(f.ctx, f.id("owner"), ws, f.id("owner"), memberservice.RoleAdmin); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner demoted: %v", err)
	}
	membership, err := f.store.FindMembership(f.ctx, ws, f.id("owner"))
	if err != nil || membership.Role != memberservice.RoleOwner {
		t.Fatalf("failed demotion changed the last owner: %+v %v", membership, err)
	}
}

func TestRemoveMemberRulesAndDeviceRevocation(t *testing.T) {
	f := newFixture(t, map[string]memberservice.Role{
		"admin": memberservice.RoleAdmin, "manager": memberservice.RoleManager,
		"operator": memberservice.RoleOperator, "coowner": memberservice.RoleOwner,
	})
	ws := f.workspace.ID
	devices := deviceservice.New(f.store, func() (string, string, error) {
		raw := uuid.NewString()
		return raw, "hash-" + raw, nil
	}, f.service)
	registration, err := devices.Register(f.ctx, f.id("operator"), deviceservice.RegisterInput{WorkspaceID: ws, Name: "Agent", Platform: "linux"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.service.RemoveMember(f.ctx, f.id("manager"), ws, f.id("operator")); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("manager removed a member: %v", err)
	}
	if _, err := f.service.RemoveMember(f.ctx, f.id("admin"), ws, f.id("coowner")); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("admin removed an owner: %v", err)
	}
	removal, err := f.service.RemoveMember(f.ctx, f.id("admin"), ws, f.id("operator"))
	if err != nil {
		t.Fatal(err)
	}
	if removal.Membership.Status != "removed" || len(removal.RevokedDeviceIDs) != 1 || removal.RevokedDeviceIDs[0] != registration.Device.ID {
		t.Fatalf("unexpected removal: %+v", removal)
	}
	if _, err := devices.Authenticate(f.ctx, registration.Device.ID, "hash-"+registration.Credential); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("removed member's device still authenticates: %v", err)
	}
	if _, err := f.service.RemoveMember(f.ctx, f.id("admin"), ws, f.id("operator")); !errors.Is(err, workspaceservice.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}

	// Owners can remove owners, but never the last one.
	if _, err := f.service.RemoveMember(f.ctx, f.id("owner"), ws, f.id("coowner")); err != nil {
		t.Fatalf("owner could not remove co-owner: %v", err)
	}
	if _, err := f.service.RemoveMember(f.ctx, f.id("owner"), ws, f.id("owner")); !errors.Is(err, workspaceservice.ErrLastOwner) {
		t.Fatalf("last owner removed: %v", err)
	}

	// A removed member can be added back; the revoked device stays revoked.
	if _, err := f.service.AddMember(f.ctx, f.id("owner"), ws, f.id("operator"), memberservice.RoleViewer); err != nil {
		t.Fatalf("removed member could not be re-added: %v", err)
	}
	if err := f.service.Require(f.ctx, ws, f.id("operator"), memberservice.PermissionWorkspaceRead); err != nil {
		t.Fatalf("re-added member denied: %v", err)
	}
	if _, err := devices.Authenticate(f.ctx, registration.Device.ID, "hash-"+registration.Credential); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("re-adding a member restored a revoked device: %v", err)
	}
	if _, err := f.service.AddMember(f.ctx, f.id("owner"), ws, f.id("operator"), memberservice.RoleViewer); !errors.Is(err, workspaceservice.ErrMemberExists) {
		t.Fatalf("duplicate active member: %v", err)
	}
}

func TestWorkspaceRoleIsCallerSpecificAndNotPersisted(t *testing.T) {
	f := newFixture(t, map[string]memberservice.Role{"viewer": memberservice.RoleViewer})
	if f.workspace.Role != memberservice.RoleOwner {
		t.Fatalf("create response role = %q", f.workspace.Role)
	}
	for name, want := range map[string]memberservice.Role{"owner": memberservice.RoleOwner, "viewer": memberservice.RoleViewer} {
		got, err := f.service.Get(f.ctx, f.id(name), f.workspace.ID)
		if err != nil || got.Role != want {
			t.Fatalf("%s get role = %q (%v), want %q", name, got.Role, err, want)
		}
		items, err := f.service.List(f.ctx, f.id(name))
		if err != nil || len(items) != 1 || items[0].Role != want {
			t.Fatalf("%s list = %+v (%v), want role %q", name, items, err, want)
		}
	}
	updated, err := f.service.Update(f.ctx, f.id("owner"), f.workspace.ID, "Renamed", f.workspace.Version)
	if err != nil || updated.Role != memberservice.RoleOwner || updated.Name != "Renamed" {
		t.Fatalf("update = %+v (%v)", updated, err)
	}
	stored, err := f.store.FindWorkspace(f.ctx, f.workspace.ID)
	if err != nil || stored.Role != "" {
		t.Fatalf("role was persisted on the workspace: %+v (%v)", stored, err)
	}
	members, err := f.service.Members(f.ctx, f.id("viewer"), f.workspace.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v (%v)", members, err)
	}
	for _, member := range members {
		if member.Email == "" || member.DisplayName == "" {
			t.Fatalf("member profile missing: %+v", member)
		}
	}
}
