// Package adminservice contains the deliberately small platform-admin surface.
// Platform administrators are separate from organization/workspace members.
// Every method takes the actor id explicitly so an HTTP caller cannot smuggle
// a tenant or system-operation value into a repository call.
package adminservice

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	PlatformAdminRole           = "platform_admin"
	UserStatusActive            = "active"
	UserStatusSuspended         = "suspended"
	OrganizationStatusActive    = "active"
	OrganizationStatusSuspended = "suspended"
)

var (
	ErrForbidden         = errors.New("platform admin permission denied")
	ErrNotFound          = errors.New("admin resource not found")
	ErrInvalidStatus     = errors.New("invalid admin status")
	ErrInvalidRole       = errors.New("invalid platform admin role")
	ErrSelfModification  = errors.New("platform admin cannot modify itself")
	ErrLastPlatformAdmin = errors.New("at least one active platform admin is required")
)

type PlatformAdmin struct {
	UserID    string     `json:"userId"`
	Role      string     `json:"role"`
	Status    string     `json:"status"`
	GrantedBy string     `json:"grantedBy"`
	GrantedAt time.Time  `json:"grantedAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// User is a password-free projection intended for admin responses.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Organization struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Status      string    `json:"status"`
	OwnerUserID string    `json:"ownerUserId"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Workspace struct {
	ID               string     `json:"id"`
	OrganizationID   string     `json:"organizationId"`
	OrganizationName string     `json:"organizationName,omitempty"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	Status           string     `json:"status"`
	Version          int64      `json:"version"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DeletedAt        *time.Time `json:"deletedAt,omitempty"`
}

type ListOptions struct {
	Query  string `json:"query,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

func (o ListOptions) normalized() ListOptions {
	o.Query = strings.TrimSpace(o.Query)
	o.Status = strings.TrimSpace(strings.ToLower(o.Status))
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 50
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
	return o
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type AuditEvent struct {
	OrganizationID string         `json:"organizationId,omitempty"`
	WorkspaceID    string         `json:"workspaceId,omitempty"`
	ActorUserID    string         `json:"actorUserId"`
	Action         string         `json:"action"`
	ResourceType   string         `json:"resourceType"`
	ResourceID     string         `json:"resourceId,omitempty"`
	Outcome        string         `json:"outcome"`
	RequestID      string         `json:"requestId,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
}

// Repository is intentionally admin-specific. It has no generic cross-tenant
// query method. PostgreSQL implementations must run these calls under their
// fixed admin system-operation transaction.
type Repository interface {
	IsPlatformAdmin(context.Context, string) (bool, error)
	GrantPlatformAdmin(context.Context, string, PlatformAdmin) error
	RevokePlatformAdmin(context.Context, string, string, time.Time) error
	ListAdminUsers(context.Context, string, ListOptions) (Page[User], error)
	ListAdminOrganizations(context.Context, string, ListOptions) (Page[Organization], error)
	ListAdminWorkspaces(context.Context, string, ListOptions) (Page[Workspace], error)
	SetUserStatus(context.Context, string, string, string, time.Time) (User, error)
	SetOrganizationStatus(context.Context, string, string, string, time.Time) (Organization, error)
	AppendAdminAudit(context.Context, AuditEvent) error
}

type Service struct {
	repository Repository
	now        func() time.Time
}

type requestIDContextKey struct{}

// WithRequestID lets transport adapters attach their correlation id without
// coupling the admin domain service to a specific HTTP package.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, strings.TrimSpace(requestID))
}

func New(repository Repository) *Service { return &Service{repository: repository, now: time.Now} }

func (s *Service) requireAdmin(ctx context.Context, actorID string) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrForbidden
	}
	ok, err := s.repository.IsPlatformAdmin(ctx, actorID)
	if err != nil || !ok {
		return ErrForbidden
	}
	return nil
}

func (s *Service) GrantPlatformAdmin(ctx context.Context, actorID, targetUserID string, requestedRole ...string) (PlatformAdmin, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "platform_admin.grant", targetUserID, err)
		return PlatformAdmin{}, err
	}
	if strings.TrimSpace(targetUserID) == "" {
		return PlatformAdmin{}, ErrNotFound
	}
	role := PlatformAdminRole
	if len(requestedRole) > 0 && strings.TrimSpace(requestedRole[0]) != "" {
		role = strings.ToLower(strings.TrimSpace(requestedRole[0]))
	}
	if role != PlatformAdminRole {
		s.auditDenied(ctx, actorID, "platform_admin.grant", targetUserID, ErrInvalidRole)
		return PlatformAdmin{}, ErrInvalidRole
	}
	now := s.now().UTC()
	admin := PlatformAdmin{UserID: targetUserID, Role: role, Status: "active", GrantedBy: actorID, GrantedAt: now, UpdatedAt: now}
	if err := s.repository.GrantPlatformAdmin(ctx, actorID, admin); err != nil {
		s.auditDenied(ctx, actorID, "platform_admin.grant", targetUserID, err)
		return PlatformAdmin{}, err
	}
	if err := s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "platform_admin.grant", ResourceType: "platform_admin", ResourceID: targetUserID, Outcome: "success", CreatedAt: now}); err != nil {
		return PlatformAdmin{}, err
	}
	return admin, nil
}

// AuthorizePlatformAdmin is a descriptive alias for integrations whose API
// calls the grant operation "authorize".
func (s *Service) AuthorizePlatformAdmin(ctx context.Context, actorID, targetUserID string, requestedRole ...string) (PlatformAdmin, error) {
	return s.GrantPlatformAdmin(ctx, actorID, targetUserID, requestedRole...)
}

func (s *Service) RevokePlatformAdmin(ctx context.Context, actorID, targetUserID string, _ ...string) error {
	if actorID == targetUserID {
		s.auditDenied(ctx, actorID, "platform_admin.revoke", targetUserID, ErrSelfModification)
		return ErrSelfModification
	}
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "platform_admin.revoke", targetUserID, err)
		return err
	}
	now := s.now().UTC()
	if err := s.repository.RevokePlatformAdmin(ctx, actorID, targetUserID, now); err != nil {
		s.auditDenied(ctx, actorID, "platform_admin.revoke", targetUserID, err)
		return err
	}
	return s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "platform_admin.revoke", ResourceType: "platform_admin", ResourceID: targetUserID, Outcome: "success", CreatedAt: now})
}

func (s *Service) ListUsers(ctx context.Context, actorID string, options ListOptions) (Page[User], error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "admin.users.list", "", err)
		return Page[User]{}, err
	}
	page, err := s.repository.ListAdminUsers(ctx, actorID, options.normalized())
	if err != nil {
		return page, err
	}
	return page, s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "admin.users.list", ResourceType: "user", Outcome: "success", CreatedAt: s.now().UTC()})
}

func (s *Service) ListOrganizations(ctx context.Context, actorID string, options ListOptions) (Page[Organization], error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "admin.organizations.list", "", err)
		return Page[Organization]{}, err
	}
	page, err := s.repository.ListAdminOrganizations(ctx, actorID, options.normalized())
	if err != nil {
		return page, err
	}
	return page, s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "admin.organizations.list", ResourceType: "organization", Outcome: "success", CreatedAt: s.now().UTC()})
}

func (s *Service) ListWorkspaces(ctx context.Context, actorID string, options ListOptions) (Page[Workspace], error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "admin.workspaces.list", "", err)
		return Page[Workspace]{}, err
	}
	page, err := s.repository.ListAdminWorkspaces(ctx, actorID, options.normalized())
	if err != nil {
		return page, err
	}
	return page, s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "admin.workspaces.list", ResourceType: "workspace", Outcome: "success", CreatedAt: s.now().UTC()})
}

func (s *Service) SetUserStatus(ctx context.Context, actorID, targetUserID, status string, reason ...string) (User, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != UserStatusActive && status != UserStatusSuspended {
		return User{}, ErrInvalidStatus
	}
	if actorID == targetUserID {
		s.auditDenied(ctx, actorID, "admin.user.status", targetUserID, ErrSelfModification)
		return User{}, ErrSelfModification
	}
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "admin.user.status", targetUserID, err)
		return User{}, err
	}
	now := s.now().UTC()
	user, err := s.repository.SetUserStatus(ctx, actorID, targetUserID, status, now)
	if err != nil {
		s.auditDenied(ctx, actorID, "admin.user.status", targetUserID, err)
		return User{}, err
	}
	metadata := map[string]any{"status": status}
	if len(reason) > 0 && strings.TrimSpace(reason[0]) != "" {
		metadata["reason"] = strings.TrimSpace(reason[0])
	}
	err = s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "admin.user.status", ResourceType: "user", ResourceID: targetUserID, Outcome: "success", Metadata: metadata, CreatedAt: now})
	return user, err
}

func (s *Service) DisableUser(ctx context.Context, actorID, targetUserID string, reason ...string) (User, error) {
	return s.SetUserStatus(ctx, actorID, targetUserID, UserStatusSuspended, reason...)
}

func (s *Service) RestoreUser(ctx context.Context, actorID, targetUserID string, reason ...string) (User, error) {
	return s.SetUserStatus(ctx, actorID, targetUserID, UserStatusActive, reason...)
}

func (s *Service) SetOrganizationStatus(ctx context.Context, actorID, organizationID, status string, reason ...string) (Organization, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != OrganizationStatusActive && status != OrganizationStatusSuspended {
		return Organization{}, ErrInvalidStatus
	}
	if err := s.requireAdmin(ctx, actorID); err != nil {
		s.auditDenied(ctx, actorID, "admin.organization.status", organizationID, err)
		return Organization{}, err
	}
	now := s.now().UTC()
	org, err := s.repository.SetOrganizationStatus(ctx, actorID, organizationID, status, now)
	if err != nil {
		s.auditDenied(ctx, actorID, "admin.organization.status", organizationID, err)
		return Organization{}, err
	}
	metadata := map[string]any{"status": status}
	if len(reason) > 0 && strings.TrimSpace(reason[0]) != "" {
		metadata["reason"] = strings.TrimSpace(reason[0])
	}
	err = s.audit(ctx, AuditEvent{ActorUserID: actorID, Action: "admin.organization.status", ResourceType: "organization", ResourceID: organizationID, Outcome: "success", Metadata: metadata, CreatedAt: now})
	return org, err
}

func (s *Service) DisableOrganization(ctx context.Context, actorID, organizationID string, reason ...string) (Organization, error) {
	return s.SetOrganizationStatus(ctx, actorID, organizationID, OrganizationStatusSuspended, reason...)
}

func (s *Service) RestoreOrganization(ctx context.Context, actorID, organizationID string, reason ...string) (Organization, error) {
	return s.SetOrganizationStatus(ctx, actorID, organizationID, OrganizationStatusActive, reason...)
}

func (s *Service) audit(ctx context.Context, event AuditEvent) error {
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now().UTC()
	}
	if event.RequestID == "" {
		event.RequestID, _ = ctx.Value(requestIDContextKey{}).(string)
	}
	return s.repository.AppendAdminAudit(ctx, event)
}

func (s *Service) auditDenied(ctx context.Context, actor, action, resource string, cause error) {
	_ = s.audit(ctx, AuditEvent{ActorUserID: actor, Action: action, ResourceType: "admin", ResourceID: resource, Outcome: "denied", Metadata: map[string]any{"error": cause.Error()}, CreatedAt: s.now().UTC()})
}
