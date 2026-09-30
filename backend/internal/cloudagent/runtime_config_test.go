package cloudagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	runtimeTestWorkspaceID = "11111111-1111-4111-8111-111111111111"
	runtimeTestDeviceID    = "22222222-2222-4222-8222-222222222222"
	runtimeTestInstanceID  = "33333333-3333-4333-8333-333333333333"
	runtimeTestTemplateID  = "44444444-4444-4444-8444-444444444444"
	runtimeTestCredential  = "test-device-credential"
)

func TestNewRuntimeConfigClientRequiresHTTPSOriginAndDeviceIdentity(t *testing.T) {
	t.Parallel()
	validClient := &http.Client{Timeout: 2 * time.Minute}
	client, err := NewRuntimeConfigClient(
		" https://cloud.example.test/ ",
		strings.ToUpper(runtimeTestWorkspaceID),
		strings.ToUpper(runtimeTestDeviceID),
		"  "+runtimeTestCredential+"  ",
		validClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != "https://cloud.example.test" || client.workspaceID != runtimeTestWorkspaceID || client.deviceID != runtimeTestDeviceID || client.credential != runtimeTestCredential {
		t.Fatalf("runtime client identity was not normalized: %#v", client)
	}
	if client.http == validClient || client.http.Timeout != 15*time.Second || validClient.Timeout != 2*time.Minute || validClient.CheckRedirect != nil {
		t.Fatal("runtime client did not safely clone and constrain the supplied HTTP client")
	}

	tests := []struct {
		name        string
		baseURL     string
		workspaceID string
		deviceID    string
		credential  string
	}{
		{name: "HTTP endpoint", baseURL: "http://cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: runtimeTestCredential},
		{name: "endpoint path", baseURL: "https://cloud.example.test/api", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: runtimeTestCredential},
		{name: "endpoint credentials", baseURL: "https://user:secret@cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: runtimeTestCredential},
		{name: "endpoint query", baseURL: "https://cloud.example.test?redirect=evil", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: runtimeTestCredential},
		{name: "invalid workspace", baseURL: "https://cloud.example.test", workspaceID: "not-a-uuid", deviceID: runtimeTestDeviceID, credential: runtimeTestCredential},
		{name: "invalid device", baseURL: "https://cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: "not-a-uuid", credential: runtimeTestCredential},
		{name: "empty credential", baseURL: "https://cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: "  "},
		{name: "credential newline", baseURL: "https://cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: "secret\nforwarded"},
		{name: "credential too long", baseURL: "https://cloud.example.test", workspaceID: runtimeTestWorkspaceID, deviceID: runtimeTestDeviceID, credential: strings.Repeat("x", 513)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewRuntimeConfigClient(test.baseURL, test.workspaceID, test.deviceID, test.credential, nil); err == nil {
				t.Fatal("invalid runtime configuration client was accepted")
			}
		})
	}
}

func TestRuntimeConfigClientResolvesAdvancedTemplateWithDeviceAuthentication(t *testing.T) {
	t.Parallel()
	wantFingerprint := validRuntimeFingerprint()
	body := marshalRuntimeConfigResponse(t, runtimeTestInstanceID, wantFingerprint)
	client := runtimeClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		wantPath := "/api/v1/agent/instances/" + runtimeTestInstanceID + "/runtime-config"
		if r.URL.Path != wantPath || r.URL.RawQuery != "" {
			t.Errorf("request target = %q, want %q", r.URL.RequestURI(), wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Device "+runtimeTestCredential {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Device-ID"); got != runtimeTestDeviceID {
			t.Errorf("X-Device-ID = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	got, err := client.Resolve(context.Background(), runtimeTestInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	want := InstanceRuntimeConfig{InstanceID: runtimeTestInstanceID, Fingerprint: wantFingerprint}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved runtime config = %#v, want %#v", got, want)
	}
}

func TestRuntimeConfigClientResolvesInstanceWithoutTemplate(t *testing.T) {
	t.Parallel()
	body := marshalRuntimeConfigResponse(t, runtimeTestInstanceID, nil)
	client := runtimeClientForBody(t, http.StatusOK, body)

	got, err := client.Resolve(context.Background(), runtimeTestInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != runtimeTestInstanceID || got.Fingerprint != nil {
		t.Fatalf("resolved runtime config = %#v", got)
	}
}

func TestRuntimeConfigClientRejectsInstanceAndWorkspaceMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    func(*testing.T) []byte
		wantErr string
	}{
		{
			name: "instance",
			body: func(t *testing.T) []byte {
				return marshalRuntimeConfigResponse(t, "55555555-5555-4555-8555-555555555555", nil)
			},
			wantErr: "identity does not match",
		},
		{
			name: "workspace",
			body: func(t *testing.T) []byte {
				fingerprint := validRuntimeFingerprint()
				fingerprint.WorkspaceID = "55555555-5555-4555-8555-555555555555"
				return marshalRuntimeConfigResponse(t, runtimeTestInstanceID, fingerprint)
			},
			wantErr: "fingerprint identity is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := runtimeClientForBody(t, http.StatusOK, test.body(t))
			assertRuntimeResolveError(t, client, test.wantErr)
		})
	}
}

func TestRuntimeConfigClientRejectsUnsafeAndDuplicateArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*FingerprintRuntime)
		wantErr string
	}{
		{
			name: "proxy override",
			mutate: func(fingerprint *FingerprintRuntime) {
				fingerprint.RuntimeArgs = append(fingerprint.RuntimeArgs, "--proxy-server=http://attacker.invalid")
			},
			wantErr: "argument is not allowed",
		},
		{
			name: "profile directory override",
			mutate: func(fingerprint *FingerprintRuntime) {
				fingerprint.RuntimeArgs = append(fingerprint.RuntimeArgs, "--user-data-dir=C:/stolen-profile")
			},
			wantErr: "argument is not allowed",
		},
		{
			name: "control character",
			mutate: func(fingerprint *FingerprintRuntime) {
				fingerprint.RuntimeArgs[0] = "--fingerprint=424242\n--proxy-server=attacker.invalid"
			},
			wantErr: "argument is invalid",
		},
		{
			name: "duplicate",
			mutate: func(fingerprint *FingerprintRuntime) {
				fingerprint.RuntimeArgs = append(fingerprint.RuntimeArgs, "--lang=en-US")
			},
			wantErr: "arguments contain duplicates",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fingerprint := validRuntimeFingerprint()
			test.mutate(fingerprint)
			client := runtimeClientForBody(t, http.StatusOK, marshalRuntimeConfigResponse(t, runtimeTestInstanceID, fingerprint))
			assertRuntimeResolveError(t, client, test.wantErr)
		})
	}
}

func TestRuntimeConfigClientRejectsInvalidAdvancedConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*FingerprintConfiguration)
		wantErr string
	}{
		{name: "platform version", mutate: func(c *FingerprintConfiguration) { c.PlatformVersion = "14 5" }, wantErr: "platform version is invalid"},
		{name: "window pair", mutate: func(c *FingerprintConfiguration) { c.WindowHeight = 0 }, wantErr: "window configuration is invalid"},
		{name: "CPU", mutate: func(c *FingerprintConfiguration) { c.HardwareConcurrency = 129 }, wantErr: "CPU configuration is invalid"},
		{name: "device memory", mutate: func(c *FingerprintConfiguration) { c.DeviceMemory = 3 }, wantErr: "memory configuration is invalid"},
		{name: "color depth", mutate: func(c *FingerprintConfiguration) { c.ColorDepth = 12 }, wantErr: "color-depth configuration is invalid"},
		{name: "touch points", mutate: func(c *FingerprintConfiguration) { invalid := -1; c.MaxTouchPoints = &invalid }, wantErr: "touch configuration is invalid"},
		{name: "do not track", mutate: func(c *FingerprintConfiguration) { c.DoNotTrack = "yes" }, wantErr: "tracking configuration is invalid"},
		{name: "screen pair", mutate: func(c *FingerprintConfiguration) { c.ScreenHeight = 0 }, wantErr: "screen configuration is invalid"},
		{name: "scale factor", mutate: func(c *FingerprintConfiguration) { c.DeviceScaleFactor = 8 }, wantErr: "scale configuration is invalid"},
		{name: "WebRTC policy", mutate: func(c *FingerprintConfiguration) { c.WebRTCPolicy = "leak_everything" }, wantErr: "WebRTC configuration is invalid"},
		{name: "font length", mutate: func(c *FingerprintConfiguration) { c.Fonts = []string{strings.Repeat("x", 101)} }, wantErr: "font configuration is invalid"},
		{name: "duplicate fonts", mutate: func(c *FingerprintConfiguration) { c.Fonts = []string{"Inter", "inter"} }, wantErr: "font configuration contains duplicates"},
		{name: "WebGL pair", mutate: func(c *FingerprintConfiguration) { c.WebGLRenderer = "" }, wantErr: "WebGL configuration is invalid"},
		{name: "media devices", mutate: func(c *FingerprintConfiguration) { c.MediaDevices.AudioInputs = 17 }, wantErr: "media configuration is invalid"},
		{name: "battery", mutate: func(c *FingerprintConfiguration) { c.Battery.Level = 1.1 }, wantErr: "battery configuration is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fingerprint := validRuntimeFingerprint()
			test.mutate(&fingerprint.Configuration)
			client := runtimeClientForBody(t, http.StatusOK, marshalRuntimeConfigResponse(t, runtimeTestInstanceID, fingerprint))
			assertRuntimeResolveError(t, client, test.wantErr)
		})
	}
}

func TestRuntimeConfigClientRejectsArgumentTemplateMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*FingerprintRuntime)
		wantErr string
	}{
		{name: "seed", mutate: func(f *FingerprintRuntime) { f.RuntimeArgs[0] = "--fingerprint=424243" }, wantErr: "arguments do not match"},
		{name: "platform version", mutate: func(f *FingerprintRuntime) { f.RuntimeArgs[4] = "--fingerprint-platform-version=11.0" }, wantErr: "platform version does not match"},
		{name: "window size", mutate: func(f *FingerprintRuntime) { f.RuntimeArgs[8] = "--window-size=1441,900" }, wantErr: "window size does not match"},
		{name: "CPU", mutate: func(f *FingerprintRuntime) { f.RuntimeArgs[9] = "--fingerprint-hardware-concurrency=16" }, wantErr: "CPU value does not match"},
		{name: "WebRTC", mutate: func(f *FingerprintRuntime) {
			f.RuntimeArgs[10] = "--fingerprinting-canvas-image-data-noise"
			f.RuntimeArgs = append(f.RuntimeArgs[:11], f.RuntimeArgs[12:]...)
		}, wantErr: "switches do not match"},
		{name: "canvas", mutate: func(f *FingerprintRuntime) { f.Configuration.CanvasNoise = false }, wantErr: "switches do not match"},
		{name: "client rects", mutate: func(f *FingerprintRuntime) { f.Configuration.ClientRectsNoise = false }, wantErr: "switches do not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fingerprint := validRuntimeFingerprint()
			test.mutate(fingerprint)
			client := runtimeClientForBody(t, http.StatusOK, marshalRuntimeConfigResponse(t, runtimeTestInstanceID, fingerprint))
			assertRuntimeResolveError(t, client, test.wantErr)
		})
	}
}

func TestRuntimeConfigClientRejectsOversizedResponse(t *testing.T) {
	t.Parallel()
	client := runtimeClientForBody(t, http.StatusOK, []byte(strings.Repeat("x", maxRuntimeConfigResponse+1)))
	assertRuntimeResolveError(t, client, "too large or unreadable")
}

func TestRuntimeConfigClientRefusesRedirectBeforeForwardingCredential(t *testing.T) {
	t.Parallel()
	credentialSink := make(chan http.Header, 1)
	client := runtimeClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/credential-sink" {
			credentialSink <- r.Header.Clone()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/credential-sink", http.StatusTemporaryRedirect)
	})

	assertRuntimeResolveError(t, client, "HTTP 307")
	select {
	case headers := <-credentialSink:
		t.Fatalf("redirect was followed with Authorization %q", headers.Get("Authorization"))
	default:
	}
}

func TestRuntimeConfigClientRejectsMalformedAndTrailingJSON(t *testing.T) {
	t.Parallel()
	valid := marshalRuntimeConfigResponse(t, runtimeTestInstanceID, nil)
	tests := []struct {
		name string
		body []byte
	}{
		{name: "malformed", body: []byte(`{"data":`)},
		{name: "trailing object", body: append(append([]byte(nil), valid...), []byte(` {}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := runtimeClientForBody(t, http.StatusOK, test.body)
			assertRuntimeResolveError(t, client, "response is invalid")
		})
	}
}

func validRuntimeFingerprint() *FingerprintRuntime {
	touchPoints := 5
	return &FingerprintRuntime{
		ID:            runtimeTestTemplateID,
		WorkspaceID:   runtimeTestWorkspaceID,
		Mode:          "fixed",
		BrowserFamily: "chromium",
		BrowserMajor:  144,
		Platform:      "windows",
		Seed:          424242,
		Locale:        "en-US",
		Timezone:      "America/Los_Angeles",
		RuntimeArgs: []string{
			"--fingerprint=424242",
			"--fingerprint-brand=Chrome",
			"--fingerprint-brand-version=144.0.0.0",
			"--fingerprint-platform=windows",
			"--fingerprint-platform-version=10.0.22631",
			"--lang=en-US",
			"--accept-lang=en-US,en",
			"--timezone=America/Los_Angeles",
			"--window-size=1440,900",
			"--fingerprint-hardware-concurrency=8",
			"--disable-non-proxied-udp",
			"--fingerprinting-canvas-image-data-noise",
			"--fingerprinting-client-rects-noise",
		},
		Configuration: FingerprintConfiguration{
			PlatformVersion:     "10.0.22631",
			WindowWidth:         1440,
			WindowHeight:        900,
			HardwareConcurrency: 8,
			DeviceMemory:        8,
			ColorDepth:          24,
			MaxTouchPoints:      &touchPoints,
			DoNotTrack:          "1",
			ScreenWidth:         2560,
			ScreenHeight:        1440,
			DeviceScaleFactor:   1.25,
			WebRTCPolicy:        "disable_non_proxied_udp",
			CanvasNoise:         true,
			AudioNoise:          true,
			ClientRectsNoise:    true,
			Fonts:               []string{"Inter", "Arial", "Segoe UI"},
			WebGLVendor:         "Google Inc. (Intel)",
			WebGLRenderer:       "ANGLE (Intel, Intel Iris Xe Graphics, D3D11)",
			MediaDevices:        &FingerprintMediaDevices{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
			Battery:             &FingerprintBattery{Charging: true, Level: 0.82, ChargingTimeSeconds: 900, DischargingTimeSeconds: 7200},
		},
		Version: 7,
	}
}

func marshalRuntimeConfigResponse(t *testing.T, instanceID string, fingerprint *FingerprintRuntime) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		Data InstanceRuntimeConfig `json:"data"`
	}{Data: InstanceRuntimeConfig{InstanceID: instanceID, Fingerprint: fingerprint}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func runtimeClientForBody(t *testing.T, status int, body []byte) *RuntimeConfigClient {
	t.Helper()
	return runtimeClientForHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func runtimeClientForHandler(t *testing.T, handler http.HandlerFunc) *RuntimeConfigClient {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewRuntimeConfigClient(server.URL, runtimeTestWorkspaceID, runtimeTestDeviceID, runtimeTestCredential, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertRuntimeResolveError(t *testing.T, client *RuntimeConfigClient, want string) {
	t.Helper()
	if _, err := client.Resolve(context.Background(), runtimeTestInstanceID); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Resolve error = %v, want error containing %q", err, want)
	}
}
