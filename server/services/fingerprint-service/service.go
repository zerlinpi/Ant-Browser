package fingerprintservice

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound        = errors.New("fingerprint template not found")
	ErrVersionConflict = errors.New("fingerprint template version conflict")
	ErrInUse           = errors.New("fingerprint template is in use")
	ErrInvalidInput    = errors.New("fingerprint template input is invalid")
	ErrNameConflict    = errors.New("fingerprint template name already exists")
	ErrEmptyBatch      = errors.New("fingerprint template batch is empty")
	ErrBatchTooLarge   = errors.New("fingerprint template batch is too large")
)

const MaxBatchTemplates = 100

var (
	languageTagPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	versionTextPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

type Template struct {
	ID            string        `json:"id"`
	WorkspaceID   string        `json:"workspaceId"`
	Name          string        `json:"name"`
	Mode          string        `json:"mode"`
	BrowserFamily string        `json:"browserFamily"`
	BrowserMajor  int           `json:"browserMajor"`
	Platform      string        `json:"platform"`
	Seed          int64         `json:"seed,string"`
	Locale        string        `json:"locale"`
	Timezone      string        `json:"timezone"`
	RuntimeArgs   []string      `json:"runtimeArgs"`
	Configuration Configuration `json:"configuration"`
	Version       int64         `json:"version"`
	CreatedBy     string        `json:"createdBy,omitempty"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
	DeletedAt     *time.Time    `json:"deletedAt,omitempty"`
}

type Configuration struct {
	PlatformVersion     string                     `json:"platformVersion,omitempty"`
	WindowWidth         int                        `json:"windowWidth,omitempty"`
	WindowHeight        int                        `json:"windowHeight,omitempty"`
	HardwareConcurrency int                        `json:"hardwareConcurrency,omitempty"`
	DeviceMemory        int                        `json:"deviceMemory,omitempty"`
	ColorDepth          int                        `json:"colorDepth,omitempty"`
	MaxTouchPoints      *int                       `json:"maxTouchPoints,omitempty"`
	DoNotTrack          string                     `json:"doNotTrack,omitempty"`
	ScreenWidth         int                        `json:"screenWidth,omitempty"`
	ScreenHeight        int                        `json:"screenHeight,omitempty"`
	DeviceScaleFactor   float64                    `json:"deviceScaleFactor,omitempty"`
	WebRTCPolicy        string                     `json:"webrtcPolicy,omitempty"`
	CanvasNoise         bool                       `json:"canvasNoise"`
	AudioNoise          bool                       `json:"audioNoise"`
	ClientRectsNoise    bool                       `json:"clientRectsNoise"`
	Fonts               []string                   `json:"fonts,omitempty"`
	WebGLVendor         string                     `json:"webglVendor,omitempty"`
	WebGLRenderer       string                     `json:"webglRenderer,omitempty"`
	MediaDevices        *MediaDevicesConfiguration `json:"mediaDevices,omitempty"`
	Battery             *BatteryConfiguration      `json:"battery,omitempty"`
}

// MediaDevicesConfiguration describes the stable device inventory exposed to
// pages. Device identifiers are derived from the template seed at runtime and
// are never stored in the template itself.
type MediaDevicesConfiguration struct {
	AudioInputs  int `json:"audioInputs"`
	VideoInputs  int `json:"videoInputs"`
	AudioOutputs int `json:"audioOutputs"`
}

// BatteryConfiguration represents the navigator.getBattery surface. A nil
// Battery on Configuration leaves the host implementation untouched.
type BatteryConfiguration struct {
	Charging               bool    `json:"charging"`
	Level                  float64 `json:"level"`
	ChargingTimeSeconds    int64   `json:"chargingTimeSeconds"`
	DischargingTimeSeconds int64   `json:"dischargingTimeSeconds"`
}

type CreateInput struct {
	Name          string        `json:"name"`
	Mode          string        `json:"mode"`
	BrowserMajor  int           `json:"browserMajor"`
	Platform      string        `json:"platform"`
	Seed          *int64        `json:"seed,string,omitempty"`
	Locale        string        `json:"locale"`
	Timezone      string        `json:"timezone"`
	Configuration Configuration `json:"configuration"`
}

type UpdateInput struct {
	Name          string        `json:"name"`
	Mode          string        `json:"mode"`
	BrowserMajor  int           `json:"browserMajor"`
	Platform      string        `json:"platform"`
	Seed          int64         `json:"seed,string"`
	Locale        string        `json:"locale"`
	Timezone      string        `json:"timezone"`
	Configuration Configuration `json:"configuration"`
	Version       int64         `json:"version"`
}

type BatchCreateInput struct {
	NamePrefix    string        `json:"namePrefix"`
	Count         int           `json:"count"`
	Mode          string        `json:"mode"`
	BrowserMajor  int           `json:"browserMajor"`
	Platform      string        `json:"platform"`
	SeedStart     *int64        `json:"seedStart,string,omitempty"`
	Locale        string        `json:"locale"`
	Timezone      string        `json:"timezone"`
	Configuration Configuration `json:"configuration"`
}

type Preset struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Input       CreateInput `json:"input"`
}

type Repository interface {
	CreateFingerprintTemplate(context.Context, Template) error
	CreateFingerprintTemplates(context.Context, []Template) error
	FindFingerprintTemplate(context.Context, string, string) (Template, error)
	ListFingerprintTemplates(context.Context, string) ([]Template, error)
	UpdateFingerprintTemplate(context.Context, Template, int64) (Template, error)
	DeleteFingerprintTemplate(context.Context, string, string, int64, time.Time) error
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
	newSeed    func() (int64, error)
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now, newSeed: randomSeed}
}

func (s *Service) Create(ctx context.Context, actorID, workspaceID string, input CreateInput) (Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return Template{}, err
	}
	template, err := s.buildTemplate(actorID, workspaceID, input, s.now().UTC())
	if err != nil {
		return Template{}, err
	}
	if err := s.repository.CreateFingerprintTemplate(ctx, template); err != nil {
		return Template{}, err
	}
	return template, nil
}

func (s *Service) buildTemplate(actorID, workspaceID string, input CreateInput, now time.Time) (Template, error) {
	seed := int64(0)
	if input.Seed != nil {
		seed = *input.Seed
		if seed <= 0 {
			return Template{}, fmt.Errorf("%w: seed must be a positive integer", ErrInvalidInput)
		}
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "seeded"
	}
	if mode == "fixed" && input.Seed == nil {
		return Template{}, fmt.Errorf("%w: seed is required in fixed mode", ErrInvalidInput)
	}
	if input.Seed == nil {
		var err error
		seed, err = s.newSeed()
		if err != nil {
			return Template{}, err
		}
	}
	template := Template{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Name: input.Name,
		Mode: mode, BrowserFamily: "chromium", BrowserMajor: input.BrowserMajor,
		Platform: input.Platform, Seed: seed, Locale: input.Locale, Timezone: input.Timezone,
		Configuration: cloneConfiguration(input.Configuration), Version: 1, CreatedBy: actorID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := normalizeTemplate(&template); err != nil {
		return Template{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return template, nil
}

func (s *Service) CreateBatch(ctx context.Context, actorID, workspaceID string, input BatchCreateInput) ([]Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return nil, err
	}
	if input.Count <= 0 {
		return nil, ErrEmptyBatch
	}
	if input.Count > MaxBatchTemplates {
		return nil, ErrBatchTooLarge
	}
	namePrefix := strings.TrimSpace(input.NamePrefix)
	if namePrefix == "" || len([]rune(namePrefix)) > 110 {
		return nil, fmt.Errorf("%w: valid namePrefix is required", ErrInvalidInput)
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "seeded"
	}
	if mode == "fixed" && input.SeedStart == nil {
		return nil, fmt.Errorf("%w: seedStart is required in fixed mode", ErrInvalidInput)
	}
	seedStart := int64(0)
	if input.SeedStart != nil {
		seedStart = *input.SeedStart
		if seedStart <= 0 || seedStart > math.MaxInt64-int64(input.Count-1) {
			return nil, fmt.Errorf("%w: seedStart cannot produce the requested batch", ErrInvalidInput)
		}
	} else {
		var err error
		seedStart, err = s.newSeed()
		if err != nil {
			return nil, err
		}
		limit := int64(math.MaxInt64) - int64(input.Count-1)
		if seedStart > limit {
			seedStart = ((seedStart - 1) % limit) + 1
		}
	}

	now := s.now().UTC()
	digits := len(strconv.Itoa(input.Count))
	if digits < 2 {
		digits = 2
	}
	templates := make([]Template, 0, input.Count)
	for index := 0; index < input.Count; index++ {
		seed := seedStart + int64(index)
		createInput := CreateInput{
			Name: fmt.Sprintf("%s %0*d", namePrefix, digits, index+1), Mode: mode,
			BrowserMajor: input.BrowserMajor, Platform: input.Platform, Seed: &seed,
			Locale: input.Locale, Timezone: input.Timezone, Configuration: cloneConfiguration(input.Configuration),
		}
		template, err := s.buildTemplate(actorID, workspaceID, createInput, now)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	if err := s.repository.CreateFingerprintTemplates(ctx, templates); err != nil {
		return nil, err
	}
	return cloneTemplates(templates), nil
}

func (s *Service) ListPresets(ctx context.Context, actorID, workspaceID string) ([]Preset, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintRead); err != nil {
		return nil, err
	}
	return examplePresets(), nil
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintRead); err != nil {
		return nil, err
	}
	templates, err := s.repository.ListFingerprintTemplates(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for index := range templates {
		withCurrentRuntimeArgs(&templates[index])
	}
	return templates, nil
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, templateID string) (Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintRead); err != nil {
		return Template{}, err
	}
	template, err := s.repository.FindFingerprintTemplate(ctx, workspaceID, templateID)
	if err != nil {
		return Template{}, err
	}
	withCurrentRuntimeArgs(&template)
	return template, nil
}

// withCurrentRuntimeArgs derives the Chromium flags from the template fields
// on every read. Stored runtime_args are a cache written by older releases;
// serving them verbatim would let legacy or empty rows (or a later change to
// buildRuntimeArgs) disagree with the configuration that desktop agents
// re-derive and verify, which fails every cloud start of that template.
func withCurrentRuntimeArgs(template *Template) {
	template.RuntimeArgs = buildRuntimeArgs(*template)
}

func (s *Service) Update(ctx context.Context, actorID, workspaceID, templateID string, input UpdateInput) (Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return Template{}, err
	}
	if input.Version <= 0 {
		return Template{}, fmt.Errorf("%w: version is required", ErrInvalidInput)
	}
	current, err := s.repository.FindFingerprintTemplate(ctx, workspaceID, templateID)
	if err != nil {
		return Template{}, err
	}
	current.Name = input.Name
	current.Mode = input.Mode
	current.BrowserMajor = input.BrowserMajor
	current.Platform = input.Platform
	current.Seed = input.Seed
	current.Locale = input.Locale
	current.Timezone = input.Timezone
	current.Configuration = cloneConfiguration(input.Configuration)
	current.UpdatedAt = s.now().UTC()
	if err := normalizeTemplate(&current); err != nil {
		return Template{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return s.repository.UpdateFingerprintTemplate(ctx, current, input.Version)
}

func (s *Service) Delete(ctx context.Context, actorID, workspaceID, templateID string, version int64) error {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return err
	}
	if version <= 0 {
		return fmt.Errorf("%w: version is required", ErrInvalidInput)
	}
	return s.repository.DeleteFingerprintTemplate(ctx, workspaceID, templateID, version, s.now().UTC())
}

func normalizeTemplate(template *Template) error {
	template.Name = strings.TrimSpace(template.Name)
	if template.Name == "" || len([]rune(template.Name)) > 120 {
		return errors.New("valid fingerprint template name is required")
	}
	template.Mode = strings.ToLower(strings.TrimSpace(template.Mode))
	if template.Mode != "seeded" && template.Mode != "fixed" && template.Mode != "custom" {
		return errors.New("mode must be seeded, fixed, or custom")
	}
	if template.BrowserMajor == 0 {
		template.BrowserMajor = 144
	}
	if template.BrowserMajor < 120 || template.BrowserMajor > 250 {
		return errors.New("browserMajor must be between 120 and 250")
	}
	template.Platform = strings.ToLower(strings.TrimSpace(template.Platform))
	if template.Platform == "mac" || template.Platform == "darwin" {
		template.Platform = "macos"
	}
	if template.Platform != "windows" && template.Platform != "linux" && template.Platform != "macos" {
		return errors.New("platform must be windows, linux, or macos")
	}
	if template.Seed <= 0 {
		return errors.New("seed must be a positive integer")
	}
	template.Locale = strings.TrimSpace(template.Locale)
	if !languageTagPattern.MatchString(template.Locale) {
		return errors.New("locale must be a valid language tag")
	}
	template.Timezone = strings.TrimSpace(template.Timezone)
	if template.Timezone == "" {
		return errors.New("timezone is required")
	}
	if _, err := time.LoadLocation(template.Timezone); err != nil {
		return errors.New("timezone must be a valid IANA timezone")
	}
	if err := normalizeConfiguration(&template.Configuration); err != nil {
		return err
	}
	if encoded, err := json.Marshal(template.Configuration); err != nil || len(encoded) > 16*1024 {
		return errors.New("fingerprint configuration is invalid")
	}
	template.RuntimeArgs = buildRuntimeArgs(*template)
	return nil
}

func normalizeConfiguration(configuration *Configuration) error {
	if configuration == nil {
		return errors.New("fingerprint configuration is required")
	}
	configuration.PlatformVersion = strings.TrimSpace(configuration.PlatformVersion)
	configuration.WebRTCPolicy = strings.ToLower(strings.TrimSpace(configuration.WebRTCPolicy))
	configuration.DoNotTrack = strings.ToLower(strings.TrimSpace(configuration.DoNotTrack))
	configuration.WebGLVendor = strings.TrimSpace(configuration.WebGLVendor)
	configuration.WebGLRenderer = strings.TrimSpace(configuration.WebGLRenderer)
	fonts := make([]string, 0, len(configuration.Fonts))
	seenFonts := make(map[string]struct{}, len(configuration.Fonts))
	for _, value := range configuration.Fonts {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seenFonts[key]; exists {
			continue
		}
		seenFonts[key] = struct{}{}
		fonts = append(fonts, value)
	}
	configuration.Fonts = fonts
	return validateConfiguration(*configuration)
}

func validateConfiguration(configuration Configuration) error {
	if configuration.PlatformVersion != "" && !versionTextPattern.MatchString(configuration.PlatformVersion) {
		return errors.New("platformVersion contains unsupported characters")
	}
	if (configuration.WindowWidth == 0) != (configuration.WindowHeight == 0) {
		return errors.New("windowWidth and windowHeight must be configured together")
	}
	if configuration.WindowWidth != 0 && (configuration.WindowWidth < 800 || configuration.WindowWidth > 7680 || configuration.WindowHeight < 600 || configuration.WindowHeight > 4320) {
		return errors.New("window dimensions are outside the supported range")
	}
	if configuration.HardwareConcurrency != 0 && (configuration.HardwareConcurrency < 1 || configuration.HardwareConcurrency > 128) {
		return errors.New("hardwareConcurrency must be between 1 and 128")
	}
	switch configuration.DeviceMemory {
	case 0, 1, 2, 4, 8:
	default:
		return errors.New("deviceMemory must be one of 1, 2, 4, or 8 GiB")
	}
	switch configuration.ColorDepth {
	case 0, 16, 24, 30, 32:
	default:
		return errors.New("colorDepth must be one of 16, 24, 30, or 32")
	}
	if configuration.MaxTouchPoints != nil && (*configuration.MaxTouchPoints < 0 || *configuration.MaxTouchPoints > 20) {
		return errors.New("maxTouchPoints must be between 0 and 20")
	}
	switch configuration.DoNotTrack {
	case "", "0", "1", "unspecified":
	default:
		return errors.New("doNotTrack must be 0, 1, or unspecified")
	}
	if (configuration.ScreenWidth == 0) != (configuration.ScreenHeight == 0) {
		return errors.New("screenWidth and screenHeight must be configured together")
	}
	if configuration.ScreenWidth != 0 && (configuration.ScreenWidth < 800 || configuration.ScreenWidth > 15360 || configuration.ScreenHeight < 600 || configuration.ScreenHeight > 8640) {
		return errors.New("screen dimensions are outside the supported range")
	}
	if configuration.DeviceScaleFactor != 0 && (configuration.DeviceScaleFactor < 0.5 || configuration.DeviceScaleFactor > 4) {
		return errors.New("deviceScaleFactor must be between 0.5 and 4")
	}
	switch configuration.WebRTCPolicy {
	case "", "default", "default_public_and_private_interfaces", "default_public_interface_only", "disable_non_proxied_udp":
	default:
		return errors.New("webrtcPolicy is invalid")
	}
	if (configuration.WebGLVendor == "") != (configuration.WebGLRenderer == "") {
		return errors.New("webglVendor and webglRenderer must be configured together")
	}
	if len(configuration.WebGLVendor) > 160 || len(configuration.WebGLRenderer) > 256 ||
		hasControlCharacters(configuration.WebGLVendor) || hasControlCharacters(configuration.WebGLRenderer) {
		return errors.New("WebGL identity is invalid")
	}
	if len(configuration.Fonts) > 128 {
		return errors.New("fonts cannot contain more than 128 entries")
	}
	for _, font := range configuration.Fonts {
		if len([]rune(font)) > 100 || hasControlCharacters(font) {
			return errors.New("font name is invalid")
		}
	}
	if configuration.MediaDevices != nil {
		media := configuration.MediaDevices
		if media.AudioInputs < 0 || media.AudioInputs > 16 || media.VideoInputs < 0 || media.VideoInputs > 16 ||
			media.AudioOutputs < 0 || media.AudioOutputs > 16 || media.AudioInputs+media.VideoInputs+media.AudioOutputs > 32 {
			return errors.New("media device counts are outside the supported range")
		}
	}
	if configuration.Battery != nil {
		battery := configuration.Battery
		if battery.Level < 0 || battery.Level > 1 || battery.ChargingTimeSeconds < 0 || battery.DischargingTimeSeconds < 0 ||
			battery.ChargingTimeSeconds > 365*24*60*60 || battery.DischargingTimeSeconds > 365*24*60*60 {
			return errors.New("battery configuration is invalid")
		}
	}
	return nil
}

func hasControlCharacters(value string) bool {
	return strings.IndexFunc(value, func(char rune) bool { return char < 0x20 || char == 0x7f }) >= 0
}

func buildRuntimeArgs(template Template) []string {
	args := []string{
		"--fingerprint=" + strconv.FormatInt(template.Seed, 10),
		"--fingerprint-brand=Chrome",
		fmt.Sprintf("--fingerprint-brand-version=%d.0.0.0", template.BrowserMajor),
		"--fingerprint-platform=" + template.Platform,
	}
	if template.Configuration.PlatformVersion != "" {
		args = append(args, "--fingerprint-platform-version="+template.Configuration.PlatformVersion)
	}
	acceptLanguage := template.Locale
	if index := strings.IndexByte(template.Locale, '-'); index > 0 {
		acceptLanguage += "," + strings.ToLower(template.Locale[:index])
	}
	args = append(args, "--lang="+template.Locale, "--accept-lang="+acceptLanguage, "--timezone="+template.Timezone)
	if template.Configuration.WindowWidth > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", template.Configuration.WindowWidth, template.Configuration.WindowHeight))
	}
	if template.Configuration.HardwareConcurrency > 0 {
		args = append(args, "--fingerprint-hardware-concurrency="+strconv.Itoa(template.Configuration.HardwareConcurrency))
	}
	switch template.Configuration.WebRTCPolicy {
	case "disable_non_proxied_udp":
		args = append(args, "--disable-non-proxied-udp")
	case "default_public_and_private_interfaces", "default_public_interface_only":
		args = append(args, "--webrtc-ip-handling-policy="+template.Configuration.WebRTCPolicy)
	}
	if template.Configuration.CanvasNoise {
		args = append(args, "--fingerprinting-canvas-image-data-noise")
	}
	if template.Configuration.ClientRectsNoise {
		args = append(args, "--fingerprinting-client-rects-noise")
	}
	return args
}

func examplePresets() []Preset {
	touchDesktop := 0
	touchEnabled := 5
	presets := []Preset{
		{
			Key: "amazon-us", Name: "Amazon US", Description: "Windows desktop profile for Amazon US operations.",
			Input: CreateInput{
				Name: "Amazon US", Mode: "seeded", BrowserMajor: 144, Platform: "windows", Locale: "en-US", Timezone: "America/Los_Angeles",
				Configuration: Configuration{
					PlatformVersion: "10.0.0", WindowWidth: 1440, WindowHeight: 900, HardwareConcurrency: 8,
					DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchDesktop, DoNotTrack: "1",
					ScreenWidth: 1920, ScreenHeight: 1080, DeviceScaleFactor: 1,
					WebRTCPolicy: "disable_non_proxied_udp", CanvasNoise: true, AudioNoise: true, ClientRectsNoise: true,
					Fonts:       []string{"Arial", "Segoe UI", "Times New Roman", "Verdana"},
					WebGLVendor: "Google Inc. (Intel)", WebGLRenderer: "ANGLE (Intel, Intel Iris Xe Graphics, D3D11)",
					MediaDevices: &MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
					Battery:      &BatteryConfiguration{Charging: true, Level: 0.82, ChargingTimeSeconds: 900, DischargingTimeSeconds: 7200},
				},
			},
		},
		{
			Key: "tiktok-us", Name: "TikTok US", Description: "Touch-capable US profile for TikTok account operations.",
			Input: CreateInput{
				Name: "TikTok US", Mode: "seeded", BrowserMajor: 144, Platform: "windows", Locale: "en-US", Timezone: "America/New_York",
				Configuration: Configuration{
					PlatformVersion: "10.0.0", WindowWidth: 1365, WindowHeight: 768, HardwareConcurrency: 8,
					DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchEnabled, DoNotTrack: "unspecified",
					ScreenWidth: 1920, ScreenHeight: 1080, DeviceScaleFactor: 1,
					WebRTCPolicy: "disable_non_proxied_udp", CanvasNoise: true, AudioNoise: true, ClientRectsNoise: true,
					Fonts:       []string{"Arial", "Segoe UI", "Roboto", "Verdana"},
					WebGLVendor: "Google Inc. (NVIDIA)", WebGLRenderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060, D3D11)",
					MediaDevices: &MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
					Battery:      &BatteryConfiguration{Charging: false, Level: 0.67, DischargingTimeSeconds: 14400},
				},
			},
		},
		{
			Key: "facebook-eu", Name: "Facebook EU", Description: "English-language EU profile for Facebook business operations.",
			Input: CreateInput{
				Name: "Facebook EU", Mode: "seeded", BrowserMajor: 144, Platform: "windows", Locale: "en-GB", Timezone: "Europe/London",
				Configuration: Configuration{
					PlatformVersion: "10.0.0", WindowWidth: 1536, WindowHeight: 864, HardwareConcurrency: 8,
					DeviceMemory: 8, ColorDepth: 24, MaxTouchPoints: &touchDesktop, DoNotTrack: "1",
					ScreenWidth: 2560, ScreenHeight: 1440, DeviceScaleFactor: 1.25,
					WebRTCPolicy: "default_public_interface_only", CanvasNoise: true, AudioNoise: true, ClientRectsNoise: true,
					Fonts:       []string{"Arial", "Segoe UI", "Calibri", "Times New Roman"},
					WebGLVendor: "Google Inc. (Intel)", WebGLRenderer: "ANGLE (Intel, Intel UHD Graphics 770, D3D11)",
					MediaDevices: &MediaDevicesConfiguration{AudioInputs: 1, VideoInputs: 1, AudioOutputs: 1},
					Battery:      &BatteryConfiguration{Charging: true, Level: 1, ChargingTimeSeconds: 0, DischargingTimeSeconds: 10800},
				},
			},
		},
	}
	for index := range presets {
		presets[index].Input.Configuration = cloneConfiguration(presets[index].Input.Configuration)
	}
	return presets
}

func cloneConfiguration(configuration Configuration) Configuration {
	configuration.Fonts = append([]string(nil), configuration.Fonts...)
	if configuration.MaxTouchPoints != nil {
		value := *configuration.MaxTouchPoints
		configuration.MaxTouchPoints = &value
	}
	if configuration.MediaDevices != nil {
		value := *configuration.MediaDevices
		configuration.MediaDevices = &value
	}
	if configuration.Battery != nil {
		value := *configuration.Battery
		configuration.Battery = &value
	}
	return configuration
}

func cloneTemplates(templates []Template) []Template {
	cloned := make([]Template, len(templates))
	for index, template := range templates {
		template.RuntimeArgs = append([]string(nil), template.RuntimeArgs...)
		template.Configuration = cloneConfiguration(template.Configuration)
		if template.DeletedAt != nil {
			value := *template.DeletedAt
			template.DeletedAt = &value
		}
		cloned[index] = template
	}
	return cloned
}

func randomSeed() (int64, error) {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return 0, err
	}
	seed := int64(binary.BigEndian.Uint64(buffer[:]) & ((1 << 63) - 1))
	if seed == 0 {
		seed = 1
	}
	return seed, nil
}
