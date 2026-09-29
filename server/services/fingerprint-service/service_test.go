package fingerprintservice_test

import (
	"context"
	"errors"
	"fmt"
	"math"
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

type authorizationCall struct {
	workspaceID string
	actorID     string
	permission  memberservice.Permission
}

type recordingAuthorizer struct {
	err   error
	calls []authorizationCall
}

func (a *recordingAuthorizer) Require(_ context.Context, workspaceID, actorID string, permission memberservice.Permission) error {
	a.calls = append(a.calls, authorizationCall{workspaceID: workspaceID, actorID: actorID, permission: permission})
	return a.err
}

func TestFingerprintTemplateLifecycleAndRuntimeArgs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	service := fingerprintservice.New(store, allowAuthorizer{})
	seed := int64(676448312360042767)
	touchPoints := 5
	template, err := service.Create(ctx, "user", "workspace", fingerprintservice.CreateInput{
		Name: "Amazon US", Mode: "fixed", BrowserMajor: 144, Platform: "darwin",
		Seed: &seed, Locale: "en-US", Timezone: "America/Los_Angeles",
		Configuration: fingerprintservice.Configuration{
			PlatformVersion: "14.5", WindowWidth: 1440, WindowHeight: 900,
			HardwareConcurrency: 8, WebRTCPolicy: "disable_non_proxied_udp",
			DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchPoints, DoNotTrack: "1",
			ScreenWidth: 2560, ScreenHeight: 1440, DeviceScaleFactor: 2,
			CanvasNoise: true, AudioNoise: true, ClientRectsNoise: true,
			Fonts:       []string{" Inter ", "Arial", "inter"},
			WebGLVendor: "Apple Inc.", WebGLRenderer: "Apple M2",
			MediaDevices: &fingerprintservice.MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
			Battery:      &fingerprintservice.BatteryConfiguration{Charging: true, Level: 0.82, ChargingTimeSeconds: 900, DischargingTimeSeconds: 7200},
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
	if len(template.Configuration.Fonts) != 2 || template.Configuration.Fonts[0] != "Inter" || template.Configuration.Fonts[1] != "Arial" {
		t.Fatalf("font normalization = %#v", template.Configuration.Fonts)
	}
	for _, arg := range template.RuntimeArgs {
		if strings.HasPrefix(arg, "--proxy-") || strings.HasPrefix(arg, "--user-data-dir") {
			t.Fatalf("fingerprint template emitted an unsafe runtime argument: %q", arg)
		}
		for _, unsupported := range []string{"--fingerprint-device-memory", "--fingerprint-audio", "--fingerprint-font", "--fingerprint-webgl", "--fingerprint-screen"} {
			if strings.HasPrefix(arg, unsupported) {
				t.Fatalf("template revived a Chrome-144 no-effect argument: %q", arg)
			}
		}
	}
	loaded, err := service.Get(ctx, "user", "workspace", template.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Configuration.Fonts[0] = "mutated"
	*loaded.Configuration.MaxTouchPoints = 0
	loaded.Configuration.MediaDevices.AudioInputs = 9
	loaded.Configuration.Battery.Level = 0
	reloaded, err := service.Get(ctx, "user", "workspace", template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Configuration.Fonts[0] != "Inter" || *reloaded.Configuration.MaxTouchPoints != 5 ||
		reloaded.Configuration.MediaDevices.AudioInputs != 1 || reloaded.Configuration.Battery.Level != 0.82 {
		t.Fatalf("stored advanced configuration was aliased: %+v", reloaded.Configuration)
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

// staleRuntimeArgsRepository simulates rows written by an older release
// whose cached runtime_args are empty or no longer match the configuration.
type staleRuntimeArgsRepository struct {
	fingerprintservice.Repository
}

func (r staleRuntimeArgsRepository) FindFingerprintTemplate(ctx context.Context, workspaceID, templateID string) (fingerprintservice.Template, error) {
	template, err := r.Repository.FindFingerprintTemplate(ctx, workspaceID, templateID)
	template.RuntimeArgs = []string{"--fingerprint=1", "--lang=xx-XX"}
	return template, err
}

func (r staleRuntimeArgsRepository) ListFingerprintTemplates(ctx context.Context, workspaceID string) ([]fingerprintservice.Template, error) {
	templates, err := r.Repository.ListFingerprintTemplates(ctx, workspaceID)
	for index := range templates {
		templates[index].RuntimeArgs = nil
	}
	return templates, err
}

func TestFingerprintReadsRecomputeRuntimeArgsFromConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	seed := int64(4242)
	created, err := fingerprintservice.New(store, allowAuthorizer{}).Create(ctx, "user", "workspace", fingerprintservice.CreateInput{
		Name: "Legacy row", Mode: "fixed", BrowserMajor: 144, Platform: "windows", Seed: &seed,
		Locale: "de-DE", Timezone: "Europe/Berlin",
		Configuration: fingerprintservice.Configuration{HardwareConcurrency: 4, WebRTCPolicy: "default_public_interface_only", CanvasNoise: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := fingerprintservice.New(staleRuntimeArgsRepository{Repository: store}, allowAuthorizer{})
	loaded, err := service.Get(ctx, "user", "workspace", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.RuntimeArgs, " ") != strings.Join(created.RuntimeArgs, " ") {
		t.Fatalf("Get served stale runtime args %v, want %v", loaded.RuntimeArgs, created.RuntimeArgs)
	}
	listed, err := service.List(ctx, "user", "workspace")
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %v, %v", listed, err)
	}
	if strings.Join(listed[0].RuntimeArgs, " ") != strings.Join(created.RuntimeArgs, " ") {
		t.Fatalf("List served stale runtime args %v, want %v", listed[0].RuntimeArgs, created.RuntimeArgs)
	}
	assertArg(t, loaded.RuntimeArgs, "--accept-lang=de-DE,de")
	assertArg(t, loaded.RuntimeArgs, "--webrtc-ip-handling-policy=default_public_interface_only")
}

func TestAdvancedFingerprintConfigurationValidation(t *testing.T) {
	t.Parallel()
	negative := -1
	valid := fingerprintservice.Configuration{
		DeviceMemory: 8, ColorDepth: 24, ScreenWidth: 1920, ScreenHeight: 1080,
		DeviceScaleFactor: 1.25, WebGLVendor: "Intel Inc.", WebGLRenderer: "Intel Iris Xe",
		MediaDevices: &fingerprintservice.MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
		Battery:      &fingerprintservice.BatteryConfiguration{Charging: false, Level: 0.5, ChargingTimeSeconds: 0, DischargingTimeSeconds: 3600},
	}
	tests := []struct {
		name   string
		mutate func(*fingerprintservice.Configuration)
	}{
		{name: "device memory", mutate: func(c *fingerprintservice.Configuration) { c.DeviceMemory = 3 }},
		{name: "color depth", mutate: func(c *fingerprintservice.Configuration) { c.ColorDepth = 12 }},
		{name: "touch points", mutate: func(c *fingerprintservice.Configuration) { c.MaxTouchPoints = &negative }},
		{name: "do not track", mutate: func(c *fingerprintservice.Configuration) { c.DoNotTrack = "yes" }},
		{name: "screen pair", mutate: func(c *fingerprintservice.Configuration) { c.ScreenHeight = 0 }},
		{name: "scale", mutate: func(c *fingerprintservice.Configuration) { c.DeviceScaleFactor = 8 }},
		{name: "webgl pair", mutate: func(c *fingerprintservice.Configuration) { c.WebGLRenderer = "" }},
		{name: "fonts", mutate: func(c *fingerprintservice.Configuration) { c.Fonts = []string{strings.Repeat("x", 101)} }},
		{name: "media", mutate: func(c *fingerprintservice.Configuration) { c.MediaDevices.AudioInputs = 17 }},
		{name: "battery", mutate: func(c *fingerprintservice.Configuration) { c.Battery.Level = 1.1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := memory.New()
			seedFingerprintWorkspace(t, store)
			service := fingerprintservice.New(store, allowAuthorizer{})
			configuration := valid
			media := *valid.MediaDevices
			battery := *valid.Battery
			configuration.MediaDevices = &media
			configuration.Battery = &battery
			test.mutate(&configuration)
			seed := int64(123)
			_, err := service.Create(context.Background(), "user", "workspace", fingerprintservice.CreateInput{
				Name: "Invalid", Mode: "fixed", BrowserMajor: 144, Platform: "windows", Seed: &seed,
				Locale: "en-US", Timezone: "UTC", Configuration: configuration,
			})
			if err == nil {
				t.Fatal("invalid advanced fingerprint configuration was accepted")
			}
		})
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
	for _, seed := range []int64{0, -1} {
		if _, err := service.Create(context.Background(), "user", "workspace", fingerprintservice.CreateInput{
			Name: "Fixed", Mode: "fixed", BrowserMajor: 144, Platform: "windows", Seed: &seed,
			Locale: "en-US", Timezone: "UTC",
		}); !errors.Is(err, fingerprintservice.ErrInvalidInput) {
			t.Fatalf("fixed fingerprint seed %d error = %v, want ErrInvalidInput", seed, err)
		}
	}
}

func TestFingerprintBatchCreateNormalizesAndPersistsIndependentTemplates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	authorizer := &recordingAuthorizer{}
	service := fingerprintservice.New(store, authorizer)
	seedStart := int64(9000)
	touchPoints := 5
	input := fingerprintservice.BatchCreateInput{
		NamePrefix: "  Amazon US  ", Count: 3, Mode: " FIXED ", BrowserMajor: 144,
		Platform: "WINDOWS", SeedStart: &seedStart, Locale: "en-US", Timezone: "America/Los_Angeles",
		Configuration: fingerprintservice.Configuration{
			WindowWidth: 1440, WindowHeight: 900, HardwareConcurrency: 8,
			DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchPoints, DoNotTrack: " 1 ",
			ScreenWidth: 1920, ScreenHeight: 1080, DeviceScaleFactor: 1,
			WebRTCPolicy: " DISABLE_NON_PROXIED_UDP ", CanvasNoise: true, AudioNoise: true, ClientRectsNoise: true,
			Fonts:       []string{" Arial ", "arial", "Inter"},
			WebGLVendor: " Intel Inc. ", WebGLRenderer: " Intel Iris Xe ",
			MediaDevices: &fingerprintservice.MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
			Battery:      &fingerprintservice.BatteryConfiguration{Charging: true, Level: 0.75, ChargingTimeSeconds: 600, DischargingTimeSeconds: 7200},
		},
	}

	templates, err := service.CreateBatch(ctx, "user", "workspace", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != (authorizationCall{
		workspaceID: "workspace", actorID: "user", permission: memberservice.PermissionFingerprintManage,
	}) {
		t.Fatalf("batch authorization calls = %+v", authorizer.calls)
	}
	if len(templates) != 3 {
		t.Fatalf("batch size = %d, want 3", len(templates))
	}
	createdAt := templates[0].CreatedAt
	for index, template := range templates {
		expectedName := []string{"Amazon US 01", "Amazon US 02", "Amazon US 03"}[index]
		if template.Name != expectedName || template.Seed != seedStart+int64(index) || template.Mode != "fixed" || template.Platform != "windows" {
			t.Fatalf("template %d was not deterministically normalized: %+v", index, template)
		}
		if !template.CreatedAt.Equal(createdAt) || !template.UpdatedAt.Equal(createdAt) {
			t.Fatalf("template %d did not use the shared batch timestamp: %+v", index, template)
		}
		if len(template.Configuration.Fonts) != 2 || template.Configuration.Fonts[0] != "Arial" || template.Configuration.Fonts[1] != "Inter" {
			t.Fatalf("template %d fonts = %#v", index, template.Configuration.Fonts)
		}
		assertArg(t, template.RuntimeArgs, "--fingerprint="+stringInt64(seedStart+int64(index)))
		assertArg(t, template.RuntimeArgs, "--disable-non-proxied-udp")
	}

	// Every batch member and the repository snapshot must own its nested
	// configuration. Mutating a response must not alter a sibling or storage.
	templates[0].Configuration.Fonts[0] = "mutated"
	*templates[0].Configuration.MaxTouchPoints = 0
	templates[0].Configuration.MediaDevices.AudioInputs = 9
	templates[0].Configuration.Battery.Level = 0
	if templates[1].Configuration.Fonts[0] != "Arial" || *templates[1].Configuration.MaxTouchPoints != 5 ||
		templates[1].Configuration.MediaDevices.AudioInputs != 1 || templates[1].Configuration.Battery.Level != 0.75 {
		t.Fatalf("batch members share nested configuration: %+v", templates[1].Configuration)
	}
	stored, err := store.ListFingerprintTemplates(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[0].Configuration.Fonts[0] != "Arial" || *stored[0].Configuration.MaxTouchPoints != 5 ||
		stored[0].Configuration.MediaDevices.AudioInputs != 1 || stored[0].Configuration.Battery.Level != 0.75 {
		t.Fatalf("stored batch was aliased to response: %+v", stored)
	}
}

func TestFingerprintBatchValidationDoesNotWritePartialData(t *testing.T) {
	t.Parallel()
	zero := int64(0)
	overflow := int64(math.MaxInt64)
	validSeed := int64(100)
	tests := []struct {
		name   string
		input  fingerprintservice.BatchCreateInput
		target error
	}{
		{name: "empty", input: validFingerprintBatch(0, &validSeed), target: fingerprintservice.ErrEmptyBatch},
		{name: "negative count", input: validFingerprintBatch(-1, &validSeed), target: fingerprintservice.ErrEmptyBatch},
		{name: "too large", input: validFingerprintBatch(fingerprintservice.MaxBatchTemplates+1, &validSeed), target: fingerprintservice.ErrBatchTooLarge},
		{name: "fixed without seed", input: validFingerprintBatch(2, nil), target: fingerprintservice.ErrInvalidInput},
		{name: "zero seed", input: validFingerprintBatch(2, &zero), target: fingerprintservice.ErrInvalidInput},
		{name: "overflowing seed range", input: validFingerprintBatch(2, &overflow), target: fingerprintservice.ErrInvalidInput},
		{name: "invalid mode", input: func() fingerprintservice.BatchCreateInput {
			input := validFingerprintBatch(2, &validSeed)
			input.Mode = "randomish"
			return input
		}(), target: fingerprintservice.ErrInvalidInput},
		{name: "invalid advanced configuration", input: func() fingerprintservice.BatchCreateInput {
			input := validFingerprintBatch(2, &validSeed)
			input.Configuration.DeviceMemory = 3
			return input
		}(), target: fingerprintservice.ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := memory.New()
			seedFingerprintWorkspace(t, store)
			service := fingerprintservice.New(store, allowAuthorizer{})
			if _, err := service.CreateBatch(context.Background(), "user", "workspace", test.input); !errors.Is(err, test.target) {
				t.Fatalf("CreateBatch error = %v, want %v", err, test.target)
			}
			stored, err := store.ListFingerprintTemplates(context.Background(), "workspace")
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != 0 {
				t.Fatalf("invalid batch wrote templates: %+v", stored)
			}
		})
	}
}

func TestFingerprintBatchNameConflictIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	service := fingerprintservice.New(store, allowAuthorizer{})
	seed := int64(41)
	if _, err := service.Create(ctx, "user", "workspace", fingerprintservice.CreateInput{
		Name: "store 02", Mode: "fixed", BrowserMajor: 144, Platform: "windows", Seed: &seed,
		Locale: "en-US", Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	seedStart := int64(100)
	if _, err := service.CreateBatch(ctx, "user", "workspace", validFingerprintBatch(3, &seedStart)); !errors.Is(err, fingerprintservice.ErrNameConflict) {
		t.Fatalf("CreateBatch conflict error = %v", err)
	}
	stored, err := store.ListFingerprintTemplates(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Name != "store 02" {
		t.Fatalf("conflicting batch was partially persisted: %+v", stored)
	}

	// The repository also rejects duplicate names inside one write before it
	// mutates state, protecting callers other than the service generator.
	if err := store.CreateFingerprintTemplates(ctx, []fingerprintservice.Template{
		{ID: "duplicate-a", WorkspaceID: "workspace", Name: "Duplicate"},
		{ID: "duplicate-b", WorkspaceID: "workspace", Name: "duplicate"},
	}); !errors.Is(err, fingerprintservice.ErrNameConflict) {
		t.Fatalf("intra-batch duplicate error = %v", err)
	}
	stored, err = store.ListFingerprintTemplates(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("intra-batch conflict wrote partial data: %+v", stored)
	}
}

func TestFingerprintPresetsAreAuthorizedValidAndDeepCloned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	authorizer := &recordingAuthorizer{}
	service := fingerprintservice.New(store, authorizer)
	presets, err := service.ListPresets(ctx, "user", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0].permission != memberservice.PermissionFingerprintRead {
		t.Fatalf("preset authorization calls = %+v", authorizer.calls)
	}
	if len(presets) != 3 {
		t.Fatalf("preset count = %d, want 3", len(presets))
	}
	keys := make(map[string]struct{}, len(presets))
	for index, preset := range presets {
		if preset.Key == "" || preset.Name == "" || preset.Description == "" {
			t.Fatalf("preset %d metadata is incomplete: %+v", index, preset)
		}
		if _, duplicate := keys[preset.Key]; duplicate {
			t.Fatalf("duplicate preset key %q", preset.Key)
		}
		keys[preset.Key] = struct{}{}
		input := preset.Input
		seed := int64(500 + index)
		input.Seed = &seed
		if _, err := service.Create(ctx, "user", "workspace", input); err != nil {
			t.Fatalf("preset %q is not a valid create input: %v", preset.Key, err)
		}
	}

	// Mutating one returned preset must not affect another preset sharing the
	// same conceptual defaults or a subsequent ListPresets response.
	presets[0].Input.Configuration.Fonts[0] = "mutated"
	*presets[0].Input.Configuration.MaxTouchPoints = 19
	presets[0].Input.Configuration.MediaDevices.AudioInputs = 9
	presets[0].Input.Configuration.Battery.Level = 0
	if *presets[2].Input.Configuration.MaxTouchPoints != 0 {
		t.Fatalf("preset touch-point pointers are aliased: %+v", presets)
	}
	again, err := service.ListPresets(ctx, "user", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Input.Configuration.Fonts[0] != "Arial" || *again[0].Input.Configuration.MaxTouchPoints != 0 ||
		again[0].Input.Configuration.MediaDevices.AudioInputs != 1 || again[0].Input.Configuration.Battery.Level != 0.82 {
		t.Fatalf("preset catalog leaked caller mutations: %+v", again[0])
	}
}

func TestFingerprintBatchAndPresetPermissionsAreFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	seedFingerprintWorkspace(t, store)
	denied := errors.New("denied by policy")
	authorizer := &recordingAuthorizer{err: denied}
	service := fingerprintservice.New(store, authorizer)
	seedStart := int64(100)
	if _, err := service.CreateBatch(ctx, "user", "workspace", validFingerprintBatch(2, &seedStart)); !errors.Is(err, denied) {
		t.Fatalf("denied batch error = %v", err)
	}
	if _, err := service.ListPresets(ctx, "user", "workspace"); !errors.Is(err, denied) {
		t.Fatalf("denied presets error = %v", err)
	}
	if len(authorizer.calls) != 2 || authorizer.calls[0].permission != memberservice.PermissionFingerprintManage ||
		authorizer.calls[1].permission != memberservice.PermissionFingerprintRead {
		t.Fatalf("permission checks = %+v", authorizer.calls)
	}
	stored, err := store.ListFingerprintTemplates(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("denied batch wrote templates: %+v", stored)
	}
}

func validFingerprintBatch(count int, seedStart *int64) fingerprintservice.BatchCreateInput {
	return fingerprintservice.BatchCreateInput{
		NamePrefix: "Store", Count: count, Mode: "fixed", BrowserMajor: 144,
		Platform: "windows", SeedStart: seedStart, Locale: "en-US", Timezone: "UTC",
		Configuration: fingerprintservice.Configuration{
			WindowWidth: 1440, WindowHeight: 900, HardwareConcurrency: 8,
			DeviceMemory: 8, ColorDepth: 24, ScreenWidth: 1920, ScreenHeight: 1080,
		},
	}
}

func stringInt64(value int64) string {
	return fmt.Sprintf("%d", value)
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
