package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

const userColumns = `id::text, email, password_hash, display_name, status, created_at, updated_at`

func (s *Store) CreateUser(ctx context.Context, user authservice.User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, display_name, status, created_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
	`, user.ID, user.Email, user.PasswordHash, user.DisplayName, user.Status, user.CreatedAt, user.UpdatedAt)
	if isUniqueViolation(err) {
		return authservice.ErrEmailExists
	}
	return err
}

func (s *Store) FindUserByEmail(ctx context.Context, email string) (authservice.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE lower(email) = lower($1) AND deleted_at IS NULL
	`, email))
}

func (s *Store) FindUserByID(ctx context.Context, id string) (authservice.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE id = $1::uuid AND deleted_at IS NULL
	`, id))
}

func scanUser(row scanner) (authservice.User, error) {
	var user authservice.User
	if err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName, &user.Status, &user.CreatedAt, &user.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authservice.User{}, authservice.ErrNotFound
		}
		return authservice.User{}, err
	}
	return user, nil
}

func (s *Store) SaveSession(ctx context.Context, session authservice.Session, token authservice.RefreshToken) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (
			id, user_id, device_id, user_agent, ip_address, expires_at,
			created_at, last_seen_at, revoke_reason
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, NULLIF($5, '')::inet, $6, $7, $8, ''
		)
	`, session.ID, session.UserID, session.DeviceID, session.UserAgent, session.IPAddress, session.ExpiresAt, session.CreatedAt, session.LastSeenAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, parent_id, issued_at, expires_at)
		VALUES ($1::uuid, $2::uuid, decode($3, 'hex'), NULLIF($4, '')::uuid, $5, $6)
	`, token.ID, token.SessionID, token.TokenHash, token.ParentID, token.CreatedAt, token.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FindRefreshToken(ctx context.Context, hash string) (authservice.RefreshToken, authservice.Session, authservice.User, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			rt.id::text, rt.session_id::text, encode(rt.token_hash, 'hex'),
			COALESCE(rt.parent_id::text, ''), rt.issued_at, rt.expires_at,
			rt.consumed_at, rt.revoked_at, COALESCE(rt.replaced_by_id::text, ''),
			s.id::text, s.user_id::text, s.device_id, s.user_agent,
			COALESCE(s.ip_address::text, ''), s.created_at, s.last_seen_at,
			s.expires_at, s.revoked_at, s.revoke_reason,
			u.id::text, u.email, u.password_hash, u.display_name, u.status, u.created_at, u.updated_at
		FROM refresh_tokens rt
		JOIN sessions s ON s.id = rt.session_id
		JOIN users u ON u.id = s.user_id
		WHERE rt.token_hash = decode($1, 'hex') AND u.deleted_at IS NULL
	`, hash)
	return scanRefreshSessionUser(row)
}

func scanRefreshSessionUser(row scanner) (authservice.RefreshToken, authservice.Session, authservice.User, error) {
	var token authservice.RefreshToken
	var session authservice.Session
	var user authservice.User
	err := row.Scan(
		&token.ID, &token.SessionID, &token.TokenHash, &token.ParentID,
		&token.CreatedAt, &token.ExpiresAt, &token.ConsumedAt, &token.RevokedAt, &token.ReplacedByID,
		&session.ID, &session.UserID, &session.DeviceID, &session.UserAgent,
		&session.IPAddress, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt,
		&session.RevokedAt, &session.RevokeReason,
		&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authservice.RefreshToken{}, authservice.Session{}, authservice.User{}, authservice.ErrNotFound
		}
		return authservice.RefreshToken{}, authservice.Session{}, authservice.User{}, err
	}
	return token, session, user, nil
}

func (s *Store) RotateRefreshToken(ctx context.Context, currentHash string, next authservice.RefreshToken) (authservice.Session, authservice.User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return authservice.Session{}, authservice.User{}, err
	}
	defer rollback(ctx, tx)
	current, session, user, err := scanRefreshSessionUser(tx.QueryRow(ctx, `
		SELECT
			rt.id::text, rt.session_id::text, encode(rt.token_hash, 'hex'),
			COALESCE(rt.parent_id::text, ''), rt.issued_at, rt.expires_at,
			rt.consumed_at, rt.revoked_at, COALESCE(rt.replaced_by_id::text, ''),
			s.id::text, s.user_id::text, s.device_id, s.user_agent,
			COALESCE(s.ip_address::text, ''), s.created_at, s.last_seen_at,
			s.expires_at, s.revoked_at, s.revoke_reason,
			u.id::text, u.email, u.password_hash, u.display_name, u.status, u.created_at, u.updated_at
		FROM refresh_tokens rt
		JOIN sessions s ON s.id = rt.session_id
		JOIN users u ON u.id = s.user_id
		WHERE rt.token_hash = decode($1, 'hex') AND u.deleted_at IS NULL
		FOR UPDATE OF rt, s
	`, currentHash))
	if err != nil {
		return authservice.Session{}, authservice.User{}, err
	}
	now := time.Now().UTC()
	if current.ConsumedAt != nil || current.RevokedAt != nil || !current.ExpiresAt.After(now) || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return authservice.Session{}, authservice.User{}, authservice.ErrInvalidCredentials
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET consumed_at = $2, rotated_at = $2, replaced_by_id = $3::uuid
		WHERE token_hash = decode($1, 'hex') AND consumed_at IS NULL AND revoked_at IS NULL
	`, currentHash, now, next.ID)
	if err != nil || commandTag.RowsAffected() != 1 {
		return authservice.Session{}, authservice.User{}, authservice.ErrInvalidCredentials
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, session_id, token_hash, parent_id, issued_at, expires_at)
		VALUES ($1::uuid, $2::uuid, decode($3, 'hex'), $4::uuid, $5, $6)
	`, next.ID, next.SessionID, next.TokenHash, next.ParentID, next.CreatedAt, next.ExpiresAt)
	if err != nil {
		return authservice.Session{}, authservice.User{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1::uuid`, session.ID, now)
	if err != nil {
		return authservice.Session{}, authservice.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return authservice.Session{}, authservice.User{}, err
	}
	session.LastSeenAt = now
	return session, user, nil
}

func (s *Store) RevokeSession(ctx context.Context, userID, sessionID, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, `
		UPDATE sessions
		SET revoked_at = COALESCE(revoked_at, $3), revoke_reason = $4
		WHERE id = $1::uuid AND user_id = $2::uuid
	`, sessionID, userID, now, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return authservice.ErrNotFound
	}
	_, err = tx.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id = $1::uuid
	`, sessionID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SessionActive(ctx context.Context, userID, sessionID string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sessions s
			JOIN users u ON u.id = s.user_id
			WHERE s.id = $1::uuid AND s.user_id = $2::uuid
			  AND s.revoked_at IS NULL AND s.expires_at > now() AND u.status = 'active'
		)
	`, sessionID, userID).Scan(&active)
	return active, err
}
