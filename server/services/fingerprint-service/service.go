package fingerprintservice

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
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
)

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
	PlatformVersion     string `json:"platformVersion,omitempty"`
	WindowWidth         int    `json:"windowWidth,omitempty"`
	WindowHeight        int    `json:"windowHeight,omitempty"`
	HardwareConcurrency int    `json:"hardwareConcurrency,omitempty"`
	WebRTCPolicy        string `json:"webrtcPolicy,omitempty"`
	CanvasNoise         bool   `json:"canvasNoise"`
	ClientRectsNoise    bool   `json:"clientRectsNoise"`
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

type Repository interface {
	CreateFingerprintTemplate(context.Context, Template) error
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
	seed := int64(0)
	if input.Seed != nil {
		seed = *input.Seed
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "seeded"
	}
	if mode == "fixed" && input.Seed == nil {
		return Template{}, errors.New("seed is required in fixed mode")
	}
	if seed <= 0 {
		var err error
		seed, err = s.newSeed()
		if err != nil {
			return Template{}, err
		}
	}
	now := s.now().UTC()
	template := Template{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Name: input.Name,
		Mode: mode, BrowserFamily: "chromium", BrowserMajor: input.BrowserMajor,
		Platform: input.Platform, Seed: seed, Locale: input.Locale, Timezone: input.Timezone,
		Configuration: input.Configuration, Version: 1, CreatedBy: actorID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := normalizeTemplate(&template); err != nil {
		return Template{}, err
	}
	if err := s.repository.CreateFingerprintTemplate(ctx, template); err != nil {
		return Template{}, err
	}
	return template, nil
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintRead); err != nil {
		return nil, err
	}
	return s.repository.ListFingerprintTemplates(ctx, workspaceID)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, templateID string) (Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintRead); err != nil {
		return Template{}, err
	}
	return s.repository.FindFingerprintTemplate(ctx, workspaceID, templateID)
}

func (s *Service) Update(ctx context.Context, actorID, workspaceID, templateID string, input UpdateInput) (Template, error) {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return Template{}, err
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
	current.Configuration = input.Configuration
	current.UpdatedAt = s.now().UTC()
	if err := normalizeTemplate(&current); err != nil {
		return Template{}, err
	}
	return s.repository.UpdateFingerprintTemplate(ctx, current, input.Version)
}

func (s *Service) Delete(ctx context.Context, actorID, workspaceID, templateID string, version int64) error {
	if err := s.authorizer.Require(ctx, workspaceID, actorID, memberservice.PermissionFingerprintManage); err != nil {
		return err
	}
	if version <= 0 {
		return errors.New("version is required")
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
	if err := validateConfiguration(template.Configuration); err != nil {
		return err
	}
	if encoded, err := json.Marshal(template.Configuration); err != nil || len(encoded) > 16*1024 {
		return errors.New("fingerprint configuration is invalid")
	}
	template.RuntimeArgs = buildRuntimeArgs(*template)
	return nil
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
	switch configuration.WebRTCPolicy {
	case "", "default", "default_public_and_private_interfaces", "default_public_interface_only", "disable_non_proxied_udp":
	default:
		return errors.New("webrtcPolicy is invalid")
	}
	return nil
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
