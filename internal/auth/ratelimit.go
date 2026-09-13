package auth

import (
	"sync"
	"time"
)

// The concrete numbers from §6.3.
const (
	rateLimitThreshold      = 5
	rateLimitInitialLockout = time.Minute
	rateLimitMaxLockout     = 30 * time.Minute
	rateLimitResetAfter     = 24 * time.Hour
)

// ipAttempts is one source IP's pairing-attempt history.
type ipAttempts struct {
	failures     int
	lockoutUntil time.Time
	lockoutDur   time.Duration
	lastFailure  time.Time
}

// RateLimiter enforces the per-source-IP pairing lockout from §6.3: after 5
// consecutive failed attempts, a 1-minute lockout; each further attempt
// while still locked out doubles the lockout (capped at 30 minutes); the
// failure count resets on a success or after 24 hours with no failures.
// Shared by every endpoint that accepts a guessable pairing secret —
// currently /api/pairing/bootstrap only; /api/pairing/approve reuses the
// same type in a later milestone.
type RateLimiter struct {
	mu  sync.Mutex
	ips map[string]*ipAttempts
}

// NewRateLimiter returns an empty RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{ips: make(map[string]*ipAttempts)}
}

// Attempt records one pairing attempt from ip at time now and reports
// whether it may proceed (i.e. was both correct and not currently locked
// out). An incorrect attempt always counts as a failure. An attempt made
// while already locked out — correct or not — is also refused and, per
// §6.3, doubles the remaining lockout rather than merely being ignored:
// this is what makes "further failed attempt while still within a lockout
// period" concrete rather than silently dropped.
func (rl *RateLimiter) Attempt(ip string, now time.Time, correct bool) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	st := rl.ips[ip]
	if st != nil && now.Sub(st.lastFailure) >= rateLimitResetAfter {
		st = nil
	}

	if st != nil && now.Before(st.lockoutUntil) {
		st.failures++
		st.lastFailure = now
		st.lockoutDur *= 2
		if st.lockoutDur > rateLimitMaxLockout {
			st.lockoutDur = rateLimitMaxLockout
		}
		st.lockoutUntil = now.Add(st.lockoutDur)
		rl.ips[ip] = st
		return false
	}

	if correct {
		delete(rl.ips, ip)
		return true
	}

	if st == nil {
		st = &ipAttempts{}
	}
	st.failures++
	st.lastFailure = now
	if st.failures >= rateLimitThreshold {
		st.lockoutDur = rateLimitInitialLockout
		st.lockoutUntil = now.Add(st.lockoutDur)
	}
	rl.ips[ip] = st
	return false
}

// LockedOut reports whether ip is currently within a lockout window,
// without recording a new attempt. Attempt's own return value conflates
// "this attempt was wrong" with "this source is locked out" into a single
// false — the right call for an endpoint (bootstrap, approve) that reports
// both the same way, via PAIRING_RATE_LIMITED. A caller that must tell the
// two apart — e.g. one that owes a distinct "not found" response for a
// simply-wrong lookup, and only PAIRING_RATE_LIMITED once genuinely locked
// out — checks this first.
func (rl *RateLimiter) LockedOut(ip string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	st := rl.ips[ip]
	if st == nil {
		return false
	}
	if now.Sub(st.lastFailure) >= rateLimitResetAfter {
		return false
	}
	return now.Before(st.lockoutUntil)
}
