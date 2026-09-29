package profilesyncservice_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type allowAuthorizer struct{}

func (allowAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

func TestProfileRevisionLeaseCommitAndConflictRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedProfileWorkspace(t, store)
	service := profilesyncservice.New(
		store, allowAuthorizer{}, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{},
		"development-envelope-key", "memory-test",
	)
	profile, err := service.CreateProfile(ctx, "user", "workspace", profilesyncservice.CreateProfileInput{Name: "Amazon US Profile"})
	if err != nil {
		t.Fatal(err)
	}
	firstLease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil || firstLease.Token == "" {
		t.Fatalf("first lease=%+v err=%v", firstLease, err)
	}
	if _, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-b"}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("competing lease error=%v", err)
	}
	firstPlan, err := service.BeginRevision(ctx, "user", "workspace", profile.ID, profilesyncservice.BeginRevisionInput{
		DeviceID: "device-a", LeaseToken: firstLease.Token,
		Files: []profilesyncservice.FileInput{
			{Path: "Default/Cookies", CiphertextSHA256: strings.Repeat("b", 64), SizeBytes: 2048, ContentType: "application/octet-stream"},
			{Path: "Local State", CiphertextSHA256: strings.Repeat("a", 64), SizeBytes: 512, ContentType: "application/json"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Revision.Revision != 1 || firstPlan.Conflict != nil || firstPlan.Manifest.FileCount != 2 || firstPlan.Manifest.TotalBytes != 2560 {
		t.Fatalf("unexpected first revision plan: %+v", firstPlan)
	}
	if firstPlan.Manifest.Files[0].Path != "Default/Cookies" || strings.Contains(firstPlan.Objects[0].ObjectKey, "Cookies") {
		t.Fatalf("manifest order/object key is unsafe: %+v", firstPlan)
	}
	committedProfile, firstRevision, err := service.CommitRevision(ctx, "user", "workspace", profile.ID, firstPlan.Revision.ID, profilesyncservice.LeaseTokenInput{
		DeviceID: "device-a", Token: firstLease.Token,
	})
	if err != nil || firstRevision.Status != "committed" || committedProfile.CurrentRevisionID != firstRevision.ID {
		t.Fatalf("first commit profile=%+v revision=%+v err=%v", committedProfile, firstRevision, err)
	}

	secondLease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil {
		t.Fatal(err)
	}
	secondPlan := beginSingleFile(t, service, profile.ID, "device-a", secondLease.Token, firstRevision.ID, "c")
	_, secondRevision, err := service.CommitRevision(ctx, "user", "workspace", profile.ID, secondPlan.Revision.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-a", Token: secondLease.Token})
	if err != nil {
		t.Fatal(err)
	}

	conflictLease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-b"})
	if err != nil {
		t.Fatal(err)
	}
	conflictPlan := beginSingleFile(t, service, profile.ID, "device-b", conflictLease.Token, firstRevision.ID, "d")
	if conflictPlan.Conflict == nil || conflictPlan.Conflict.RemoteRevisionID != secondRevision.ID {
		t.Fatalf("stale base did not produce a conflict: %+v", conflictPlan)
	}
	if _, _, err := service.CommitRevision(ctx, "user", "workspace", profile.ID, conflictPlan.Revision.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-b", Token: conflictLease.Token}); !errors.Is(err, profilesyncservice.ErrRevisionConflict) {
		t.Fatalf("unresolved conflict commit error=%v", err)
	}
	// While the conflict is open nothing may pull, push or restore.
	if _, err := service.BeginRevision(ctx, "user", "workspace", profile.ID, profilesyncservice.BeginRevisionInput{
		DeviceID: "device-b", LeaseToken: conflictLease.Token, BaseRevisionID: secondRevision.ID, Mode: "incremental",
		Files: []profilesyncservice.FileInput{{Path: "Default/Cookies", CiphertextSHA256: strings.Repeat("e", 64), SizeBytes: 1}},
	}); !errors.Is(err, profilesyncservice.ErrConflictUnresolved) {
		t.Fatalf("revision begun during an open conflict: %v", err)
	}
	if err := service.ReleaseLease(ctx, "user", "workspace", profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-b", Token: conflictLease.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"}); !errors.Is(err, profilesyncservice.ErrConflictUnresolved) {
		t.Fatalf("lease granted during an open conflict: %v", err)
	}
	if conflicted, err := service.GetProfile(ctx, "user", "workspace", profile.ID); err != nil || conflicted.Status != "conflict" {
		t.Fatalf("profile status after release = %+v, %v; want conflict", conflicted, err)
	}
	// An incremental conflict revision cannot become current on its own:
	// its delta applies to a base that is no longer current.
	if _, err := service.ResolveConflict(ctx, "user", "workspace", profile.ID, conflictPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"}); !errors.Is(err, profilesyncservice.ErrRevisionState) {
		t.Fatalf("incremental conflict revision promoted: %v", err)
	}
	discarded, err := service.ResolveConflict(ctx, "user", "workspace", profile.ID, conflictPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_remote"})
	if err != nil || discarded.Status != "resolved" || discarded.Resolution != "keep_remote" {
		t.Fatalf("keep_remote conflict=%+v err=%v", discarded, err)
	}
	if afterRemote, err := service.GetProfile(ctx, "user", "workspace", profile.ID); err != nil || afterRemote.Status != "active" || afterRemote.CurrentRevisionID != secondRevision.ID {
		t.Fatalf("keep_remote changed the current revision: %+v, %v", afterRemote, err)
	}

	// A full snapshot uploaded from a stale base can be kept: it becomes the
	// current revision once no device is mid-transfer.
	snapshotLease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-b"})
	if err != nil {
		t.Fatal(err)
	}
	snapshotPlan, err := service.BeginRevision(ctx, "user", "workspace", profile.ID, profilesyncservice.BeginRevisionInput{
		DeviceID: "device-b", LeaseToken: snapshotLease.Token, BaseRevisionID: firstRevision.ID, Mode: "snapshot",
		Files: []profilesyncservice.FileInput{{Path: "profile.zip.enc", CiphertextSHA256: strings.Repeat("f", 64), SizeBytes: 4096}},
	})
	if err != nil || snapshotPlan.Conflict == nil {
		t.Fatalf("stale snapshot plan=%+v err=%v", snapshotPlan, err)
	}
	if _, err := service.ResolveConflict(ctx, "user", "workspace", profile.ID, snapshotPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("keep_local ran while a device held the lease: %v", err)
	}
	if err := service.ReleaseLease(ctx, "user", "workspace", profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-b", Token: snapshotLease.Token}); err != nil {
		t.Fatal(err)
	}
	kept, err := service.ResolveConflict(ctx, "user", "workspace", profile.ID, snapshotPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"})
	if err != nil || kept.Status != "resolved" || kept.Resolution != "keep_local" {
		t.Fatalf("keep_local conflict=%+v err=%v", kept, err)
	}
	finalProfile, err := service.GetProfile(ctx, "user", "workspace", profile.ID)
	if err != nil || finalProfile.CurrentRevisionID != snapshotPlan.Revision.ID || finalProfile.Status != "active" {
		t.Fatalf("keep_local did not promote the snapshot: %+v, %v", finalProfile, err)
	}
	if promoted, err := service.GetRevision(ctx, "user", "workspace", profile.ID, snapshotPlan.Revision.ID); err != nil || promoted.Revision.Status != "committed" {
		t.Fatalf("promoted revision = %+v, %v", promoted.Revision, err)
	}
	if replaced, err := service.GetRevision(ctx, "user", "workspace", profile.ID, secondRevision.ID); err != nil || replaced.Revision.Status != "superseded" {
		t.Fatalf("replaced remote revision = %+v, %v", replaced.Revision, err)
	}
	snapshot, err := service.GetRevision(ctx, "user", "workspace", profile.ID, firstRevision.ID)
	if err != nil || snapshot.Revision.Status != "superseded" || len(snapshot.Objects) != 2 {
		t.Fatalf("historical revision snapshot=%+v err=%v", snapshot, err)
	}
	download, err := service.PrepareObjectDownload(ctx, "user", "workspace", profile.ID, firstRevision.ID, snapshot.Objects[0].ID)
	if err != nil || download.Method != "GET" || !strings.HasPrefix(download.URL, "memory://profile-objects/") {
		t.Fatalf("historical object download grant=%+v err=%v", download, err)
	}
	restoreLease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil {
		t.Fatal(err)
	}
	restoredProfile, restoredRevision, err := service.RestoreRevision(
		ctx, "user", "workspace", profile.ID, firstRevision.ID,
		profilesyncservice.LeaseTokenInput{DeviceID: "device-a", Token: restoreLease.Token},
	)
	if err != nil || restoredProfile.CurrentRevisionID != firstRevision.ID || restoredRevision.Status != "committed" {
		t.Fatalf("historical restore profile=%+v revision=%+v err=%v", restoredProfile, restoredRevision, err)
	}
}

func TestDeviceReclaimsOnlyItsOwnActiveLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedProfileWorkspace(t, store)
	service := profilesyncservice.New(store, allowAuthorizer{}, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{}, "key", "memory-test")
	profile, err := service.CreateProfile(ctx, "user", "workspace", profilesyncservice.CreateProfileInput{Name: "Profile"})
	if err != nil {
		t.Fatal(err)
	}
	deviceA := memberservice.WithDeviceAuthorization(ctx, "workspace", "device-a", memberservice.PermissionProfileRead, memberservice.PermissionProfileSync)
	lost, err := service.AcquireLease(deviceA, "", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil {
		t.Fatal(err)
	}
	// A workspace user naming the device must not preempt its transfer.
	if _, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("user preempted a device lease: %v", err)
	}
	deviceB := memberservice.WithDeviceAuthorization(ctx, "workspace", "device-b", memberservice.PermissionProfileRead, memberservice.PermissionProfileSync)
	if _, err := service.AcquireLease(deviceB, "", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-b"}); !errors.Is(err, profilesyncservice.ErrLeaseHeld) {
		t.Fatalf("another device took the lease: %v", err)
	}
	// After a crash the device lost its token; it may replace its own lease.
	reclaimed, err := service.AcquireLease(deviceA, "", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil || reclaimed.Token == lost.Token {
		t.Fatalf("device could not reclaim its own lease: %+v, %v", reclaimed, err)
	}
	if _, err := service.RenewLease(deviceA, "", "workspace", profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-a", Token: lost.Token}, time.Minute); !errors.Is(err, profilesyncservice.ErrLeaseInvalid) {
		t.Fatalf("the replaced token still renews: %v", err)
	}
	if _, err := service.RenewLease(deviceA, "", "workspace", profile.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-a", Token: reclaimed.Token}, time.Minute); err != nil {
		t.Fatalf("the reclaimed token does not renew: %v", err)
	}
}

func TestProfileManifestRejectsTraversal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedProfileWorkspace(t, store)
	service := profilesyncservice.New(store, allowAuthorizer{}, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{}, "key", "memory-test")
	profile, err := service.CreateProfile(ctx, "user", "workspace", profilesyncservice.CreateProfileInput{Name: "Profile"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := service.AcquireLease(ctx, "user", "workspace", profile.ID, profilesyncservice.AcquireLeaseInput{DeviceID: "device-a"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.BeginRevision(ctx, "user", "workspace", profile.ID, profilesyncservice.BeginRevisionInput{
		DeviceID: "device-a", LeaseToken: lease.Token,
		Files: []profilesyncservice.FileInput{{Path: "../Cookies", CiphertextSHA256: strings.Repeat("a", 64), SizeBytes: 1}},
	})
	if err == nil {
		t.Fatal("profile traversal path was accepted")
	}
}

func beginSingleFile(t *testing.T, service *profilesyncservice.Service, profileID, deviceID, leaseToken, baseRevisionID, hashCharacter string) profilesyncservice.RevisionPlan {
	t.Helper()
	plan, err := service.BeginRevision(context.Background(), "user", "workspace", profileID, profilesyncservice.BeginRevisionInput{
		DeviceID: deviceID, LeaseToken: leaseToken, BaseRevisionID: baseRevisionID, Mode: "incremental",
		Files:        []profilesyncservice.FileInput{{Path: "Default/Cookies", CiphertextSHA256: strings.Repeat(hashCharacter, 64), SizeBytes: 2048}},
		DeletedPaths: []string{"Default/Session Storage/obsolete"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Manifest.Mode != "incremental" || len(plan.Manifest.DeletedPaths) != 1 {
		t.Fatalf("incremental manifest was not preserved: %+v", plan.Manifest)
	}
	return plan
}

func seedProfileWorkspace(t *testing.T, store *memory.Store) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateUser(context.Background(), authservice.User{ID: "user", Email: "profile@example.com", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOrganizationWorkspace(
		context.Background(),
		workspaceservice.Organization{ID: "organization", Name: "Organization", Slug: "organization", Version: 1, CreatedAt: now, UpdatedAt: now},
		workspaceservice.Workspace{ID: "workspace", OrganizationID: "organization", Name: "Workspace", Slug: "workspace", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now},
		workspaceservice.Membership{ID: "membership", WorkspaceID: "workspace", UserID: "user", Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now},
	); err != nil {
		t.Fatal(err)
	}
	for _, deviceID := range []string{"device-a", "device-b"} {
		if err := store.CreateDevice(context.Background(), deviceservice.Device{
			ID: deviceID, WorkspaceID: "workspace", UserID: "user", Name: deviceID,
			Platform: "windows", Status: "offline", Capabilities: map[string]interface{}{}, CreatedAt: now, UpdatedAt: now,
		}, deviceservice.Credential{ID: deviceID + "-credential", DeviceID: deviceID, SecretHash: deviceID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
}
