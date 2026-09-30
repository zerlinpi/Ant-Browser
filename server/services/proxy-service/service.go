// Package proxyservice owns the cloud Proxy Center contract.  It deliberately
// stores only references to credentials: fetching a secret is a local runtime
// concern and is never part of an API response.
package proxyservice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound           = errors.New("proxy not found")
	ErrVersionConflict    = errors.New("proxy version conflict")
	ErrAssignmentConflict = errors.New("proxy assignment version conflict")
	ErrInUse              = errors.New("proxy is assigned")
	ErrSecretProvider     = errors.New("proxy secret provider is unavailable")
	ErrAuthorizer         = errors.New("proxy authorizer is unavailable")
	ErrUnsupportedRoute   = errors.New("proxy protocol is not supported by connector")
	// ErrNameConflict: proxy names are unique per workspace among live
	// proxies, compared case-insensitively.
	ErrNameConflict = errors.New("proxy name already exists")
)

type ConnectorType string
type Kernel string

const (
	ConnectorXray   ConnectorType = "xray"   // Xray + sing-box combination stack.
	ConnectorMihomo ConnectorType = "mihomo" // Independent Mihomo stack.

	KernelDirect  Kernel = "direct"
	KernelXray    Kernel = "xray"
	KernelSingBox Kernel = "sing-box"
	KernelMihomo  Kernel = "mihomo"
)

// Proxy contains the safe, non-secret representation returned by the service.
// SecretRef is an opaque provider key, never a password, token, or config blob.
type Proxy struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspaceId"`
	Name           string        `json:"name"`
	Protocol       string        `json:"protocol"`
	Host           string        `json:"host"`
	Port           int           `json:"port"`
	Username       string        `json:"username,omitempty"`
	HasCredentials bool          `json:"hasCredentials"`
	SecretRef      string        `json:"-"`
	ConnectorType  ConnectorType `json:"connectorType"`
	Kernel         Kernel        `json:"kernel"`
	Status         string        `json:"status"`
	Version        int64         `json:"version"`
	CreatedBy      string        `json:"createdBy,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	DeletedAt      *time.Time    `json:"deletedAt,omitempty"`
}

// Secret is passed to a SecretProvider and is intentionally not embedded in
// Proxy or any request/response model that can be marshalled to JSON.
type Secret struct {
	Password string `json:"-"`
	Token    string `json:"-"`
}

type SecretInput struct {
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Clear    bool   `json:"clear,omitempty"`
}

type CreateInput struct {
	Name          string        `json:"name"`
	Protocol      string        `json:"protocol"`
	Host          string        `json:"host"`
	Port          int           `json:"port"`
	Username      string        `json:"username,omitempty"`
	SecretRef     string        `json:"secretRef,omitempty"`
	Secret        *SecretInput  `json:"secret,omitempty"`
	ConnectorType ConnectorType `json:"connectorType"`
}

type UpdateInput struct {
	Name          string        `json:"name"`
	Protocol      string        `json:"protocol"`
	Host          string        `json:"host"`
	Port          int           `json:"port"`
	Username      string        `json:"username,omitempty"`
	SecretRef     string        `json:"secretRef,omitempty"`
	Secret        *SecretInput  `json:"secret,omitempty"`
	ConnectorType ConnectorType `json:"connectorType"`
	Status        string        `json:"status"`
	Version       int64         `json:"version"`
}

type Assignment struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	ProxyID     string `json:"proxyId"`
	// ProxyName is the assigned proxy's current name, resolved whenever an
	// assignment is read so clients need no second lookup. It is not stored
	// with the assignment and is empty only if the proxy row is missing.
	ProxyName  string     `json:"proxyName,omitempty"`
	TargetID   string     `json:"targetId"`
	TargetType string     `json:"targetType"`
	Version    int64      `json:"version"`
	CreatedBy  string     `json:"createdBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	DeletedAt  *time.Time `json:"deletedAt,omitempty"`
}

// AssignmentFilter narrows ListAssignments; empty fields match everything.
type AssignmentFilter struct {
	ProxyID    string
	TargetType string
}

type HealthCheck struct {
	ID            string        `json:"id"`
	WorkspaceID   string        `json:"workspaceId"`
	ProxyID       string        `json:"proxyId"`
	RequestID     string        `json:"requestId"`
	ConnectorType ConnectorType `json:"connectorType"`
	Kernel        Kernel        `json:"kernel"`
	Status        string        `json:"status"`
	IP            string        `json:"ip,omitempty"`
	LatencyMS     int64         `json:"latencyMs,omitempty"`
	ErrorCode     string        `json:"errorCode,omitempty"`
	ErrorMessage  string        `json:"errorMessage,omitempty"`
	CreatedBy     string        `json:"createdBy"`
	CreatedAt     time.Time     `json:"createdAt"`
	CompletedAt   *time.Time    `json:"completedAt,omitempty"`
}

type HealthResult struct {
	Status       string `json:"status"`
	IP           string `json:"ip,omitempty"`
	LatencyMS    int64  `json:"latencyMs,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type Repository interface {
	CreateProxy(context.Context, Proxy) error
	FindProxy(context.Context, string, string) (Proxy, error)
	ListProxies(context.Context, string) ([]Proxy, error)
	UpdateProxy(context.Context, Proxy, int64) (Proxy, error)
	DeleteProxy(context.Context, string, string, int64, time.Time) error
	CreateProxyAssignment(context.Context, Assignment) (Assignment, error)
	// FindProxyAssignment and ListProxyAssignments return live assignments
	// with ProxyName resolved.
	FindProxyAssignment(ctx context.Context, workspaceID, targetID, targetType string) (Assignment, error)
	ListProxyAssignments(ctx context.Context, workspaceID string, filter AssignmentFilter) ([]Assignment, error)
	DeleteProxyAssignment(context.Context, string, string, string, int64, time.Time) error
	CreateProxyHealthCheck(context.Context, HealthCheck) error
	FindProxyHealthCheck(context.Context, string, string) (HealthCheck, error)
	ListProxyHealthChecks(context.Context, string, string, int) ([]HealthCheck, error)
	CompleteProxyHealthCheck(context.Context, string, string, HealthResult, time.Time) (HealthCheck, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type SecretProvider interface {
	Put(context.Context, string, string, Secret) (string, error)
	Delete(context.Context, string) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	secrets    SecretProvider
	now        func() time.Time
}

// New accepts an optional secret provider to keep local/test construction
// convenient while production callers should always provide one when storing
// authenticated proxies.
func New(repository Repository, authorizer Authorizer, providers ...SecretProvider) *Service {
	var secrets SecretProvider
	if len(providers) > 0 {
		secrets = providers[0]
	}
	return &Service{repository: repository, authorizer: authorizer, secrets: secrets, now: time.Now}
}

func (s *Service) Create(ctx context.Context, actorID, workspaceID string, input CreateInput) (Proxy, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return Proxy{}, err
	}
	proxy, err := s.buildProxy(ctx, workspaceID, actorID, input.Name, input.Protocol, input.Host, input.Port, input.Username, input.SecretRef, input.Secret, input.ConnectorType)
	if err != nil {
		return Proxy{}, err
	}
	if err := s.repository.CreateProxy(ctx, proxy); err != nil {
		if proxy.SecretRef != "" && s.secrets != nil {
			_ = s.secrets.Delete(ctx, proxy.SecretRef)
		}
		return Proxy{}, err
	}
	return proxy, nil
}

func (s *Service) List(ctx context.Context, actorID, workspaceID string) ([]Proxy, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return nil, err
	}
	return s.repository.ListProxies(ctx, workspaceID)
}

func (s *Service) Get(ctx context.Context, actorID, workspaceID, proxyID string) (Proxy, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return Proxy{}, err
	}
	return s.repository.FindProxy(ctx, workspaceID, proxyID)
}

func (s *Service) Update(ctx context.Context, actorID, workspaceID, proxyID string, input UpdateInput) (Proxy, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return Proxy{}, err
	}
	if input.Version <= 0 {
		return Proxy{}, errors.New("version is required")
	}
	if strings.TrimSpace(input.SecretRef) != "" {
		return Proxy{}, errors.New("secretRef is managed by the secret provider")
	}
	current, err := s.repository.FindProxy(ctx, workspaceID, proxyID)
	if err != nil {
		return Proxy{}, err
	}
	current.Name, current.Protocol, current.Host, current.Port = strings.TrimSpace(input.Name), strings.ToLower(strings.TrimSpace(input.Protocol)), strings.TrimSpace(input.Host), input.Port
	connector := input.ConnectorType
	if strings.TrimSpace(string(connector)) == "" {
		connector = current.ConnectorType
	}
	current.Username, current.ConnectorType, current.Status = strings.TrimSpace(input.Username), normalizeConnector(connector), strings.ToLower(strings.TrimSpace(input.Status))
	if current.Status == "" {
		current.Status = "active"
	}
	oldSecretRef := current.SecretRef
	deleteOldSecret := false
	if input.Secret != nil {
		if input.Secret.Clear {
			deleteOldSecret = current.SecretRef != ""
			current.SecretRef, current.HasCredentials = "", false
		} else {
			ref, secretErr := s.putSecret(ctx, workspaceID, proxyID, input.Secret)
			if secretErr != nil {
				return Proxy{}, secretErr
			}
			deleteOldSecret = current.SecretRef != "" && current.SecretRef != ref
			current.SecretRef, current.HasCredentials = ref, true
		}
	} else if strings.TrimSpace(input.SecretRef) != "" {
		current.SecretRef, current.HasCredentials = strings.TrimSpace(input.SecretRef), true
	}
	if err := validateProxy(&current); err != nil {
		if input.Secret != nil && current.SecretRef != "" && current.SecretRef != oldSecretRef && s.secrets != nil {
			_ = s.secrets.Delete(ctx, current.SecretRef)
		}
		return Proxy{}, err
	}
	current.UpdatedAt = s.now().UTC()
	updated, err := s.repository.UpdateProxy(ctx, current, input.Version)
	if err != nil {
		if input.Secret != nil && current.SecretRef != "" && current.SecretRef != oldSecretRef && s.secrets != nil {
			_ = s.secrets.Delete(ctx, current.SecretRef)
		}
		return Proxy{}, err
	}
	if deleteOldSecret && s.secrets != nil {
		if err := s.secrets.Delete(ctx, oldSecretRef); err != nil {
			return Proxy{}, err
		}
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, actorID, workspaceID, proxyID string, version int64) error {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return err
	}
	if version <= 0 {
		return errors.New("version is required")
	}
	if err := s.repository.DeleteProxy(ctx, workspaceID, proxyID, version, s.now().UTC()); err != nil {
		return err
	}
	return nil
}

func (s *Service) Assign(ctx context.Context, actorID, workspaceID, proxyID, targetID, targetType string) (Assignment, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return Assignment{}, err
	}
	proxy, err := s.repository.FindProxy(ctx, workspaceID, proxyID)
	if err != nil {
		return Assignment{}, err
	}
	targetID, targetType = strings.TrimSpace(targetID), strings.ToLower(strings.TrimSpace(targetType))
	if err := validateAssignmentTarget(targetID, targetType); err != nil {
		return Assignment{}, err
	}
	now := s.now().UTC()
	assignment, err := s.repository.CreateProxyAssignment(ctx, Assignment{ID: uuid.NewString(), WorkspaceID: workspaceID, ProxyID: proxyID, TargetID: targetID, TargetType: targetType, Version: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return Assignment{}, err
	}
	assignment.ProxyName = proxy.Name
	return assignment, nil
}

// GetAssignment returns the live assignment of one target, including the
// version Unassign requires. Reading needs only proxy.read.
func (s *Service) GetAssignment(ctx context.Context, actorID, workspaceID, targetType, targetID string) (Assignment, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return Assignment{}, err
	}
	targetID, targetType = strings.TrimSpace(targetID), strings.ToLower(strings.TrimSpace(targetType))
	if err := validateAssignmentTarget(targetID, targetType); err != nil {
		return Assignment{}, err
	}
	return s.repository.FindProxyAssignment(ctx, strings.TrimSpace(workspaceID), targetID, targetType)
}

// ListAssignments returns the workspace's live assignments, optionally only
// those of one proxy or one target type.
func (s *Service) ListAssignments(ctx context.Context, actorID, workspaceID string, filter AssignmentFilter) ([]Assignment, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return nil, err
	}
	filter.ProxyID = strings.TrimSpace(filter.ProxyID)
	if filter.ProxyID != "" {
		if _, err := uuid.Parse(filter.ProxyID); err != nil {
			return nil, errors.New("proxyId must be a UUID")
		}
	}
	filter.TargetType = strings.ToLower(strings.TrimSpace(filter.TargetType))
	switch filter.TargetType {
	case "", "account", "profile", "browser_instance":
	default:
		return nil, errors.New("targetType must be account, profile, or browser_instance")
	}
	return s.repository.ListProxyAssignments(ctx, strings.TrimSpace(workspaceID), filter)
}

// AssignProxy is the explicit name used by API adapters; Assign remains the
// concise service spelling used by other cloud services.
func (s *Service) AssignProxy(ctx context.Context, actorID, workspaceID, proxyID, targetID, targetType string) (Assignment, error) {
	return s.Assign(ctx, actorID, workspaceID, proxyID, targetID, targetType)
}

func (s *Service) Unassign(ctx context.Context, actorID, workspaceID, targetID, targetType string, version int64) error {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return err
	}
	if version <= 0 {
		return errors.New("version is required")
	}
	if err := validateAssignmentTarget(strings.TrimSpace(targetID), strings.ToLower(strings.TrimSpace(targetType))); err != nil {
		return err
	}
	return s.repository.DeleteProxyAssignment(ctx, workspaceID, strings.TrimSpace(targetID), strings.ToLower(strings.TrimSpace(targetType)), version, s.now().UTC())
}

func validateAssignmentTarget(id, kind string) error {
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("targetId must be a UUID")
	}
	switch kind {
	case "account", "profile", "browser_instance":
		return nil
	default:
		return errors.New("targetType must be account, profile, or browser_instance")
	}
}

func (s *Service) RequestHealthCheck(ctx context.Context, actorID, workspaceID, proxyID string) (HealthCheck, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyManage); err != nil {
		return HealthCheck{}, err
	}
	proxy, err := s.repository.FindProxy(ctx, workspaceID, proxyID)
	if err != nil {
		return HealthCheck{}, err
	}
	now := s.now().UTC()
	check := HealthCheck{ID: uuid.NewString(), WorkspaceID: workspaceID, ProxyID: proxyID, RequestID: uuid.NewString(), ConnectorType: proxy.ConnectorType, Kernel: proxy.Kernel, Status: "queued", CreatedBy: actorID, CreatedAt: now}
	if err := s.repository.CreateProxyHealthCheck(ctx, check); err != nil {
		return HealthCheck{}, err
	}
	return check, nil
}

func (s *Service) RequestProxyHealthCheck(ctx context.Context, actorID, workspaceID, proxyID string) (HealthCheck, error) {
	return s.RequestHealthCheck(ctx, actorID, workspaceID, proxyID)
}

func (s *Service) GetHealthCheck(ctx context.Context, actorID, workspaceID, checkID string) (HealthCheck, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return HealthCheck{}, err
	}
	return s.repository.FindProxyHealthCheck(ctx, workspaceID, checkID)
}

func (s *Service) ListHealthChecks(ctx context.Context, actorID, workspaceID, proxyID string, limit int) ([]HealthCheck, error) {
	if err := s.require(ctx, workspaceID, actorID, memberservice.PermissionProxyRead); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repository.ListProxyHealthChecks(ctx, workspaceID, proxyID, limit)
}

func (s *Service) CompleteHealthCheck(ctx context.Context, workspaceID, checkID string, result HealthResult) (HealthCheck, error) {
	status := strings.ToLower(strings.TrimSpace(result.Status))
	if status != "succeeded" && status != "failed" {
		return HealthCheck{}, errors.New("health status must be succeeded or failed")
	}
	result.Status = status
	return s.repository.CompleteProxyHealthCheck(ctx, workspaceID, checkID, result, s.now().UTC())
}

func ResolveProxyKernelForConnector(connectorType ConnectorType, protocol string, authenticatedValues ...bool) (Kernel, error) {
	authenticated := len(authenticatedValues) > 0 && authenticatedValues[0]
	connectorType = normalizeConnector(connectorType)
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if connectorType != ConnectorXray && connectorType != ConnectorMihomo {
		return "", ErrUnsupportedRoute
	}
	if protocol == "direct" {
		return KernelDirect, nil
	}
	native := protocol == "http" || protocol == "https" || protocol == "socks5"
	if native && !authenticated {
		return KernelDirect, nil
	}
	if connectorType == ConnectorMihomo {
		switch protocol {
		case "vmess", "vless", "trojan", "shadowsocks", "hysteria", "hysteria2", "tuic", "anytls", "http", "https", "socks5", "wireguard", "snell", "ssr", "shadowtls", "http3", "quic", "naive":
			return KernelMihomo, nil
		default:
			return "", ErrUnsupportedRoute
		}
	}
	if native {
		return KernelXray, nil
	}
	switch protocol {
	case "vmess", "vless", "trojan", "shadowsocks", "chained":
		return KernelXray, nil
	case "hysteria", "hysteria2", "tuic", "anytls":
		return KernelSingBox, nil
	default:
		return "", ErrUnsupportedRoute
	}
}

func (s *Service) buildProxy(ctx context.Context, workspaceID, actorID, name, protocol, host string, port int, username, secretRef string, secret *SecretInput, connector ConnectorType) (Proxy, error) {
	if strings.TrimSpace(secretRef) != "" {
		return Proxy{}, errors.New("secretRef is managed by the secret provider")
	}
	proxy := Proxy{ID: uuid.NewString(), WorkspaceID: strings.TrimSpace(workspaceID), Name: strings.TrimSpace(name), Protocol: strings.ToLower(strings.TrimSpace(protocol)), Host: strings.TrimSpace(host), Port: port, Username: strings.TrimSpace(username), ConnectorType: normalizeConnector(connector), Status: "active", Version: 1, CreatedBy: actorID, CreatedAt: s.now().UTC(), UpdatedAt: s.now().UTC()}
	if secret != nil {
		proxy.HasCredentials = true
	}
	if err := validateProxy(&proxy); err != nil {
		return Proxy{}, err
	}
	if secret != nil {
		ref, err := s.putSecret(ctx, workspaceID, proxy.ID, secret)
		if err != nil {
			return Proxy{}, err
		}
		proxy.SecretRef = ref
	}
	return proxy, nil
}

func (s *Service) putSecret(ctx context.Context, workspaceID, proxyID string, input *SecretInput) (string, error) {
	if s.secrets == nil {
		return "", ErrSecretProvider
	}
	if strings.TrimSpace(input.Password) == "" && strings.TrimSpace(input.Token) == "" {
		return "", errors.New("secret value is required")
	}
	return s.secrets.Put(ctx, workspaceID, proxyID, Secret{Password: input.Password, Token: input.Token})
}

func validateProxy(proxy *Proxy) error {
	if proxy.Name == "" || len([]rune(proxy.Name)) > 120 {
		return errors.New("valid proxy name is required")
	}
	if proxy.Protocol != "direct" {
		if proxy.Host == "" || len(proxy.Host) > 255 {
			return errors.New("valid proxy host is required")
		}
		if proxy.Port < 1 || proxy.Port > 65535 {
			return errors.New("proxy port must be between 1 and 65535")
		}
	}
	if proxy.Status != "active" && proxy.Status != "disabled" {
		return errors.New("proxy status must be active or disabled")
	}
	if proxy.ConnectorType != ConnectorXray && proxy.ConnectorType != ConnectorMihomo {
		return errors.New("connectorType must be xray or mihomo")
	}
	kernel, err := ResolveProxyKernelForConnector(proxy.ConnectorType, proxy.Protocol, proxy.HasCredentials || proxy.Username != "")
	if err != nil {
		return err
	}
	proxy.Kernel = kernel
	return nil
}

func normalizeConnector(value ConnectorType) ConnectorType {
	value = ConnectorType(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		return ConnectorXray
	}
	return value
}

func (s *Service) require(ctx context.Context, workspaceID, actorID string, permission memberservice.Permission) error {
	if s.authorizer == nil {
		return ErrAuthorizer
	}
	return s.authorizer.Require(ctx, workspaceID, actorID, permission)
}
