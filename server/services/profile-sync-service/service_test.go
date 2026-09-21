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
	resolved, err := service.ResolveConflict(ctx, "user", "workspace", profile.ID, conflictPlan.Conflict.ID, profilesyncservice.ResolveConflictInput{Resolution: "keep_local"})
	if err != nil || resolved.Status != "resolved" {
		t.Fatalf("resolved conflict=%+v err=%v", resolved, err)
	}
	finalProfile, finalRevision, err := service.CommitRevision(ctx, "user", "workspace", profile.ID, conflictPlan.Revision.ID, profilesyncservice.LeaseTokenInput{DeviceID: "device-b", Token: conflictLease.Token})
	if err != nil || finalProfile.CurrentRevisionID != finalRevision.ID || finalRevision.Revision != 3 {
		t.Fatalf("conflict recovery commit profile=%+v revision=%+v err=%v", finalProfile, finalRevision, err)
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
