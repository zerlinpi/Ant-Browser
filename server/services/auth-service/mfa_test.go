package authservice_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

const mfaPassword = "SecurePassword123"

type mfaFixture struct {
	t       *testing.T
	store   *memory.Store
	service *authservice.Service
	clock   time.Time
	userID  string
	email   string
}

func newMFAService(store *memory.Store, withSealer bool) *authservice.Service {
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	service := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	if withSealer {
		crypto, err := secureenvelope.New("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=", "test-mfa-key", "v1")
		if err != nil {
			panic(err)
		}
		sealer, err := secureenvelope.NewTextSealer(crypto)
		if err != nil {
			panic(err)
		}
		service.ConfigureMFA(sealer, "Ant Browser")
	}
	return service
}

func newMFAFixture(t *testing.T) *mfaFixture {
	t.Helper()
	// The memory store checks session expiry against the wall clock, so the
	// test clock starts at real time and only moves forward.
	fixture := &mfaFixture{t: t, store: memory.New(), clock: time.Now().UTC().Truncate(time.Second), email: "mfa-owner@example.com"}
	fixture.service = newMFAService(fixture.store, true)
	fixture.service.SetClock(func() time.Time { return fixture.clock })
	registered, err := fixture.service.Register(context.Background(), authservice.RegisterInput{
		Email: fixture.email, Password: mfaPassword, DisplayName: "Owner",
	}, authservice.SessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.userID = registered.User.ID
	return fixture
}

func (f *mfaFixture) advance(duration time.Duration) { f.clock = f.clock.Add(duration) }

// code is what the authenticator app shows now.
func (f *mfaFixture) code(secret string) string {
	f.t.Helper()
	code, err := authservice.GenerateTOTPCode(secret, f.clock)
	if err != nil {
		f.t.Fatal(err)
	}
	return code
}

// wrongCode differs from every code valid now.
func (f *mfaFixture) wrongCode(secret string) string {
	f.t.Helper()
	valid := map[string]bool{}
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, err := authservice.GenerateTOTPCode(secret, f.clock.Add(offset))
		if err != nil {
			f.t.Fatal(err)
		}
		valid[code] = true
	}
	for candidate := 0; ; candidate++ {
		if code := fmt.Sprintf("%06d", candidate); !valid[code] {
			return code
		}
	}
}

func (f *mfaFixture) login() authservice.LoginResult {
	f.t.Helper()
	result, err := f.service.Login(context.Background(), authservice.LoginInput{Email: f.email, Password: mfaPassword, DeviceID: "desktop-1"},
		authservice.SessionMetadata{UserAgent: "test", IPAddress: "127.0.0.1"})
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *mfaFixture) challenge() string {
	f.t.Helper()
	result := f.login()
	if !result.MFARequired || result.TokenPair != nil || result.MFAChallenge == nil || result.MFAChallenge.Token == "" {
		f.t.Fatalf("login did not require the second factor: %+v", result)
	}
	return result.MFAChallenge.Token
}

func (f *mfaFixture) verify(challenge string, input authservice.MFAVerifyInput) (authservice.TokenPair, error) {
	input.ChallengeToken = challenge
	return f.service.VerifyMFA(context.Background(), input, authservice.SessionMetadata{UserAgent: "verify-agent", IPAddress: "127.0.0.2"})
}

// enroll completes setup and confirmation and returns the secret and codes.
func (f *mfaFixture) enroll() (string, []string) {
	f.t.Helper()
	setup, err := f.service.SetupTOTP(context.Background(), f.userID, mfaPassword)
	if err != nil {
		f.t.Fatal(err)
	}
	codes, err := f.service.ConfirmTOTP(context.Background(), f.userID, f.code(setup.Secret))
	if err != nil {
		f.t.Fatal(err)
	}
	return setup.Secret, codes.Codes
}

func TestTOTPEnrollmentLoginRecoveryCodesAndDisable(t *testing.T) {
	t.Parallel()
	f := newMFAFixture(t)
	ctx := context.Background()

	status, err := f.service.MFAStatus(ctx, f.userID)
	if err != nil || !status.Available || status.Enabled {
		t.Fatalf("initial status=%+v err=%v", status, err)
	}
	if _, err := f.service.SetupTOTP(ctx, f.userID, "WrongPassword123"); !errors.Is(err, authservice.ErrInvalidPassword) {
		t.Fatalf("setup with a wrong password error=%v", err)
	}
	setup, err := f.service.SetupTOTP(ctx, f.userID, mfaPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.Secret) != 32 || setup.Digits != 6 || setup.Period != 30 || setup.Algorithm != "SHA1" || setup.AccountName != f.email ||
		!strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/Ant%20Browser:mfa-owner%40example.com?secret="+setup.Secret+"&issuer=Ant%20Browser") {
		t.Fatalf("setup=%+v", setup)
	}
	// An unconfirmed setup changes nothing about signing in.
	if result := f.login(); result.MFARequired || result.TokenPair == nil {
		t.Fatalf("pending setup required a second factor: %+v", result)
	}
	if _, err := f.service.ConfirmTOTP(ctx, f.userID, f.wrongCode(setup.Secret)); !errors.Is(err, authservice.ErrMFAInvalidCode) {
		t.Fatalf("confirm with a wrong code error=%v", err)
	}
	confirmCode := f.code(setup.Secret)
	recovery, err := f.service.ConfirmTOTP(ctx, f.userID, confirmCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Codes) != 10 {
		t.Fatalf("recovery codes=%v", recovery.Codes)
	}
	status, err = f.service.MFAStatus(ctx, f.userID)
	if err != nil || !status.Enabled || status.RecoveryCodesRemaining != 10 || status.EnabledAt == nil || !status.EnabledAt.Equal(f.clock) {
		t.Fatalf("enabled status=%+v err=%v", status, err)
	}
	if _, err := f.service.SetupTOTP(ctx, f.userID, mfaPassword); !errors.Is(err, authservice.ErrMFAAlreadyEnabled) {
		t.Fatalf("setup while enabled error=%v", err)
	}

	result := f.login()
	if !result.MFARequired || result.TokenPair != nil || result.MFAChallenge == nil ||
		!result.MFAChallenge.ExpiresAt.Equal(f.clock.Add(5*time.Minute)) ||
		strings.Join(result.MFAChallenge.Methods, ",") != "totp,recovery_code" {
		t.Fatalf("login result=%+v", result)
	}
	challenge := result.MFAChallenge.Token
	if _, err := f.verify(challenge, authservice.MFAVerifyInput{}); !errors.Is(err, authservice.ErrMFACodeRequired) {
		t.Fatalf("verify without a code error=%v", err)
	}
	if _, err := f.verify("unknown-challenge", authservice.MFAVerifyInput{Code: f.code(setup.Secret)}); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
		t.Fatalf("verify with an unknown challenge error=%v", err)
	}
	// The confirmation code was used for its time step and cannot sign in.
	if _, err := f.verify(challenge, authservice.MFAVerifyInput{Code: confirmCode}); !errors.Is(err, authservice.ErrMFAInvalidCode) {
		t.Fatalf("replayed confirmation code error=%v", err)
	}
	f.advance(30 * time.Second)
	pair, err := f.verify(challenge, authservice.MFAVerifyInput{Code: f.code(setup.Secret)})
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.User.ID != f.userID {
		t.Fatalf("verified pair=%+v", pair)
	}
	if err := f.service.ValidateSession(ctx, f.userID, pair.SessionID); err != nil {
		t.Fatalf("session from the verified challenge: %v", err)
	}
	sessions, err := f.service.ListSessions(ctx, f.userID, pair.SessionID)
	if err != nil || len(sessions) == 0 || sessions[0].DeviceID != "desktop-1" || sessions[0].UserAgent != "verify-agent" || sessions[0].IPAddress != "127.0.0.2" {
		t.Fatalf("verified session keeps the login device and the verify request metadata: %+v err=%v", sessions, err)
	}
	if _, err := f.verify(challenge, authservice.MFAVerifyInput{Code: f.code(setup.Secret)}); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
		t.Fatalf("reused challenge error=%v", err)
	}

	// Recovery codes accept any case and separators, and work once.
	if _, err := f.verify(f.challenge(), authservice.MFAVerifyInput{RecoveryCode: " " + strings.ToUpper(recovery.Codes[0]) + " "}); err != nil {
		t.Fatalf("recovery code sign-in: %v", err)
	}
	if status, _ := f.service.MFAStatus(ctx, f.userID); status.RecoveryCodesRemaining != 9 {
		t.Fatalf("remaining recovery codes=%d", status.RecoveryCodesRemaining)
	}
	pending := f.challenge()
	if _, err := f.verify(pending, authservice.MFAVerifyInput{RecoveryCode: recovery.Codes[0]}); !errors.Is(err, authservice.ErrMFAInvalidCode) {
		t.Fatalf("reused recovery code error=%v", err)
	}

	// Regenerating (authorized here by a recovery code) invalidates the old set.
	fresh, err := f.service.RegenerateRecoveryCodes(ctx, f.userID, authservice.MFACodeInput{RecoveryCode: recovery.Codes[1]})
	if err != nil || len(fresh.Codes) != 10 {
		t.Fatalf("regenerated=%v err=%v", fresh.Codes, err)
	}
	if _, err := f.verify(pending, authservice.MFAVerifyInput{RecoveryCode: recovery.Codes[2]}); !errors.Is(err, authservice.ErrMFAInvalidCode) {
		t.Fatalf("recovery code from the replaced set error=%v", err)
	}
	if _, err := f.verify(pending, authservice.MFAVerifyInput{RecoveryCode: fresh.Codes[0]}); err != nil {
		t.Fatalf("new recovery code sign-in: %v", err)
	}

	// Disabling needs the password and a second factor.
	beforeDisable := f.challenge()
	if err := f.service.DisableMFA(ctx, f.userID, authservice.DisableMFAInput{Password: "WrongPassword123", RecoveryCode: fresh.Codes[1]}); !errors.Is(err, authservice.ErrInvalidPassword) {
		t.Fatalf("disable with a wrong password error=%v", err)
	}
	if err := f.service.DisableMFA(ctx, f.userID, authservice.DisableMFAInput{Password: mfaPassword}); !errors.Is(err, authservice.ErrMFACodeRequired) {
		t.Fatalf("disable without a code error=%v", err)
	}
	f.advance(30 * time.Second)
	if err := f.service.DisableMFA(ctx, f.userID, authservice.DisableMFAInput{Password: mfaPassword, Code: f.code(setup.Secret)}); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.service.MFAStatus(ctx, f.userID); status.Enabled || status.RecoveryCodesRemaining != 0 {
		t.Fatalf("status after disable=%+v", status)
	}
	if result := f.login(); result.MFARequired || result.TokenPair == nil {
		t.Fatalf("login after disable=%+v", result)
	}
	if _, err := f.verify(beforeDisable, authservice.MFAVerifyInput{RecoveryCode: fresh.Codes[2]}); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
		t.Fatalf("challenge issued before disable error=%v", err)
	}
	if err := f.service.DisableMFA(ctx, f.userID, authservice.DisableMFAInput{Password: mfaPassword, RecoveryCode: fresh.Codes[2]}); !errors.Is(err, authservice.ErrMFANotEnabled) {
		t.Fatalf("disable while not enabled error=%v", err)
	}
}

func TestMFALockoutAndChallengeAttemptLimit(t *testing.T) {
	t.Parallel()
	f := newMFAFixture(t)
	ctx := context.Background()
	secret, recovery := f.enroll()

	first := f.challenge()
	for attempt := 1; attempt <= 5; attempt++ {
		if _, err := f.verify(first, authservice.MFAVerifyInput{Code: f.wrongCode(secret)}); !errors.Is(err, authservice.ErrMFAInvalidCode) {
			t.Fatalf("wrong code %d error=%v", attempt, err)
		}
	}
	f.advance(30 * time.Second)
	// The challenge allows five attempts, even when the sixth is right.
	if _, err := f.verify(first, authservice.MFAVerifyInput{Code: f.code(secret)}); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
		t.Fatalf("sixth attempt on one challenge error=%v", err)
	}
	// Five failures lock every second-factor check for the user.
	second := f.challenge()
	_, err := f.verify(second, authservice.MFAVerifyInput{Code: f.code(secret)})
	var locked *authservice.MFALockedError
	if !errors.As(err, &locked) || !errors.Is(err, authservice.ErrMFALocked) || !locked.Until.After(f.clock) {
		t.Fatalf("verify while locked error=%v", err)
	}
	if _, err := f.service.RegenerateRecoveryCodes(ctx, f.userID, authservice.MFACodeInput{RecoveryCode: recovery[0]}); !errors.Is(err, authservice.ErrMFALocked) {
		t.Fatalf("regenerate while locked error=%v", err)
	}

	f.clock = locked.Until.Add(time.Second)
	if _, err := f.verify(f.challenge(), authservice.MFAVerifyInput{Code: f.code(secret)}); err != nil {
		t.Fatalf("verify after the lockout: %v", err)
	}
	// A success clears the count: four more failures do not lock.
	third := f.challenge()
	for attempt := 1; attempt <= 4; attempt++ {
		if _, err := f.verify(third, authservice.MFAVerifyInput{Code: f.wrongCode(secret)}); !errors.Is(err, authservice.ErrMFAInvalidCode) {
			t.Fatalf("wrong code %d after success error=%v", attempt, err)
		}
	}
	if _, err := f.verify(third, authservice.MFAVerifyInput{RecoveryCode: recovery[0]}); err != nil {
		t.Fatalf("recovery code before the limit: %v", err)
	}
}

func TestMFAChallengeExpires(t *testing.T) {
	t.Parallel()
	f := newMFAFixture(t)
	secret, _ := f.enroll()
	challenge := f.challenge()
	f.advance(5*time.Minute + time.Second)
	if _, err := f.verify(challenge, authservice.MFAVerifyInput{Code: f.code(secret)}); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
		t.Fatalf("expired challenge error=%v", err)
	}
}

func TestConfirmTOTPNeedsTheLatestPendingSetup(t *testing.T) {
	t.Parallel()
	f := newMFAFixture(t)
	ctx := context.Background()
	if _, err := f.service.ConfirmTOTP(ctx, f.userID, "123456"); !errors.Is(err, authservice.ErrMFASetupRequired) {
		t.Fatalf("confirm without setup error=%v", err)
	}
	if _, err := f.service.RegenerateRecoveryCodes(ctx, f.userID, authservice.MFACodeInput{Code: "123456"}); !errors.Is(err, authservice.ErrMFANotEnabled) {
		t.Fatalf("regenerate without MFA error=%v", err)
	}
	stale, err := f.service.SetupTOTP(ctx, f.userID, mfaPassword)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := f.service.SetupTOTP(ctx, f.userID, mfaPassword)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Secret == latest.Secret {
		t.Fatal("a new setup reused the secret")
	}
	if code := f.code(stale.Secret); code != f.code(latest.Secret) {
		if _, err := f.service.ConfirmTOTP(ctx, f.userID, code); !errors.Is(err, authservice.ErrMFAInvalidCode) {
			t.Fatalf("confirm with the replaced secret error=%v", err)
		}
	}
	if _, err := f.service.ConfirmTOTP(ctx, f.userID, f.code(latest.Secret)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfirmTOTP(ctx, f.userID, f.code(latest.Secret)); !errors.Is(err, authservice.ErrMFAAlreadyEnabled) {
		t.Fatalf("second confirm error=%v", err)
	}
}

func TestMFAFailsClosedWithoutASealer(t *testing.T) {
	t.Parallel()
	f := newMFAFixture(t)
	ctx := context.Background()
	secret, recovery := f.enroll()

	unsealed := newMFAService(f.store, false)
	status, err := unsealed.MFAStatus(ctx, f.userID)
	if err != nil || status.Available || !status.Enabled {
		t.Fatalf("status without a sealer=%+v err=%v", status, err)
	}
	if _, err := unsealed.SetupTOTP(ctx, f.userID, mfaPassword); !errors.Is(err, authservice.ErrMFAUnavailable) {
		t.Fatalf("setup without a sealer error=%v", err)
	}
	result, err := unsealed.Login(ctx, authservice.LoginInput{Email: f.email, Password: mfaPassword}, authservice.SessionMetadata{})
	if err != nil || !result.MFARequired || result.TokenPair != nil {
		t.Fatalf("login without a sealer must still require the factor: %+v err=%v", result, err)
	}
	code, err := authservice.GenerateTOTPCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsealed.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: result.MFAChallenge.Token, Code: code}, authservice.SessionMetadata{}); !errors.Is(err, authservice.ErrMFAUnavailable) {
		t.Fatalf("TOTP without a sealer error=%v", err)
	}
	if _, err := unsealed.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: result.MFAChallenge.Token, RecoveryCode: recovery[0]}, authservice.SessionMetadata{}); err != nil {
		t.Fatalf("recovery code without a sealer: %v", err)
	}
}
