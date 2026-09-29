package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type allowProfileAuthorizer struct{}

func (allowProfileAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

// TestProfileConflictLifecycleAgainstPostgres exercises the conflict rules of
// the PostgreSQL repository: an open conflict blocks leases and revisions,
// keep_local promotes a verified snapshot (and is audited), keep_remote
// discards the local revision, and a device may replace only its own lease.
// Opt-in through ANT_TEST_DATABASE_URL like the other integration tests.
func TestProfileConflictLifecycleAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsPath); err != nil {
		t.Fatal(err)
	}
	// Fixtures are created with the migration connection; every profile sync
	// operation runs as the RLS-enforced ant_control_plane runtime role, as
	// in production, so missing tenant scope or policies fail here.
	admin, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const runtimePassword = "integration-control-plane"
	if _, err := pool.Exec(ctx, `ALTER ROLE ant_control_plane WITH LOGIN PASSWORD '`+runtimePassword+`'`); err != nil {
		t.Fatal(err)
	}
	runtimeURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL.User = url.UserPassword("ant_control_plane", runtimePassword)
	store, err := postgres.Open(ctx, runtimeURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := authservice.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@profile-sync.test", PasswordHash: "test",
		DisplayName: "Owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := admin.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	organization := workspaceservice.Organization{ID: uuid.NewString(), Name: "Profiles", Slug: "org-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	workspace := workspaceservice.Workspace{ID: uuid.NewString(), OrganizationID: organization.ID, Name: "Profiles", Slug: "ws-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := admin.CreateOrganizationWorkspace(ctx, organization, workspace, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: owner.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	user := postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: workspace.ID, UserID: owner.ID})
	deviceContext := func() (string, context.Context) {
		_, hash, err := security.NewOpaqueToken()
		if err != nil {
			t.Fatal(err)
		}
		device := deviceservice.Device{ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: owner.ID, Name: "Agent", Platform: "windows", Status: "offline", CreatedAt: now, UpdatedAt: now}
		if err := admin.CreateDevice(user, device, deviceservice.Credential{ID: uuid.NewString(), DeviceID: device.ID, SecretHash: hash, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		// Request-equivalent context of an agent route (see agent_profile.go).
		scoped := postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: workspace.ID})
		return device.ID, memberservice.WithDeviceAuthorization(scoped, workspace.ID, device.ID, memberservice.PermissionProfileRead, memberservice.PermissionProfileSync)
	}
	deviceA, asA := deviceContext()
	deviceB, asB := deviceContext()

	service := profilesyncservice.New(store, allowProfileAuthorizer{}, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{}, "integration-key", "memory-test")
	profile, err := service.CreateProfile(user, owner.ID, workspace.ID, profilesyncservice.CreateProfileInput{Name: "Conflicted profile"})
	if err != nil {
		t.Fatal(err)
	}
	lease := func(as context.Context, deviceID string) profilesyncservice.LeaseGrant {
		t.Helper()
		grant, err := service.AcquireLease(as, "", workspace.ID, profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: deviceID})
		if err != nil {
			t.Fatalf("lease for %s: %v", deviceID, err)
		}
		return grant
	}
	begin := func(as context.Context, deviceID string, grant profilesyncservice.LeaseGrant, base, hash string) (profilesyncservice.RevisionPlan, error) {
		return service.BeginRevision(as, "", workspace.ID, profile.ID, profilesyncservice.BeginRevisionInput{
			DeviceID: deviceID, LeaseToken: grant.Token, BaseRevisionID: base, Mode: "snapshot",
			Files: []profilesyncservice.FileInput{{Path: "profile.zip.enc", CiphertextSHA256: strings.Repeat(hash, 64), SizeBytes: 4096}},
		})
	}
	commit := func(as context.Context, deviceID string, grant profilesyncservice.LeaseGrant, plan profilesyncservice.RevisionPlan) profilesyncservice.Revision {
		t.Helper()
		_, revision, err := service.CommitRevision(as, "", workspace.ID, profile.ID, plan.Revision.ID, profilesyncservice.LeaseTokenInput{DeviceID: deviceID, Token: grant.Token})
		if err != nil {
			t.Fatalf("commit %s: %v", plan.Revision.ID, err)
		}
		return revision
	}
	release := func(as context.Context, deviceID string, grant profilesyncservice.LeaseGrant) {
		t.Helper()
		if err := service.ReleaseLease(as, "", workspace.ID, profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: deviceID, Token: grant.Token}); err != nil {
			t.Fatal(err)
		}
	}
	currentProfile := func() profilesyncservice.Profile {
		t.Helper()
		value, err := service.GetProfile(user, owner.ID, workspace.ID, profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	grant := lease(asA, deviceA)
	first, err := begin(asA, deviceA, grant, "", "a")
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := commit(asA, deviceA, grant, first)
	grant = lease(asA, deviceA)
	second, err := begin(asA, deviceA, grant, firstRevision.ID, "b")
	if err != nil {
		t.Fatal(err)
	}
	secondRevision := commit(asA, deviceA, grant, second)

	// Device B uploads a snapshot derived from the superseded first revision.
	conflictGrant := lease(asB, deviceB)
	conflicted, err := begin(asB, deviceB, conflictGrant, firstRevision.ID, "c")
	if err != nil || conflicted.Conflict == nil || conflicted.Conflict.RemoteRevisionID != secondRevision.ID {
		t.Fatalf("stale snapshot plan = %+v, %v", conflicted, err)
	}
	// The conflicting device still holds its lease and must be able to upload
	// the snapshot, otherwise keep_local could never succeed.
	if _, err := service.PrepareObjectUpload(asB, "", workspace.ID, profile.ID, conflicted.Revision.ID, conflicted.Objects[0].ID, profilesyncservice.LeaseTokenInput{DeviceID: deviceB, Token: conflictGrant.Token}); err != nil {
		t.Fatalf("upload grant for the conflicting snapshot: %v", err)
	}
	if _, err := begin(asB, deviceB, conflictGrant, secondRevision.ID, "d"); !errors.Is(err, profilesyncservice.ErrConflictUnresolved) {
		t.Fatalf("revision begun during an open conflict: %v", err)
	}
	if _, err := service.ResolveConflict(user, owner.ID, workspace.ID, profile.ID, conflicted.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("keep_local ran while a device held the lease: %v", err)
	}
	release(asB, deviceB, conflictGrant)
	if _, err := service.AcquireLease(asA, "", workspace.ID, profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: deviceA}); !errors.Is(err, profilesyncservice.ErrConflictUnresolved) {
		t.Fatalf("lease granted during an open conflict: %v", err)
	}
	if status := currentProfile().Status; status != "conflict" {
		t.Fatalf("profile status = %q, want conflict", status)
	}

	kept, err := service.ResolveConflict(user, owner.ID, workspace.ID, profile.ID, conflicted.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"})
	if err != nil || kept.Status != "resolved" || kept.Resolution != "keep_local" || kept.ResolvedBy != owner.ID {
		t.Fatalf("keep_local = %+v, %v", kept, err)
	}
	if promoted := currentProfile(); promoted.CurrentRevisionID != conflicted.Revision.ID || promoted.Status != "active" {
		t.Fatalf("keep_local did not promote the snapshot: %+v", promoted)
	}
	for id, want := range map[string]string{conflicted.Revision.ID: "committed", secondRevision.ID: "superseded"} {
		snapshot, err := service.GetRevision(user, owner.ID, workspace.ID, profile.ID, id)
		if err != nil || snapshot.Revision.Status != want {
			t.Fatalf("revision %s = %+v, %v; want %s", id, snapshot.Revision, err, want)
		}
	}
	var audited int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM audit_events
		WHERE workspace_id = $1::uuid AND action = 'profile.conflict.resolve' AND resource_id = $2::uuid
		  AND actor_user_id = $3::uuid AND metadata->>'resolution' = 'keep_local'
	`, workspace.ID, profile.ID, owner.ID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("keep_local audit rows = %d, %v", audited, err)
	}

	// Only the device itself may replace its active lease after a crash.
	lost := lease(asA, deviceA)
	if _, err := service.AcquireLease(user, owner.ID, workspace.ID, profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: deviceA}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("user preempted the device lease: %v", err)
	}
	if _, err := service.AcquireLease(asB, "", workspace.ID, profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: deviceB}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("another device took the lease: %v", err)
	}
	reclaimed := lease(asA, deviceA)
	if _, err := service.RenewLease(asA, "", workspace.ID, profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: deviceA, Token: lost.Token}, time.Minute); !errors.Is(err, profilesyncservice.ErrLeaseInvalid) {
		t.Fatalf("replaced lease token still renews: %v", err)
	}

	// keep_remote discards a stale upload and leaves the current revision.
	discardedPlan, err := begin(asA, deviceA, reclaimed, secondRevision.ID, "e")
	if err != nil || discardedPlan.Conflict == nil {
		t.Fatalf("second stale snapshot = %+v, %v", discardedPlan, err)
	}
	release(asA, deviceA, reclaimed)
	discarded, err := service.ResolveConflict(user, owner.ID, workspace.ID, profile.ID, discardedPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_remote"})
	if err != nil || discarded.Resolution != "keep_remote" {
		t.Fatalf("keep_remote = %+v, %v", discarded, err)
	}
	if after := currentProfile(); after.CurrentRevisionID != conflicted.Revision.ID || after.Status != "active" {
		t.Fatalf("keep_remote changed the profile: %+v", after)
	}
	if snapshot, err := service.GetRevision(user, owner.ID, workspace.ID, profile.ID, discardedPlan.Revision.ID); err != nil || snapshot.Revision.Status != "superseded" {
		t.Fatalf("discarded revision = %+v, %v", snapshot.Revision, err)
	}
	// Leases are available again once no conflict is open, and a historical
	// revision can be restored under the runtime role.
	restoreGrant := lease(asA, deviceA)
	restoredProfile, restored, err := service.RestoreRevision(asA, "", workspace.ID, profile.ID, firstRevision.ID, profilesyncservice.LeaseTokenInput{DeviceID: deviceA, Token: restoreGrant.Token})
	if err != nil || restored.Status != "committed" || restoredProfile.CurrentRevisionID != firstRevision.ID {
		t.Fatalf("restore = %+v %+v, %v", restoredProfile, restored, err)
	}
}
