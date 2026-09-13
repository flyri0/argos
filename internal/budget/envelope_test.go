package budget

import "testing"

func TestAvailable_PositiveRollover(t *testing.T) {
	// Last month ended with 5000 left over; this month budgets 2000 more
	// and has 1000 of spending (activity is negative).
	got := Available(5000, 2000, -1000)
	want := int64(6000)
	if got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

func TestAvailable_ResetsAfterOverspending(t *testing.T) {
	// Last month overspent by 3000; the negative must NOT carry forward.
	got := Available(-3000, 1000, -500)
	want := int64(500) // 0 (reset) + 1000 - 500
	if got != want {
		t.Fatalf("expected %d (negative rollover reset to 0), got %d", want, got)
	}
}

func TestAvailable_FirstMonthBudgeted(t *testing.T) {
	// No prior month at all: caller passes previousAvailable as 0.
	got := Available(0, 1000, -200)
	want := int64(800)
	if got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}
