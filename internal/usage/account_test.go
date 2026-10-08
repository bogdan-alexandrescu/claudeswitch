package usage

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GROUND_TRUTH §42: the usage endpoint's limit is per ACCOUNT. These tests
// isolate that allowance from the machine-wide burst guard by raising the
// guard out of the way.
func accountBudget(start time.Time) (*Budget, *time.Time) {
	b, now := fixedBudget(start)
	b.SetAllowance(10_000)
	return b, now
}

// Three tiers (DESIGN 4.3c): a routine poll leaves both the hot reserve and
// the swap reserve alone; a hot poll may spend the hot reserve; a swap check
// may spend the rest, down to empty. Another account is untouched.
func TestAnAccountsAllowanceIsSpentInTiers(t *testing.T) {
	b, _ := accountBudget(time.Now())
	spend := func(p Priority, n int, what string) {
		t.Helper()
		for i := 0; i < n; i++ {
			if ok, reason := b.Allow("a", p); !ok {
				t.Fatalf("%s call %d refused early: %q", what, i+1, reason)
			}
		}
		if ok, reason := b.Allow("a", p); ok || reason != ReasonAccount {
			t.Fatalf("a %s call past its tier: ok=%v reason=%q, want %q", what, ok, reason, ReasonAccount)
		}
	}
	spend(Scheduled, AccountBurst-AccountReserve-DefaultHotReserve, "routine")
	spend(Hot, DefaultHotReserve, "hot")
	spend(Swap, AccountReserve, "swap")
	if ok, reason := b.Allow("b", Scheduled); !ok {
		t.Fatalf("another account was refused by a's allowance: %q", reason)
	}
}

// An overdue read of the account in use — its reading older than the
// cadence allows, typically right after a hot spell spent the hot reserve —
// may go below the hot reserve, down to the swap reserve plus OverdueFloor.
func TestAnOverdueReadMaySpendBelowTheHotReserve(t *testing.T) {
	b, _ := accountBudget(time.Now())
	for i := 0; i < AccountBurst-AccountReserve-DefaultHotReserve; i++ {
		b.Allow("a", Scheduled)
	}
	n := 0
	for ; n < 50; n++ {
		if ok, _ := b.Allow("a", Overdue); !ok {
			break
		}
	}
	if want := DefaultHotReserve - OverdueFloor; n != want {
		t.Errorf("%d overdue reads below the hot reserve, want %d", n, want)
	}
	if ok, _ := b.Allow("a", Hot); !ok {
		t.Error("a hot read was refused above the swap reserve")
	}
}

// The model's two advanced numbers come from config (owner decision
// 2026-10-07): hot_reserve and unseen_calls_per_hour.
func TestTheModelsAdvancedNumbersAreSettable(t *testing.T) {
	b, _ := accountBudget(time.Now())
	b.SetModel(0, 0)
	for i := 0; i < AccountBurst-AccountReserve; i++ {
		if ok, reason := b.Allow("a", Scheduled); !ok {
			t.Fatalf("with no hot reserve, routine call %d refused: %q", i+1, reason)
		}
	}
	// Out of range is refused and reported, and changes nothing.
	if err := b.SetModel(99, 50); err == nil {
		t.Error("an out-of-range model was accepted silently")
	}

	// The live account's refill is the full 30/h less the unseen spend.
	for _, c := range []struct{ unseen, want float64 }{{0, 5}, {6, 4}} {
		m, now := accountBudget(time.Now())
		m.SetModel(DefaultHotReserve, c.unseen)
		m.MarkLive("a")
		for i := 0; i < 10; i++ {
			m.Allow("a", Swap)
		}
		lvl := m.AccountLevel("a")
		*now = now.Add(10 * time.Minute)
		if got := m.AccountLevel("a") - lvl; math.Abs(got-c.want) > 1e-6 {
			t.Errorf("unseen %v/h: a live account regained %v in 10 min, want %v", c.unseen, got, c.want)
		}
	}
}

// The allowance refills one call per AccountRefill, and says when the next
// scheduled call will fit.
func TestAnAccountsAllowanceRefills(t *testing.T) {
	b, now := accountBudget(time.Now())
	for i := 0; i < AccountBurst-AccountReserve-DefaultHotReserve; i++ {
		b.Allow("a", Scheduled)
	}
	at := b.AccountReadyAt("a", Scheduled)
	if want := now.Add(AccountRefill); !at.Equal(want) {
		t.Fatalf("ready at %v, want one refill (%v) away", at.Sub(*now), AccountRefill)
	}
	*now = at.Add(-time.Second)
	if ok, _ := b.Allow("a", Scheduled); ok {
		t.Fatal("allowed before the refill")
	}
	*now = at
	if ok, reason := b.Allow("a", Scheduled); !ok {
		t.Fatalf("refused after the refill: %q", reason)
	}
	if got := b.AccountReadyAt("b", Scheduled); got.After(*now) {
		t.Errorf("an untouched account is ready at %v, want now", got)
	}
}

// An interactive call is charged, so the poller sees what it cost, but never
// refused: a login is the expensive part (see Interactive).
func TestAnInteractiveCallIsChargedButNeverRefused(t *testing.T) {
	b, _ := accountBudget(time.Now())
	for i := 0; i < AccountBurst+5; i++ {
		if ok, reason := b.Allow("a", Interactive); !ok {
			t.Fatalf("interactive call %d refused: %q", i+1, reason)
		}
	}
	if ok, _ := b.Allow("a", Swap); ok {
		t.Error("interactive calls were not charged to the account")
	}
}

// The model's numbers, checked against what they must allow and refuse.
// Steady polling at the refill rate never defers, all day; a hot spell at a
// minute for the whole lookahead never defers either; 20-second polling (the
// cadence that drew 429s, §42) is deferred before the measured edge of ~24.
func TestTheAccountModelAgainstTheMeasurement(t *testing.T) {
	steady, now := accountBudget(time.Now())
	for i := 0; i < 24*30; i++ {
		if ok, reason := steady.Allow("a", Scheduled); !ok {
			t.Fatalf("steady polling every %v deferred at call %d: %q", AccountRefill, i+1, reason)
		}
		*now = now.Add(AccountRefill)
	}

	// The hot spell comes after a working day of routine polling at the
	// refill rate on the live account, whose allowance Claude Code spends
	// too: the hot reserve is what is left for it.
	hot, now := accountBudget(time.Now())
	hot.MarkLive("a")
	for i := 0; i < 8*30; i++ {
		if i%30 == 0 {
			hot.MarkLive("a")
		}
		hot.Allow("a", Scheduled) // deferred or not; routine
		*now = now.Add(AccountRefill)
	}
	for i := 0; i < 15; i++ {
		if ok, reason := hot.Allow("a", Hot); !ok {
			t.Fatalf("a 15-minute hot spell at 60s, after a day's routine polling, deferred at call %d: %q", i+1, reason)
		}
		*now = now.Add(time.Minute)
	}

	fast, now := accountBudget(time.Now())
	n := 0
	for ; n < 100; n++ {
		if ok, _ := fast.Allow("a", Swap); !ok {
			break
		}
		*now = now.Add(20 * time.Second)
	}
	if n >= 24 {
		t.Errorf("20-second calls were allowed %d times; the endpoint refused the 25th (§42)", n)
	}
}

// The per-account record lives in the shared ledger, by digest, so every
// process sees one allowance per account and the file never holds a token.
func TestTheAccountAllowanceIsSharedThroughTheLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-calls.json")
	start := time.Now()
	one, _ := accountBudget(start)
	one.ledger = path
	for i := 0; i < AccountBurst-AccountReserve; i++ {
		one.Allow("sk-secret-token", Scheduled)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret-token") {
		t.Fatalf("the ledger holds a token: %s", raw)
	}
	two, _ := accountBudget(start)
	two.ledger = path
	if ok, reason := two.Allow("sk-secret-token", Scheduled); ok || reason != ReasonAccount {
		t.Errorf("a second process got a fresh allowance: ok=%v %q", ok, reason)
	}
}

// A credential known to be live — any profile's — gets the live cap whoever
// reports its 429: the vault's swap and probe calls use Penalize, and a
// twenty-minute lock on the token a session runs on is the outage the cap
// exists to prevent. The mark is in the ledger, so it reaches every process.
func TestALiveCredentialGetsTheLiveCapFromAnyCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-calls.json")
	start := time.Now()
	marker, _ := fixedBudget(start)
	marker.ledger = path
	marker.MarkLive("live-token")

	b, now := fixedBudget(start)
	b.ledger = path
	var live, idle time.Duration
	for i := 0; i < 5; i++ {
		if i > 0 {
			b.MarkLive("live-token") // the daemon re-reads live items every 15 minutes
		}
		b.Penalize("live-token", 0)
		b.Penalize("idle-token", 0)
		live, _ = b.CurrentBackoff("live-token")
		idle, _ = b.CurrentBackoff("idle-token")
		*now = now.Add(MaxBackoff + time.Second)
	}
	if live > MaxLiveBackoff {
		t.Errorf("a marked live credential backed off %v, cap %v", live, MaxLiveBackoff)
	}
	if idle != MaxBackoff {
		t.Errorf("an unmarked credential backed off %v, want %v", idle, MaxBackoff)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "live-token") {
		t.Errorf("the ledger holds a token: %s", raw)
	}
}

// §42: Retry-After: 0 says nothing. The backoff is our own: five minutes,
// doubling, at most twenty — and at most ten for the account in use, since
// the refusal clears within 10–15 minutes and 5+10 covers that.
func TestTheBackoffFollowsOurScheduleNotRetryAfterZero(t *testing.T) {
	for _, c := range []struct {
		live bool
		want []time.Duration
	}{
		{false, []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 20 * time.Minute}},
		{true, []time.Duration{5 * time.Minute, 10 * time.Minute, 10 * time.Minute}},
	} {
		b, now := fixedBudget(time.Now())
		for i, want := range c.want {
			if c.live {
				b.PenalizeLive("tok", 0)
			} else {
				b.Penalize("tok", 0)
			}
			got, _ := b.CurrentBackoff("tok")
			if got != want {
				t.Errorf("live=%v refusal %d: backoff %v, want %v", c.live, i+1, got, want)
			}
			*now = now.Add(got + time.Second)
		}
	}
}
