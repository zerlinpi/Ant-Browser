package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

// Second-factor tables (migration 029) are user scoped and carry no RLS
// policy, so these queries run outside a tenant transaction.

const mfaFactorColumns = `
	f.user_id::text, f.secret_envelope, f.status, f.last_used_step, f.failed_attempts,
	f.locked_until, f.created_at, f.confirmed_at, f.updated_at,
	(SELECT count(*) FROM user_mfa_recovery_codes c WHERE c.user_id = f.user_id AND c.used_at IS NULL)`

func scanMFAFactor(row scanner) (authservice.MFAFactor, error) {
	var factor authservice.MFAFactor
	err := row.Scan(
		&factor.UserID, &factor.SecretEnvelope, &factor.Status, &factor.LastUsedStep, &factor.FailedAttempts,
		&factor.LockedUntil, &factor.CreatedAt, &factor.ConfirmedAt, &factor.UpdatedAt,
		&factor.RecoveryCodesRemaining,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return authservice.MFAFactor{}, authservice.ErrNotFound
	}
	if err != nil {
		return authservice.MFAFactor{}, err
	}
	return factor, nil
}

func (s *Store) FindMFAFactor(ctx context.Context, userID string) (authservice.MFAFactor, error) {
	return scanMFAFactor(s.pool.QueryRow(ctx, `
		SELECT `+mfaFactorColumns+`
		FROM user_mfa_factors f
		WHERE f.user_id = $1::uuid
	`, userID))
}

func (s *Store) SavePendingMFAFactor(ctx context.Context, factor authservice.MFAFactor) error {
	// The conditional upsert replaces only a pending enrollment; an active
	// factor makes it a no-op, reported as ErrMFAAlreadyEnabled.
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO user_mfa_factors (user_id, secret_envelope, status, created_at, updated_at)
		VALUES ($1::uuid, $2, 'pending', $3, $4)
		ON CONFLICT (user_id) DO UPDATE
		SET secret_envelope = EXCLUDED.secret_envelope, last_used_step = 0, failed_attempts = 0,
		    locked_until = NULL, created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
		WHERE user_mfa_factors.status = 'pending'
	`, factor.UserID, factor.SecretEnvelope, factor.CreatedAt, factor.UpdatedAt)
	if isForeignKeyViolation(err) {
		return authservice.ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authservice.ErrMFAAlreadyEnabled
	}
	return nil
}

func (s *Store) ActivateMFAFactor(ctx context.Context, userID, secretEnvelope string, step int64, recoveryCodeHashes []string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	var status, envelope string
	err = tx.QueryRow(ctx, `
		SELECT status, secret_envelope FROM user_mfa_factors WHERE user_id = $1::uuid FOR UPDATE
	`, userID).Scan(&status, &envelope)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return authservice.ErrMFASetupRequired
	case err != nil:
		return err
	case status == authservice.MFAStatusActive:
		return authservice.ErrMFAAlreadyEnabled
	case envelope != secretEnvelope:
		return authservice.ErrMFASetupRequired
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_mfa_factors
		SET status = 'active', confirmed_at = $2, last_used_step = $3,
		    failed_attempts = 0, locked_until = NULL, updated_at = $2
		WHERE user_id = $1::uuid
	`, userID, now, step); err != nil {
		return err
	}
	if err := replaceRecoveryCodes(ctx, tx, userID, recoveryCodeHashes, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginMFAAttempt(ctx context.Context, userID, status string, now time.Time, maxFailures int, lockout time.Duration) (authservice.MFAFactor, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return authservice.MFAFactor{}, err
	}
	defer rollback(ctx, tx)
	// The row lock serializes concurrent attempts, so each one sees the
	// count left by the previous one and the limit cannot be overshot.
	factor, err := scanMFAFactor(tx.QueryRow(ctx, `
		SELECT `+mfaFactorColumns+`
		FROM user_mfa_factors f
		WHERE f.user_id = $1::uuid AND f.status = $2
		FOR UPDATE OF f
	`, userID, status))
	if err != nil {
		return authservice.MFAFactor{}, err
	}
	if factor.LockedUntil != nil {
		if factor.LockedUntil.After(now) {
			return authservice.MFAFactor{}, &authservice.MFALockedError{Until: *factor.LockedUntil}
		}
		factor.FailedAttempts, factor.LockedUntil = 0, nil
	}
	factor.FailedAttempts++
	if factor.FailedAttempts >= maxFailures {
		until := now.Add(lockout)
		factor.LockedUntil = &until
	}
	factor.UpdatedAt = now
	if _, err := tx.Exec(ctx, `
		UPDATE user_mfa_factors SET failed_attempts = $2, locked_until = $3, updated_at = $4
		WHERE user_id = $1::uuid
	`, userID, factor.FailedAttempts, factor.LockedUntil, now); err != nil {
		return authservice.MFAFactor{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return authservice.MFAFactor{}, err
	}
	return factor, nil
}

func (s *Store) AcceptTOTPStep(ctx context.Context, userID string, step int64, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE user_mfa_factors
		SET last_used_step = $2, failed_attempts = 0, locked_until = NULL, updated_at = $3
		WHERE user_id = $1::uuid AND status = 'active' AND last_used_step < $2
	`, userID, step, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, codeHash string, now time.Time) (bool, error) {
	// Concurrent uses of one code serialize on its row; the loser re-checks
	// used_at and matches nothing.
	tag, err := s.pool.Exec(ctx, `
		WITH used AS (
			UPDATE user_mfa_recovery_codes c
			SET used_at = $3
			WHERE c.user_id = $1::uuid AND c.code_hash = decode($2, 'hex') AND c.used_at IS NULL
			RETURNING c.user_id
		)
		UPDATE user_mfa_factors f
		SET failed_attempts = 0, locked_until = NULL, updated_at = $3
		FROM used
		WHERE f.user_id = used.user_id AND f.status = 'active'
	`, userID, codeHash, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM user_mfa_factors WHERE user_id = $1::uuid FOR UPDATE`, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != authservice.MFAStatusActive) {
		return authservice.ErrMFANotEnabled
	}
	if err != nil {
		return err
	}
	if err := replaceRecoveryCodes(ctx, tx, userID, codeHashes, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string, codeHashes []string, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa_recovery_codes WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO user_mfa_recovery_codes (user_id, code_hash, created_at)
		SELECT $1::uuid, decode(hash, 'hex'), $3 FROM unnest($2::text[]) AS hash
	`, userID, codeHashes, now)
	return err
}

func (s *Store) DeleteMFA(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	// Recovery codes cascade with the factor.
	tag, err := tx.Exec(ctx, `DELETE FROM user_mfa_factors WHERE user_id = $1::uuid AND status = 'active'`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return authservice.ErrMFANotEnabled
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_login_challenges WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateMFAChallenge(ctx context.Context, challenge authservice.MFAChallenge) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	// Finished challenges are pruned per user as new ones are issued.
	if _, err := tx.Exec(ctx, `
		DELETE FROM mfa_login_challenges
		WHERE user_id = $1::uuid AND (consumed_at IS NOT NULL OR expires_at <= $2)
	`, challenge.UserID, challenge.CreatedAt); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mfa_login_challenges (id, user_id, token_hash, device_id, created_at, expires_at)
		VALUES ($1::uuid, $2::uuid, decode($3, 'hex'), $4, $5, $6)
	`, challenge.ID, challenge.UserID, challenge.TokenHash, challenge.DeviceID, challenge.CreatedAt, challenge.ExpiresAt)
	if isForeignKeyViolation(err) {
		return authservice.ErrNotFound
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginMFAChallengeAttempt(ctx context.Context, tokenHash string, maxAttempts int, now time.Time) (authservice.MFAChallenge, error) {
	var challenge authservice.MFAChallenge
	err := s.pool.QueryRow(ctx, `
		UPDATE mfa_login_challenges
		SET attempts = attempts + 1
		WHERE token_hash = decode($1, 'hex') AND consumed_at IS NULL AND expires_at > $2 AND attempts < $3
		RETURNING id::text, user_id::text, encode(token_hash, 'hex'), device_id, attempts,
		          created_at, expires_at, consumed_at
	`, tokenHash, now, maxAttempts).Scan(
		&challenge.ID, &challenge.UserID, &challenge.TokenHash, &challenge.DeviceID, &challenge.Attempts,
		&challenge.CreatedAt, &challenge.ExpiresAt, &challenge.ConsumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return authservice.MFAChallenge{}, authservice.ErrMFAChallengeInvalid
	}
	if err != nil {
		return authservice.MFAChallenge{}, err
	}
	return challenge, nil
}

func (s *Store) ConsumeMFAChallenge(ctx context.Context, challengeID string, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mfa_login_challenges SET consumed_at = $2
		WHERE id = $1::uuid AND consumed_at IS NULL
	`, challengeID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
