package gatewayservice_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
)

const mfaTestPassword = "SecurePassword123"

// generousMFALimits keeps the per-process limiters out of flows that make
// many second-factor calls in a burst.
func generousMFALimits() gatewayservice.RateLimits {
	generous := gatewayservice.RateLimit{Requests: 1000, Interval: time.Minute, Burst: 100}
	return gatewayservice.RateLimits{MFAVerifyPerIP: generous, MFAManagePerUser: generous}
}

func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := authservice.GenerateTOTPCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// invalidTOTP returns a six-digit code that no step near now accepts.
func invalidTOTP(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	valid := map[string]bool{}
	for step := -2; step <= 2; step++ {
		valid[totpAt(t, secret, now.Add(time.Duration(step)*30*time.Second))] = true
	}
	for candidate := 0; ; candidate++ {
		if code := fmt.Sprintf("%06d", candidate); !valid[code] {
			return code
		}
	}
}

func enrollTOTP(t *testing.T, handler http.Handler, accessToken string) (authservice.TOTPSetup, []string) {
	t.Helper()
	setupResponse := perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/setup", accessToken, "", map[string]string{"password": mfaTestPassword})
	assertStatus(t, setupResponse, http.StatusOK)
	setup := decodeData[authservice.TOTPSetup](t, setupResponse)
	confirmed := perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/confirm", accessToken, "", map[string]string{"code": totpAt(t, setup.Secret, time.Now())})
	assertStatus(t, confirmed, http.StatusOK)
	return setup, decodeData[authservice.RecoveryCodes](t, confirmed).Codes
}

func loginForChallenge(t *testing.T, handler http.Handler, email string) string {
	t.Helper()
	response := perform(t, handler, http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{"email": email, "password": mfaTestPassword})
	assertStatus(t, response, http.StatusOK)
	if strings.Contains(response.Body.String(), "accessToken") {
		t.Fatalf("login of an MFA account returned tokens: %s", response.Body.String())
	}
	result := decodeData[authservice.LoginResult](t, response)
	if !result.MFARequired || result.TokenPair != nil || result.MFAChallenge == nil || result.MFAChallenge.Token == "" {
		t.Fatalf("login result=%+v", result)
	}
	return result.MFAChallenge.Token
}

func verifyMFA(t *testing.T, handler http.Handler, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return perform(t, handler, http.MethodPost, "/api/v1/auth/mfa/verify", "", "", body)
}

func mfaStatus(t *testing.T, handler http.Handler, accessToken string) authservice.MFAStatus {
	t.Helper()
	response := perform(t, handler, http.MethodGet, "/api/v1/me/mfa", accessToken, "", nil)
	assertStatus(t, response, http.StatusOK)
	return decodeData[authservice.MFAStatus](t, response)
}

func TestTwoFactorEnrollmentLoginAndRemovalOverHTTP(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(generousMFALimits())
	const email = "mfa-flow@example.test"
	owner := register(t, handler, email)
	token := owner.AccessToken

	if status := mfaStatus(t, handler, token); !status.Available || status.Enabled {
		t.Fatalf("initial status=%+v", status)
	}
	assertErrorCode(t, perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/confirm", token, "", map[string]string{"code": "123456"}), http.StatusConflict, "mfa_setup_required")
	assertErrorCode(t, perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/setup", token, "", map[string]string{"password": "WrongPassword123"}), http.StatusUnprocessableEntity, "invalid_password")
	setupResponse := perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/setup", token, "", map[string]string{"password": mfaTestPassword})
	assertStatus(t, setupResponse, http.StatusOK)
	setup := decodeData[authservice.TOTPSetup](t, setupResponse)
	if len(setup.Secret) != 32 || !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/") || setup.AccountName != email {
		t.Fatalf("setup=%+v", setup)
	}
	assertErrorCode(t, perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/confirm", token, "", map[string]string{"code": invalidTOTP(t, setup.Secret)}), http.StatusUnprocessableEntity, "mfa_invalid_code")
	confirmed := perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/confirm", token, "", map[string]string{"code": totpAt(t, setup.Secret, time.Now())})
	assertStatus(t, confirmed, http.StatusOK)
	recovery := decodeData[authservice.RecoveryCodes](t, confirmed).Codes
	if len(recovery) != 10 {
		t.Fatalf("recovery codes=%v", recovery)
	}
	assertErrorCode(t, perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/setup", token, "", map[string]string{"password": mfaTestPassword}), http.StatusConflict, "mfa_already_enabled")
	if status := mfaStatus(t, handler, token); !status.Enabled || status.RecoveryCodesRemaining != 10 || status.EnabledAt == nil {
		t.Fatalf("enabled status=%+v", status)
	}
	// Enabling MFA keeps existing sessions signed in.
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me", token, "", nil), http.StatusOK)

	challenge := loginForChallenge(t, handler, email)
	assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": "not-a-challenge", "code": "123456"}), http.StatusUnauthorized, "mfa_challenge_invalid")
	assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": challenge}), http.StatusUnprocessableEntity, "mfa_code_required")
	assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": challenge, "code": invalidTOTP(t, setup.Secret)}), http.StatusUnprocessableEntity, "mfa_invalid_code")
	// The confirmation used the current step; the next step's code signs in.
	verified := verifyMFA(t, handler, map[string]string{"challengeToken": challenge, "code": totpAt(t, setup.Secret, time.Now().Add(30*time.Second))})
	assertStatus(t, verified, http.StatusOK)
	pair := decodeData[authservice.TokenPair](t, verified)
	if pair.AccessToken == "" || pair.User.ID != owner.User.ID {
		t.Fatalf("verified pair=%+v", pair)
	}
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me", pair.AccessToken, "", nil), http.StatusOK)
	assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": challenge, "recoveryCode": recovery[0]}), http.StatusUnauthorized, "mfa_challenge_invalid")

	recovered := verifyMFA(t, handler, map[string]string{"challengeToken": loginForChallenge(t, handler, email), "recoveryCode": strings.ToUpper(recovery[0])})
	assertStatus(t, recovered, http.StatusOK)
	assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": loginForChallenge(t, handler, email), "recoveryCode": recovery[0]}), http.StatusUnprocessableEntity, "mfa_invalid_code")

	regenerated := perform(t, handler, http.MethodPost, "/api/v1/me/mfa/recovery-codes", token, "", map[string]string{"recoveryCode": recovery[1]})
	assertStatus(t, regenerated, http.StatusOK)
	fresh := decodeData[authservice.RecoveryCodes](t, regenerated).Codes
	if len(fresh) != 10 {
		t.Fatalf("regenerated codes=%v", fresh)
	}
	if status := mfaStatus(t, handler, token); status.RecoveryCodesRemaining != 10 {
		t.Fatalf("status after regeneration=%+v", status)
	}

	disable := func(body map[string]string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodDelete, "/api/v1/me/mfa", token, "", body)
	}
	assertErrorCode(t, disable(map[string]string{"password": "WrongPassword123", "recoveryCode": fresh[0]}), http.StatusUnprocessableEntity, "invalid_password")
	assertErrorCode(t, disable(map[string]string{"password": mfaTestPassword}), http.StatusUnprocessableEntity, "mfa_code_required")
	assertErrorCode(t, disable(map[string]string{"password": mfaTestPassword, "recoveryCode": recovery[2]}), http.StatusUnprocessableEntity, "mfa_invalid_code")
	assertStatus(t, disable(map[string]string{"password": mfaTestPassword, "recoveryCode": fresh[0]}), http.StatusNoContent)
	if status := mfaStatus(t, handler, token); status.Enabled {
		t.Fatalf("status after disable=%+v", status)
	}
	assertErrorCode(t, disable(map[string]string{"password": mfaTestPassword, "recoveryCode": fresh[1]}), http.StatusConflict, "mfa_not_enabled")
	plain := perform(t, handler, http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{"email": email, "password": mfaTestPassword})
	assertStatus(t, plain, http.StatusOK)
	if loggedIn := decodeData[authservice.TokenPair](t, plain); loggedIn.AccessToken == "" {
		t.Fatalf("login after disable=%s", plain.Body.String())
	}
}

func TestTwoFactorLockoutAnswersRetryAfter(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(generousMFALimits())
	const email = "mfa-lockout@example.test"
	owner := register(t, handler, email)
	setup, _ := enrollTOTP(t, handler, owner.AccessToken)

	challenge := loginForChallenge(t, handler, email)
	for attempt := 0; attempt < 5; attempt++ {
		assertErrorCode(t, verifyMFA(t, handler, map[string]string{"challengeToken": challenge, "code": invalidTOTP(t, setup.Secret)}), http.StatusUnprocessableEntity, "mfa_invalid_code")
	}
	locked := verifyMFA(t, handler, map[string]string{"challengeToken": loginForChallenge(t, handler, email), "code": totpAt(t, setup.Secret, time.Now().Add(30*time.Second))})
	assertErrorCode(t, locked, http.StatusTooManyRequests, "mfa_locked")
	retryAfter, err := strconv.Atoi(locked.Header().Get("Retry-After"))
	if err != nil || retryAfter < 14*60 || retryAfter > 15*60 {
		t.Fatalf("Retry-After=%q", locked.Header().Get("Retry-After"))
	}
	// Signed-in management checks share the lockout.
	assertErrorCode(t, perform(t, handler, http.MethodPost, "/api/v1/me/mfa/recovery-codes", owner.AccessToken, "", map[string]string{"code": totpAt(t, setup.Secret, time.Now())}), http.StatusTooManyRequests, "mfa_locked")
}

func TestTwoFactorManagementIsRateLimitedPerUser(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(gatewayservice.RateLimits{MFAManagePerUser: gatewayservice.RateLimit{Requests: 1, Interval: time.Hour, Burst: 2}})
	first := register(t, handler, "mfa-limit-a@example.test")
	second := register(t, handler, "mfa-limit-b@example.test")
	setup := func(accessToken string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, "/api/v1/me/mfa/totp/setup", accessToken, "", map[string]string{"password": "WrongPassword123"})
	}
	assertErrorCode(t, setup(first.AccessToken), http.StatusUnprocessableEntity, "invalid_password")
	assertErrorCode(t, setup(first.AccessToken), http.StatusUnprocessableEntity, "invalid_password")
	limited := setup(first.AccessToken)
	assertErrorCode(t, limited, http.StatusTooManyRequests, "rate_limited")
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited response without Retry-After")
	}
	assertErrorCode(t, setup(second.AccessToken), http.StatusUnprocessableEntity, "invalid_password")
	// Reading the status is not limited.
	assertStatus(t, perform(t, handler, http.MethodGet, "/api/v1/me/mfa", first.AccessToken, "", nil), http.StatusOK)
}
