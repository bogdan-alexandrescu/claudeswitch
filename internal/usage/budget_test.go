package usage

import (
	"os"
	"path/filepath"
	"strings"
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
		if ok, _ := b.Allow("tok", Scheduled); !ok {
			t.Fatalf("scheduled call %d refused, expected allowed", i)
		}
	}
	if ok, reason := b.Allow("tok", Scheduled); ok || reason != ReasonReserved {
		t.Fatalf("scheduled call past the allowance: ok=%v reason=%q", ok, reason)
	}
	if ok, _ := b.Allow("tok", Swap); !ok {
		t.Fatal("priority call refused; the reserved slot must remain for a swap check")
	}
	if ok, reason := b.Allow("tok", Swap); ok || reason != ReasonBudget {
		t.Fatalf("budget exhausted but call allowed: ok=%v reason=%q", ok, reason)
	}
}

func TestBudgetRecoversAfterWindow(t *testing.T) {
	b, now := fixedBudget(time.Now())
	for i := 0; i < DefaultAllowance; i++ {
		b.Allow("tok", Swap)
	}
	if ok, _ := b.Allow("tok", Scheduled); ok {
		t.Fatal("expected exhausted budget")
	}
	*now = now.Add(DefaultWindow + time.Second)
	if ok, _ := b.Allow("tok", Scheduled); !ok {
		t.Fatal("budget did not recover after the window elapsed")
	}
}

func TestPenalizeBlocksEvenPriorityCallsWithThatCredential(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("tok", 299*time.Second)
	if ok, reason := b.Allow("tok", Swap); ok || reason != ReasonLockout {
		t.Fatalf("a 429 lockout must stop every call with that credential: ok=%v reason=%q", ok, reason)
	}
	if _, locked := b.LockedUntil("tok"); !locked {
		t.Fatal("LockedUntil should report the backoff")
	}
	*now = now.Add(300 * time.Second)
	if ok, _ := b.Allow("tok", Scheduled); !ok {
		t.Fatal("lockout did not expire")
	}
}

// Every caller shares one budget. A budget the vault could walk around is not a
// budget. A budget
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
	b.Penalize("tok", 0)
	til, locked := b.LockedUntil("tok")
	if !locked {
		t.Fatal("a zero Retry-After must not mean zero backoff")
	}
	if got := til.Sub(*now); got < MinBackoff {
		t.Fatalf("backoff %v is under the %v minimum", got, MinBackoff)
	}
	if ok, reason := b.Allow("tok", Swap); ok || reason != ReasonLockout {
		t.Fatalf("calls must be refused during the backoff: ok=%v reason=%q", ok, reason)
	}
}

func TestPenalizeHonoursALongerRetryAfter(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("tok", 299*time.Second)
	til, _ := b.LockedUntil("tok")
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
		b.Penalize("tok", 0)
		d, strikes := b.CurrentBackoff("tok")
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
	b.Penalize("tok", 0)
	// Past the first lock, so this is a second refusal rather than an echo of
	// the first. A refusal arriving while the lock still holds is ignored.
	*now = now.Add(MinBackoff + time.Second)
	b.Penalize("tok", 0)
	if _, strikes := b.CurrentBackoff("tok"); strikes != 2 {
		t.Fatalf("expected 2 strikes, got %d", strikes)
	}
	b.Succeeded("tok")
	if _, strikes := b.CurrentBackoff("tok"); strikes != 0 {
		t.Fatal("a successful call must clear the strike count")
	}
	*now = now.Add(MaxBackoff)
	b.Penalize("tok", 0)
	d, _ := b.CurrentBackoff("tok")
	if d > 2*MinBackoff {
		t.Fatalf("after a reset the next backoff should start small, got %v", d)
	}
}

// The server's own Retry-After still wins when it asks for longer.
func TestServerRetryAfterWinsWhenLonger(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	b.Penalize("tok", 10*time.Minute)
	if d, _ := b.CurrentBackoff("tok"); d < 10*time.Minute {
		t.Fatalf("got %v, want at least the 10m the server asked for", d)
	}
}

// A refusal that arrives while a lock is already held says nothing new — it is
// almost always a call that slipped past the budget. Counting it re-armed the
// full penalty, so the lock kept renewing itself and never ran down. Twelve
// watchdog restarts in two hours came of this, with no reading in between.
func TestARefusalDuringALockDoesNotExtendIt(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("tok", 10*time.Minute)
	til, locked := b.LockedUntil("tok")
	if !locked {
		t.Fatal("expected a lock after the first refusal")
	}

	*now = now.Add(time.Minute)
	b.Penalize("tok", 10*time.Minute)
	after, _ := b.LockedUntil("tok")
	if !after.Equal(til) {
		t.Errorf("the lock moved from %s to %s; a refusal during a lock must not extend it",
			til.Format(time.Kitchen), after.Format(time.Kitchen))
	}
	if _, strikes := b.CurrentBackoff("tok"); strikes != 1 {
		t.Errorf("expected the echo not to count as a strike, got %d", strikes)
	}
}

// Retry-After is advice, not a command. Honouring an hour literally meant the
// watchdog killed the daemon six times over while it waited, and each restart
// inherited the same lock.
func TestRetryAfterIsCappedSoItCannotOutlastTheWatchdog(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	b.Penalize("tok", time.Hour)
	d, _ := b.CurrentBackoff("tok")
	if d > MaxLock {
		t.Fatalf("lock of %v exceeds the %v cap", d, MaxLock)
	}
}

// A 429 is the API refusing one account. The account refused most is the live
// one, whose allowance Claude Code spends as well, and a single lock turned
// that into a pause on reading every other account (observed 2026-09-16).
func TestALockHoldsOnlyTheCredentialItWasArmedAgainst(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	b.Penalize("live", 10*time.Minute)

	if ok, reason := b.Allow("live", Scheduled); ok || reason != ReasonLockout {
		t.Errorf("the refused credential must wait: ok=%v %q", ok, reason)
	}
	if ok, reason := b.Allow("idle", Scheduled); !ok {
		t.Errorf("another credential must not inherit the lock: %q", reason)
	}
	if _, locked := b.LockedUntil("idle"); locked {
		t.Error("LockedUntil reports a lock on a credential that was never refused")
	}
	if _, n := b.AnyLocked(); n != 1 {
		t.Errorf("AnyLocked = %d, want 1", n)
	}

	b.Penalize("idle", 0)
	if til, n := b.AnyLocked(); n != 2 || til.Before(time.Now().Add(9*time.Minute)) {
		t.Errorf("AnyLocked = %d until %s, want 2 until the later lock", n, til)
	}
	b.Succeeded("idle")
	if _, locked := b.LockedUntil("live"); !locked {
		t.Error("a success with one credential cleared another's lock")
	}
}

// The window is still one for everyone: it is the burst guard.
func TestTheCallWindowIsSharedAcrossCredentials(t *testing.T) {
	b, _ := fixedBudget(time.Now())
	for i := 0; i < DefaultAllowance-ReservedForSwap; i++ {
		b.Allow(string(rune('a'+i)), Scheduled)
	}
	if ok, reason := b.Allow("fresh", Scheduled); ok || reason != ReasonReserved {
		t.Errorf("a new credential must not get a window of its own: ok=%v %q", ok, reason)
	}
}

// The ledger names credentials by digest, and never holds a token.
func TestTheLedgerKeepsPerCredentialLocksWithoutTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-calls.json")
	// A ledger written by the previous version, with its single global lock.
	legacy := `{"calls":[],"locked_until":"2099-01-01T00:00:00Z","strikes":3}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := fixedBudget(time.Now())
	b.ledger = path

	if ok, reason := b.Allow("idle", Scheduled); !ok {
		t.Fatalf("the old global lock must not survive the upgrade: %q", reason)
	}
	b.Penalize("sk-secret-token", time.Minute)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret-token") {
		t.Fatalf("the ledger holds a token: %s", raw)
	}
	if strings.Contains(string(raw), "locked_until") {
		t.Errorf("the legacy lock was written back: %s", raw)
	}

	// A second process reading the same file sees the lock.
	other, _ := fixedBudget(time.Now())
	other.ledger = path
	if _, locked := other.LockedUntil("sk-secret-token"); !locked {
		t.Error("the lock did not reach another process through the ledger")
	}
}

// A lock whose token was refreshed away is never cleared by a success, so it
// must not accumulate forever.
func TestExpiredLocksAreForgotten(t *testing.T) {
	b, now := fixedBudget(time.Now())
	b.Penalize("old-token", 0)
	*now = now.Add(MinBackoff + forgetLocksAfter + time.Minute)
	b.Allow("other", Scheduled)
	if len(b.locks) != 0 {
		t.Errorf("stale lock kept: %v", b.locks)
	}
}
