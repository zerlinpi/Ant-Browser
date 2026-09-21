package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
)

// withAdmin is the only entry point used by this adapter. The operation names
// are constants owned by this package, never copied from an HTTP request. RLS
// can therefore distinguish the cross-tenant admin surface from normal tenant
// queries while every call remains tied to the authenticated actor.
func (s *Store) withAdmin(ctx context.Context, actor, operation string, fn func(context.Context, pgx.Tx) error) error {
	return s.WithTenant(ctx, TenantScope{UserID: strings.TrimSpace(actor), SystemOperation: operation}, fn)
}

func (s *Store) IsPlatformAdmin(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := s.withAdmin(ctx, userID, "admin_read", func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform_admins pa JOIN users u ON u.id=pa.user_id WHERE pa.user_id=$1::uuid AND pa.status='active' AND pa.role='platform_admin' AND u.status='active' AND u.deleted_at IS NULL)`, userID).Scan(&ok)
	})
	return ok, err
}

func (s *Store) GrantPlatformAdmin(ctx context.Context, actor string, admin adminservice.PlatformAdmin) error {
	return s.withAdmin(ctx, actor, "admin_mutation", func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1::uuid AND deleted_at IS NULL`, admin.UserID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return adminservice.ErrNotFound
			}
			return err
		}
		if status != "active" {
			return fmt.Errorf("%w: target user is not active", adminservice.ErrForbidden)
		}
		if admin.Role != adminservice.PlatformAdminRole {
			return adminservice.ErrInvalidRole
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO platform_admins (user_id, role, status, granted_by, granted_at, updated_at, revoked_at)
			VALUES ($1::uuid, $2, 'active', $3::uuid, $4, $4, NULL)
			ON CONFLICT (user_id) DO UPDATE SET role=EXCLUDED.role, status='active', granted_by=EXCLUDED.granted_by, updated_at=EXCLUDED.updated_at, revoked_at=NULL
		`, admin.UserID, admin.Role, admin.GrantedBy, admin.GrantedAt)
		return err
	})
}

func (s *Store) RevokePlatformAdmin(ctx context.Context, actor, target string, now time.Time) error {
	return s.withAdmin(ctx, actor, "admin_mutation", func(ctx context.Context, tx pgx.Tx) error {
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform_admins WHERE status='active'`).Scan(&active); err != nil {
			return err
		}
		if active <= 1 {
			return adminservice.ErrLastPlatformAdmin
		}
		tag, err := tx.Exec(ctx, `UPDATE platform_admins SET status='revoked', revoked_at=COALESCE(revoked_at,$2), updated_at=$2 WHERE user_id=$1::uuid AND status='active'`, target, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return adminservice.ErrNotFound
		}
		return nil
	})
}

func (s *Store) ListAdminUsers(ctx context.Context, actor string, options adminservice.ListOptions) (adminservice.Page[adminservice.User], error) {
	var page adminservice.Page[adminservice.User]
	err := s.withAdmin(ctx, actor, "admin_read", func(ctx context.Context, tx pgx.Tx) error {
		opts := normalizeAdminOptions(options)
		page.Limit, page.Offset = opts.Limit, opts.Offset
		pattern := "%" + opts.Query + "%"
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL AND ($1='' OR status=$1) AND ($2='' OR email ILIKE $3 OR display_name ILIKE $3)`, opts.Status, opts.Query, pattern).Scan(&page.Total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id::text,email,display_name,status,created_at,updated_at FROM users WHERE deleted_at IS NULL AND ($1='' OR status=$1) AND ($2='' OR email ILIKE $3 OR display_name ILIKE $3) ORDER BY created_at,id LIMIT $4 OFFSET $5`, opts.Status, opts.Query, pattern, opts.Limit, opts.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		page.Items = make([]adminservice.User, 0)
		for rows.Next() {
			var item adminservice.User
			if err := rows.Scan(&item.ID, &item.Email, &item.DisplayName, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}

func (s *Store) ListAdminOrganizations(ctx context.Context, actor string, options adminservice.ListOptions) (adminservice.Page[adminservice.Organization], error) {
	var page adminservice.Page[adminservice.Organization]
	err := s.withAdmin(ctx, actor, "admin_read", func(ctx context.Context, tx pgx.Tx) error {
		opts := normalizeAdminOptions(options)
		page.Limit, page.Offset = opts.Limit, opts.Offset
		pattern := "%" + opts.Query + "%"
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE ($1='' OR status=$1) AND ($2='' OR name ILIKE $3 OR slug ILIKE $3)`, opts.Status, opts.Query, pattern).Scan(&page.Total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id::text,name,slug,status,owner_user_id::text,version,created_at,updated_at FROM organizations WHERE ($1='' OR status=$1) AND ($2='' OR name ILIKE $3 OR slug ILIKE $3) ORDER BY created_at,id LIMIT $4 OFFSET $5`, opts.Status, opts.Query, pattern, opts.Limit, opts.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		page.Items = make([]adminservice.Organization, 0)
		for rows.Next() {
			var item adminservice.Organization
			if err := rows.Scan(&item.ID, &item.Name, &item.Slug, &item.Status, &item.OwnerUserID, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}

func (s *Store) ListAdminWorkspaces(ctx context.Context, actor string, options adminservice.ListOptions) (adminservice.Page[adminservice.Workspace], error) {
	var page adminservice.Page[adminservice.Workspace]
	err := s.withAdmin(ctx, actor, "admin_read", func(ctx context.Context, tx pgx.Tx) error {
		opts := normalizeAdminOptions(options)
		page.Limit, page.Offset = opts.Limit, opts.Offset
		pattern := "%" + opts.Query + "%"
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workspaces w WHERE ($1='' OR w.status=$1) AND ($2='' OR w.name ILIKE $3 OR w.slug ILIKE $3)`, opts.Status, opts.Query, pattern).Scan(&page.Total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT w.id::text,w.organization_id::text,o.name,w.name,w.slug,w.status,w.version,w.created_at,w.updated_at,w.deleted_at FROM workspaces w JOIN organizations o ON o.id=w.organization_id WHERE ($1='' OR w.status=$1) AND ($2='' OR w.name ILIKE $3 OR w.slug ILIKE $3) ORDER BY w.created_at,w.id LIMIT $4 OFFSET $5`, opts.Status, opts.Query, pattern, opts.Limit, opts.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		page.Items = make([]adminservice.Workspace, 0)
		for rows.Next() {
			var item adminservice.Workspace
			if err := rows.Scan(&item.ID, &item.OrganizationID, &item.OrganizationName, &item.Name, &item.Slug, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}

func (s *Store) SetUserStatus(ctx context.Context, actor, target, status string, now time.Time) (adminservice.User, error) {
	var item adminservice.User
	err := s.withAdmin(ctx, actor, "admin_mutation", func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE users SET status=$2,updated_at=$3 WHERE id=$1::uuid AND status <> 'deleted' AND deleted_at IS NULL RETURNING id::text,email,display_name,status,created_at,updated_at`, target, status, now).Scan(&item.ID, &item.Email, &item.DisplayName, &item.Status, &item.CreatedAt, &item.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return adminservice.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == adminservice.UserStatusSuspended {
			_, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,$2), revoke_reason='admin_user_suspended' WHERE user_id=$1::uuid`, target, now)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id IN (SELECT id FROM sessions WHERE user_id=$1::uuid)`, target, now)
		}
		return err
	})
	return item, err
}

func (s *Store) SetOrganizationStatus(ctx context.Context, actor, target, status string, now time.Time) (adminservice.Organization, error) {
	var item adminservice.Organization
	err := s.withAdmin(ctx, actor, "admin_mutation", func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE organizations SET status=$2,updated_at=$3 WHERE id=$1::uuid RETURNING id::text,name,slug,status,owner_user_id::text,version,created_at,updated_at`, target, status, now).Scan(&item.ID, &item.Name, &item.Slug, &item.Status, &item.OwnerUserID, &item.Version, &item.CreatedAt, &item.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return adminservice.ErrNotFound
		}
		return err
	})
	return item, err
}

func (s *Store) AppendAdminAudit(ctx context.Context, event adminservice.AuditEvent) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return s.withAdmin(ctx, event.ActorUserID, "admin_audit", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_events (organization_id,workspace_id,actor_user_id,action,resource_type,resource_id,outcome,request_id,metadata,created_at) VALUES (NULLIF($1,'')::uuid,NULLIF($2,'')::uuid,$3::uuid,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9::jsonb,$10)`, event.OrganizationID, event.WorkspaceID, event.ActorUserID, event.Action, event.ResourceType, event.ResourceID, event.Outcome, event.RequestID, metadata, event.CreatedAt)
		return err
	})
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
