package cloudagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
)

const maxRuntimeConfigResponse = 128 << 10

var (
	runtimeArgNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	versionValuePattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	languageValuePattern  = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
)

type FingerprintMediaDevices struct {
	AudioInputs  int `json:"audioInputs"`
	VideoInputs  int `json:"videoInputs"`
	AudioOutputs int `json:"audioOutputs"`
}

type FingerprintBattery struct {
	Charging               bool    `json:"charging"`
	Level                  float64 `json:"level"`
	ChargingTimeSeconds    int64   `json:"chargingTimeSeconds"`
	DischargingTimeSeconds int64   `json:"dischargingTimeSeconds"`
}

type FingerprintConfiguration struct {
	PlatformVersion     string                   `json:"platformVersion,omitempty"`
	WindowWidth         int                      `json:"windowWidth,omitempty"`
	WindowHeight        int                      `json:"windowHeight,omitempty"`
	HardwareConcurrency int                      `json:"hardwareConcurrency,omitempty"`
	DeviceMemory        int                      `json:"deviceMemory,omitempty"`
	ColorDepth          int                      `json:"colorDepth,omitempty"`
	MaxTouchPoints      *int                     `json:"maxTouchPoints,omitempty"`
	DoNotTrack          string                   `json:"doNotTrack,omitempty"`
	ScreenWidth         int                      `json:"screenWidth,omitempty"`
	ScreenHeight        int                      `json:"screenHeight,omitempty"`
	DeviceScaleFactor   float64                  `json:"deviceScaleFactor,omitempty"`
	WebRTCPolicy        string                   `json:"webrtcPolicy,omitempty"`
	CanvasNoise         bool                     `json:"canvasNoise"`
	AudioNoise          bool                     `json:"audioNoise"`
	ClientRectsNoise    bool                     `json:"clientRectsNoise"`
	Fonts               []string                 `json:"fonts,omitempty"`
	WebGLVendor         string                   `json:"webglVendor,omitempty"`
	WebGLRenderer       string                   `json:"webglRenderer,omitempty"`
	MediaDevices        *FingerprintMediaDevices `json:"mediaDevices,omitempty"`
	Battery             *FingerprintBattery      `json:"battery,omitempty"`
}

type FingerprintRuntime struct {
	ID            string                   `json:"id"`
	WorkspaceID   string                   `json:"workspaceId"`
	Mode          string                   `json:"mode"`
	BrowserFamily string                   `json:"browserFamily"`
	BrowserMajor  int                      `json:"browserMajor"`
	Platform      string                   `json:"platform"`
	Seed          int64                    `json:"seed,string"`
	Locale        string                   `json:"locale"`
	Timezone      string                   `json:"timezone"`
	RuntimeArgs   []string                 `json:"runtimeArgs"`
	Configuration FingerprintConfiguration `json:"configuration"`
	Version       int64                    `json:"version"`
}

type InstanceRuntimeConfig struct {
	InstanceID  string              `json:"instanceId"`
	Fingerprint *FingerprintRuntime `json:"fingerprint,omitempty"`
}

type RuntimeConfigClient struct {
	baseURL     string
	deviceID    string
	workspaceID string
	credential  string
	http        *http.Client
}

func NewRuntimeConfigClient(baseURL, workspaceID, deviceID, credential string, httpClient *http.Client) (*RuntimeConfigClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("runtime configuration endpoint must be an HTTPS origin")
	}
	parsed.Path = ""
	workspace, workspaceErr := uuid.Parse(strings.TrimSpace(workspaceID))
	device, deviceErr := uuid.Parse(strings.TrimSpace(deviceID))
	credential = strings.TrimSpace(credential)
	if workspaceErr != nil || deviceErr != nil || credential == "" || len(credential) > 512 || strings.ContainsAny(credential, "\r\n") {
		return nil, errors.New("runtime configuration identity is invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	} else {
		clone := *httpClient
		httpClient = &clone
		if httpClient.Timeout <= 0 || httpClient.Timeout > time.Minute {
			httpClient.Timeout = 15 * time.Second
		}
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &RuntimeConfigClient{
		baseURL: strings.TrimRight(parsed.String(), "/"), workspaceID: workspace.String(), deviceID: device.String(),
		credential: credential, http: httpClient,
	}, nil
}

func (c *RuntimeConfigClient) Resolve(ctx context.Context, instanceID string) (InstanceRuntimeConfig, error) {
	parsedID, err := uuid.Parse(strings.TrimSpace(instanceID))
	if err != nil || parsedID.String() != strings.TrimSpace(instanceID) {
		return InstanceRuntimeConfig{}, errors.New("runtime configuration instance ID is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/agent/instances/"+url.PathEscape(parsedID.String())+"/runtime-config", nil)
	if err != nil {
		return InstanceRuntimeConfig{}, errors.New("create runtime configuration request")
	}
	request.Header.Set("Authorization", "Device "+c.credential)
	request.Header.Set("X-Device-ID", c.deviceID)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return InstanceRuntimeConfig{}, errors.New("runtime configuration request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return InstanceRuntimeConfig{}, fmt.Errorf("runtime configuration request rejected: HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeConfigResponse+1))
	if err != nil || len(payload) > maxRuntimeConfigResponse {
		return InstanceRuntimeConfig{}, errors.New("runtime configuration response is too large or unreadable")
	}
	var envelope struct {
		Data InstanceRuntimeConfig `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&envelope); err != nil || decoder.Decode(new(any)) != io.EOF {
		return InstanceRuntimeConfig{}, errors.New("runtime configuration response is invalid")
	}
	if envelope.Data.InstanceID != parsedID.String() {
		return InstanceRuntimeConfig{}, errors.New("runtime configuration identity does not match the request")
	}
	if envelope.Data.Fingerprint != nil {
		if err := validateFingerprintRuntime(envelope.Data.Fingerprint, c.workspaceID); err != nil {
			return InstanceRuntimeConfig{}, err
		}
	}
	return envelope.Data, nil
}

func validateFingerprintRuntime(runtime *FingerprintRuntime, workspaceID string) error {
	if runtime == nil {
		return nil
	}
	id, idErr := uuid.Parse(runtime.ID)
	workspace, workspaceErr := uuid.Parse(runtime.WorkspaceID)
	if idErr != nil || id.String() != runtime.ID || workspaceErr != nil || workspace.String() != workspaceID ||
		runtime.Version < 1 || runtime.Seed < 1 || runtime.BrowserFamily != "chromium" || runtime.BrowserMajor < 120 || runtime.BrowserMajor > 250 {
		return errors.New("runtime fingerprint identity is invalid")
	}
	switch runtime.Mode {
	case "seeded", "fixed", "custom":
	default:
		return errors.New("runtime fingerprint mode is invalid")
	}
	switch runtime.Platform {
	case "windows", "linux", "macos":
	default:
		return errors.New("runtime fingerprint platform is invalid")
	}
	if !languageValuePattern.MatchString(runtime.Locale) || strings.TrimSpace(runtime.Timezone) != runtime.Timezone || runtime.Timezone == "" || len(runtime.Timezone) > 128 || hasRuntimeControl(runtime.Timezone) {
		return errors.New("runtime fingerprint locale or timezone is invalid")
	}
	if _, err := time.LoadLocation(runtime.Timezone); err != nil {
		return errors.New("runtime fingerprint timezone is invalid")
	}
	args, err := sanitizeFingerprintRuntimeArgs(runtime.RuntimeArgs)
	if err != nil {
		return err
	}
	runtime.RuntimeArgs = args
	if err := validateFingerprintConfiguration(runtime.Configuration); err != nil {
		return err
	}
	return validateFingerprintRuntimeConsistency(runtime)
}

func validateFingerprintRuntimeConsistency(runtime *FingerprintRuntime) error {
	values := make(map[string]string, len(runtime.RuntimeArgs))
	present := make(map[string]bool, len(runtime.RuntimeArgs))
	for _, argument := range runtime.RuntimeArgs {
		name, value, _ := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
		values[strings.ToLower(name)] = value
		present[strings.ToLower(name)] = true
	}
	expectedAcceptLanguage := runtime.Locale
	if index := strings.IndexByte(runtime.Locale, '-'); index > 0 {
		expectedAcceptLanguage += "," + strings.ToLower(runtime.Locale[:index])
	}
	if values["fingerprint"] != strconv.FormatInt(runtime.Seed, 10) ||
		values["fingerprint-brand"] != "Chrome" ||
		values["fingerprint-brand-version"] != fmt.Sprintf("%d.0.0.0", runtime.BrowserMajor) ||
		values["fingerprint-platform"] != runtime.Platform ||
		values["lang"] != runtime.Locale ||
		values["accept-lang"] != expectedAcceptLanguage ||
		values["timezone"] != runtime.Timezone {
		return errors.New("runtime fingerprint arguments do not match the template")
	}

	configuration := runtime.Configuration
	if !matchesOptionalArgument(values, present, "fingerprint-platform-version", configuration.PlatformVersion) {
		return errors.New("runtime fingerprint platform version does not match the template")
	}
	windowSize := ""
	if configuration.WindowWidth > 0 {
		windowSize = fmt.Sprintf("%d,%d", configuration.WindowWidth, configuration.WindowHeight)
	}
	if !matchesOptionalArgument(values, present, "window-size", windowSize) {
		return errors.New("runtime fingerprint window size does not match the template")
	}
	hardwareConcurrency := ""
	if configuration.HardwareConcurrency > 0 {
		hardwareConcurrency = strconv.Itoa(configuration.HardwareConcurrency)
	}
	if !matchesOptionalArgument(values, present, "fingerprint-hardware-concurrency", hardwareConcurrency) {
		return errors.New("runtime fingerprint CPU value does not match the template")
	}

	expectedWebRTCPolicy := ""
	expectedDisableNonProxiedUDP := false
	switch configuration.WebRTCPolicy {
	case "disable_non_proxied_udp":
		expectedDisableNonProxiedUDP = true
	case "default_public_and_private_interfaces", "default_public_interface_only":
		expectedWebRTCPolicy = configuration.WebRTCPolicy
	}
	if !matchesOptionalArgument(values, present, "webrtc-ip-handling-policy", expectedWebRTCPolicy) ||
		present["disable-non-proxied-udp"] != expectedDisableNonProxiedUDP ||
		present["fingerprinting-canvas-image-data-noise"] != configuration.CanvasNoise ||
		present["fingerprinting-client-rects-noise"] != configuration.ClientRectsNoise {
		return errors.New("runtime fingerprint switches do not match the template")
	}
	return nil
}

func matchesOptionalArgument(values map[string]string, present map[string]bool, name, expected string) bool {
	if expected == "" {
		return !present[name]
	}
	return present[name] && values[name] == expected
}

func sanitizeFingerprintRuntimeArgs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 32 {
		return nil, errors.New("runtime fingerprint arguments are invalid")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) != raw || len(raw) < 3 || len(raw) > 512 || !strings.HasPrefix(raw, "--") || hasRuntimeControl(raw) {
			return nil, errors.New("runtime fingerprint argument is invalid")
		}
		name, value, hasValue := strings.TrimPrefix(raw, "--"), "", false
		if index := strings.IndexByte(name, '='); index >= 0 {
			value, name, hasValue = name[index+1:], name[:index], true
		}
		if name != strings.ToLower(name) || !runtimeArgNamePattern.MatchString(name) {
			return nil, errors.New("runtime fingerprint argument name is invalid")
		}
		if _, exists := seen[name]; exists {
			return nil, errors.New("runtime fingerprint arguments contain duplicates")
		}
		seen[name] = struct{}{}
		if err := validateFingerprintRuntimeArg(name, value, hasValue); err != nil {
			return nil, err
		}
		result = append(result, raw)
	}
	for _, required := range []string{"fingerprint", "fingerprint-brand", "fingerprint-brand-version", "fingerprint-platform", "lang", "accept-lang", "timezone"} {
		if _, exists := seen[required]; !exists {
			return nil, errors.New("runtime fingerprint arguments are incomplete")
		}
	}
	return result, nil
}

func validateFingerprintRuntimeArg(name, value string, hasValue bool) error {
	requireValue := func() error {
		if !hasValue || strings.TrimSpace(value) == "" {
			return errors.New("runtime fingerprint argument value is required")
		}
		return nil
	}
	switch name {
	case "fingerprint":
		if err := requireValue(); err != nil {
			return err
		}
		seed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seed < 1 {
			return errors.New("runtime fingerprint seed argument is invalid")
		}
	case "fingerprint-brand":
		if err := requireValue(); err != nil || value != "Chrome" {
			return errors.New("runtime fingerprint brand argument is invalid")
		}
	case "fingerprint-brand-version", "fingerprint-platform-version":
		if err := requireValue(); err != nil || !versionValuePattern.MatchString(value) {
			return errors.New("runtime fingerprint version argument is invalid")
		}
	case "fingerprint-platform":
		if err := requireValue(); err != nil || (value != "windows" && value != "linux" && value != "macos") {
			return errors.New("runtime fingerprint platform argument is invalid")
		}
	case "lang":
		if err := requireValue(); err != nil || !languageValuePattern.MatchString(value) {
			return errors.New("runtime fingerprint language argument is invalid")
		}
	case "accept-lang":
		if err := requireValue(); err != nil {
			return err
		}
		for _, item := range strings.Split(value, ",") {
			if !languageValuePattern.MatchString(strings.TrimSpace(item)) {
				return errors.New("runtime fingerprint accept-language argument is invalid")
			}
		}
	case "timezone":
		if err := requireValue(); err != nil || len(value) > 128 {
			return errors.New("runtime fingerprint timezone argument is invalid")
		}
	case "window-size":
		if err := requireValue(); err != nil {
			return err
		}
		parts := strings.Split(value, ",")
		width, widthErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		height, heightErr := 0, errors.New("height missing")
		if len(parts) == 2 {
			height, heightErr = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
		if len(parts) != 2 || widthErr != nil || heightErr != nil || width < 800 || width > 7680 || height < 600 || height > 4320 {
			return errors.New("runtime fingerprint window argument is invalid")
		}
	case "fingerprint-hardware-concurrency":
		if err := requireValue(); err != nil {
			return err
		}
		count, err := strconv.Atoi(value)
		if err != nil || count < 1 || count > 128 {
			return errors.New("runtime fingerprint CPU argument is invalid")
		}
	case "webrtc-ip-handling-policy":
		if err := requireValue(); err != nil || (value != "default_public_and_private_interfaces" && value != "default_public_interface_only") {
			return errors.New("runtime fingerprint WebRTC argument is invalid")
		}
	case "disable-non-proxied-udp", "fingerprinting-canvas-image-data-noise", "fingerprinting-client-rects-noise":
		if hasValue {
			return errors.New("runtime fingerprint switch argument is invalid")
		}
	default:
		return errors.New("runtime fingerprint argument is not allowed")
	}
	return nil
}

func validateFingerprintConfiguration(configuration FingerprintConfiguration) error {
	if configuration.PlatformVersion != "" && !versionValuePattern.MatchString(configuration.PlatformVersion) {
		return errors.New("runtime fingerprint platform version is invalid")
	}
	if (configuration.WindowWidth == 0) != (configuration.WindowHeight == 0) ||
		(configuration.WindowWidth != 0 && (configuration.WindowWidth < 800 || configuration.WindowWidth > 7680 || configuration.WindowHeight < 600 || configuration.WindowHeight > 4320)) {
		return errors.New("runtime fingerprint window configuration is invalid")
	}
	if configuration.HardwareConcurrency != 0 && (configuration.HardwareConcurrency < 1 || configuration.HardwareConcurrency > 128) {
		return errors.New("runtime fingerprint CPU configuration is invalid")
	}
	switch configuration.DeviceMemory {
	case 0, 1, 2, 4, 8:
	default:
		return errors.New("runtime fingerprint memory configuration is invalid")
	}
	switch configuration.ColorDepth {
	case 0, 16, 24, 30, 32:
	default:
		return errors.New("runtime fingerprint color-depth configuration is invalid")
	}
	if configuration.MaxTouchPoints != nil && (*configuration.MaxTouchPoints < 0 || *configuration.MaxTouchPoints > 20) {
		return errors.New("runtime fingerprint touch configuration is invalid")
	}
	switch configuration.DoNotTrack {
	case "", "0", "1", "unspecified":
	default:
		return errors.New("runtime fingerprint tracking configuration is invalid")
	}
	if (configuration.ScreenWidth == 0) != (configuration.ScreenHeight == 0) ||
		(configuration.ScreenWidth != 0 && (configuration.ScreenWidth < 800 || configuration.ScreenWidth > 15360 || configuration.ScreenHeight < 600 || configuration.ScreenHeight > 8640)) {
		return errors.New("runtime fingerprint screen configuration is invalid")
	}
	if configuration.DeviceScaleFactor != 0 && (configuration.DeviceScaleFactor < 0.5 || configuration.DeviceScaleFactor > 4) {
		return errors.New("runtime fingerprint scale configuration is invalid")
	}
	switch configuration.WebRTCPolicy {
	case "", "default", "default_public_and_private_interfaces", "default_public_interface_only", "disable_non_proxied_udp":
	default:
		return errors.New("runtime fingerprint WebRTC configuration is invalid")
	}
	if len(configuration.Fonts) > 128 || len(configuration.WebGLVendor) > 160 || len(configuration.WebGLRenderer) > 256 {
		return errors.New("runtime fingerprint advanced configuration is invalid")
	}
	seenFonts := make(map[string]struct{}, len(configuration.Fonts))
	for _, value := range configuration.Fonts {
		if strings.TrimSpace(value) != value || value == "" || len([]rune(value)) > 100 || hasRuntimeControl(value) {
			return errors.New("runtime fingerprint font configuration is invalid")
		}
		key := strings.ToLower(value)
		if _, exists := seenFonts[key]; exists {
			return errors.New("runtime fingerprint font configuration contains duplicates")
		}
		seenFonts[key] = struct{}{}
	}
	if (configuration.WebGLVendor == "") != (configuration.WebGLRenderer == "") ||
		strings.TrimSpace(configuration.WebGLVendor) != configuration.WebGLVendor || strings.TrimSpace(configuration.WebGLRenderer) != configuration.WebGLRenderer ||
		hasRuntimeControl(configuration.WebGLVendor) || hasRuntimeControl(configuration.WebGLRenderer) {
		return errors.New("runtime fingerprint WebGL configuration is invalid")
	}
	if configuration.MediaDevices != nil {
		media := configuration.MediaDevices
		if media.AudioInputs < 0 || media.AudioInputs > 16 || media.VideoInputs < 0 || media.VideoInputs > 16 || media.AudioOutputs < 0 || media.AudioOutputs > 16 || media.AudioInputs+media.VideoInputs+media.AudioOutputs > 32 {
			return errors.New("runtime fingerprint media configuration is invalid")
		}
	}
	if configuration.Battery != nil {
		battery := configuration.Battery
		if battery.Level < 0 || battery.Level > 1 || battery.ChargingTimeSeconds < 0 || battery.DischargingTimeSeconds < 0 || battery.ChargingTimeSeconds > 365*24*60*60 || battery.DischargingTimeSeconds > 365*24*60*60 {
			return errors.New("runtime fingerprint battery configuration is invalid")
		}
	}
	return nil
}

func hasRuntimeControl(value string) bool {
	return strings.IndexFunc(value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0
}
