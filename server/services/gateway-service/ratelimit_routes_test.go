package gatewayservice_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
)

// oncePerHour admits a single request per key for the duration of a test.
var oncePerHour = gatewayservice.RateLimit{Requests: 1, Interval: time.Hour, Burst: 1}

func requireRateLimited(t *testing.T, response *httptest.ResponseRecorder, maxRetryAfter int) {
	t.Helper()
	requireErrorCode(t, response, http.StatusTooManyRequests, "rate_limited")
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Message != "Too many requests; retry later" {
		t.Fatalf("rate limit message = %q", body.Error.Message)
	}
	retryAfter, err := strconv.Atoi(response.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 || retryAfter > maxRetryAfter {
		t.Fatalf("Retry-After = %q, want 1..%d seconds", response.Header().Get("Retry-After"), maxRetryAfter)
	}
}

func TestLoginIsRateLimitedPerClientIP(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(gatewayservice.RateLimits{
		LoginPerIP:    gatewayservice.RateLimit{Requests: 1, Interval: time.Hour, Burst: 2},
		LoginPerEmail: gatewayservice.RateLimit{Requests: 100, Interval: time.Minute, Burst: 100},
	})
	register(t, handler, "limited-login@example.com")
	attempt := func(remote, email string) *httptest.ResponseRecorder {
		return performFrom(t, handler, remote, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": email, "password": "WrongPassword123"}, nil)
	}
	assertStatus(t, attempt("198.51.100.1:1000", "limited-login@example.com"), http.StatusUnauthorized)
	assertStatus(t, attempt("198.51.100.1:1001", "other@example.com"), http.StatusUnauthorized)
	limited := attempt("198.51.100.1:1002", "limited-login@example.com")
	requireRateLimited(t, limited, 3600)
	if limited.Header().Get("Retry-After") != "3600" {
		t.Fatalf("Retry-After = %q, want 3600 for one token per hour", limited.Header().Get("Retry-After"))
	}
	// Another client address has its own bucket.
	assertStatus(t, attempt("198.51.100.2:1000", "limited-login@example.com"), http.StatusUnauthorized)
}

func TestLoginIsRateLimitedPerEmailAcrossIPs(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(gatewayservice.RateLimits{LoginPerEmail: oncePerHour})
	registered := register(t, handler, "stuffed@example.com")
	login := func(remote, email, password string) *httptest.ResponseRecorder {
		return performFrom(t, handler, remote, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, nil)
	}
	assertStatus(t, login("198.51.100.10:1", "stuffed@example.com", "WrongPassword123"), http.StatusUnauthorized)
	// Same account from a different IP, with different case and padding.
	requireRateLimited(t, login("198.51.100.11:1", "  STUFFED@example.com ", "SecurePassword123"), 3600)
	assertStatus(t, login("198.51.100.11:2", "someone-else@example.com", "WrongPassword123"), http.StatusUnauthorized)
	if registered.User.Email != "stuffed@example.com" {
		t.Fatalf("unexpected registered user %+v", registered.User)
	}
}

func TestRegisterRefreshAndAcceptAreRateLimitedPerClientIP(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(gatewayservice.RateLimits{
		RegisterPerIP: gatewayservice.RateLimit{Requests: 1, Interval: time.Hour, Burst: 2},
		RefreshPerIP:  oncePerHour, AcceptInvitePerIP: oncePerHour,
	})
	registerFrom := func(remote, email string) *httptest.ResponseRecorder {
		return performFrom(t, handler, remote, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
			"email": email, "password": "SecurePassword123", "displayName": "Limited",
		}, nil)
	}
	first := registerFrom("127.0.0.1:1", "limited-one@example.com")
	assertStatus(t, first, http.StatusCreated)
	assertStatus(t, registerFrom("127.0.0.1:2", "limited-two@example.com"), http.StatusCreated)
	requireRateLimited(t, registerFrom("127.0.0.1:3", "limited-three@example.com"), 3600)
	assertStatus(t, registerFrom("203.0.113.40:1", "limited-three@example.com"), http.StatusCreated)

	refresh := func() *httptest.ResponseRecorder {
		return performFrom(t, handler, "127.0.0.1:4", http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refreshToken": "not-a-valid-token"}, nil)
	}
	assertStatus(t, refresh(), http.StatusUnauthorized)
	requireRateLimited(t, refresh(), 3600)

	pair := decodeData[struct {
		AccessToken string `json:"accessToken"`
	}](t, first)
	accept := func() *httptest.ResponseRecorder {
		return performFrom(t, handler, "127.0.0.1:5", http.MethodPost, "/api/v1/workspaces/00000000-0000-4000-8000-000000000009/invitations/accept", pair.AccessToken, map[string]string{"token": "invalid"}, nil)
	}
	assertStatus(t, accept(), http.StatusUnprocessableEntity)
	requireRateLimited(t, accept(), 3600)
}

func TestRateLimitKeysUseTrustedProxyResolution(t *testing.T) {
	t.Parallel()
	handler := newTestGateway(
		gatewayservice.TrustedProxies{"10.0.0.0/8"},
		gatewayservice.RateLimits{RefreshPerIP: oncePerHour},
	)
	refreshVia := func(remote, forwardedFor string) *httptest.ResponseRecorder {
		headers := map[string]string{}
		if forwardedFor != "" {
			headers["X-Forwarded-For"] = forwardedFor
		}
		return performFrom(t, handler, remote, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refreshToken": "invalid"}, headers)
	}
	// Behind the trusted proxy, each forwarded client has its own bucket.
	assertStatus(t, refreshVia("10.0.0.1:443", "198.51.100.20"), http.StatusUnauthorized)
	assertStatus(t, refreshVia("10.0.0.1:443", "198.51.100.21"), http.StatusUnauthorized)
	// A client cannot escape its bucket by prepending addresses.
	requireRateLimited(t, refreshVia("10.0.0.2:443", "203.0.113.99, 198.51.100.20"), 3600)
	// From an untrusted peer, X-Forwarded-For is ignored: rotating spoofed
	// values still hits the peer's single bucket.
	assertStatus(t, refreshVia("203.0.113.50:9000", "198.51.100.30"), http.StatusUnauthorized)
	requireRateLimited(t, refreshVia("203.0.113.50:9001", "198.51.100.31"), 3600)
}

func TestDefaultRateLimitsAreConservative(t *testing.T) {
	t.Parallel()
	limits := gatewayservice.DefaultRateLimits()
	for name, scenario := range map[string]struct {
		got  gatewayservice.RateLimit
		want gatewayservice.RateLimit
	}{
		"login per IP":    {limits.LoginPerIP, gatewayservice.RateLimit{Requests: 10, Interval: time.Minute, Burst: 5}},
		"login per email": {limits.LoginPerEmail, gatewayservice.RateLimit{Requests: 5, Interval: time.Minute, Burst: 5}},
		"register per IP": {limits.RegisterPerIP, gatewayservice.RateLimit{Requests: 5, Interval: 10 * time.Minute, Burst: 5}},
		"refresh per IP":  {limits.RefreshPerIP, gatewayservice.RateLimit{Requests: 60, Interval: time.Minute, Burst: 30}},
		"accept per IP":   {limits.AcceptInvitePerIP, gatewayservice.RateLimit{Requests: 10, Interval: time.Minute, Burst: 10}},
	} {
		if scenario.got != scenario.want {
			t.Fatalf("%s = %+v, want %+v", name, scenario.got, scenario.want)
		}
	}
	if limits.MaxKeys != 10000 || limits.Now == nil {
		t.Fatalf("limiter bounds = %d keys, clock set %v", limits.MaxKeys, limits.Now != nil)
	}
}
