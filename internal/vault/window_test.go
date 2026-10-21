package vault

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// clocked is a budget on a clock the test owns, so pacing costs no time.
func clocked() *usage.Budget {
	now := time.Now()
	return usage.NewBudgetWithClock(func() time.Time { return now },
		func(d time.Duration) { now = now.Add(d) })
}

// fullWindow is a clocked budget, its machine-wide window filled to the edge
// routine polling stops at, by other credentials — two profiles polling hot,
// as on 2026-10-20.
func fullWindow(t *testing.T) *usage.Budget {
	t.Helper()
	b := clocked()
	for i := 0; ; i++ {
		if ok, _ := b.Allow(string(rune('k'+i)), usage.Scheduled); !ok {
			break
		}
	}
	return b
}

// `cs use` makes two swap calls — whose credential it overwrites, then the
// incoming one's verify — and the window held back one. The verify was
// refused by our own bookkeeping and a good switch was rolled back:
// "the new credential could not read usage: usage API call budget exhausted".
func TestASwapIsVerifiedWhenRoutinePollingHasFilledTheWindow(t *testing.T) {
	v, ep, _, it := swapWorld(t, "a-new", map[string]string{"a-new": seatA})
	v.budget = fullWindow(t)
	calls := usageCalls(ep)

	res, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if err != nil {
		t.Fatalf("swap refused with the window full: %v", err)
	}
	if res.RolledBack || res.Usage == nil {
		t.Fatalf("rolled back %v, verified %v; want verified and kept", res.RolledBack, res.Usage != nil)
	}
	if ep.profiles != 1 {
		t.Errorf("%d identity lookups, want the one for the overwritten credential", ep.profiles)
	}
	if len(*calls) != 1 || (*calls)[0] != "b-token" {
		t.Errorf("usage calls %v, want b's verify", *calls)
	}
}

// When the incoming account's own allowance is spent, a call would only buy
// a 429, which already leaves the swap installed and unverified. Our own
// refusal is no evidence against the credential either, so it is treated the
// same way: kept, not rolled back.
func TestASwapStandsWhenTheIncomingAccountsAllowanceIsSpent(t *testing.T) {
	v, ep, _, it := swapWorld(t, "a-old", map[string]string{"a-old": seatA})
	v.budget = clocked()
	for v.budget.AccountLevel("b-token") >= 1 {
		v.budget.Allow("b-token", usage.Interactive)
	}
	calls := usageCalls(ep)

	res, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if err != nil {
		t.Fatalf("swap failed on our own bookkeeping: %v", err)
	}
	if res.RolledBack {
		t.Fatal("a swap our own budget could not verify was rolled back")
	}
	if len(*calls) != 0 {
		t.Errorf("usage calls %v, want none", *calls)
	}
	if it.blob.ClaudeAIOAuth.AccessToken != "b-token" {
		t.Errorf("live holds %q, want b's credential installed", it.blob.ClaudeAIOAuth.AccessToken)
	}
}

// The refusal says it was ours, not the API's.
func TestALocalRefusalIsNotReportedAsTheAPIs(t *testing.T) {
	v, _, _, _ := swapWorld(t, "a-old", nil)
	v.budget = clocked()
	for v.budget.AccountLevel("b-token") >= 1 {
		v.budget.Allow("b-token", usage.Interactive)
	}
	_, err := v.fetch(context.Background(), "b-token", usage.Swap)
	rl, ok := usage.IsRateLimited(err)
	if !ok || !rl.Local {
		t.Fatalf("fetch = %v, want a local pause", err)
	}
	if rl.RetryAfter <= 0 {
		t.Errorf("retry after %v, want when the allowance refills", rl.RetryAfter)
	}
	if strings.Contains(err.Error(), "usage API rate limited") {
		t.Errorf("%q blames the API", err)
	}
}
