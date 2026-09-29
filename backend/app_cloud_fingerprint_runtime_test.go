package backend

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/config"
)

func TestValidateCloudFingerprintRuntimeCompatibility(t *testing.T) {
	hostPlatform := cloudFingerprintPlatformForGOOS(goruntime.GOOS)
	if hostPlatform == "" {
		t.Skip("unsupported test host")
	}
	valid := &cloudagent.InstanceRuntimeConfig{Fingerprint: &cloudagent.FingerprintRuntime{Platform: hostPlatform, BrowserMajor: 144}}
	if err := validateCloudFingerprintRuntimeCompatibility(valid, 144); err != nil {
		t.Fatal(err)
	}
	if err := validateCloudFingerprintRuntimeCompatibility(nil, 0); err != nil {
		t.Fatalf("runtime without a cloud template should preserve the local path: %v", err)
	}

	wrongPlatform := *valid.Fingerprint
	if hostPlatform == "windows" {
		wrongPlatform.Platform = "linux"
	} else {
		wrongPlatform.Platform = "windows"
	}
	for name, test := range map[string]struct {
		config *cloudagent.InstanceRuntimeConfig
		major  int
		want   string
	}{
		"platform":     {config: &cloudagent.InstanceRuntimeConfig{Fingerprint: &wrongPlatform}, major: 144, want: "does not match"},
		"unknown core": {config: valid, major: 0, want: "could not be verified"},
		"wrong major":  {config: valid, major: 145, want: "requires Chromium 144"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCloudFingerprintRuntimeCompatibility(test.config, test.major); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("compatibility error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareCloudFingerprintRuntimeExtensionMapsAdvancedConfiguration(t *testing.T) {
	appRoot := t.TempDir()
	app := NewApp(appRoot)
	touchPoints := 5
	runtimeConfig := cloudagent.InstanceRuntimeConfig{
		InstanceID: "33333333-3333-4333-8333-333333333333",
		Fingerprint: &cloudagent.FingerprintRuntime{
			ID: "44444444-4444-4444-8444-444444444444", Version: 7, Seed: 424242,
			Configuration: cloudagent.FingerprintConfiguration{
				DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchPoints, DoNotTrack: "1",
				ScreenWidth: 2560, ScreenHeight: 1440, DeviceScaleFactor: 1.25, AudioNoise: true,
				Fonts: []string{"Inter", "Arial"}, WebGLVendor: "Google Inc. (Intel)", WebGLRenderer: "ANGLE (Intel)",
				MediaDevices: &cloudagent.FingerprintMediaDevices{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
				Battery:      &cloudagent.FingerprintBattery{Charging: true, Level: 0.82, ChargingTimeSeconds: 900, DischargingTimeSeconds: 7200},
			},
		},
	}
	directory, err := app.prepareCloudFingerprintRuntimeExtension(browserStartInput{CloudRuntimeConfig: &runtimeConfig})
	if err != nil {
		t.Fatal(err)
	}
	// The server bumps the version on every template update, including a
	// rename. Only page-visible inputs may move the extension directory.
	revised := runtimeConfig
	revisedFingerprint := *runtimeConfig.Fingerprint
	revisedFingerprint.Version++
	revised.Fingerprint = &revisedFingerprint
	if revisedDirectory, err := app.prepareCloudFingerprintRuntimeExtension(browserStartInput{CloudRuntimeConfig: &revised}); err != nil || revisedDirectory != directory {
		t.Fatalf("version-only revision moved the extension: %q -> %q (%v)", directory, revisedDirectory, err)
	}
	wantRoot := filepath.Join(appRoot, "data", "runtime", "fingerprints")
	if filepath.Dir(directory) != wantRoot || !strings.HasPrefix(filepath.Base(directory), "mv3-") {
		t.Fatalf("extension directory = %q, want child of %q", directory, wantRoot)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"world": "MAIN"`) {
		t.Fatalf("runtime extension manifest is invalid: %v\n%s", err, manifest)
	}
	script, err := os.ReadFile(filepath.Join(directory, "runtime.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"deviceMemory":8`, `"maxTouchPoints":5`, `"audioNoise":true`, `"webglVendor":"Google Inc. (Intel)"`, `"charging":true`} {
		if !strings.Contains(string(script), expected) {
			t.Errorf("runtime script does not contain %s", expected)
		}
	}

	empty := runtimeConfig
	emptyFingerprint := *runtimeConfig.Fingerprint
	emptyFingerprint.Configuration = cloudagent.FingerprintConfiguration{}
	empty.Fingerprint = &emptyFingerprint
	directory, err = app.prepareCloudFingerprintRuntimeExtension(browserStartInput{CloudRuntimeConfig: &empty})
	if err != nil || directory != "" {
		t.Fatalf("empty advanced config generated %q, error %v", directory, err)
	}
}

func TestRemoveCloudFingerprintLaunchOverridesPreservesUnrelatedArguments(t *testing.T) {
	filtered, removed := removeCloudFingerprintLaunchOverrides([]string{
		"--lang", "zh-CN",
		"--timezone=Asia/Shanghai",
		"--fingerprint=1",
		"--fingerprinting-canvas-image-data-noise",
		"https://example.test/",
		"--user-agent=conflicting-agent",
		"--custom-switch=value",
	})
	wantFiltered := []string{"https://example.test/", "--custom-switch=value"}
	if strings.Join(filtered, "|") != strings.Join(wantFiltered, "|") {
		t.Fatalf("filtered arguments = %#v, want %#v", filtered, wantFiltered)
	}
	for _, key := range []string{"--lang", "--timezone", "--fingerprint", "--fingerprinting-canvas-image-data-noise", "--user-agent"} {
		if !containsFold(removed, key) {
			t.Errorf("removed keys %#v do not include %q", removed, key)
		}
	}
}

func TestPrepareBrowserLaunchContextUsesCloudFingerprintAsAuthority(t *testing.T) {
	appRoot := t.TempDir()
	app := NewApp(appRoot)
	app.config = &config.Config{}
	app.browserMgr = browser.NewManager(app.config, appRoot)
	coreDir := filepath.Join(appRoot, "core-144")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(coreDir, browser.CoreExecutableCandidates()[0])
	if err := os.WriteFile(executable, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "manifest.json"), []byte(`{"version":"144.0.7559.132"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app.config.Browser.Cores = []browser.Core{{CoreId: "core-144", CoreName: "Chrome 144", CorePath: coreDir, IsDefault: true}}
	profile := &browser.Profile{
		ProfileId: "local-profile", CoreId: "core-144",
		FingerprintArgs: []string{"--fingerprint=111", "--lang=zh-CN", "--timezone=Asia/Shanghai"},
		LaunchArgs:      []string{"--lang=fr-FR", "--user-agent=local-override", "--custom-switch=kept"},
	}
	app.browserMgr.Profiles[profile.ProfileId] = profile
	hostPlatform := cloudFingerprintPlatformForGOOS(goruntime.GOOS)
	if hostPlatform == "" {
		t.Skip("unsupported test host")
	}
	runtimeConfig := cloudagent.InstanceRuntimeConfig{Fingerprint: &cloudagent.FingerprintRuntime{
		ID: "44444444-4444-4444-8444-444444444444", Version: 1, Seed: 222,
		Platform: hostPlatform, BrowserMajor: 144,
		RuntimeArgs: []string{
			"--fingerprint=222", "--fingerprint-brand=Chrome", "--fingerprint-brand-version=144.0.0.0",
			"--fingerprint-platform=" + hostPlatform, "--lang=en-US", "--accept-lang=en-US,en", "--timezone=UTC",
		},
	}}
	input := newBrowserStartInput(profile.ProfileId, nil, nil, false, false, false, "", "")
	input.CloudRuntimeConfig = &runtimeConfig
	profileArgs, _, fingerprintArgs, _, _, err := app.prepareBrowserLaunchContext(input, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if browserArgValue(fingerprintArgs, "--fingerprint") != "222" || browserArgValue(fingerprintArgs, "--lang") != "en-US" {
		t.Fatalf("cloud runtime was not authoritative: %#v", fingerprintArgs)
	}
	if len(profileArgs) != 1 || profileArgs[0] != "--custom-switch=kept" {
		t.Fatalf("conflicting local launch args were not removed: %#v", profileArgs)
	}
}

func TestValidateRunningCloudRuntimeRequiresExactTemplateAndExtension(t *testing.T) {
	appRoot := t.TempDir()
	app := NewApp(appRoot)
	app.config = &config.Config{}
	app.browserMgr = browser.NewManager(app.config, appRoot)
	coreDir := filepath.Join(appRoot, "core-144")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(coreDir, browser.CoreExecutableCandidates()[0])
	if err := os.WriteFile(executable, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "manifest.json"), []byte(`{"version":"144.0.7559.132"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app.config.Browser.Cores = []browser.Core{{CoreId: "core-144", CoreName: "Chrome 144", CorePath: coreDir, IsDefault: true}}
	hostPlatform := cloudFingerprintPlatformForGOOS(goruntime.GOOS)
	if hostPlatform == "" {
		t.Skip("unsupported test host")
	}

	const profileID = "33333333-3333-4333-8333-333333333333"
	runtimeConfig := cloudagent.InstanceRuntimeConfig{
		InstanceID: profileID,
		Fingerprint: &cloudagent.FingerprintRuntime{
			ID: "44444444-4444-4444-8444-444444444444", Version: 3, Seed: 222,
			Platform: hostPlatform, BrowserMajor: 144,
			RuntimeArgs: []string{
				"--fingerprint=222", "--fingerprint-brand=Chrome", "--fingerprint-brand-version=144.0.0.0",
				"--fingerprint-platform=" + hostPlatform, "--lang=en-US", "--accept-lang=en-US,en", "--timezone=UTC",
			},
			Configuration: cloudagent.FingerprintConfiguration{DeviceMemory: 8},
		},
	}
	input := newBrowserStartInput(profileID, nil, nil, false, false, false, "", "")
	input.CloudRuntimeConfig = &runtimeConfig
	input.CloudInstanceID = runtimeConfig.InstanceID
	extensionDir, err := app.prepareCloudFingerprintRuntimeExtension(input)
	if err != nil || extensionDir == "" {
		t.Fatalf("prepare extension = %q, %v", extensionDir, err)
	}
	// Provenance is trusted only for a browser this app launched and is still
	// tracking as running.
	profile := &browser.Profile{ProfileId: profileID, CoreId: "core-144", Running: true}
	profile.LastLaunchArgs = append(profile.LastLaunchArgs, runtimeConfig.Fingerprint.RuntimeArgs...)
	profile.LastLaunchArgs = append(profile.LastLaunchArgs,
		"--load-extension="+extensionDir,
		"--disable-extensions-except="+extensionDir,
	)
	if err := app.validateRunningCloudRuntime(input, profile); err != nil {
		t.Fatalf("valid runtime rejected: %v", err)
	}

	tests := map[string]struct {
		mutate func(*browser.Profile, *browserStartInput)
		want   string
	}{
		"no provenance": {
			mutate: func(profile *browser.Profile, _ *browserStartInput) { profile.LastLaunchArgs = nil },
			want:   "provenance",
		},
		"untracked process with leftover provenance": {
			mutate: func(profile *browser.Profile, _ *browserStartInput) { profile.Running = false },
			want:   "provenance",
		},
		"duplicate managed argument": {
			mutate: func(profile *browser.Profile, _ *browserStartInput) {
				profile.LastLaunchArgs = append(profile.LastLaunchArgs, "--fingerprint=222")
			},
			want: "--fingerprint",
		},
		"stale seed": {
			mutate: func(profile *browser.Profile, _ *browserStartInput) {
				for index, value := range profile.LastLaunchArgs {
					if strings.HasPrefix(value, "--fingerprint=") {
						profile.LastLaunchArgs[index] = "--fingerprint=111"
					}
				}
			},
			want: "--fingerprint",
		},
		"missing extension": {
			mutate: func(profile *browser.Profile, _ *browserStartInput) {
				profile.LastLaunchArgs = profile.LastLaunchArgs[:len(profile.LastLaunchArgs)-2]
			},
			want: "assigned fingerprint extension",
		},
		"wrong instance": {
			mutate: func(_ *browser.Profile, input *browserStartInput) {
				input.CloudInstanceID = "55555555-5555-4555-8555-555555555555"
			},
			want: "does not match instance",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			profileCopy := *profile
			profileCopy.LastLaunchArgs = append([]string(nil), profile.LastLaunchArgs...)
			inputCopy := input
			test.mutate(&profileCopy, &inputCopy)
			if err := app.validateRunningCloudRuntime(inputCopy, &profileCopy); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	// A local profile bound under another identifier than the cloud UUID is a
	// valid binding; the runtime config only has to match the cloud instance.
	localBinding := input
	localBinding.ProfileID = "local-profile"
	if err := app.validateRunningCloudRuntime(localBinding, profile); err != nil {
		t.Fatalf("local profile binding rejected: %v", err)
	}

	withoutTemplate := input
	withoutTemplate.CloudRuntimeConfig = &cloudagent.InstanceRuntimeConfig{InstanceID: profileID}
	if err := app.validateRunningCloudRuntime(withoutTemplate, profile); err == nil || !strings.Contains(err.Error(), "still contains") {
		t.Fatalf("removed template reuse error = %v", err)
	}

	// A rename bumps the template version without changing page-visible
	// behavior; the running browser must still be accepted for idempotent start.
	renamedConfig := runtimeConfig
	renamedFingerprint := *runtimeConfig.Fingerprint
	renamedFingerprint.Version++
	renamedConfig.Fingerprint = &renamedFingerprint
	renamed := input
	renamed.CloudRuntimeConfig = &renamedConfig
	if err := app.validateRunningCloudRuntime(renamed, profile); err != nil {
		t.Fatalf("metadata-only template revision rejected the running browser: %v", err)
	}
	changedConfig := renamedConfig
	changedFingerprint := renamedFingerprint
	changedFingerprint.Configuration.DeviceMemory = 4
	changedConfig.Fingerprint = &changedFingerprint
	changed := input
	changed.CloudRuntimeConfig = &changedConfig
	if err := app.validateRunningCloudRuntime(changed, profile); err == nil || !strings.Contains(err.Error(), "assigned fingerprint extension") {
		t.Fatalf("page-visible template change accepted the running browser: %v", err)
	}
}

func TestCloudRuntimeStartRejectsMismatchedInstanceIdentity(t *testing.T) {
	app := NewApp(t.TempDir())
	if _, err := app.browserInstanceStartWithCloudRuntimeContext(
		context.Background(),
		"33333333-3333-4333-8333-333333333333", "local-profile",
		cloudagent.InstanceRuntimeConfig{InstanceID: "44444444-4444-4444-8444-444444444444"},
	); err == nil || !strings.Contains(err.Error(), "does not match instance") {
		t.Fatalf("identity error = %v", err)
	}
	if _, err := app.browserInstanceStartWithCloudRuntimeContext(
		context.Background(),
		"33333333-3333-4333-8333-333333333333", "",
		cloudagent.InstanceRuntimeConfig{InstanceID: "33333333-3333-4333-8333-333333333333"},
	); err == nil || !strings.Contains(err.Error(), "no local profile binding") {
		t.Fatalf("missing binding error = %v", err)
	}
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
