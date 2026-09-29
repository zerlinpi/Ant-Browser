package postgres

import (
	"context"
	"errors"
	"sort"
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

// ListWorkspaces returns the user's active workspaces. Role is the user's own
// membership role, read from the same membership row that grants visibility.
func (s *Store) ListWorkspaces(ctx context.Context, userID string) ([]workspaceservice.Workspace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+workspaceColumns+`, r.code
		FROM workspaces w
		JOIN organizations o ON o.id = w.organization_id
		JOIN workspace_members wm ON wm.workspace_id = w.id
		JOIN roles r ON r.id = wm.role_id
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
		var workspace workspaceservice.Workspace
		var role string
		if scanErr := rows.Scan(
			&workspace.ID, &workspace.OrganizationID, &workspace.Name, &workspace.Slug,
			&workspace.Status, &workspace.Version, &workspace.CreatedAt, &workspace.UpdatedAt, &workspace.DeletedAt,
			&role,
		); scanErr != nil {
			return nil, scanErr
		}
		workspace.Role = memberservice.Role(role)
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

// memberProfileColumns extends the membership projection with the member's
// user profile; queries using it must join users as u.
const memberProfileColumns = `
	wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
	wm.status, wm.joined_at, wm.updated_at, u.email, u.display_name`

func (s *Store) ListMembers(ctx context.Context, workspaceID string) ([]workspaceservice.Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+memberProfileColumns+`
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		JOIN users u ON u.id = wm.user_id
		WHERE wm.workspace_id = $1::uuid AND wm.status = 'active'
		ORDER BY wm.joined_at, wm.user_id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]workspaceservice.Membership, 0)
	for rows.Next() {
		membership, scanErr := scanMemberProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, membership)
	}
	return items, rows.Err()
}

func scanMemberProfile(row scanner) (workspaceservice.Membership, error) {
	var membership workspaceservice.Membership
	var role string
	if err := row.Scan(
		&membership.ID, &membership.WorkspaceID, &membership.UserID, &role,
		&membership.Status, &membership.JoinedAt, &membership.UpdatedAt,
		&membership.Email, &membership.DisplayName,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaceservice.Membership{}, workspaceservice.ErrNotFound
		}
		return workspaceservice.Membership{}, err
	}
	membership.Role = memberservice.Role(role)
	return membership, nil
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

// AddMember inserts a membership. A previously removed membership of the same
// user is re-activated in place (the row is kept because notifications and
// preferences reference it); an active or invited one is a conflict. The
// team-seat trigger fires for both the insert and the re-activation.
func (s *Store) AddMember(ctx context.Context, membership workspaceservice.Membership) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO workspace_members (
			id, workspace_id, user_id, role_id, status, joined_at, created_at, updated_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, (SELECT r.id FROM roles r WHERE r.code = $7),
			$4, $5, $5, $6
		)
		ON CONFLICT (workspace_id, user_id) DO UPDATE
		SET id = EXCLUDED.id, role_id = EXCLUDED.role_id, status = EXCLUDED.status,
		    joined_at = EXCLUDED.joined_at, updated_at = EXCLUDED.updated_at
		WHERE workspace_members.status = 'removed'
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
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// The conflicting membership is not removed, so the upsert was a no-op.
		return workspaceservice.ErrMemberExists
	}
	return nil
}

// UpdateMemberRole changes the role of an active membership. Demoting the
// last active owner fails with ErrLastOwner; SERIALIZABLE isolation keeps two
// concurrent owner demotions from both passing the owner count.
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
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid AND wm.status = 'active'
		FOR UPDATE OF wm
	`, workspaceID, userID))
	if err != nil {
		return workspaceservice.Membership{}, err
	}
	if current.Role == memberservice.RoleOwner && role != memberservice.RoleOwner {
		if err := requireAnotherOwner(ctx, tx, workspaceID); err != nil {
			return workspaceservice.Membership{}, err
		}
	}
	now := time.Now().UTC()
	updated, err := scanMemberProfile(tx.QueryRow(ctx, `
		UPDATE workspace_members wm
		SET role_id = r.id, updated_at = $4
		FROM roles r, users u
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid AND wm.status = 'active'
		  AND r.code = $3 AND u.id = wm.user_id
		RETURNING `+memberProfileColumns+`
	`, workspaceID, userID, string(role), now))
	if err != nil {
		return workspaceservice.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workspaceservice.Membership{}, err
	}
	return updated, nil
}

// RemoveMember marks an active membership removed and, in the same
// transaction, revokes the member's devices and all of their credentials in
// the workspace, so the member's agents stop authenticating at commit.
func (s *Store) RemoveMember(ctx context.Context, workspaceID, userID string, now time.Time) (workspaceservice.MemberRemoval, error) {
	// Device rows are protected by RLS. Bind the workspace scope explicitly so
	// revocation can never silently match zero rows because a caller omitted
	// the request tenant scope; a conflicting scope fails the transaction.
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	defer rollback(ctx, tx)
	current, err := scanMembership(tx.QueryRow(ctx, `
		SELECT wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		       wm.status, wm.joined_at, wm.updated_at
		FROM workspace_members wm
		JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid AND wm.status = 'active'
		FOR UPDATE OF wm
	`, workspaceID, userID))
	if err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	if current.Role == memberservice.RoleOwner {
		if err := requireAnotherOwner(ctx, tx, workspaceID); err != nil {
			return workspaceservice.MemberRemoval{}, err
		}
	}
	removed, err := scanMembership(tx.QueryRow(ctx, `
		UPDATE workspace_members wm
		SET status = 'removed', updated_at = $3
		FROM roles r
		WHERE wm.workspace_id = $1::uuid AND wm.user_id = $2::uuid AND wm.status = 'active'
		  AND r.id = wm.role_id
		RETURNING wm.id::text, wm.workspace_id::text, wm.user_id::text, r.code,
		          wm.status, wm.joined_at, wm.updated_at
	`, workspaceID, userID, now))
	if err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	rows, err := tx.Query(ctx, `
		UPDATE devices
		SET status = 'revoked', revoked_at = $3, updated_at = $3
		WHERE workspace_id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL
		RETURNING id::text
	`, workspaceID, userID, now)
	if err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	revoked := make([]string, 0)
	for rows.Next() {
		var deviceID string
		if err := rows.Scan(&deviceID); err != nil {
			rows.Close()
			return workspaceservice.MemberRemoval{}, err
		}
		revoked = append(revoked, deviceID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	// Also covers devices revoked earlier whose credentials were left active.
	if _, err := tx.Exec(ctx, `
		UPDATE device_credentials dc
		SET revoked_at = $3
		FROM devices d
		WHERE dc.device_id = d.id AND d.workspace_id = $1::uuid AND d.user_id = $2::uuid
		  AND dc.revoked_at IS NULL
	`, workspaceID, userID, now); err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workspaceservice.MemberRemoval{}, err
	}
	sort.Strings(revoked)
	return workspaceservice.MemberRemoval{Membership: removed, RevokedDeviceIDs: revoked}, nil
}

// requireAnotherOwner fails with ErrLastOwner unless the workspace has more
// than one active owner.
func requireAnotherOwner(ctx context.Context, tx pgx.Tx, workspaceID string) error {
	var ownerCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM workspace_members wm JOIN roles r ON r.id = wm.role_id
		WHERE wm.workspace_id = $1::uuid AND wm.status = 'active' AND r.code = 'owner'
	`, workspaceID).Scan(&ownerCount); err != nil {
		return err
	}
	if ownerCount <= 1 {
		return workspaceservice.ErrLastOwner
	}
	return nil
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
		// A removed membership is re-activated in place; an active one is a
		// conflict and leaves the invitation pending.
		tag, err := tx.Exec(ctx, `INSERT INTO workspace_members(id,workspace_id,user_id,role_id,status,joined_at,created_at,updated_at)
			SELECT $1::uuid,$2::uuid,$3::uuid,r.id,'active',$4,$4,$4 FROM roles r WHERE r.code=$5
			ON CONFLICT (workspace_id, user_id) DO UPDATE
			SET id = EXCLUDED.id, role_id = EXCLUDED.role_id, status = EXCLUDED.status,
			    joined_at = EXCLUDED.joined_at, updated_at = EXCLUDED.updated_at
			WHERE workspace_members.status = 'removed'`, membership.ID, workspaceID, membership.UserID, membership.JoinedAt, role)
		if err != nil {
			if isUniqueViolation(err) {
				return workspaceservice.ErrMemberExists
			}
			if isBillingQuotaViolation(err) {
				return billingservice.ErrQuotaExceeded
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return workspaceservice.ErrMemberExists
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
