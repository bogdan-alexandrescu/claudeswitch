package vault

import (
	"context"
	"testing"
	"time"
)

// age moves a cached probe into the past, as if d had gone by.
func age(v *Vault, token string, d time.Duration) {
	v.probeMu.Lock()
	defer v.probeMu.Unlock()
	p := v.probes[token]
	p.at = p.at.Add(-d)
	v.probes[token] = p
}

// An unknown probe (refused by the budget, cancelled, failed) is a doubt the
// daemon refuses swaps over (D18), so it must clear soon: it is cached for a
// short while only. A definite seat keeps the long period.
func TestSeatProbeUnknownExpiresSoonerThanDefinite(t *testing.T) {
	v, ep := testVaultWith(t, nil, nil, map[string]string{"known-tok": "ux@org"})
	ctx := context.Background()

	if s := v.seatBehind(ctx, "known-tok"); s != "ux@org" {
		t.Fatalf("seat = %q, want ux@org", s)
	}
	if s := v.seatBehind(ctx, "unknown-tok"); s != "" {
		t.Fatalf("seat = %q, want unknown", s)
	}
	if ep.profiles != 2 {
		t.Fatalf("setup: %d identity lookups, want 2", ep.profiles)
	}

	// Two minutes on: the unknown is asked again, the definite answer is not.
	age(v, "known-tok", 2*time.Minute)
	age(v, "unknown-tok", 2*time.Minute)
	v.seatBehind(ctx, "known-tok")
	if ep.profiles != 2 {
		t.Fatalf("a definite seat was re-probed after 2m (%d calls); it holds for %s", ep.profiles, holdsProbeTTL)
	}
	v.seatBehind(ctx, "unknown-tok")
	if ep.profiles != 3 {
		t.Fatalf("an unknown probe was still cached after 2m (%d calls); it must expire sooner", ep.profiles)
	}

	// Within the short period it is still cached, so a lasting refusal does
	// not spend a call every tick.
	v.seatBehind(ctx, "unknown-tok")
	if ep.profiles != 3 {
		t.Fatalf("an unknown probe was re-asked at once (%d calls)", ep.profiles)
	}
}
