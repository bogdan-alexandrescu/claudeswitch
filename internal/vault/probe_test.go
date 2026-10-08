package vault

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// A refusal that persists re-checks every tick. The seat behind a token is
// asked once per probe period, and a changed token is asked afresh; anything
// more spends the budget scheduled polls live on.
func TestHoldsAccountProbesOncePerTokenPerPeriod(t *testing.T) {
	entries := map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")}
	v, ep := testVaultWith(t, entries, nil, map[string]string{
		"x-fresh-login": "ux@org",
		"x-relogin":     "ux@org",
	})
	ctx := context.Background()
	before := v.budget.Remaining()

	for i := 0; i < 15; i++ { // five minutes of ticks
		if holds, known := v.HoldsAccount(ctx, item("b", "x-fresh-login"), "x", "ux@org"); !holds || !known {
			t.Fatalf("tick %d: (%v, %v), want held", i, holds, known)
		}
	}
	if ep.profiles != 1 {
		t.Fatalf("%d identity lookups over fifteen ticks, want 1", ep.profiles)
	}
	if spent := before - v.budget.Remaining(); spent > 1 {
		t.Errorf("the probes spent %d scheduled calls, want at most 1", spent)
	}

	// A new token is a new question.
	v.HoldsAccount(ctx, item("b", "x-relogin"), "x", "ux@org")
	if ep.profiles != 2 {
		t.Fatalf("%d identity lookups after the token changed, want 2", ep.profiles)
	}

	// An unsettled answer is cached too, so a failing probe is not retried
	// every tick either.
	for i := 0; i < 5; i++ {
		if _, known := v.HoldsAccount(ctx, item("b", "no-profile"), "x", "ux@org"); known {
			t.Fatal("a failed probe must be unknown")
		}
	}
	if ep.profiles != 3 {
		t.Fatalf("%d identity lookups after five failing ticks, want 3", ep.profiles)
	}

	// Once the period is over, the question is asked again.
	old := holdsProbeTTL
	holdsProbeTTL = 0
	t.Cleanup(func() { holdsProbeTTL = old })
	v.HoldsAccount(ctx, item("b", "x-fresh-login"), "x", "ux@org")
	if ep.profiles != 4 {
		t.Fatalf("%d identity lookups after the period, want 4", ep.profiles)
	}
}

// The probe is not a swap: it never spends the call reserved for one.
func TestHoldsAccountProbeLeavesTheSwapReserve(t *testing.T) {
	v, ep := testVaultWith(t, map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")},
		nil, map[string]string{"x-fresh-login": "ux@org"})
	b := usage.NewBudget()
	b.SetAllowance(2) // one scheduled call, one reserved
	v.budget = b
	if ok, _ := b.Allow("someone-else", usage.Scheduled); !ok {
		t.Fatal("setup: the scheduled call was refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, known := v.HoldsAccount(ctx, item("b", "x-fresh-login"), "x", "ux@org"); known {
		t.Error("with only the reserve left the probe must not run, so the answer is unknown")
	}
	if ep.profiles != 0 {
		t.Errorf("the probe spent the swap reserve (%d calls)", ep.profiles)
	}
}
