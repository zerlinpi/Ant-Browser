package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

const workspaceColumns = `
	w.id::text, w.organization_id::text, w.name, w.slug, w.status,
	w.version, w.created_at, w.updated_at, w.deleted_at`

func (s *Store) CreateOrganizationWorkspace(ctx context.Context, organization workspaceservice.Organization, workspace workspaceservice.Workspace, membership workspaceservice.Membership) error {
	if organization.Status == "" {
		organization.Status = "active"
	}
	if workspace.Status == "" {
		workspace.Status = "active"
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `
		INSERT INTO organizations (id, name, slug, owner_user_id, status, version, created_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6, $7, $8)
	`, organization.ID, organization.Name, organization.Slug, membership.UserID, organization.Status, organization.Version, organization.CreatedAt, organization.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return workspaceservice.ErrInvalidWorkspace
		}
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role_id, joined_at, created_at)
		SELECT $1::uuid, $2::uuid, id, $3, $3 FROM roles WHERE code = 'owner'
	`, organization.ID, membership.UserID, membership.JoinedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO workspaces (
			id, organization_id, name, slug, status, version, created_by,
			created_at, updated_at, deleted_at
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9, $10)
	`, workspace.ID, workspace.OrganizationID, workspace.Name, workspace.Slug, workspace.Status, workspace.Version, membership.UserID, workspace.CreatedAt, workspace.UpdatedAt, workspace.DeletedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO workspace_members (
			id, workspace_id, user_id, role_id, status, joined_at, created_at, updated_at
		)
		SELECT $1::uuid, $2::uuid, $3::uuid, id, $4, $5, $5, $6
		FROM roles WHERE code = $7
	`, membership.ID, membership.WorkspaceID, membership.UserID, membership.Status, membership.JoinedAt, membership.UpdatedAt, string(membership.Role))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListWorkspaces(ctx context.Context, userID string) ([]workspaceservice.Workspace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+workspaceColumns+`
		FROM workspaces w
		JOIN organizations o ON o.id = w.organization_id
		JOIN workspace_members wm ON wm.workspace_id = w.id
		WHERE wm.user_id = $1::uuid AND wm.status = 'active'
		  AND w.status = 'active' AND o.status = 'active' AND w.deleted_at IS NULL
		ORDER BY w.created_at, w.id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]workspaceservice.Workspace, 0)
	for rows.Next() {
		workspace, scanErr := scanWorkspace(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, workspace)
	}
	return items, rows.Err()
}

func (s *Store) FindWorkspace(ctx context.Context, id string) (workspaceservice.Workspace, error) {
	var workspace workspaceservice.Workspace
	err := s.withWorkspaceTx(ctx, id, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `
		SELECT `+workspaceColumns+`
			FROM workspaces w
			JOIN organizations o ON o.id = w.organization_id
			WHERE w.id = $1::uuid AND w.status = 'active' AND o.status = 'active' AND w.deleted_at IS NULL
		`, id))
		return err
	})
	return workspace, err
}

func scanWorkspace(row scanner) (workspaceservice.Workspace, error) {
	var workspace workspaceservice.Workspace
	if err := row.Scan(
		&workspace.ID, &workspace.OrganizationID, &workspace.Name, &workspace.Slug,
		&workspace.Status, &workspace.Version, &workspace.CreatedAt, &workspace.UpdatedAt, &workspace.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceservice.Workspace{}, workspaceservice.ErrNotFound
		}
		return workspaceservice.Workspace{}, err
	}
	return workspace, nil
}

func (s *Store) UpdateWorkspace(ctx context.Context, workspace workspaceservice.Workspace, expectedVersion int64) (workspaceservice.Workspace, error) {
	var updated workspaceservice.Workspace
	err := s.withWorkspaceTx(ctx, workspace.ID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		updated, err = scanWorkspace(tx.QueryRow(ctx, `
			UPDATE workspaces AS w
			SET name = $3, slug = $4, version = version + 1, updated_at = $5
			WHERE w.id = $1::uuid AND w.version = $2 AND w.status = 'active' AND w.deleted_at IS NULL
			  AND EXISTS (SELECT 1 FROM organizations o WHERE o.id = w.organization_id AND o.status = 'active')
			RETURNING `+workspaceColumns+`
		`, workspace.ID, expectedVersion, workspace.Name, workspace.Slug, workspace.UpdatedAt))
		return err
	})
	if errors.Is(err, workspaceservice.ErrNotFound) {
		if _, findErr := s.FindWorkspace(ctx, workspace.ID); findErr == nil {
			return workspaceservice.Workspace{}, workspaceservice.ErrVersionConflict
		}
	}
	if isUniqueViolation(err) {
		return workspaceservice.Workspace{}, workspaceservice.ErrInvalidWorkspace
	}
	return updated, err
}

func (s *Store) FindMembership(ctx context.Context, workspaceID, userID string) (workspaceservice.Membership, error) {
	var membership workspaceservice.Membership
	err := s.withWorkspaceTx(ctx, workspaceID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		membership, err = scanMembership(tx.QueryRow(ctx, `
		SELECT wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		       wm.status, wm.joined_at, wm.updated_at
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid
		`, workspaceID, userID))
		return err
	})
	return membership, err
}

func (s *Store) FindOrganizationMembership(ctx context.Context, organizationID, userID string) (workspaceservice.OrganizationMembership, error) {
	var membership workspaceservice.OrganizationMembership
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT w.organization_id::text, wm.user_id::text, r.code, 'active'
		FROM workspace_members wm
		JOIN workspaces w ON w.id = wm.workspace_id
		JOIN organizations o ON o.id = w.organization_id
		JOIN users u ON u.id = wm.user_id
		JOIN roles r ON r.id = wm.role_id
		WHERE w.organization_id = $1::uuid AND wm.user_id = $2::uuid
		  AND wm.status = 'active' AND w.status = 'active' AND w.deleted_at IS NULL
		  AND o.status = 'active' AND u.status = 'active' AND u.deleted_at IS NULL
		ORDER BY CASE r.code
			WHEN 'owner' THEN 5 WHEN 'admin' THEN 4 WHEN 'manager' THEN 3
			WHEN 'operator' THEN 2 WHEN 'viewer' THEN 1 ELSE 0 END DESC
		LIMIT 1
	`, organizationID, userID).Scan(&membership.OrganizationID, &membership.UserID, &role, &membership.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspaceservice.OrganizationMembership{}, workspaceservice.ErrNotFound
	}
	if err != nil {
		return workspaceservice.OrganizationMembership{}, err
	}
	membership.Role = memberservice.Role(role)
	return membership, nil
}

func (s *Store) ListMembers(ctx context.Context, workspaceID string) ([]workspaceservice.Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		       wm.status, wm.joined_at, wm.updated_at
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.status = 'active'
		ORDER BY wm.joined_at, wm.user_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]workspaceservice.Membership, 0)
	for rows.Next() {
		membership, scanErr := scanMembership(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, membership)
	}
	return items, rows.Err()
}

func scanMembership(row scanner) (workspaceservice.Membership, error) {
	var membership workspaceservice.Membership
	var role string
	if err := row.Scan(
		&membership.ID, &membership.WorkspaceID, &membership.UserID, &role,
		&membership.Status, &membership.JoinedAt, &membership.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceservice.Membership{}, workspaceservice.ErrNotFound
		}
		return workspaceservice.Membership{}, err
	}
	membership.Role = memberservice.Role(role)
	return membership, nil
}

func (s *Store) AddMember(ctx context.Context, membership workspaceservice.Membership) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_members (
			id, workspace_id, user_id, role_id, status, joined_at, created_at, updated_at
		)
		SELECT $1::uuid, $2::uuid, $3::uuid, id, $4, $5, $5, $6
		FROM roles WHERE code = $7
	`, membership.ID, membership.WorkspaceID, membership.UserID, membership.Status, membership.JoinedAt, membership.UpdatedAt, string(membership.Role))
	if isUniqueViolation(err) {
		return workspaceservice.ErrMemberExists
	}
	if isForeignKeyViolation(err) {
		return workspaceservice.ErrNotFound
	}
	if isBillingQuotaViolation(err) {
		return billingservice.ErrQuotaExceeded
	}
	return err
}

func (s *Store) UpdateMemberRole(ctx context.Context, workspaceID, userID string, role memberservice.Role) (workspaceservice.Membership, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workspaceservice.Membership{}, err
	}
	defer rollback(ctx, tx)
	current, err := scanMembership(tx.QueryRow(ctx, `
		SELECT wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		       wm.status, wm.joined_at, wm.updated_at
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid
		FOR UPDATE OF wm
	`, workspaceID, userID))
	if err != nil {
		return workspaceservice.Membership{}, err
	}
	if current.Role == memberservice.RoleOwner && role != memberservice.RoleOwner {
		var ownerCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM workspace_members wm JOIN roles r ON r.id = wm.role_id
			WHERE wm.workspace_id = $1::uuid AND wm.status = 'active' AND r.code = 'owner'
		`, workspaceID).Scan(&ownerCount); err != nil {
			return workspaceservice.Membership{}, err
		}
		if ownerCount <= 1 {
			return workspaceservice.Membership{}, workspaceservice.ErrLastOwner
		}
	}
	now := time.Now().UTC()
	updated, err := scanMembership(tx.QueryRow(ctx, `
		UPDATE workspace_members wm
		SET role_id = r.id, updated_at = $4
		FROM roles r
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid AND r.code = $3
		RETURNING wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		          wm.status, wm.joined_at, wm.updated_at
	`, workspaceID, userID, string(role), now))
	if err != nil {
		return workspaceservice.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workspaceservice.Membership{}, err
	}
	return updated, nil
}

func (s *Store) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	current, err := scanMembership(tx.QueryRow(ctx, `
		SELECT wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		       wm.status, wm.joined_at, wm.updated_at
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid
		FOR UPDATE OF wm
	`, workspaceID, userID))
	if err != nil {
		return err
	}
	if current.Role == memberservice.RoleOwner {
		var ownerCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM workspace_members wm JOIN roles r ON r.id = wm.role_id
			WHERE wm.workspace_id = $1::uuid AND wm.status = 'active' AND r.code = 'owner'
		`, workspaceID).Scan(&ownerCount); err != nil {
			return err
		}
		if ownerCount <= 1 {
			return workspaceservice.ErrLastOwner
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE workspace_members SET status = 'removed', updated_at = now()
		WHERE workspace_id = $1::uuid AND user_id = $2::uuid AND status <> 'removed'
	`, workspaceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return workspaceservice.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateInvitation(ctx context.Context, invitation workspaceservice.Invitation, tokenHash string) error {
	err := s.withWorkspaceTx(ctx, invitation.WorkspaceID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workspace_invitations (id, workspace_id, email, role_id, token_hash, status, expires_at, created_at) SELECT $1::uuid,$2::uuid,$3,r.id,$4,'pending',$5,$6 FROM roles r WHERE r.code=$7`, invitation.ID, invitation.WorkspaceID, invitation.Email, tokenHash, invitation.ExpiresAt, invitation.CreatedAt, string(invitation.Role))
		return err
	})
	if isUniqueViolation(err) {
		return workspaceservice.ErrInvitationInvalid
	}
	if isForeignKeyViolation(err) {
		return workspaceservice.ErrNotFound
	}
	return err
}
func (s *Store) ListInvitations(ctx context.Context, workspaceID string) ([]workspaceservice.Invitation, error) {
	rows, err := s.pool.Query(ctx, `SELECT i.id::text,i.workspace_id::text,i.email,r.code,i.status,i.expires_at,i.created_at,i.revoked_at,i.accepted_at FROM workspace_invitations i JOIN roles r ON r.id=i.role_id WHERE i.workspace_id=$1::uuid AND i.status='pending' ORDER BY i.created_at,i.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []workspaceservice.Invitation{}
	for rows.Next() {
		var i workspaceservice.Invitation
		var role string
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.Email, &role, &i.Status, &i.ExpiresAt, &i.CreatedAt, &i.RevokedAt, &i.AcceptedAt); err != nil {
			return nil, err
		}
		i.Role = memberservice.Role(role)
		items = append(items, i)
	}
	return items, rows.Err()
}
func (s *Store) RevokeInvitation(ctx context.Context, workspaceID, id string, now time.Time) error {
	var affected int64
	err := s.withWorkspaceTx(ctx, workspaceID, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `UPDATE workspace_invitations SET status='revoked', revoked_at=$3 WHERE workspace_id=$1::uuid AND id=$2::uuid AND status='pending'`, workspaceID, id, now)
		affected = tag.RowsAffected()
		return e
	})
	if err == nil && affected != 1 {
		return workspaceservice.ErrNotFound
	}
	return err
}
func (s *Store) AcceptInvitation(ctx context.Context, workspaceID, tokenHash string, now time.Time, membership workspaceservice.Membership) (workspaceservice.Membership, error) {
	if membership.WorkspaceID != workspaceID {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationInvalid
	}
	var result workspaceservice.Membership
	err := s.withWorkspaceTx(ctx, workspaceID, func(ctx context.Context, tx pgx.Tx) error {
		// Bind acceptance to a persisted, active identity, not caller-supplied email.
		// Hold the tenant/user rows stable until membership and consumption commit.
		var email string
		err := tx.QueryRow(ctx, `SELECT u.email FROM users u
			JOIN workspaces w ON w.id=$1::uuid
			JOIN organizations o ON o.id=w.organization_id
			WHERE u.id=$2::uuid AND u.status='active' AND w.status='active'
			AND w.deleted_at IS NULL AND o.status='active'
			FOR SHARE OF u,w,o`, workspaceID, membership.UserID).Scan(&email)
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceservice.ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		var role string
		var id string
		var inviteEmail string
		var status string
		var expires time.Time
		err = tx.QueryRow(ctx, `SELECT i.id::text,i.email,r.code,i.status,i.expires_at FROM workspace_invitations i JOIN roles r ON r.id=i.role_id WHERE i.workspace_id=$1::uuid AND i.token_hash=$2 FOR UPDATE OF i`, workspaceID, tokenHash).Scan(&id, &inviteEmail, &role, &status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceservice.ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		if status == "revoked" {
			return workspaceservice.ErrInvitationRevoked
		}
		if status != "pending" {
			return workspaceservice.ErrInvitationUsed
		}
		if !expires.After(now) {
			return workspaceservice.ErrInvitationExpired
		}
		if strings.ToLower(strings.TrimSpace(inviteEmail)) != strings.ToLower(strings.TrimSpace(email)) {
			return workspaceservice.ErrInvitationInvalid
		}
		_, err = tx.Exec(ctx, `INSERT INTO workspace_members(id,workspace_id,user_id,role_id,status,joined_at,created_at,updated_at) SELECT $1::uuid,$2::uuid,$3::uuid,r.id,'active',$4,$4,$4 FROM roles r WHERE r.code=$5`, membership.ID, workspaceID, membership.UserID, membership.JoinedAt, role)
		if err != nil {
			if isUniqueViolation(err) {
				return workspaceservice.ErrMemberExists
			}
			if isBillingQuotaViolation(err) {
				return billingservice.ErrQuotaExceeded
			}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE workspace_invitations SET status='accepted',accepted_at=$2 WHERE id=$1::uuid`, id, now)
		if err == nil {
			membership.Role = memberservice.Role(role)
			result = membership
		}
		return err
	})
	return result, err
}
