package memory

import (
	"context"
	"errors"
	"time"

	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

type memoryRecoveryCode struct {
	hash   string
	usedAt *time.Time
}

func (s *Store) FindMFAFactor(_ context.Context, userID string) (authservice.MFAFactor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	factor, ok := s.mfaFactors[userID]
	if !ok {
		return authservice.MFAFactor{}, authservice.ErrNotFound
	}
	return s.mfaFactorViewLocked(factor), nil
}

func (s *Store) SavePendingMFAFactor(_ context.Context, factor authservice.MFAFactor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[factor.UserID]; !ok {
		return authservice.ErrNotFound
	}
	if current, ok := s.mfaFactors[factor.UserID]; ok && current.Status == authservice.MFAStatusActive {
		return authservice.ErrMFAAlreadyEnabled
	}
	s.mfaFactors[factor.UserID] = authservice.MFAFactor{
		UserID: factor.UserID, SecretEnvelope: factor.SecretEnvelope, Status: authservice.MFAStatusPending,
		CreatedAt: factor.CreatedAt, UpdatedAt: factor.UpdatedAt,
	}
	return nil
}

func (s *Store) ActivateMFAFactor(_ context.Context, userID, secretEnvelope string, step int64, recoveryCodeHashes []string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	factor, ok := s.mfaFactors[userID]
	switch {
	case ok && factor.Status == authservice.MFAStatusActive:
		return authservice.ErrMFAAlreadyEnabled
	case !ok || factor.SecretEnvelope != secretEnvelope:
		return authservice.ErrMFASetupRequired
	}
	if err := uniqueRecoveryHashes(recoveryCodeHashes); err != nil {
		return err
	}
	factor.Status = authservice.MFAStatusActive
	factor.ConfirmedAt = timePtr(now)
	factor.LastUsedStep = step
	factor.FailedAttempts, factor.LockedUntil = 0, nil
	factor.UpdatedAt = now
	s.mfaFactors[userID] = factor
	s.mfaRecoveryCodes[userID] = newMemoryRecoveryCodes(recoveryCodeHashes)
	return nil
}

func (s *Store) BeginMFAAttempt(_ context.Context, userID, status string, now time.Time, maxFailures int, lockout time.Duration) (authservice.MFAFactor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	factor, ok := s.mfaFactors[userID]
	if !ok || factor.Status != status {
		return authservice.MFAFactor{}, authservice.ErrNotFound
	}
	if factor.LockedUntil != nil {
		if factor.LockedUntil.After(now) {
			return authservice.MFAFactor{}, &authservice.MFALockedError{Until: *factor.LockedUntil}
		}
		// The lockout has expired: the next attempts start a fresh count.
		factor.FailedAttempts, factor.LockedUntil = 0, nil
	}
	factor.FailedAttempts++
	if factor.FailedAttempts >= maxFailures {
		factor.LockedUntil = timePtr(now.Add(lockout))
	}
	factor.UpdatedAt = now
	s.mfaFactors[userID] = factor
	return s.mfaFactorViewLocked(factor), nil
}

func (s *Store) AcceptTOTPStep(_ context.Context, userID string, step int64, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	factor, ok := s.mfaFactors[userID]
	if !ok || factor.Status != authservice.MFAStatusActive || step <= factor.LastUsedStep {
		return false, nil
	}
	factor.LastUsedStep = step
	factor.FailedAttempts, factor.LockedUntil = 0, nil
	factor.UpdatedAt = now
	s.mfaFactors[userID] = factor
	return true, nil
}

func (s *Store) ConsumeRecoveryCode(_ context.Context, userID, codeHash string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	factor, ok := s.mfaFactors[userID]
	if !ok || factor.Status != authservice.MFAStatusActive {
		return false, nil
	}
	codes := s.mfaRecoveryCodes[userID]
	for index := range codes {
		if codes[index].hash == codeHash && codes[index].usedAt == nil {
			codes[index].usedAt = timePtr(now)
			factor.FailedAttempts, factor.LockedUntil = 0, nil
			factor.UpdatedAt = now
			s.mfaFactors[userID] = factor
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) ReplaceRecoveryCodes(_ context.Context, userID string, codeHashes []string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if factor, ok := s.mfaFactors[userID]; !ok || factor.Status != authservice.MFAStatusActive {
		return authservice.ErrMFANotEnabled
	}
	if err := uniqueRecoveryHashes(codeHashes); err != nil {
		return err
	}
	s.mfaRecoveryCodes[userID] = newMemoryRecoveryCodes(codeHashes)
	return nil
}

func (s *Store) DeleteMFA(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if factor, ok := s.mfaFactors[userID]; !ok || factor.Status != authservice.MFAStatusActive {
		return authservice.ErrMFANotEnabled
	}
	delete(s.mfaFactors, userID)
	delete(s.mfaRecoveryCodes, userID)
	for hash, challenge := range s.mfaChallenges {
		if challenge.UserID == userID {
			delete(s.mfaChallenges, hash)
		}
	}
	return nil
}

func (s *Store) CreateMFAChallenge(_ context.Context, challenge authservice.MFAChallenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[challenge.UserID]; !ok {
		return authservice.ErrNotFound
	}
	if _, exists := s.mfaChallenges[challenge.TokenHash]; exists {
		return errors.New("mfa challenge token collision")
	}
	for hash, existing := range s.mfaChallenges {
		if existing.UserID == challenge.UserID && (existing.ConsumedAt != nil || !existing.ExpiresAt.After(challenge.CreatedAt)) {
			delete(s.mfaChallenges, hash)
		}
	}
	challenge.Attempts, challenge.ConsumedAt = 0, nil
	s.mfaChallenges[challenge.TokenHash] = challenge
	return nil
}

func (s *Store) BeginMFAChallengeAttempt(_ context.Context, tokenHash string, maxAttempts int, now time.Time) (authservice.MFAChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.mfaChallenges[tokenHash]
	if !ok || challenge.ConsumedAt != nil || !challenge.ExpiresAt.After(now) || challenge.Attempts >= maxAttempts {
		return authservice.MFAChallenge{}, authservice.ErrMFAChallengeInvalid
	}
	challenge.Attempts++
	s.mfaChallenges[tokenHash] = challenge
	return challenge, nil
}

func (s *Store) ConsumeMFAChallenge(_ context.Context, challengeID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, challenge := range s.mfaChallenges {
		if challenge.ID != challengeID {
			continue
		}
		if challenge.ConsumedAt != nil {
			return false, nil
		}
		challenge.ConsumedAt = timePtr(now)
		s.mfaChallenges[hash] = challenge
		return true, nil
	}
	return false, nil
}

// mfaFactorViewLocked returns a copy that shares no pointers with the store,
// with the unused recovery code count filled in.
func (s *Store) mfaFactorViewLocked(factor authservice.MFAFactor) authservice.MFAFactor {
	factor.LockedUntil = cloneTimePtr(factor.LockedUntil)
	factor.ConfirmedAt = cloneTimePtr(factor.ConfirmedAt)
	factor.RecoveryCodesRemaining = 0
	for _, code := range s.mfaRecoveryCodes[factor.UserID] {
		if code.usedAt == nil {
			factor.RecoveryCodesRemaining++
		}
	}
	return factor
}

func newMemoryRecoveryCodes(hashes []string) []memoryRecoveryCode {
	codes := make([]memoryRecoveryCode, 0, len(hashes))
	for _, hash := range hashes {
		codes = append(codes, memoryRecoveryCode{hash: hash})
	}
	return codes
}

// uniqueRecoveryHashes mirrors the PostgreSQL UNIQUE (user_id, code_hash).
func uniqueRecoveryHashes(hashes []string) error {
	seen := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		if _, duplicate := seen[hash]; duplicate {
			return errors.New("duplicate recovery code")
		}
		seen[hash] = struct{}{}
	}
	return nil
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
