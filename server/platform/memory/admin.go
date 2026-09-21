package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func (s *Store) IsPlatformAdmin(_ context.Context, userID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.platformAdmins[strings.TrimSpace(userID)]
	user, userExists := s.users[strings.TrimSpace(userID)]
	return ok && userExists && user.Status == "active" && a.Status == "active" && a.Role == adminservice.PlatformAdminRole, nil
}

// SeedPlatformAdmin is a test/bootstrap adapter helper. Production bootstrap
// should insert the first row through a reviewed migration/job.
func (s *Store) SeedPlatformAdmin(admin adminservice.PlatformAdmin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if admin.Status == "" {
		admin.Status = "active"
	}
	if admin.Role == "" {
		admin.Role = adminservice.PlatformAdminRole
	}
	s.platformAdmins[admin.UserID] = admin
}

func (s *Store) GrantPlatformAdmin(_ context.Context, _ string, admin adminservice.PlatformAdmin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[admin.UserID]
	if !ok {
		return adminservice.ErrNotFound
	}
	if user.Status != "active" {
		return authservice.ErrUserDisabled
	}
	if admin.Role != adminservice.PlatformAdminRole {
		return adminservice.ErrInvalidRole
	}
	if existing, exists := s.platformAdmins[admin.UserID]; exists {
		existing.Status, existing.Role, existing.GrantedBy, existing.UpdatedAt, existing.RevokedAt = "active", admin.Role, admin.GrantedBy, admin.UpdatedAt, nil
		s.platformAdmins[admin.UserID] = existing
		return nil
	}
	s.platformAdmins[admin.UserID] = admin
	return nil
}

func (s *Store) RevokePlatformAdmin(_ context.Context, _ string, targetID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.platformAdmins[targetID]
	if !ok || a.Status != "active" {
		return adminservice.ErrNotFound
	}
	active := 0
	for _, item := range s.platformAdmins {
		if item.Status == "active" {
			active++
		}
	}
	if active <= 1 {
		return adminservice.ErrLastPlatformAdmin
	}
	a.Status, a.UpdatedAt, a.RevokedAt = "revoked", now, &now
	s.platformAdmins[targetID] = a
	return nil
}

func (s *Store) ListAdminUsers(_ context.Context, _ string, options adminservice.ListOptions) (adminservice.Page[adminservice.User], error) {
	options = normalizeAdminOptions(options)
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminservice.User, 0)
	q := strings.ToLower(strings.TrimSpace(options.Query))
	for _, value := range s.users {
		if options.Status != "" && value.Status != options.Status {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(value.Email), q) && !strings.Contains(strings.ToLower(value.DisplayName), q) {
			continue
		}
		items = append(items, adminUser(value))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return pageUsers(items, options), nil
}

func (s *Store) ListAdminOrganizations(_ context.Context, _ string, options adminservice.ListOptions) (adminservice.Page[adminservice.Organization], error) {
	options = normalizeAdminOptions(options)
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminservice.Organization, 0)
	q := strings.ToLower(strings.TrimSpace(options.Query))
	for _, value := range s.organizations {
		status := value.Status
		if status == "" {
			status = "active"
		}
		if options.Status != "" && status != options.Status {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(value.Name), q) && !strings.Contains(strings.ToLower(value.Slug), q) {
			continue
		}
		items = append(items, adminOrganization(value))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return pageOrganizations(items, options), nil
}

func (s *Store) ListAdminWorkspaces(_ context.Context, _ string, options adminservice.ListOptions) (adminservice.Page[adminservice.Workspace], error) {
	options = normalizeAdminOptions(options)
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]adminservice.Workspace, 0)
	q := strings.ToLower(strings.TrimSpace(options.Query))
	for _, value := range s.workspaces {
		if options.Status != "" && value.Status != options.Status {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(value.Name), q) && !strings.Contains(strings.ToLower(value.Slug), q) {
			continue
		}
		items = append(items, adminWorkspace(value, s.organizations[value.OrganizationID]))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return pageWorkspaces(items, options), nil
}

func (s *Store) SetUserStatus(_ context.Context, _ string, targetID, status string, now time.Time) (adminservice.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.users[targetID]
	if !ok || value.Status == "deleted" {
		return adminservice.User{}, adminservice.ErrNotFound
	}
	value.Status, value.UpdatedAt = status, now
	s.users[targetID] = value
	if status == adminservice.UserStatusSuspended {
		for id, session := range s.sessions {
			if session.UserID != targetID || session.RevokedAt != nil {
				continue
			}
			session.RevokedAt = &now
			session.RevokeReason = "admin_user_suspended"
			s.sessions[id] = session
		}
		for hash, token := range s.refresh {
			session, exists := s.sessions[token.SessionID]
			if exists && session.UserID == targetID && token.RevokedAt == nil {
				token.RevokedAt = &now
				s.refresh[hash] = token
			}
		}
	}
	return adminUser(value), nil
}

func (s *Store) SetOrganizationStatus(_ context.Context, _ string, targetID, status string, now time.Time) (adminservice.Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.organizations[targetID]
	if !ok {
		return adminservice.Organization{}, adminservice.ErrNotFound
	}
	value.Status, value.UpdatedAt = status, now
	s.organizations[targetID] = value
	return adminOrganization(value), nil
}

func (s *Store) AppendAdminAudit(_ context.Context, event adminservice.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	s.adminAudit = append(s.adminAudit, event)
	return nil
}

func (s *Store) AdminAuditEvents() []adminservice.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]adminservice.AuditEvent(nil), s.adminAudit...)
}

func adminUser(value authservice.User) adminservice.User {
	return adminservice.User{ID: value.ID, Email: value.Email, DisplayName: value.DisplayName, Status: value.Status, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
func adminOrganization(value workspaceservice.Organization) adminservice.Organization {
	status := value.Status
	if status == "" {
		status = "active"
	}
	return adminservice.Organization{ID: value.ID, Name: value.Name, Slug: value.Slug, Status: status, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
func adminWorkspace(value workspaceservice.Workspace, org workspaceservice.Organization) adminservice.Workspace {
	return adminservice.Workspace{ID: value.ID, OrganizationID: value.OrganizationID, OrganizationName: org.Name, Name: value.Name, Slug: value.Slug, Status: value.Status, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, DeletedAt: value.DeletedAt}
}

func pageBounds(total, limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return start, end
}
func pageUsers(items []adminservice.User, o adminservice.ListOptions) adminservice.Page[adminservice.User] {
	start, end := pageBounds(len(items), o.Limit, o.Offset)
	return adminservice.Page[adminservice.User]{Items: items[start:end], Total: len(items), Limit: o.Limit, Offset: o.Offset}
}
func pageOrganizations(items []adminservice.Organization, o adminservice.ListOptions) adminservice.Page[adminservice.Organization] {
	start, end := pageBounds(len(items), o.Limit, o.Offset)
	return adminservice.Page[adminservice.Organization]{Items: items[start:end], Total: len(items), Limit: o.Limit, Offset: o.Offset}
}
func pageWorkspaces(items []adminservice.Workspace, o adminservice.ListOptions) adminservice.Page[adminservice.Workspace] {
	start, end := pageBounds(len(items), o.Limit, o.Offset)
	return adminservice.Page[adminservice.Workspace]{Items: items[start:end], Total: len(items), Limit: o.Limit, Offset: o.Offset}
}

func normalizeAdminOptions(o adminservice.ListOptions) adminservice.ListOptions {
	o.Query = strings.TrimSpace(o.Query)
	o.Status = strings.ToLower(strings.TrimSpace(o.Status))
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 50
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
	return o
}
