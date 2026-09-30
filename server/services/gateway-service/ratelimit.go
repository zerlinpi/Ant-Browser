package gatewayservice

import (
	"container/list"
	"crypto/sha256"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
)

// Default abuse limits for unauthenticated and token-consuming endpoints.
// They are deliberately conservative; a legitimate client rarely needs more
// than a handful of these calls per minute. Keys are resolved client IPs
// (see TrustedProxies) and, for login, the normalized email address.
const (
	loginPerIPRequests    = 10 // per minute
	loginPerIPBurst       = 5
	loginPerEmailRequests = 5 // per minute, across all client IPs
	loginPerEmailBurst    = 5
	registerPerIPRequests = 5 // per 10 minutes
	registerPerIPBurst    = 5
	refreshPerIPRequests  = 60 // per minute
	refreshPerIPBurst     = 30
	acceptPerIPRequests   = 10 // per minute
	acceptPerIPBurst      = 10
	// Second-factor codes are also subject to a persisted per-user lockout
	// in the auth service; these limits bound request volume per process.
	mfaVerifyPerIPRequests   = 10 // per minute
	mfaVerifyPerIPBurst      = 5
	mfaManagePerUserRequests = 10 // per minute
	mfaManagePerUserBurst    = 5

	// defaultRateLimitKeys bounds the keys each limiter tracks. Idle keys are
	// evicted first; beyond the cap the least recently used key is dropped.
	defaultRateLimitKeys = 10000
)

// RateLimit is a token-bucket policy: up to Burst requests at once, refilled
// at Requests per Interval.
type RateLimit struct {
	Requests int
	Interval time.Duration
	Burst    int
}

func (l RateLimit) valid() bool {
	return l.Requests > 0 && l.Interval > 0 && l.Burst > 0
}

// RateLimits configures the gateway's in-process limiters. Pass it to
// NewWithInfrastructure as an option to override the defaults; any policy
// left zero (or invalid) keeps its default.
//
// The limiters are per process. A deployment running several control-plane
// replicas multiplies the effective limits by the replica count and needs a
// shared limiter (for example Redis) for exact enforcement.
type RateLimits struct {
	LoginPerIP        RateLimit
	LoginPerEmail     RateLimit
	RegisterPerIP     RateLimit
	RefreshPerIP      RateLimit
	AcceptInvitePerIP RateLimit
	// MFAVerifyPerIP limits the second login step per client IP.
	MFAVerifyPerIP RateLimit
	// MFAManagePerUser limits two-factor setup, confirmation, recovery code
	// regeneration and removal per signed-in user; each checks a password
	// or a code.
	MFAManagePerUser RateLimit
	// MaxKeys bounds the memory of each limiter (default 10000 keys).
	MaxKeys int
	// Now overrides the clock; intended for tests.
	Now func() time.Time
}

// DefaultRateLimits returns the production defaults.
func DefaultRateLimits() RateLimits {
	return RateLimits{
		LoginPerIP:        RateLimit{Requests: loginPerIPRequests, Interval: time.Minute, Burst: loginPerIPBurst},
		LoginPerEmail:     RateLimit{Requests: loginPerEmailRequests, Interval: time.Minute, Burst: loginPerEmailBurst},
		RegisterPerIP:     RateLimit{Requests: registerPerIPRequests, Interval: 10 * time.Minute, Burst: registerPerIPBurst},
		RefreshPerIP:      RateLimit{Requests: refreshPerIPRequests, Interval: time.Minute, Burst: refreshPerIPBurst},
		AcceptInvitePerIP: RateLimit{Requests: acceptPerIPRequests, Interval: time.Minute, Burst: acceptPerIPBurst},
		MFAVerifyPerIP:    RateLimit{Requests: mfaVerifyPerIPRequests, Interval: time.Minute, Burst: mfaVerifyPerIPBurst},
		MFAManagePerUser:  RateLimit{Requests: mfaManagePerUserRequests, Interval: time.Minute, Burst: mfaManagePerUserBurst},
		MaxKeys:           defaultRateLimitKeys,
		Now:               time.Now,
	}
}

type rateLimiters struct {
	loginIP       *tokenBucketLimiter
	loginEmail    *tokenBucketLimiter
	registerIP    *tokenBucketLimiter
	refreshIP     *tokenBucketLimiter
	acceptIP      *tokenBucketLimiter
	mfaVerifyIP   *tokenBucketLimiter
	mfaManageUser *tokenBucketLimiter
}

func newRateLimiters(overrides *RateLimits) *rateLimiters {
	limits := DefaultRateLimits()
	if overrides != nil {
		pick := func(target *RateLimit, value RateLimit) {
			if value.valid() {
				*target = value
			}
		}
		pick(&limits.LoginPerIP, overrides.LoginPerIP)
		pick(&limits.LoginPerEmail, overrides.LoginPerEmail)
		pick(&limits.RegisterPerIP, overrides.RegisterPerIP)
		pick(&limits.RefreshPerIP, overrides.RefreshPerIP)
		pick(&limits.AcceptInvitePerIP, overrides.AcceptInvitePerIP)
		pick(&limits.MFAVerifyPerIP, overrides.MFAVerifyPerIP)
		pick(&limits.MFAManagePerUser, overrides.MFAManagePerUser)
		if overrides.MaxKeys > 0 {
			limits.MaxKeys = overrides.MaxKeys
		}
		if overrides.Now != nil {
			limits.Now = overrides.Now
		}
	}
	build := func(policy RateLimit) *tokenBucketLimiter {
		return newTokenBucketLimiter(policy, limits.MaxKeys, limits.Now)
	}
	return &rateLimiters{
		loginIP: build(limits.LoginPerIP), loginEmail: build(limits.LoginPerEmail),
		registerIP: build(limits.RegisterPerIP), refreshIP: build(limits.RefreshPerIP),
		acceptIP:    build(limits.AcceptInvitePerIP),
		mfaVerifyIP: build(limits.MFAVerifyPerIP), mfaManageUser: build(limits.MFAManagePerUser),
	}
}

// tokenBucketLimiter is a memory-bounded set of token buckets. Buckets are
// kept in least-recently-used order; a bucket untouched for fillTime has
// refilled completely and is indistinguishable from a new one, so it is
// evicted. When maxKeys buckets are active, the least recently used one is
// dropped to admit a new key.
type tokenBucketLimiter struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	burst    float64
	fillTime time.Duration
	maxKeys  int
	now      func() time.Time
	order    *list.List // front: most recently used *tokenBucket
	buckets  map[string]*list.Element
}

type tokenBucket struct {
	key     string
	tokens  float64
	updated time.Time
}

func newTokenBucketLimiter(policy RateLimit, maxKeys int, now func() time.Time) *tokenBucketLimiter {
	if maxKeys <= 0 {
		maxKeys = defaultRateLimitKeys
	}
	if now == nil {
		now = time.Now
	}
	rate := float64(policy.Requests) / policy.Interval.Seconds()
	return &tokenBucketLimiter{
		rate: rate, burst: float64(policy.Burst),
		fillTime: time.Duration(float64(policy.Burst) / rate * float64(time.Second)),
		maxKeys:  maxKeys, now: now,
		order: list.New(), buckets: make(map[string]*list.Element),
	}
}

// allow consumes a token for key. When none is available it returns false and
// the time until the next token.
func (l *tokenBucketLimiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictIdleLocked(now)
	var bucket *tokenBucket
	if element, ok := l.buckets[key]; ok {
		bucket = element.Value.(*tokenBucket)
		if elapsed := now.Sub(bucket.updated); elapsed > 0 {
			bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed.Seconds()*l.rate)
			bucket.updated = now
		}
		l.order.MoveToFront(element)
	} else {
		if len(l.buckets) >= l.maxKeys {
			l.removeLocked(l.order.Back())
		}
		bucket = &tokenBucket{key: key, tokens: l.burst, updated: now}
		l.buckets[key] = l.order.PushFront(bucket)
	}
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	return false, time.Duration((1 - bucket.tokens) / l.rate * float64(time.Second))
}

func (l *tokenBucketLimiter) evictIdleLocked(now time.Time) {
	for element := l.order.Back(); element != nil; element = l.order.Back() {
		if now.Sub(element.Value.(*tokenBucket).updated) < l.fillTime {
			return
		}
		l.removeLocked(element)
	}
}

func (l *tokenBucketLimiter) removeLocked(element *list.Element) {
	if element == nil {
		return
	}
	delete(l.buckets, element.Value.(*tokenBucket).key)
	l.order.Remove(element)
}

func (l *tokenBucketLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// limitByClientIP applies limiter to the resolved client IP before next runs.
func (g *Gateway) limitByClientIP(limiter *tokenBucketLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowRequest(w, r, limiter, g.clientIP(r)) {
			return
		}
		next(w, r)
	}
}

// allowRequest consumes a token for key, writing 429 rate_limited with a
// Retry-After header (whole seconds, at least 1) when none is available.
func allowRequest(w http.ResponseWriter, r *http.Request, limiter *tokenBucketLimiter, key string) bool {
	if limiter == nil {
		return true
	}
	allowed, wait := limiter.allow(key)
	if allowed {
		return true
	}
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	httpx.WriteError(w, r, httpx.Problem{
		Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many requests; retry later",
	})
	return false
}

// loginEmailKey normalizes an email the way the auth service does and hashes
// it, so limiter keys have a fixed size and addresses are not retained in
// memory. It returns "" when there is no email to key on.
func loginEmailKey(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(email))
	return string(sum[:])
}
