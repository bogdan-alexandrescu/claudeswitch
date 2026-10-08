package vault

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// R1: when the seat behind a live token cannot be read because the shared
// budget holds a rate-limit lock on that token, the unknown says when the
// lock clears, so a refusal can name the time to try again.
func TestHoldsAccountWhyNamesTheLockOnTheLiveToken(t *testing.T) {
	v, ep := testVaultWith(t, map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")},
		nil, map[string]string{"x-fresh-login": "ux@org"})
	v.budget.PenalizeLive("x-fresh-login", 90*time.Second)
	want, locked := v.budget.LockedUntil("x-fresh-login")
	if !locked {
		t.Fatal("setup: the token is not locked")
	}
	holds, known, retryAt := v.HoldsAccountWhy(context.Background(), item("b", "x-fresh-login"), "x", "ux@org")
	if holds || known {
		t.Fatalf("(%v, %v) under a lock, want unknown", holds, known)
	}
	if !retryAt.Equal(want) {
		t.Fatalf("retryAt = %v, want the lock's end %v", retryAt, want)
	}
	if ep.profiles != 0 {
		t.Errorf("%d identity lookups through a lock, want 0", ep.profiles)
	}
	// Cached: the next tick says the same without asking.
	if _, _, again := v.HoldsAccountWhy(context.Background(), item("b", "x-fresh-login"), "x", "ux@org"); !again.Equal(want) {
		t.Errorf("cached retryAt = %v, want %v", again, want)
	}
	// HoldsAccount is the same answer without the time.
	if _, known := v.HoldsAccount(context.Background(), item("b", "x-fresh-login"), "x", "ux@org"); known {
		t.Error("HoldsAccount must stay unknown under the lock")
	}
}

// A 429 from the profile endpoint arms the lock, and the unknown names its end.
func TestHoldsAccountWhyNamesTheLockA429Armed(t *testing.T) {
	v, ep := testVaultWith(t, map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")}, nil, nil)
	ep.limited = map[string]bool{"x-limited": true}
	_, known, retryAt := v.HoldsAccountWhy(context.Background(), item("b", "x-limited"), "x", "ux@org")
	if known {
		t.Fatal("a 429 must leave the answer unknown")
	}
	want, locked := v.budget.LockedUntil("x-limited")
	if !locked || !retryAt.Equal(want) {
		t.Fatalf("retryAt = %v, want the lock the 429 armed (%v, locked %v)", retryAt, want, locked)
	}
}

// Any other unknown carries no retry time: today's wording stands.
func TestHoldsAccountWhyOtherUnknownsHaveNoRetryTime(t *testing.T) {
	v, _ := testVaultWith(t, map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")}, nil, nil)
	ctx := context.Background()
	if _, known, at := v.HoldsAccountWhy(ctx, item("b", "no-profile"), "x", "ux@org"); known || !at.IsZero() {
		t.Errorf("failed lookup: known %v, retryAt %v; want unknown with no time", known, at)
	}
	if _, known, at := v.HoldsAccountWhy(ctx, item("b", "anything"), "nobody", ""); known || !at.IsZero() {
		t.Errorf("no seat to compare: known %v, retryAt %v; want unknown with no time", known, at)
	}
	// The window budget refusing is not a lock on this token: no time either.
	b := usage.NewBudget()
	b.SetAllowance(2)
	v.budget = b
	if ok, _ := b.Allow("someone-else", usage.Scheduled); !ok {
		t.Fatal("setup: the scheduled call was refused")
	}
	if _, known, at := v.HoldsAccountWhy(ctx, item("b", "x-other"), "x", "ux@org"); known || !at.IsZero() {
		t.Errorf("reserve only: known %v, retryAt %v; want unknown with no time", known, at)
	}
}

// A cached lock that has run out is not reported: the question is asked again.
func TestHoldsAccountWhyExpiredLockIsAskedAgain(t *testing.T) {
	v, ep := testVaultWith(t, map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")},
		nil, map[string]string{"x-fresh-login": "ux@org"})
	v.probes = map[string]seatProbe{"x-fresh-login": {at: time.Now(), retryAt: time.Now().Add(-time.Second)}}
	holds, known, at := v.HoldsAccountWhy(context.Background(), item("b", "x-fresh-login"), "x", "ux@org")
	if !holds || !known || !at.IsZero() {
		t.Fatalf("(%v, %v, %v) after the lock ran out, want held and known", holds, known, at)
	}
	if ep.profiles != 1 {
		t.Errorf("%d identity lookups, want 1", ep.profiles)
	}
}
