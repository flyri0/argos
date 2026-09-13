package auth

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsUntilThreshold(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold-1; i++ {
		if rl.Attempt("1.2.3.4", now, false) {
			t.Fatalf("attempt %d: expected a wrong code to never be allowed", i+1)
		}
	}

	// Below the 5-failure threshold, a correct attempt right afterwards
	// must still succeed — no lockout has been triggered yet.
	if !rl.Attempt("1.2.3.4", now, true) {
		t.Fatalf("expected correct attempt below the threshold to be allowed")
	}
}

func TestRateLimiter_LocksOutAfterFiveFailures(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}

	// Immediately after the 5th failure, even a correct attempt is refused.
	if rl.Attempt("1.2.3.4", now, true) {
		t.Fatalf("expected correct attempt to be refused while locked out")
	}

	// Just before the 1-minute lockout expires, still refused.
	if rl.Attempt("1.2.3.4", now.Add(59*time.Second), true) {
		t.Fatalf("expected lockout to still be in effect at 59s")
	}
}

func TestRateLimiter_LockoutExpiresAndAllowsCorrectCode(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}

	// The doubling rule only extends the lockout for attempts made DURING
	// it; once it has naturally expired with no further attempts, a
	// correct code succeeds again.
	if !rl.Attempt("1.2.3.4", now.Add(61*time.Second), true) {
		t.Fatalf("expected correct attempt to succeed once the 1-minute lockout has elapsed")
	}
}

func TestRateLimiter_FurtherFailuresDuringLockoutDoubleItUpToCap(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}
	// Lockout is now 1 minute, until now+1m.

	// A further failure at +30s (still within the 1-minute lockout) must
	// double the lockout to 2 minutes, measured from +30s — i.e. until +2m30s.
	if rl.Attempt("1.2.3.4", now.Add(30*time.Second), false) {
		t.Fatalf("expected attempt during lockout to be refused")
	}
	if !rl.Attempt("1.2.3.4", now.Add(30*time.Second+2*time.Minute+time.Second), true) {
		t.Fatalf("expected the doubled 2-minute lockout to have expired by +2m30s+1s")
	}
}

func TestRateLimiter_LockoutCappedAt30Minutes(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}
	// 1m lockout. Keep failing immediately (always still "within" the
	// current lockout since each failure re-extends it) to run the
	// doubling up past the 30-minute cap: 1, 2, 4, 8, 16, 32(->30).
	t2 := now
	for i := 0; i < 5; i++ {
		rl.Attempt("1.2.3.4", t2, false)
	}

	// The cap must hold: 29 minutes after the last attempt, still locked.
	if rl.Attempt("1.2.3.4", t2.Add(29*time.Minute), true) {
		t.Fatalf("expected lockout to still be in effect at 29 minutes (cap is 30)")
	}
}

func TestRateLimiter_ResetsAfterSuccess(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold-1; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}
	if !rl.Attempt("1.2.3.4", now, true) {
		t.Fatalf("expected correct attempt to succeed below threshold")
	}

	// The failure count must have reset to 0: another 4 failures right
	// after a success must not trigger a lockout.
	for i := 0; i < rateLimitThreshold-1; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}
	if !rl.Attempt("1.2.3.4", now, true) {
		t.Fatalf("expected correct attempt to succeed again after a prior reset")
	}
}

func TestRateLimiter_ResetsAfter24HoursOfInactivity(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}

	// 24 hours with no attempts at all resets the failure count, even
	// though the account never succeeded.
	if !rl.Attempt("1.2.3.4", now.Add(24*time.Hour), true) {
		t.Fatalf("expected failure count to reset after 24 hours of inactivity")
	}
}

func TestRateLimiter_TracksSourcesIndependently(t *testing.T) {
	rl := NewRateLimiter()
	now := time.Now()

	for i := 0; i < rateLimitThreshold; i++ {
		rl.Attempt("1.2.3.4", now, false)
	}

	if !rl.Attempt("5.6.7.8", now, true) {
		t.Fatalf("expected a different source IP to be unaffected by another IP's lockout")
	}
}
