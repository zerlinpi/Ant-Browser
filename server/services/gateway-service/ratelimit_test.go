package gatewayservice

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time                 { return c.now }
func (c *fakeClock) Advance(duration time.Duration) { c.now = c.now.Add(duration) }

// The policies below use rates that are exact in binary floating point (two
// tokens per second), so waits and refills compare exactly.
var twoPerSecond = RateLimit{Requests: 2, Interval: time.Second, Burst: 2}

func TestTokenBucketRefillsAndReportsRetryAfter(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newTokenBucketLimiter(twoPerSecond, 10, clock.Now)
	for attempt := 0; attempt < 2; attempt++ {
		if allowed, _ := limiter.allow("client"); !allowed {
			t.Fatalf("burst request %d rejected", attempt)
		}
	}
	allowed, wait := limiter.allow("client")
	if allowed || wait != 500*time.Millisecond {
		t.Fatalf("exhausted bucket: allowed=%v wait=%s, want false/500ms", allowed, wait)
	}
	clock.Advance(250 * time.Millisecond)
	if allowed, wait := limiter.allow("client"); allowed || wait != 250*time.Millisecond {
		t.Fatalf("half-refilled bucket: allowed=%v wait=%s, want false/250ms", allowed, wait)
	}
	clock.Advance(250 * time.Millisecond)
	if allowed, _ := limiter.allow("client"); !allowed {
		t.Fatal("refilled token was not granted")
	}
	if allowed, _ := limiter.allow("other"); !allowed {
		t.Fatal("independent key was limited")
	}
	// Refill never exceeds the burst.
	clock.Advance(time.Hour)
	for attempt := 0; attempt < 2; attempt++ {
		if allowed, _ := limiter.allow("client"); !allowed {
			t.Fatalf("request %d after a long idle period rejected", attempt)
		}
	}
	if allowed, _ := limiter.allow("client"); allowed {
		t.Fatal("idle bucket accumulated more than its burst")
	}
}

func TestTokenBucketMemoryIsBounded(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	// A bucket is full again, and therefore evictable, one second after use.
	limiter := newTokenBucketLimiter(twoPerSecond, 3, clock.Now)
	for _, key := range []string{"a", "b", "c"} {
		limiter.allow(key)
		limiter.allow(key)
	}
	if limiter.size() != 3 {
		t.Fatalf("size = %d, want 3", limiter.size())
	}
	// "a" is least recently used; a fourth key evicts it to stay within bounds.
	limiter.allow("b")
	limiter.allow("c")
	limiter.allow("d")
	if limiter.size() != 3 {
		t.Fatalf("size = %d after overflow, want 3", limiter.size())
	}
	if _, tracked := limiter.buckets["a"]; tracked {
		t.Fatal("least recently used key was not evicted")
	}
	if allowed, _ := limiter.allow("b"); allowed {
		t.Fatal("recently used exhausted key lost its state")
	}

	// Idle buckets are dropped once fully refilled, without waiting for the cap.
	clock.Advance(time.Second)
	limiter.allow("e")
	if limiter.size() != 1 {
		t.Fatalf("size = %d after idle period, want only the new key", limiter.size())
	}
	for i := 0; i < 50; i++ {
		limiter.allow(string(rune('A' + i)))
	}
	if limiter.size() > 3 {
		t.Fatalf("size = %d exceeds the key bound", limiter.size())
	}
}

func TestRateLimitOverridesKeepDefaultsForInvalidPolicies(t *testing.T) {
	limiters := newRateLimiters(&RateLimits{
		LoginPerIP:    RateLimit{Requests: 0, Interval: time.Minute, Burst: 1},
		RefreshPerIP:  RateLimit{Requests: 2, Interval: time.Second, Burst: 3},
		RegisterPerIP: RateLimit{Requests: -1},
	})
	if limiters.loginIP.burst != loginPerIPBurst || limiters.registerIP.burst != registerPerIPBurst {
		t.Fatalf("invalid overrides replaced defaults: login burst %v register burst %v", limiters.loginIP.burst, limiters.registerIP.burst)
	}
	if limiters.refreshIP.burst != 3 || limiters.refreshIP.rate != 2 {
		t.Fatalf("valid override ignored: burst %v rate %v", limiters.refreshIP.burst, limiters.refreshIP.rate)
	}
}

func TestRequestMetadataUsesResolvedClientIP(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	trusted := &Gateway{logger: logger}
	trusted.clientIPs = trusted.newClientIPResolver(TrustedProxies{"10.0.0.0/8"})
	request := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	request.RemoteAddr = "10.1.2.3:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.77")
	request.Header.Set("User-Agent", "agent-test")
	metadata := trusted.requestMetadata(request)
	if metadata.IPAddress != "198.51.100.77" || metadata.UserAgent != "agent-test" {
		t.Fatalf("metadata = %+v", metadata)
	}

	// Any invalid entry disables forwarding headers entirely (fail closed).
	misconfigured := &Gateway{logger: logger}
	misconfigured.clientIPs = misconfigured.newClientIPResolver(TrustedProxies{"10.0.0.0/8", "not-a-cidr"})
	if got := misconfigured.requestMetadata(request).IPAddress; got != "10.1.2.3" {
		t.Fatalf("misconfigured proxies resolved %q, want the peer", got)
	}
	direct := &Gateway{logger: logger}
	direct.clientIPs = direct.newClientIPResolver(nil)
	if got := direct.requestMetadata(request).IPAddress; got != "10.1.2.3" {
		t.Fatalf("untrusted peer resolved %q, want the peer", got)
	}
}
