package usage

import (
	"testing"
	"time"
)

func fixedBudget(start time.Time) (*Budget, *time.Time) {
	now := start
	b := NewBudget()
	b.now = func() time.Time { return now }
	return b, &now
}

func TestBudgetHoldsBackOneCallForSwaps(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	for i := 0; i < DefaultAllowance-ReservedForSwap; i++ {
		if ok, _ := b.Allow(false); !ok {
			t.Fatalf("scheduled call %d refused, expected allowed", i)
		}
	}
	if ok, reason := b.Allow(false); ok || reason != ReasonReserved {
		t.Fatalf("scheduled call past the allowance: ok=%v reason=%q", ok, reason)
	}
	if ok, _ := b.Allow(true); !ok {
		t.Fatal("priority call refused; the reserved slot must remain for a swap check")
	}
	if ok, reason := b.Allow(true); ok || reason != ReasonBudget {
		t.Fatalf("budget exhausted but call allowed: ok=%v reason=%q", ok, reason)
	}
}

func TestBudgetRecoversAfterWindow(t *testing.T) {
	b, now := fixedBudget(time.Now())
	for i := 0; i < DefaultAllowance; i++ {
		b.Allow(true)
	}
	if ok, _ := b.Allow(false); ok {
		t.Fatal("expected exhausted budget")
	}
	*now = now.Add(DefaultWindow + time.Second)
	if ok, _ := b.Allow(false); !ok {
		t.Fatal("budget did not recover after the window elapsed")
	}
}

func TestPenalizeBlocksEvenPriorityCalls(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(299 * time.Second)
	if ok, reason := b.Allow(true); ok || reason != ReasonLockout {
		t.Fatalf("a 429 lockout must stop every call: ok=%v reason=%q", ok, reason)
	}
	if _, locked := b.LockedUntil(); !locked {
		t.Fatal("LockedUntil should report the backoff")
	}
	*now = now.Add(300 * time.Second)
	if ok, _ := b.Allow(false); !ok {
		t.Fatal("lockout did not expire")
	}
}

// The endpoint's limit applies to the machine, not to one component. A budget
// the vault could walk around is not a budget: that is how the daemon and the
// CLI together blew past the limit on 2026-09-09.
func TestSharedBudgetIsOneInstance(t *testing.T) {
	a, b := Shared(), Shared()
	if a != b {
		t.Fatal("Shared() must return the same budget to every caller")
	}
}

// A 429 whose Retry-After is zero or missing must still produce a real backoff.
// Without this the daemon kept calling an endpoint that had just refused it.
func TestPenalizeEnforcesAMinimumBackoff(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(0)
	til, locked := b.LockedUntil()
	if !locked {
		t.Fatal("a zero Retry-After must not mean zero backoff")
	}
	if got := til.Sub(*now); got < MinBackoff {
		t.Fatalf("backoff %v is under the %v minimum", got, MinBackoff)
	}
	if ok, reason := b.Allow(true); ok || reason != ReasonLockout {
		t.Fatalf("calls must be refused during the backoff: ok=%v reason=%q", ok, reason)
	}
}

func TestPenalizeHonoursALongerRetryAfter(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(299 * time.Second)
	til, _ := b.LockedUntil()
	if got := til.Sub(*now); got < 299*time.Second {
		t.Fatalf("a server-supplied backoff must not be shortened: got %v", got)
	}
}
