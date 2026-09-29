package authservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TOTP two-factor authentication.
//
// Enrollment is two steps: SetupTOTP (which re-checks the password) stores a
// pending factor with a sealed secret, and ConfirmTOTP activates it with one
// valid code and returns single-use recovery codes. Login for a user with an
// active factor returns a short-lived challenge instead of tokens; VerifyMFA
// exchanges the challenge and a TOTP or recovery code for a session.
//
// Every code check counts toward a per-user lockout before the code is
// examined, so concurrent guesses cannot exceed the limit, and a TOTP time
// step is accepted at most once.

const (
	MFAStatusPending = "pending"
	MFAStatusActive  = "active"

	MFAMethodTOTP         = "totp"
	MFAMethodRecoveryCode = "recovery_code"

	defaultMFAIssuer = "Ant Browser"
	// mfaChallengeTTL bounds how long the second login step may take.
	mfaChallengeTTL = 5 * time.Minute
	// mfaChallengeAttempts caps the codes tried against one challenge.
	mfaChallengeAttempts = 5
	// After mfaMaxFailures code checks without a success, verification is
	// refused for mfaLockout. The limit is per user and persisted, so it
	// holds across challenges, endpoints and control-plane replicas.
	mfaMaxFailures = 5
	mfaLockout     = 15 * time.Minute
)

var (
	// ErrMFAUnavailable means the deployment cannot store or read second
	// factors. Enrollment and TOTP checks fail closed.
	ErrMFAUnavailable      = errors.New("two-factor authentication is unavailable")
	ErrMFAAlreadyEnabled   = errors.New("two-factor authentication is already enabled")
	ErrMFANotEnabled       = errors.New("two-factor authentication is not enabled")
	ErrMFASetupRequired    = errors.New("two-factor setup has not been started or was replaced")
	ErrMFACodeRequired     = errors.New("provide either a verification code or a recovery code")
	ErrMFAInvalidCode      = errors.New("the verification code is incorrect")
	ErrMFAChallengeInvalid = errors.New("the sign-in verification has expired; sign in again")
	ErrMFALocked           = errors.New("too many incorrect verification codes; try again later")
	ErrInvalidPassword     = errors.New("the password is incorrect")
)

// MFALockedError reports when second-factor verification is allowed again.
type MFALockedError struct {
	Until time.Time
}

func (e *MFALockedError) Error() string { return ErrMFALocked.Error() }
func (e *MFALockedError) Unwrap() error { return ErrMFALocked }

// MFAFactor is a user's TOTP enrollment. SecretEnvelope is the sealed secret;
// the plaintext never leaves the service.
type MFAFactor struct {
	UserID         string
	SecretEnvelope string
	Status         string
	// LastUsedStep is the latest accepted TOTP time step.
	LastUsedStep int64
	// FailedAttempts counts code checks since the last success.
	FailedAttempts int
	LockedUntil    *time.Time
	CreatedAt      time.Time
	ConfirmedAt    *time.Time
	UpdatedAt      time.Time
	// RecoveryCodesRemaining is the number of unused recovery codes. It is
	// computed by the repository and ignored on writes.
	RecoveryCodesRemaining int
}

// MFAChallenge is the pending second step of a login. TokenHash is the hex
// SHA-256 of the bearer token handed to the client.
type MFAChallenge struct {
	ID         string
	UserID     string
	TokenHash  string
	DeviceID   string
	Attempts   int
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// SecretSealer encrypts small secrets at rest. The sealed form is an opaque
// string; aad binds it to its owner so it cannot be moved to another user.
type SecretSealer interface {
	Seal(ctx context.Context, plaintext, aad []byte) (string, error)
	Open(ctx context.Context, sealed string, aad []byte) ([]byte, error)
}

// MFARepository persists second factors. Service detects it on the auth
// repository; without it MFA is reported unavailable and never required.
type MFARepository interface {
	// FindMFAFactor returns the user's factor or ErrNotFound.
	FindMFAFactor(ctx context.Context, userID string) (MFAFactor, error)
	// SavePendingMFAFactor stores factor as the user's pending enrollment,
	// replacing an earlier pending one and resetting its counters. It
	// returns ErrMFAAlreadyEnabled when the user has an active factor.
	SavePendingMFAFactor(ctx context.Context, factor MFAFactor) error
	// ActivateMFAFactor activates the pending factor sealed as
	// secretEnvelope, records step as used, clears the failure count and
	// replaces the recovery codes with recoveryCodeHashes. It returns
	// ErrMFAAlreadyEnabled when the factor is active and ErrMFASetupRequired
	// when no such pending factor exists (for example a newer setup
	// replaced it).
	ActivateMFAFactor(ctx context.Context, userID, secretEnvelope string, step int64, recoveryCodeHashes []string, now time.Time) error
	// BeginMFAAttempt counts one code check against the user's factor with
	// the given status and returns the factor. It returns *MFALockedError
	// while an earlier lockout is in effect and ErrNotFound when the user
	// has no factor with that status. An expired lockout restarts the
	// count; the attempt that reaches maxFailures locks further attempts
	// until now+lockout unless it succeeds.
	BeginMFAAttempt(ctx context.Context, userID, status string, now time.Time, maxFailures int, lockout time.Duration) (MFAFactor, error)
	// AcceptTOTPStep records step as used on the active factor and clears
	// the failure count. It reports false when step is not later than the
	// last used step (a replayed code).
	AcceptTOTPStep(ctx context.Context, userID string, step int64, now time.Time) (bool, error)
	// ConsumeRecoveryCode marks the user's unused recovery code with
	// codeHash as used and clears the failure count of the active factor.
	// It reports false when no unused code matches.
	ConsumeRecoveryCode(ctx context.Context, userID, codeHash string, now time.Time) (bool, error)
	// ReplaceRecoveryCodes swaps all recovery codes of the active factor. It
	// returns ErrMFANotEnabled without an active factor.
	ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string, now time.Time) error
	// DeleteMFA removes the factor, its recovery codes and the user's login
	// challenges. It returns ErrMFANotEnabled without an active factor.
	DeleteMFA(ctx context.Context, userID string) error
	// CreateMFAChallenge stores a login challenge and may discard the
	// user's expired or consumed ones.
	CreateMFAChallenge(ctx context.Context, challenge MFAChallenge) error
	// BeginMFAChallengeAttempt counts one attempt against the live
	// challenge with tokenHash and returns it. It returns
	// ErrMFAChallengeInvalid when the challenge is unknown, consumed,
	// expired at now or has had maxAttempts attempts.
	BeginMFAChallengeAttempt(ctx context.Context, tokenHash string, maxAttempts int, now time.Time) (MFAChallenge, error)
	// ConsumeMFAChallenge ends a challenge; false means another request
	// already consumed it.
	ConsumeMFAChallenge(ctx context.Context, challengeID string, now time.Time) (bool, error)
}

// MFAStatus describes the caller's second factor.
type MFAStatus struct {
	// Available is false when this server cannot enroll new factors.
	Available              bool       `json:"available"`
	Enabled                bool       `json:"enabled"`
	EnabledAt              *time.Time `json:"enabledAt,omitempty"`
	RecoveryCodesRemaining int        `json:"recoveryCodesRemaining"`
}

// TOTPSetup is shown once to add the account to an authenticator app.
type TOTPSetup struct {
	// Secret is the base32 key (no padding) for manual entry.
	Secret      string `json:"secret"`
	OTPAuthURI  string `json:"otpauthUri"`
	Issuer      string `json:"issuer"`
	AccountName string `json:"accountName"`
	Algorithm   string `json:"algorithm"`
	Digits      int    `json:"digits"`
	Period      int    `json:"period"`
}

// RecoveryCodes are shown once; only their hashes are stored.
type RecoveryCodes struct {
	Codes []string `json:"recoveryCodes"`
}

// MFAChallengeTicket is returned by Login when a second factor is required.
type MFAChallengeTicket struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	Methods   []string  `json:"methods"`
}

// LoginResult carries either a session or, for accounts with two-factor
// authentication, a challenge for VerifyMFA. Without MFA it encodes exactly
// like TokenPair.
type LoginResult struct {
	*TokenPair
	MFARequired  bool                `json:"mfaRequired,omitempty"`
	MFAChallenge *MFAChallengeTicket `json:"mfaChallenge,omitempty"`
}

// MFAVerifyInput completes a login challenge with exactly one of Code (TOTP)
// or RecoveryCode.
type MFAVerifyInput struct {
	ChallengeToken string `json:"challengeToken"`
	Code           string `json:"code,omitempty"`
	RecoveryCode   string `json:"recoveryCode,omitempty"`
}

// MFACodeInput proves possession of the second factor with exactly one of
// Code (TOTP) or RecoveryCode.
type MFACodeInput struct {
	Code         string `json:"code,omitempty"`
	RecoveryCode string `json:"recoveryCode,omitempty"`
}

// DisableMFAInput requires the password and the second factor.
type DisableMFAInput struct {
	Password     string `json:"password"`
	Code         string `json:"code,omitempty"`
	RecoveryCode string `json:"recoveryCode,omitempty"`
}

// ConfigureMFA enables TOTP enrollment and verification. sealer encrypts
// TOTP secrets at rest; issuer labels the account in authenticator apps.
// Without a sealer, enrollment and TOTP checks fail closed with
// ErrMFAUnavailable while recovery codes keep working.
func (s *Service) ConfigureMFA(sealer SecretSealer, issuer string) {
	s.sealer = sealer
	if issuer = strings.TrimSpace(issuer); issuer != "" {
		s.mfaIssuer = issuer
	}
}

// MFAStatus reports whether the caller has an active second factor.
func (s *Service) MFAStatus(ctx context.Context, userID string) (MFAStatus, error) {
	status := MFAStatus{Available: s.mfa != nil && s.sealer != nil}
	if s.mfa == nil {
		return status, nil
	}
	factor, err := s.mfa.FindMFAFactor(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return status, nil
	}
	if err != nil {
		return MFAStatus{}, err
	}
	if factor.Status == MFAStatusActive {
		status.Enabled = true
		status.EnabledAt = factor.ConfirmedAt
		status.RecoveryCodesRemaining = factor.RecoveryCodesRemaining
	}
	return status, nil
}

// SetupTOTP starts enrollment after re-checking the password. The returned
// secret replaces any earlier unconfirmed setup and takes effect once
// ConfirmTOTP accepts a code for it.
func (s *Service) SetupTOTP(ctx context.Context, userID, password string) (TOTPSetup, error) {
	if s.mfa == nil || s.sealer == nil {
		return TOTPSetup{}, ErrMFAUnavailable
	}
	user, err := s.reauthenticate(ctx, userID, password)
	if err != nil {
		return TOTPSetup{}, err
	}
	secret := make([]byte, totpSecretBytes)
	defer clearBytes(secret)
	if _, err := rand.Read(secret); err != nil {
		return TOTPSetup{}, err
	}
	sealed, err := s.sealer.Seal(ctx, secret, totpSecretAAD(user.ID))
	if err != nil {
		return TOTPSetup{}, fmt.Errorf("%w: seal totp secret: %v", ErrMFAUnavailable, err)
	}
	now := s.now().UTC()
	if err := s.mfa.SavePendingMFAFactor(ctx, MFAFactor{
		UserID: user.ID, SecretEnvelope: sealed, Status: MFAStatusPending, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return TOTPSetup{}, err
	}
	encoded := encodeTOTPSecret(secret)
	return TOTPSetup{
		Secret: encoded, OTPAuthURI: otpauthURI(s.mfaIssuer, user.Email, encoded),
		Issuer: s.mfaIssuer, AccountName: user.Email,
		Algorithm: totpAlgorithm, Digits: totpDigits, Period: totpPeriodSeconds,
	}, nil
}

// ConfirmTOTP activates the pending factor with a code from the
// authenticator app and returns the first set of recovery codes.
func (s *Service) ConfirmTOTP(ctx context.Context, userID, code string) (RecoveryCodes, error) {
	if s.mfa == nil || s.sealer == nil {
		return RecoveryCodes{}, ErrMFAUnavailable
	}
	if code = normalizeTOTPCode(code); code == "" {
		return RecoveryCodes{}, ErrMFACodeRequired
	}
	now := s.now().UTC()
	factor, err := s.mfa.BeginMFAAttempt(ctx, userID, MFAStatusPending, now, mfaMaxFailures, mfaLockout)
	if errors.Is(err, ErrNotFound) {
		if current, findErr := s.mfa.FindMFAFactor(ctx, userID); findErr == nil && current.Status == MFAStatusActive {
			return RecoveryCodes{}, ErrMFAAlreadyEnabled
		}
		return RecoveryCodes{}, ErrMFASetupRequired
	}
	if err != nil {
		return RecoveryCodes{}, err
	}
	secret, err := s.openTOTPSecret(ctx, factor)
	if err != nil {
		return RecoveryCodes{}, err
	}
	defer clearBytes(secret)
	step, ok := matchTOTP(secret, code, now, factor.LastUsedStep)
	if !ok {
		return RecoveryCodes{}, ErrMFAInvalidCode
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return RecoveryCodes{}, err
	}
	if err := s.mfa.ActivateMFAFactor(ctx, userID, factor.SecretEnvelope, step, hashes, now); err != nil {
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{Codes: codes}, nil
}

// RegenerateRecoveryCodes replaces every recovery code after verifying the
// second factor. A recovery code used to authorize it is consumed first.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID string, input MFACodeInput) (RecoveryCodes, error) {
	if s.mfa == nil {
		return RecoveryCodes{}, ErrMFAUnavailable
	}
	proof, err := parseSecondFactor(input.Code, input.RecoveryCode)
	if err != nil {
		return RecoveryCodes{}, err
	}
	now := s.now().UTC()
	if err := s.verifySecondFactor(ctx, userID, proof, now); err != nil {
		return RecoveryCodes{}, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return RecoveryCodes{}, err
	}
	if err := s.mfa.ReplaceRecoveryCodes(ctx, userID, hashes, now); err != nil {
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{Codes: codes}, nil
}

// DisableMFA removes the second factor after re-checking the password and
// verifying a TOTP or recovery code, so a user who lost the authenticator
// can re-enroll with a recovery code.
func (s *Service) DisableMFA(ctx context.Context, userID string, input DisableMFAInput) error {
	if s.mfa == nil {
		return ErrMFAUnavailable
	}
	proof, err := parseSecondFactor(input.Code, input.RecoveryCode)
	if err != nil {
		return err
	}
	if _, err := s.reauthenticate(ctx, userID, input.Password); err != nil {
		return err
	}
	if err := s.verifySecondFactor(ctx, userID, proof, s.now().UTC()); err != nil {
		return err
	}
	return s.mfa.DeleteMFA(ctx, userID)
}

// VerifyMFA completes a login that returned a challenge. The session keeps
// the device ID given at login; user agent and IP come from metadata.
func (s *Service) VerifyMFA(ctx context.Context, input MFAVerifyInput, metadata SessionMetadata) (TokenPair, error) {
	if s.mfa == nil {
		return TokenPair{}, ErrMFAUnavailable
	}
	token := strings.TrimSpace(input.ChallengeToken)
	if token == "" || len(token) > 256 {
		return TokenPair{}, ErrMFAChallengeInvalid
	}
	proof, err := parseSecondFactor(input.Code, input.RecoveryCode)
	if err != nil {
		return TokenPair{}, err
	}
	now := s.now().UTC()
	challenge, err := s.mfa.BeginMFAChallengeAttempt(ctx, hashOpaqueToken(token), mfaChallengeAttempts, now)
	if err != nil {
		return TokenPair{}, err
	}
	user, err := s.repository.FindUserByID(ctx, challenge.UserID)
	if errors.Is(err, ErrNotFound) {
		return TokenPair{}, ErrMFAChallengeInvalid
	}
	if err != nil {
		return TokenPair{}, err
	}
	if user.Status != "active" {
		return TokenPair{}, ErrUserDisabled
	}
	if err := s.verifySecondFactor(ctx, user.ID, proof, now); err != nil {
		if errors.Is(err, ErrMFANotEnabled) {
			// MFA was turned off after the password step; start over.
			return TokenPair{}, ErrMFAChallengeInvalid
		}
		return TokenPair{}, err
	}
	consumed, err := s.mfa.ConsumeMFAChallenge(ctx, challenge.ID, now)
	if err != nil {
		return TokenPair{}, err
	}
	if !consumed {
		return TokenPair{}, ErrMFAChallengeInvalid
	}
	metadata.DeviceID = challenge.DeviceID
	return s.issueSession(ctx, user, metadata)
}

// mfaRequired reports whether a password login must pass a second factor.
// Repository errors fail closed.
func (s *Service) mfaRequired(ctx context.Context, userID string) (bool, error) {
	if s.mfa == nil {
		return false, nil
	}
	factor, err := s.mfa.FindMFAFactor(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return factor.Status == MFAStatusActive, nil
}

func (s *Service) startMFAChallenge(ctx context.Context, userID, deviceID string) (MFAChallengeTicket, error) {
	raw, hash, err := s.newOpaque()
	if err != nil {
		return MFAChallengeTicket{}, err
	}
	now := s.now().UTC()
	challenge := MFAChallenge{
		ID: uuid.NewString(), UserID: userID, TokenHash: hash, DeviceID: deviceID,
		CreatedAt: now, ExpiresAt: now.Add(mfaChallengeTTL),
	}
	if err := s.mfa.CreateMFAChallenge(ctx, challenge); err != nil {
		return MFAChallengeTicket{}, err
	}
	return MFAChallengeTicket{
		Token: raw, ExpiresAt: challenge.ExpiresAt,
		Methods: []string{MFAMethodTOTP, MFAMethodRecoveryCode},
	}, nil
}

// secondFactor is a parsed, normalized proof: exactly one field is set.
type secondFactor struct {
	code         string
	recoveryCode string
}

func parseSecondFactor(code, recoveryCode string) (secondFactor, error) {
	proof := secondFactor{code: normalizeTOTPCode(code), recoveryCode: normalizeRecoveryCode(recoveryCode)}
	if (proof.code == "") == (proof.recoveryCode == "") {
		return secondFactor{}, ErrMFACodeRequired
	}
	return proof, nil
}

// verifySecondFactor checks proof against the user's active factor. The
// attempt is counted before the code is examined.
func (s *Service) verifySecondFactor(ctx context.Context, userID string, proof secondFactor, now time.Time) error {
	factor, err := s.mfa.BeginMFAAttempt(ctx, userID, MFAStatusActive, now, mfaMaxFailures, mfaLockout)
	if errors.Is(err, ErrNotFound) {
		return ErrMFANotEnabled
	}
	if err != nil {
		return err
	}
	if proof.recoveryCode != "" {
		if !validRecoveryCode(proof.recoveryCode) {
			return ErrMFAInvalidCode
		}
		used, err := s.mfa.ConsumeRecoveryCode(ctx, userID, hashRecoveryCode(proof.recoveryCode), now)
		if err != nil {
			return err
		}
		if !used {
			return ErrMFAInvalidCode
		}
		return nil
	}
	secret, err := s.openTOTPSecret(ctx, factor)
	if err != nil {
		return err
	}
	defer clearBytes(secret)
	step, ok := matchTOTP(secret, proof.code, now, factor.LastUsedStep)
	if !ok {
		return ErrMFAInvalidCode
	}
	accepted, err := s.mfa.AcceptTOTPStep(ctx, userID, step, now)
	if err != nil {
		return err
	}
	if !accepted {
		// A concurrent request used this or a later step.
		return ErrMFAInvalidCode
	}
	return nil
}

func (s *Service) openTOTPSecret(ctx context.Context, factor MFAFactor) ([]byte, error) {
	if s.sealer == nil {
		return nil, ErrMFAUnavailable
	}
	secret, err := s.sealer.Open(ctx, factor.SecretEnvelope, totpSecretAAD(factor.UserID))
	if err != nil {
		return nil, fmt.Errorf("%w: open totp secret: %v", ErrMFAUnavailable, err)
	}
	if len(secret) < 10 {
		clearBytes(secret)
		return nil, fmt.Errorf("%w: totp secret is too short", ErrMFAUnavailable)
	}
	return secret, nil
}

// reauthenticate confirms the password of a signed-in user before a
// security-sensitive change.
func (s *Service) reauthenticate(ctx context.Context, userID, password string) (User, error) {
	user, err := s.repository.FindUserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if user.Status != "active" {
		return User{}, ErrUserDisabled
	}
	if password == "" || s.passwords.Compare(user.PasswordHash, password) != nil {
		return User{}, ErrInvalidPassword
	}
	return user, nil
}

// totpSecretAAD binds a sealed TOTP secret to its user.
func totpSecretAAD(userID string) []byte {
	return []byte("ant-browser/mfa-totp/" + userID)
}
