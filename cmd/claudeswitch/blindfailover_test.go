package main

import (
	"context"
	"testing"
	"time"
)

// blindRig is one profile whose active account a has failed n reads in a
// row, its last good reading ten minutes old, beside a healthy b.
func blindRig(t *testing.T, n int) *rig {
	t.Helper()
	r := newRig(t, testCfg(), true)
	st := r.d.st
	st.Default().SetActive("a")
	put(st, "a", 40)
	st.Accounts["a"].LastAt = time.Now().Add(-10 * time.Minute)
	st.Accounts["a"].ReadFails = n
	put(st, "b", 10)
	return r
}

// A2 end to end: the daemon fails over from an account it has been unable to
// read, into the profile's own item.
func TestDaemonFailsOverWhenBlind(t *testing.T) {
	r := blindRig(t, 3)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	swaps := r.v.ops("swap")
	if len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("swaps = %+v, want one into b", swaps)
	}
	if got := r.d.st.Default().Active; got != "b" {
		t.Errorf("active = %q, want b", got)
	}
}

// It is an ordinary rotation for timing: a busy session waits for an idle gap.
func TestDaemonBlindFailoverWaitsForAnIdleGap(t *testing.T) {
	r := blindRig(t, 3)
	r.dets["default"].idle = false
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swapped mid-turn: %+v", swaps)
	}
}

// The daemon tells the policy whether the session is busy, so an expired
// token on an idle session is held and the same token on a busy one is not.
func TestDaemonHoldsAnExpiredTokenOnAnIdleSession(t *testing.T) {
	r := blindRig(t, 3)
	r.d.st.Accounts["a"].TokenExpiry = time.Now().Add(-time.Hour)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("failed over from a token that merely expired while idle: %+v", swaps)
	}

	// Busy: the idle gap is never reached, so let max_switch_wait elapse.
	r.dets["default"].idle = false
	r.dets["default"].last = time.Now()
	il := r.d.profs[0]
	il.wantSwitchSince = time.Now().Add(-time.Hour)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("busy session with an expired token: swaps = %+v, want one into b", swaps)
	}
}

// D18 / §3 still apply: a target live in another profile is refused.
func TestDaemonBlindFailoverRefusesATargetLiveElsewhere(t *testing.T) {
	cfg := twoProfiles()
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	for _, id := range []string{"a", "b", "w1", "w2"} {
		put(st, id, 10)
	}
	st.Accounts["a"].LastAt = time.Now().Add(-10 * time.Minute)
	st.Accounts["a"].ReadFails = 3
	r.v.holds = map[string]string{"item-work/b": "yes"}
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("installed b while work's item holds it: %+v", swaps)
	}
}
