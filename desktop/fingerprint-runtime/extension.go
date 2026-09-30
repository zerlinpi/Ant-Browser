package fingerprintruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// extensionSchemaVersion namespaces the content address. Version 2 hashes
	// the generated files instead of the template record.
	extensionSchemaVersion = 2
	extensionManifestName  = "manifest.json"
	extensionScriptName    = "runtime.js"

	// The key gives the built-in unpacked extension one stable Chromium ID even
	// though its content-addressed directory changes when a template changes.
	// It is a public key, not a credential.
	extensionPublicKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAtxuZPuMlWnq8SDSn5Jej1kmmCO+xh3ZrH62IeFoXL7DYcTrnW5YG8mFEW87CSl/6CtFVg4xlfnhfzLckuUXqakOh2XdiHODR1hveuS7PAiqjfkDjeRjvgzrKCPbzrWG8qjX0gVrP3ss2OWYB9qRWzF4fZy7//lVIa55j0Yd7f9TwUryP+5RatesELXGmtdo0iKCKSg/gz8aimdNolkR3Et2Tnx3W08cYnHOtlzleT3SA/COSEUimdbtg9MXkUVSmiKkMb54kln+dHdWh3j/uRRstPAPN5L5Qum7a82Yed4/vyanE5pS+bNXji3FVryFZVpZOsRMdbzFHMGZCxFfO8wIDAQAB"
)

var ErrInvalidExtensionTemplate = errors.New("invalid fingerprint runtime extension template")

// ExtensionTemplate contains the fingerprint fields that Chromium 144 does
// not reliably implement through command-line switches. Process-level fields
// such as locale, timezone, window size, WebRTC policy and the fingerprint
// seed remain launcher arguments and deliberately do not belong here; Seed
// only derives page-level values such as audio noise and media device IDs.
//
// ID and Version identify the template record for validation, registries and
// logging only. They never reach the generated files or the content address,
// so metadata-only revisions such as a rename reuse the same artifact.
type ExtensionTemplate struct {
	ID            string                 `json:"id"`
	Version       int64                  `json:"version"`
	Seed          int64                  `json:"seed,string"`
	Configuration ExtensionConfiguration `json:"configuration"`
}

type ExtensionConfiguration struct {
	DeviceMemory      int                        `json:"deviceMemory,omitempty"`
	ColorDepth        int                        `json:"colorDepth,omitempty"`
	MaxTouchPoints    *int                       `json:"maxTouchPoints,omitempty"`
	DoNotTrack        string                     `json:"doNotTrack,omitempty"`
	ScreenWidth       int                        `json:"screenWidth,omitempty"`
	ScreenHeight      int                        `json:"screenHeight,omitempty"`
	DeviceScaleFactor float64                    `json:"deviceScaleFactor,omitempty"`
	AudioNoise        bool                       `json:"audioNoise"`
	Fonts             []string                   `json:"fonts,omitempty"`
	WebGLVendor       string                     `json:"webglVendor,omitempty"`
	WebGLRenderer     string                     `json:"webglRenderer,omitempty"`
	MediaDevices      *MediaDevicesConfiguration `json:"mediaDevices,omitempty"`
	Battery           *BatteryConfiguration      `json:"battery,omitempty"`
}

type MediaDevicesConfiguration struct {
	AudioInputs  int `json:"audioInputs"`
	VideoInputs  int `json:"videoInputs"`
	AudioOutputs int `json:"audioOutputs"`
}

type BatteryConfiguration struct {
	Charging               bool    `json:"charging"`
	Level                  float64 `json:"level"`
	ChargingTimeSeconds    int64   `json:"chargingTimeSeconds"`
	DischargingTimeSeconds int64   `json:"dischargingTimeSeconds"`
}

// GeneratedExtension is an immutable unpacked-extension artifact. An empty
// Directory means that the template has no main-world overrides and callers
// should not add an extension launch argument. Digest is the content address
// of the generated manifest and script and names the Directory.
type GeneratedExtension struct {
	Directory    string
	Digest       string
	ManifestPath string
	ScriptPath   string
}

// WriteMV3Extension materializes a content-addressed MV3 extension below root.
// Existing artifacts are verified before reuse and are never modified in
// place, so a running Chromium process cannot observe a partial update.
func WriteMV3Extension(root string, template ExtensionTemplate) (GeneratedExtension, error) {
	if err := validateExtensionTemplate(template); err != nil {
		return GeneratedExtension{}, err
	}
	if !hasMainWorldOverrides(template.Configuration) {
		return GeneratedExtension{}, nil
	}

	root = strings.TrimSpace(root)
	if root == "" {
		return GeneratedExtension{}, fmt.Errorf("%w: output root is required", ErrInvalidExtensionTemplate)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return GeneratedExtension{}, fmt.Errorf("resolve fingerprint runtime extension root: %w", err)
	}
	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return GeneratedExtension{}, fmt.Errorf("create fingerprint runtime extension root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return GeneratedExtension{}, fmt.Errorf("fingerprint runtime extension root is not a secure directory")
	}

	manifest, err := buildExtensionManifest()
	if err != nil {
		return GeneratedExtension{}, err
	}
	script, err := buildRuntimeScript(template)
	if err != nil {
		return GeneratedExtension{}, err
	}
	digest := extensionArtifactDigest(manifest, script)
	directory := filepath.Join(absoluteRoot, "mv3-"+digest)
	artifact := GeneratedExtension{
		Directory:    directory,
		Digest:       digest,
		ManifestPath: filepath.Join(directory, extensionManifestName),
		ScriptPath:   filepath.Join(directory, extensionScriptName),
	}
	if matchesExtensionArtifact(artifact, manifest, script) {
		return artifact, nil
	}
	if _, err := os.Lstat(directory); err == nil {
		if waitForMatchingExtensionArtifact(artifact, manifest, script) {
			return artifact, nil
		}
		return GeneratedExtension{}, fmt.Errorf("fingerprint runtime extension artifact %s is inconsistent", digest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return GeneratedExtension{}, fmt.Errorf("inspect fingerprint runtime extension artifact: %w", err)
	}

	temporary, err := os.MkdirTemp(absoluteRoot, ".mv3-tmp-")
	if err != nil {
		return GeneratedExtension{}, fmt.Errorf("create temporary fingerprint runtime extension: %w", err)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.WriteFile(filepath.Join(temporary, extensionManifestName), manifest, 0o600); err != nil {
		return GeneratedExtension{}, fmt.Errorf("write fingerprint runtime extension manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(temporary, extensionScriptName), script, 0o600); err != nil {
		return GeneratedExtension{}, fmt.Errorf("write fingerprint runtime extension script: %w", err)
	}
	if err := os.Rename(temporary, directory); err != nil {
		// Another concurrent launcher may have won the content-addressed write.
		// Reuse it only when both files are byte-for-byte identical.
		if waitForMatchingExtensionArtifact(artifact, manifest, script) {
			return artifact, nil
		}
		return GeneratedExtension{}, fmt.Errorf("publish fingerprint runtime extension: %w", err)
	}
	keepTemporary = true
	return artifact, nil
}

func buildExtensionManifest() ([]byte, error) {
	manifest := struct {
		ManifestVersion int    `json:"manifest_version"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		Description     string `json:"description"`
		Key             string `json:"key"`
		ContentScripts  []struct {
			Matches               []string `json:"matches"`
			JS                    []string `json:"js"`
			RunAt                 string   `json:"run_at"`
			World                 string   `json:"world"`
			AllFrames             bool     `json:"all_frames"`
			MatchAboutBlank       bool     `json:"match_about_blank"`
			MatchOriginAsFallback bool     `json:"match_origin_as_fallback"`
		} `json:"content_scripts"`
	}{
		ManifestVersion: 3,
		Name:            "Ant Browser Fingerprint Runtime",
		Version:         "1.0.0",
		Description:     "Applies the profile fingerprint before page scripts run.",
		Key:             extensionPublicKey,
	}
	manifest.ContentScripts = append(manifest.ContentScripts, struct {
		Matches               []string `json:"matches"`
		JS                    []string `json:"js"`
		RunAt                 string   `json:"run_at"`
		World                 string   `json:"world"`
		AllFrames             bool     `json:"all_frames"`
		MatchAboutBlank       bool     `json:"match_about_blank"`
		MatchOriginAsFallback bool     `json:"match_origin_as_fallback"`
	}{
		Matches:               []string{"<all_urls>"},
		JS:                    []string{extensionScriptName},
		RunAt:                 "document_start",
		World:                 "MAIN",
		AllFrames:             true,
		MatchAboutBlank:       true,
		MatchOriginAsFallback: true,
	})
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode fingerprint runtime extension manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// extensionArtifactDigest content-addresses exactly the bytes Chromium loads:
// the constant manifest and the script derived from the seed and page-level
// configuration. Template identity and revision numbers are not inputs, so
// two templates with the same page-visible behavior share one directory, and
// any change to the generated code or configuration moves to a new one.
func extensionArtifactDigest(manifest, script []byte) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "ant-browser-fingerprint-runtime-extension/v%d\n", extensionSchemaVersion)
	for _, file := range []struct {
		name    string
		content []byte
	}{{name: extensionManifestName, content: manifest}, {name: extensionScriptName, content: script}} {
		fmt.Fprintf(hash, "%s\n%d\n", file.name, len(file.content))
		hash.Write(file.content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// buildRuntimeScript embeds only the seed and page-level configuration; the
// template ID and version must never influence page-visible behavior.
func buildRuntimeScript(template ExtensionTemplate) ([]byte, error) {
	configuration := struct {
		Seed string `json:"seed"`
		ExtensionConfiguration
	}{
		Seed:                   fmt.Sprintf("%d", template.Seed),
		ExtensionConfiguration: template.Configuration,
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return nil, fmt.Errorf("encode fingerprint runtime script configuration: %w", err)
	}
	var script bytes.Buffer
	script.Grow(len(encoded) + len(runtimeScriptPrefix) + len(runtimeScriptSuffix))
	script.WriteString(runtimeScriptPrefix)
	script.Write(encoded)
	script.WriteString(runtimeScriptSuffix)
	return script.Bytes(), nil
}

func matchesExtensionArtifact(artifact GeneratedExtension, manifest, script []byte) bool {
	directoryInfo, err := os.Lstat(artifact.Directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	manifestInfo, err := os.Lstat(artifact.ManifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	scriptInfo, err := os.Lstat(artifact.ScriptPath)
	if err != nil || !scriptInfo.Mode().IsRegular() || scriptInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	actualManifest, err := os.ReadFile(artifact.ManifestPath)
	if err != nil || !bytes.Equal(actualManifest, manifest) {
		return false
	}
	actualScript, err := os.ReadFile(artifact.ScriptPath)
	return err == nil && bytes.Equal(actualScript, script)
}

func waitForMatchingExtensionArtifact(artifact GeneratedExtension, manifest, script []byte) bool {
	for attempt := 0; attempt < 20; attempt++ {
		if matchesExtensionArtifact(artifact, manifest, script) {
			return true
		}
		if attempt < 19 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	return false
}

func hasMainWorldOverrides(configuration ExtensionConfiguration) bool {
	return configuration.DeviceMemory != 0 || configuration.ColorDepth != 0 || configuration.MaxTouchPoints != nil ||
		configuration.DoNotTrack != "" || configuration.ScreenWidth != 0 || configuration.ScreenHeight != 0 ||
		configuration.DeviceScaleFactor != 0 || configuration.AudioNoise || len(configuration.Fonts) != 0 ||
		configuration.WebGLVendor != "" || configuration.WebGLRenderer != "" || configuration.MediaDevices != nil ||
		configuration.Battery != nil
}

func validateExtensionTemplate(template ExtensionTemplate) error {
	if strings.TrimSpace(template.ID) == "" || strings.TrimSpace(template.ID) != template.ID || len(template.ID) > 128 || hasControlCharacters(template.ID) {
		return fmt.Errorf("%w: template ID is invalid", ErrInvalidExtensionTemplate)
	}
	if template.Version < 1 || template.Seed < 1 {
		return fmt.Errorf("%w: template version and seed must be positive", ErrInvalidExtensionTemplate)
	}
	configuration := template.Configuration
	switch configuration.DeviceMemory {
	case 0, 1, 2, 4, 8:
	default:
		return fmt.Errorf("%w: device memory is invalid", ErrInvalidExtensionTemplate)
	}
	switch configuration.ColorDepth {
	case 0, 16, 24, 30, 32:
	default:
		return fmt.Errorf("%w: color depth is invalid", ErrInvalidExtensionTemplate)
	}
	if configuration.MaxTouchPoints != nil && (*configuration.MaxTouchPoints < 0 || *configuration.MaxTouchPoints > 20) {
		return fmt.Errorf("%w: max touch points is invalid", ErrInvalidExtensionTemplate)
	}
	switch configuration.DoNotTrack {
	case "", "0", "1", "unspecified":
	default:
		return fmt.Errorf("%w: do-not-track value is invalid", ErrInvalidExtensionTemplate)
	}
	if (configuration.ScreenWidth == 0) != (configuration.ScreenHeight == 0) ||
		(configuration.ScreenWidth != 0 && (configuration.ScreenWidth < 800 || configuration.ScreenWidth > 15360 || configuration.ScreenHeight < 600 || configuration.ScreenHeight > 8640)) {
		return fmt.Errorf("%w: screen dimensions are invalid", ErrInvalidExtensionTemplate)
	}
	if math.IsNaN(configuration.DeviceScaleFactor) || math.IsInf(configuration.DeviceScaleFactor, 0) ||
		(configuration.DeviceScaleFactor != 0 && (configuration.DeviceScaleFactor < 0.5 || configuration.DeviceScaleFactor > 4)) {
		return fmt.Errorf("%w: device scale factor is invalid", ErrInvalidExtensionTemplate)
	}
	if (configuration.WebGLVendor == "") != (configuration.WebGLRenderer == "") || len(configuration.WebGLVendor) > 160 || len(configuration.WebGLRenderer) > 256 ||
		strings.TrimSpace(configuration.WebGLVendor) != configuration.WebGLVendor || strings.TrimSpace(configuration.WebGLRenderer) != configuration.WebGLRenderer ||
		hasControlCharacters(configuration.WebGLVendor) || hasControlCharacters(configuration.WebGLRenderer) {
		return fmt.Errorf("%w: WebGL identity is invalid", ErrInvalidExtensionTemplate)
	}
	if len(configuration.Fonts) > 128 {
		return fmt.Errorf("%w: font list is too large", ErrInvalidExtensionTemplate)
	}
	seenFonts := make(map[string]struct{}, len(configuration.Fonts))
	for _, font := range configuration.Fonts {
		key := strings.ToLower(font)
		if strings.TrimSpace(font) != font || font == "" || len([]rune(font)) > 100 || hasControlCharacters(font) {
			return fmt.Errorf("%w: font name is invalid", ErrInvalidExtensionTemplate)
		}
		if _, exists := seenFonts[key]; exists {
			return fmt.Errorf("%w: font list contains duplicates", ErrInvalidExtensionTemplate)
		}
		seenFonts[key] = struct{}{}
	}
	if configuration.MediaDevices != nil {
		media := configuration.MediaDevices
		if media.AudioInputs < 0 || media.AudioInputs > 16 || media.VideoInputs < 0 || media.VideoInputs > 16 ||
			media.AudioOutputs < 0 || media.AudioOutputs > 16 || media.AudioInputs+media.VideoInputs+media.AudioOutputs > 32 {
			return fmt.Errorf("%w: media device counts are invalid", ErrInvalidExtensionTemplate)
		}
	}
	if configuration.Battery != nil {
		battery := configuration.Battery
		if math.IsNaN(battery.Level) || math.IsInf(battery.Level, 0) || battery.Level < 0 || battery.Level > 1 ||
			battery.ChargingTimeSeconds < 0 || battery.DischargingTimeSeconds < 0 ||
			battery.ChargingTimeSeconds > 365*24*60*60 || battery.DischargingTimeSeconds > 365*24*60*60 {
			return fmt.Errorf("%w: battery configuration is invalid", ErrInvalidExtensionTemplate)
		}
	}
	return nil
}

func hasControlCharacters(value string) bool {
	return strings.IndexFunc(value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0
}
