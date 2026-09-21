package fingerprintservice_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type allowAuthorizer struct{}

func (allowAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return nil
}

func TestFingerprintTemplateLifecycleAndRuntimeArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	service := fingerprintservice.New(store, allowAuthorizer{})
	seed := int64(676448312360042767)
	template, err := service.Create(ctx, "user", "workspace", fingerprintservice.CreateInput{
		Name: "Amazon US", Mode: "fixed", BrowserMajor: 144, Platform: "darwin",
		Seed: &seed, Locale: "en-US", Timezone: "America/Los_Angeles",
		Configuration: fingerprintservice.Configuration{
			PlatformVersion: "14.5", WindowWidth: 1440, WindowHeight: 900,
			HardwareConcurrency: 8, WebRTCPolicy: "disable_non_proxied_udp",
			CanvasNoise: true, ClientRectsNoise: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if template.Platform != "macos" || template.Version != 1 || template.Seed != seed {
		t.Fatalf("unexpected normalized template: %+v", template)
	}
	assertArg(t, template.RuntimeArgs, "--fingerprint=676448312360042767")
	assertArg(t, template.RuntimeArgs, "--accept-lang=en-US,en")
	assertArg(t, template.RuntimeArgs, "--timezone=America/Los_Angeles")
	assertArg(t, template.RuntimeArgs, "--disable-non-proxied-udp")
	for _, arg := range template.RuntimeArgs {
		if strings.HasPrefix(arg, "--proxy-") || strings.HasPrefix(arg, "--user-data-dir") {
			t.Fatalf("fingerprint template emitted an unsafe runtime argument: %q", arg)
		}
	}

	updated, err := service.Update(ctx, "user", "workspace", template.ID, fingerprintservice.UpdateInput{
		Name: template.Name, Mode: "fixed", BrowserMajor: 145, Platform: template.Platform,
		Seed: template.Seed, Locale: template.Locale, Timezone: template.Timezone,
		Configuration: template.Configuration, Version: template.Version,
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("updated template=%+v err=%v", updated, err)
	}
	if _, err := service.Update(ctx, "user", "workspace", template.ID, fingerprintservice.UpdateInput{
		Name: template.Name, Mode: "fixed", BrowserMajor: 145, Platform: template.Platform,
		Seed: template.Seed, Locale: template.Locale, Timezone: template.Timezone,
		Configuration: template.Configuration, Version: 1,
	}); !errors.Is(err, fingerprintservice.ErrVersionConflict) {
		t.Fatalf("stale update error=%v", err)
	}

	instance := browserinstanceservice.BrowserInstance{
		ID: "instance", WorkspaceID: "workspace", Name: "Instance", Platform: "chromium",
		FingerprintTemplateID: template.ID, DesiredState: "stopped", ObservedState: "offline",
		Version: 1, Tags: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, "user", "workspace", template.ID, updated.Version); !errors.Is(err, fingerprintservice.ErrInUse) {
		t.Fatalf("in-use delete error=%v", err)
	}
}

func TestFixedFingerprintRequiresExplicitSeed(t *testing.T) {
	t.Parallel()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	service := fingerprintservice.New(store, allowAuthorizer{})
	if _, err := service.Create(context.Background(), "user", "workspace", fingerprintservice.CreateInput{
		Name: "Fixed", Mode: "fixed", BrowserMajor: 144, Platform: "windows",
		Locale: "en-US", Timezone: "UTC",
	}); err == nil {
		t.Fatal("fixed fingerprint without a seed was accepted")
	}
}

func seedFingerprintWorkspace(t *testing.T, store *memory.Store) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateUser(context.Background(), authservice.User{
		ID: "user", Email: "fingerprint@example.com", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
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
}

func assertArg(t *testing.T, args []string, expected string) {
	t.Helper()
	for _, arg := range args {
		if arg == expected {
			return
		}
	}
	t.Fatalf("runtime args %q do not contain %q", args, expected)
}
