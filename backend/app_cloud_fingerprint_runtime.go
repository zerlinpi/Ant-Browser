package backend

import (
	"errors"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"ant-chrome/backend/internal/cloudagent"
	"ant-chrome/backend/internal/logger"
	fingerprintruntime "ant-chrome/desktop/fingerprint-runtime"
)

func validateCloudFingerprintRuntimeCompatibility(runtimeConfig *cloudagent.InstanceRuntimeConfig, chromeMajor int) error {
	if runtimeConfig == nil || runtimeConfig.Fingerprint == nil {
		return nil
	}
	fingerprint := runtimeConfig.Fingerprint
	hostPlatform := cloudFingerprintPlatformForGOOS(goruntime.GOOS)
	if hostPlatform == "" {
		return fmt.Errorf("host platform %q is unsupported", goruntime.GOOS)
	}
	if fingerprint.Platform != hostPlatform {
		return fmt.Errorf("template platform %q does not match host platform %q", fingerprint.Platform, hostPlatform)
	}
	if chromeMajor <= 0 {
		return fmt.Errorf("the selected Chromium core version could not be verified")
	}
	if fingerprint.BrowserMajor != chromeMajor {
		return fmt.Errorf("template requires Chromium %d but the selected core is Chromium %d", fingerprint.BrowserMajor, chromeMajor)
	}
	return nil
}

func cloudFingerprintPlatformForGOOS(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	default:
		return ""
	}
}

func (a *App) prepareCloudFingerprintRuntimeExtension(input browserStartInput) (string, error) {
	if input.CloudRuntimeConfig == nil || input.CloudRuntimeConfig.Fingerprint == nil {
		return "", nil
	}
	fingerprint := input.CloudRuntimeConfig.Fingerprint
	configuration := fingerprint.Configuration
	// ID and Version identify the template for validation only. The extension
	// is content-addressed by its generated files (seed + page-level
	// configuration), so metadata-only revisions such as a rename keep the
	// directory that a running browser was launched with.
	template := fingerprintruntime.ExtensionTemplate{
		ID:      fingerprint.ID,
		Version: fingerprint.Version,
		Seed:    fingerprint.Seed,
		Configuration: fingerprintruntime.ExtensionConfiguration{
			DeviceMemory:      configuration.DeviceMemory,
			ColorDepth:        configuration.ColorDepth,
			DoNotTrack:        configuration.DoNotTrack,
			ScreenWidth:       configuration.ScreenWidth,
			ScreenHeight:      configuration.ScreenHeight,
			DeviceScaleFactor: configuration.DeviceScaleFactor,
			AudioNoise:        configuration.AudioNoise,
			Fonts:             append([]string(nil), configuration.Fonts...),
			WebGLVendor:       configuration.WebGLVendor,
			WebGLRenderer:     configuration.WebGLRenderer,
		},
	}
	if configuration.MaxTouchPoints != nil {
		value := *configuration.MaxTouchPoints
		template.Configuration.MaxTouchPoints = &value
	}
	if configuration.MediaDevices != nil {
		template.Configuration.MediaDevices = &fingerprintruntime.MediaDevicesConfiguration{
			AudioInputs:  configuration.MediaDevices.AudioInputs,
			VideoInputs:  configuration.MediaDevices.VideoInputs,
			AudioOutputs: configuration.MediaDevices.AudioOutputs,
		}
	}
	if configuration.Battery != nil {
		template.Configuration.Battery = &fingerprintruntime.BatteryConfiguration{
			Charging:               configuration.Battery.Charging,
			Level:                  configuration.Battery.Level,
			ChargingTimeSeconds:    configuration.Battery.ChargingTimeSeconds,
			DischargingTimeSeconds: configuration.Battery.DischargingTimeSeconds,
		}
	}
	artifact, err := fingerprintruntime.WriteMV3Extension(
		a.cloudFingerprintRuntimeRoot(),
		template,
	)
	if err != nil {
		return "", fmt.Errorf("prepare cloud fingerprint extension: %w", err)
	}
	return artifact.Directory, nil
}

func (a *App) cloudFingerprintRuntimeRoot() string {
	return a.resolveAppPath(filepath.Join("data", "runtime", "fingerprints"))
}

// validateRunningCloudRuntime prevents an idempotent cloud start from silently
// reusing or adopting a browser that was not launched for the assigned
// template. Launch provenance (LastLaunchArgs) is recorded only after this app
// has started Chromium and is tracking that process; it is cleared when the
// profile is marked stopped, when the browser crashes or its detached process
// disappears, when a launch fails, and when a discovered process is attached.
// It is therefore trusted only while the profile is tracked as running. A
// process that is adopted or otherwise untracked has no provenance and fails
// closed when a cloud fingerprint is assigned.
func (a *App) validateRunningCloudRuntime(input browserStartInput, profile *BrowserProfile) error {
	if input.CloudRuntimeConfig == nil {
		return nil
	}
	if input.CloudRuntimeConfig.InstanceID != input.CloudInstanceID {
		return fmt.Errorf("cloud runtime configuration for %q does not match instance %q", input.CloudRuntimeConfig.InstanceID, input.CloudInstanceID)
	}
	if profile == nil {
		return errors.New("cloud runtime profile is unavailable")
	}

	var lastLaunchArgs []string
	if profile.Running {
		// Arguments left on a profile this app is not tracking (for example
		// imported with a profile package) describe no live process.
		lastLaunchArgs = normalizeNonEmptyStrings(profile.LastLaunchArgs)
	}
	fingerprint := input.CloudRuntimeConfig.Fingerprint
	if fingerprint == nil {
		if cloudRuntimeArgsContainManagedExtension(lastLaunchArgs, a.cloudFingerprintRuntimeRoot()) {
			return errors.New("running browser still contains a cloud fingerprint extension")
		}
		return nil
	}
	if len(lastLaunchArgs) == 0 {
		return errors.New("running browser launch provenance is unavailable")
	}

	report := a.buildBrowserFingerprintCapabilityReport(input.ProfileID, profile.CoreId, fingerprint.RuntimeArgs)
	if err := validateCloudFingerprintRuntimeCompatibility(input.CloudRuntimeConfig, report.ChromeMajor); err != nil {
		return err
	}
	expectedRuntimeArgs := normalizeNonEmptyStrings(report.LaunchArgs)
	expectedKeys := make(map[string]struct{}, len(expectedRuntimeArgs))
	for _, expected := range expectedRuntimeArgs {
		key := browserFingerprintArgKey(expected)
		expectedKeys[key] = struct{}{}
		if cloudRuntimeExactArgCount(lastLaunchArgs, expected) != 1 {
			return fmt.Errorf("running browser does not match runtime argument %s", key)
		}
	}
	for _, actual := range lastLaunchArgs {
		key := browserFingerprintArgKey(actual)
		managed, _ := cloudFingerprintManagedLaunchArgument(key)
		if !managed {
			continue
		}
		if _, expected := expectedKeys[key]; !expected {
			return fmt.Errorf("running browser contains unexpected runtime argument %s", key)
		}
	}

	expectedExtension, err := a.prepareCloudFingerprintRuntimeExtension(input)
	if err != nil {
		return err
	}
	for _, key := range []string{"--load-extension", "--disable-extensions-except"} {
		paths := cloudRuntimeExtensionPaths(lastLaunchArgs, key)
		if expectedExtension != "" && !cloudRuntimePathsContain(paths, expectedExtension) {
			return fmt.Errorf("running browser does not contain the assigned fingerprint extension in %s", key)
		}
		for _, path := range paths {
			if cloudRuntimePathWithin(path, a.cloudFingerprintRuntimeRoot()) &&
				(expectedExtension == "" || !cloudRuntimePathsEqual(path, expectedExtension)) {
				return fmt.Errorf("running browser contains a stale fingerprint extension in %s", key)
			}
		}
	}
	return nil
}

func cloudRuntimeExactArgCount(arguments []string, expected string) int {
	expected = strings.TrimSpace(expected)
	expectedKey := browserFingerprintArgKey(expected)
	expectedValue, expectsValue := "", false
	if index := strings.IndexByte(expected, '='); index >= 0 {
		expectedValue, expectsValue = expected[index+1:], true
	}
	count := 0
	for _, argument := range arguments {
		argument = strings.TrimSpace(argument)
		if browserFingerprintArgKey(argument) != expectedKey {
			continue
		}
		value, hasValue := "", false
		if index := strings.IndexByte(argument, '='); index >= 0 {
			value, hasValue = argument[index+1:], true
		}
		if hasValue == expectsValue && (!expectsValue || value == expectedValue) {
			count++
		}
	}
	return count
}

func cloudRuntimeExtensionPaths(arguments []string, key string) []string {
	var paths []string
	for _, argument := range arguments {
		if browserFingerprintArgKey(argument) != key {
			continue
		}
		index := strings.IndexByte(argument, '=')
		if index < 0 {
			continue
		}
		for _, path := range strings.Split(argument[index+1:], ",") {
			if path = strings.TrimSpace(path); path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func cloudRuntimeArgsContainManagedExtension(arguments []string, root string) bool {
	for _, key := range []string{"--load-extension", "--disable-extensions-except"} {
		for _, path := range cloudRuntimeExtensionPaths(arguments, key) {
			if cloudRuntimePathWithin(path, root) {
				return true
			}
		}
	}
	return false
}

func cloudRuntimePathsContain(paths []string, expected string) bool {
	for _, path := range paths {
		if cloudRuntimePathsEqual(path, expected) {
			return true
		}
	}
	return false
}

func cloudRuntimePathsEqual(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func cloudRuntimePathWithin(path, root string) bool {
	path, pathErr := filepath.Abs(strings.TrimSpace(path))
	root, rootErr := filepath.Abs(strings.TrimSpace(root))
	if pathErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." {
		return false
	}
	if relative == "." {
		return true
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func appendUniqueExtensionDir(directories []string, addition string) []string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return directories
	}
	cleanAddition := filepath.Clean(addition)
	for _, directory := range directories {
		cleanDirectory := filepath.Clean(strings.TrimSpace(directory))
		if cleanDirectory == cleanAddition || (goruntime.GOOS == "windows" && strings.EqualFold(cleanDirectory, cleanAddition)) {
			return directories
		}
	}
	return append(directories, addition)
}

func removeCloudFingerprintLaunchOverrides(arguments []string) ([]string, []string) {
	if len(arguments) == 0 {
		return nil, nil
	}
	filtered := make([]string, 0, len(arguments))
	removed := make([]string, 0, 8)
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		key := browserFingerprintArgKey(argument)
		managed, takesValue := cloudFingerprintManagedLaunchArgument(key)
		if !managed {
			if argument != "" {
				filtered = append(filtered, argument)
			}
			continue
		}
		removed = appendUniqueString(removed, key)
		if takesValue && !strings.Contains(argument, "=") && index+1 < len(arguments) {
			next := strings.TrimSpace(arguments[index+1])
			if next != "" && !strings.HasPrefix(next, "-") {
				index++
			}
		}
	}
	return filtered, removed
}

func cloudFingerprintManagedLaunchArgument(key string) (managed bool, takesValue bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "--fingerprinting-canvas-image-data-noise", "--fingerprinting-client-rects-noise", "--disable-non-proxied-udp", "--disable-gpu-fingerprint":
		return true, false
	case "--lang", "--accept-lang", "--timezone", "--window-size", "--webrtc-ip-handling-policy", "--disable-spoofing",
		"--user-agent", "--user-agent-product", "--user-agent-metadata", "--use-mobile-user-agent", "--force-device-scale-factor":
		return true, true
	default:
		if strings.HasPrefix(key, "--fingerprint-") || key == "--fingerprint" {
			return true, true
		}
		return false, false
	}
}

func logCloudFingerprintLaunchOverrides(log *logger.Logger, profileID, source string, removed []string) {
	if log == nil || len(removed) == 0 {
		return
	}
	log.Warn("cloud fingerprint template ignored conflicting local launch arguments",
		logger.F("profile_id", profileID),
		logger.F("source", source),
		logger.F("managed_args", removed),
	)
}
