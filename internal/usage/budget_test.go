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
		if ok, _ := b.Allow(Scheduled); !ok {
			t.Fatalf("scheduled call %d refused, expected allowed", i)
		}
	}
	if ok, reason := b.Allow(Scheduled); ok || reason != ReasonReserved {
		t.Fatalf("scheduled call past the allowance: ok=%v reason=%q", ok, reason)
	}
	if ok, _ := b.Allow(Swap); !ok {
		t.Fatal("priority call refused; the reserved slot must remain for a swap check")
	}
	if ok, reason := b.Allow(Swap); ok || reason != ReasonBudget {
		t.Fatalf("budget exhausted but call allowed: ok=%v reason=%q", ok, reason)
	}
}

func TestBudgetRecoversAfterWindow(t *testing.T) {
	b, now := fixedBudget(time.Now())
	for i := 0; i < DefaultAllowance; i++ {
		b.Allow(Swap)
	}
	if ok, _ := b.Allow(Scheduled); ok {
		t.Fatal("expected exhausted budget")
	}
	*now = now.Add(DefaultWindow + time.Second)
	if ok, _ := b.Allow(Scheduled); !ok {
		t.Fatal("budget did not recover after the window elapsed")
	}
}

func TestPenalizeBlocksEvenPriorityCalls(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(299 * time.Second)
	if ok, reason := b.Allow(Swap); ok || reason != ReasonLockout {
		t.Fatalf("a 429 lockout must stop every call: ok=%v reason=%q", ok, reason)
	}
	if _, locked := b.LockedUntil(); !locked {
		t.Fatal("LockedUntil should report the backoff")
	}
	*now = now.Add(300 * time.Second)
	if ok, _ := b.Allow(Scheduled); !ok {
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
	if ok, reason := b.Allow(Swap); ok || reason != ReasonLockout {
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

// A fixed backoff meant a sustained refusal was answered with one request a
// minute for as long as it lasted — 224 of them in a night. Each consecutive
// refusal must wait longer.
func TestBackoffGrowsWithConsecutiveRefusals(t *testing.T) {
	b, now := fixedBudget(time.Now())
	var waits []time.Duration
	for i := 0; i < 5; i++ {
		b.Penalize(0)
		d, strikes := b.CurrentBackoff()
		if strikes != i+1 {
			t.Fatalf("strike %d recorded as %d", i+1, strikes)
		}
		waits = append(waits, d)
		*now = now.Add(d + time.Second) // wait it out, then be refused again
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			t.Fatalf("backoff did not grow: %v then %v", waits[i-1], waits[i])
		}
	}
	if waits[len(waits)-1] > MaxBackoff {
		t.Fatalf("backoff %v exceeds the cap %v", waits[len(waits)-1], MaxBackoff)
	}
}

// One refusal must not leave the backoff escalated for the rest of the day.
func TestSuccessResetsTheBackoff(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(0)
	// Past the first lock, so this is a second refusal rather than an echo of
	// the first. A refusal arriving while the lock still holds is ignored.
	*now = now.Add(MinBackoff + time.Second)
	b.Penalize(0)
	if _, strikes := b.CurrentBackoff(); strikes != 2 {
		t.Fatalf("expected 2 strikes, got %d", strikes)
	}
	b.Succeeded()
	if _, strikes := b.CurrentBackoff(); strikes != 0 {
		t.Fatal("a successful call must clear the strike count")
	}
	*now = now.Add(MaxBackoff)
	b.Penalize(0)
	d, _ := b.CurrentBackoff()
	if d > 2*MinBackoff {
		t.Fatalf("after a reset the next backoff should start small, got %v", d)
	}
}

// The server's own Retry-After still wins when it asks for longer.
func TestServerRetryAfterWinsWhenLonger(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	b.Penalize(10 * time.Minute)
	if d, _ := b.CurrentBackoff(); d < 10*time.Minute {
		t.Fatalf("got %v, want at least the 10m the server asked for", d)
	}
}

// A refusal that arrives while a lock is already held says nothing new — it is
// almost always a call that slipped past the budget. Counting it re-armed the
// full penalty, so the lock kept renewing itself and never ran down. Twelve
// watchdog restarts in two hours came of this, with no reading in between.
func TestARefusalDuringALockDoesNotExtendIt(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize(10 * time.Minute)
	til, locked := b.LockedUntil()
	if !locked {
		t.Fatal("expected a lock after the first refusal")
	}

	*now = now.Add(time.Minute)
	b.Penalize(10 * time.Minute)
	after, _ := b.LockedUntil()
	if !after.Equal(til) {
		t.Errorf("the lock moved from %s to %s; a refusal during a lock must not extend it",
			til.Format(time.Kitchen), after.Format(time.Kitchen))
	}
	if _, strikes := b.CurrentBackoff(); strikes != 1 {
		t.Errorf("expected the echo not to count as a strike, got %d", strikes)
	}
}

// Retry-After is advice, not a command. Honouring an hour literally meant the
// watchdog killed the daemon six times over while it waited, and each restart
// inherited the same lock.
func TestRetryAfterIsCappedSoItCannotOutlastTheWatchdog(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	b.Penalize(time.Hour)
	d, _ := b.CurrentBackoff()
	if d > MaxLock {
		t.Fatalf("lock of %v exceeds the %v cap", d, MaxLock)
	}
}
