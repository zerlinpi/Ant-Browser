package fingerprintruntime

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestExtensionPublicKeyIsValidPKIX(t *testing.T) {
	encoded, err := base64.StdEncoding.DecodeString(extensionPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParsePKIXPublicKey(encoded); err != nil {
		t.Fatalf("extension public key is not valid PKIX: %v", err)
	}
}

func TestWriteMV3ExtensionCreatesImmutableMainWorldArtifact(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fingerprint-runtime")
	template := validExtensionTemplate()
	template.Configuration.Fonts = append(template.Configuration.Fonts, `</script><script>alert("x")</script>`)

	artifact, err := WriteMV3Extension(root, template)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(artifact.Directory) || len(artifact.Digest) != sha256HexLength {
		t.Fatalf("unexpected artifact metadata: %+v", artifact)
	}
	if filepath.Dir(artifact.Directory) != root || filepath.Base(artifact.Directory) != "mv3-"+artifact.Digest {
		t.Fatalf("artifact escaped its content-addressed root: %+v", artifact)
	}

	manifestBytes, err := os.ReadFile(artifact.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ManifestVersion int    `json:"manifest_version"`
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
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestVersion != 3 || manifest.Key != extensionPublicKey || len(manifest.ContentScripts) != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	content := manifest.ContentScripts[0]
	if !reflect.DeepEqual(content.Matches, []string{"<all_urls>"}) || !reflect.DeepEqual(content.JS, []string{extensionScriptName}) ||
		content.RunAt != "document_start" || content.World != "MAIN" || !content.AllFrames || !content.MatchAboutBlank || !content.MatchOriginAsFallback {
		t.Fatalf("unsafe content-script registration: %+v", content)
	}

	script, err := os.ReadFile(artifact.ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"deviceMemory", "maxTouchPoints", "devicePixelRatio", "availWidth", "AudioBuffer.prototype",
		"WebGLRenderingContext.prototype", "enumerateDevices", "getBattery", "FontFaceSet.prototype",
		"CanvasRenderingContext2D.prototype", "Function.prototype, 'toString'",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("generated runtime script does not contain %q", required)
		}
	}
	if strings.Contains(text, `</script><script>alert("x")</script>`) || !strings.Contains(text, `\u003c/script\u003e`) {
		t.Fatal("template text was not safely JSON-escaped in the runtime script")
	}

	reused, err := WriteMV3Extension(root, template)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reused, artifact) {
		t.Fatalf("content-addressed artifact was not reused: got %+v want %+v", reused, artifact)
	}

	changed := validExtensionTemplate()
	changed.Configuration.DeviceMemory = 4
	changedArtifact, err := WriteMV3Extension(root, changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedArtifact.Digest == artifact.Digest || changedArtifact.Directory == artifact.Directory {
		t.Fatal("different runtime templates reused the same content address")
	}
}

func TestWriteMV3ExtensionAddressIgnoresTemplateIdentityAndVersion(t *testing.T) {
	root := t.TempDir()
	original := validExtensionTemplate()
	renamed := validExtensionTemplate()
	renamed.ID = "55555555-5555-4555-8555-555555555555"
	renamed.Version = original.Version + 12

	first, err := WriteMV3Extension(root, original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteMV3Extension(root, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("templates differing only in ID/version used different artifacts:\n%+v\n%+v", first, second)
	}
	if want := extensionArtifactDigest(mustReadFile(t, first.ManifestPath), mustReadFile(t, first.ScriptPath)); first.Digest != want {
		t.Fatalf("artifact digest %s is not the content address of its files (%s)", first.Digest, want)
	}
	script := string(mustReadFile(t, first.ScriptPath))
	for _, identity := range []string{original.ID, renamed.ID, `"id":`, `"version":`} {
		if strings.Contains(script, identity) {
			t.Errorf("runtime script leaks template identity %q", identity)
		}
	}

	reseeded := validExtensionTemplate()
	reseeded.Seed++
	reseededArtifact, err := WriteMV3Extension(root, reseeded)
	if err != nil {
		t.Fatal(err)
	}
	if reseededArtifact.Directory == first.Directory {
		t.Fatal("a different seed reused the same content address")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestWriteMV3ExtensionSkipsTemplateWithoutMainWorldOverrides(t *testing.T) {
	artifact, err := WriteMV3Extension("", ExtensionTemplate{ID: "template", Version: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if artifact != (GeneratedExtension{}) {
		t.Fatalf("empty advanced configuration generated an extension: %+v", artifact)
	}
}

func TestWriteMV3ExtensionRejectsInvalidTemplates(t *testing.T) {
	negative := -1
	tests := []struct {
		name   string
		mutate func(*ExtensionTemplate)
	}{
		{name: "ID", mutate: func(value *ExtensionTemplate) { value.ID = " " }},
		{name: "version", mutate: func(value *ExtensionTemplate) { value.Version = 0 }},
		{name: "seed", mutate: func(value *ExtensionTemplate) { value.Seed = 0 }},
		{name: "memory", mutate: func(value *ExtensionTemplate) { value.Configuration.DeviceMemory = 3 }},
		{name: "color depth", mutate: func(value *ExtensionTemplate) { value.Configuration.ColorDepth = 12 }},
		{name: "touch points", mutate: func(value *ExtensionTemplate) { value.Configuration.MaxTouchPoints = &negative }},
		{name: "tracking", mutate: func(value *ExtensionTemplate) { value.Configuration.DoNotTrack = "yes" }},
		{name: "screen pair", mutate: func(value *ExtensionTemplate) { value.Configuration.ScreenHeight = 0 }},
		{name: "scale", mutate: func(value *ExtensionTemplate) { value.Configuration.DeviceScaleFactor = math.NaN() }},
		{name: "WebGL pair", mutate: func(value *ExtensionTemplate) { value.Configuration.WebGLRenderer = "" }},
		{name: "duplicate font", mutate: func(value *ExtensionTemplate) { value.Configuration.Fonts = []string{"Inter", "inter"} }},
		{name: "media", mutate: func(value *ExtensionTemplate) { value.Configuration.MediaDevices.AudioInputs = 17 }},
		{name: "battery", mutate: func(value *ExtensionTemplate) { value.Configuration.Battery.Level = 2 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := validExtensionTemplate()
			test.mutate(&template)
			if _, err := WriteMV3Extension(t.TempDir(), template); !errors.Is(err, ErrInvalidExtensionTemplate) {
				t.Fatalf("WriteMV3Extension error = %v", err)
			}
		})
	}
}

func TestWriteMV3ExtensionFailsClosedOnCorruptArtifact(t *testing.T) {
	root := t.TempDir()
	template := validExtensionTemplate()
	artifact, err := WriteMV3Extension(root, template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.ScriptPath, []byte("malicious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMV3Extension(root, template); err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("corrupt artifact was accepted or overwritten: %v", err)
	}
	content, err := os.ReadFile(artifact.ScriptPath)
	if err != nil || string(content) != "malicious" {
		t.Fatal("corrupt artifact was silently replaced")
	}
}

func TestWriteMV3ExtensionIsSafeForConcurrentLaunches(t *testing.T) {
	root := t.TempDir()
	template := validExtensionTemplate()
	const count = 12
	artifacts := make([]GeneratedExtension, count)
	errorsByIndex := make([]error, count)
	var wait sync.WaitGroup
	for index := range artifacts {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			artifacts[index], errorsByIndex[index] = WriteMV3Extension(root, template)
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent writer %d failed: %v", index, err)
		}
		if artifacts[index] != artifacts[0] {
			t.Fatalf("concurrent writer %d returned %+v, want %+v", index, artifacts[index], artifacts[0])
		}
	}
}

func TestGeneratedRuntimeScriptParsesWithNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	artifact, err := WriteMV3Extension(t.TempDir(), validExtensionTemplate())
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "--check", artifact.ScriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check failed: %v\n%s", err, output)
	}
}

func TestInjectorRegistersClonesAndPublishesArtifacts(t *testing.T) {
	injector := NewInjector(t.TempDir())
	template := validExtensionTemplate()
	if err := injector.Register(template); err != nil {
		t.Fatal(err)
	}
	template.Configuration.Fonts[0] = "mutated"
	*template.Configuration.MaxTouchPoints = 0
	if err := injector.Inject(template.ID); err != nil {
		t.Fatal(err)
	}
	artifact, exists := injector.Artifact(template.ID)
	if !exists || artifact.Directory == "" {
		t.Fatalf("injected artifact = %+v, exists=%v", artifact, exists)
	}
	script, err := os.ReadFile(artifact.ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "mutated") || !strings.Contains(string(script), `"maxTouchPoints":5`) {
		t.Fatal("registered template was aliased by the caller")
	}
	if err := injector.Inject("missing"); !errors.Is(err, ErrTemplateNotRegistered) {
		t.Fatalf("missing template error = %v", err)
	}

	replacement := validExtensionTemplate()
	replacement.Configuration.DeviceMemory = 4
	if err := injector.Register(replacement); err != nil {
		t.Fatal(err)
	}
	if _, exists := injector.Artifact(replacement.ID); exists {
		t.Fatal("stale artifact remained published after template replacement")
	}
}

const sha256HexLength = 64

func validExtensionTemplate() ExtensionTemplate {
	touchPoints := 5
	return ExtensionTemplate{
		ID: "44444444-4444-4444-8444-444444444444", Version: 7, Seed: 424242,
		Configuration: ExtensionConfiguration{
			DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchPoints, DoNotTrack: "1",
			ScreenWidth: 2560, ScreenHeight: 1440, DeviceScaleFactor: 1.25, AudioNoise: true,
			Fonts: []string{"Inter", "Arial", "Segoe UI"}, WebGLVendor: "Google Inc. (Intel)",
			WebGLRenderer: "ANGLE (Intel, Intel Iris Xe Graphics, D3D11)",
			MediaDevices:  &MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
			Battery: &BatteryConfiguration{
				Charging: true, Level: 0.82, ChargingTimeSeconds: 900, DischargingTimeSeconds: 7200,
			},
		},
	}
}
