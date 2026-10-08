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
	st.Accounts["a"].FailSince = time.Now().Add(-10 * time.Minute)
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

	// Idle, but the refresh token is dead too: Claude Code cannot refresh it,
	// so the expiry no longer explains anything and it fails over.
	r.d.st.Accounts["a"].RefreshExpiry = time.Now().Add(-time.Minute)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("expired token with a dead refresh token: swaps = %+v, want one into b", swaps)
	}
}

// Owner decision 2026-10-07: a blind failover is never forced mid-turn. A
// continuously busy session never gets one, however long max_switch_wait has
// been exceeded; the first idle gap does.
func TestDaemonNeverForcesABlindFailoverMidTurn(t *testing.T) {
	r := blindRig(t, 3)
	det := r.dets["default"]
	det.idle, det.last = false, time.Now()
	il := r.d.profs[0]
	for i := 0; i < 5; i++ {
		il.wantSwitchSince = time.Now().Add(-time.Duration(i+1) * time.Hour)
		r.d.evaluate(context.Background(), il, "poll")
		if swaps := r.v.ops("swap"); len(swaps) != 0 {
			t.Fatalf("forced a blind failover mid-turn after %dh: %+v", i+1, swaps)
		}
	}
	det.idle = true
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("idle gap: swaps = %+v, want one into b", swaps)
	}
}

// Time spent waiting for an idle gap on a blind failover is not time the
// ordinary switch that replaces it has waited. When the first good read after
// the blind spell is over the trigger, that switch gets its full
// max_switch_wait before it may go ahead mid-turn.
func TestDaemonAnOrdinarySwitchAfterABlindOneGetsItsOwnWait(t *testing.T) {
	r := blindRig(t, 3)
	det := r.dets["default"]
	det.idle, det.last = false, time.Now()
	il := r.d.profs[0]
	r.d.evaluate(context.Background(), il, "poll")
	il.wantSwitchSince = time.Now().Add(-10 * time.Minute) // busy for 10 min, blind
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("blind failover forced mid-turn: %+v", swaps)
	}

	// The account reads again, over its trigger: an ordinary switch.
	put(r.d.st, "a", 90)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("the ordinary switch went ahead mid-turn on the blind phase's wait: %+v", swaps)
	}

	// Once its own max_switch_wait (90s in testCfg) has passed, it goes ahead.
	il.wantSwitchSince = time.Now().Add(-2 * time.Minute)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("after its own wait: swaps = %+v, want one into b", swaps)
	}
}

// A refusal seen in the transcripts still switches at once, busy or not: it
// is not a blind failover.
func TestDaemonARefusalStillSwitchesMidTurn(t *testing.T) {
	r := blindRig(t, 3)
	det := r.dets["default"]
	det.idle, det.last = false, time.Now()
	a := r.d.st.Accounts["a"]
	a.BurntTil, a.BurntWin = time.Now().Add(time.Hour), "five_hour"
	il := r.d.profs[0]
	r.d.evaluate(context.Background(), il, "poll") // the refusal switch starts its wait
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swapped before max_switch_wait: %+v", swaps)
	}
	il.wantSwitchSince = time.Now().Add(-time.Hour)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("refused while busy: swaps = %+v, want one into b", swaps)
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
	st.Accounts["a"].FailSince = time.Now().Add(-10 * time.Minute)
	r.v.holds = map[string]string{"item-work/b": "yes"}
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("installed b while work's item holds it: %+v", swaps)
	}
}

// A streak recorded by an earlier daemon is not time this one has observed:
// a restart (or a reboot after a long sleep) must not fail over on a count
// carried forward in state.json.
func TestDaemonStartDoesNotCarryAFailureStreakForward(t *testing.T) {
	r := blindRig(t, 5)
	r.d.start(context.Background())
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("failed over at startup on an inherited streak: %+v", swaps)
	}
	a := r.d.st.Accounts["a"]
	if a.ReadFails != 0 || !a.FailSince.IsZero() {
		t.Errorf("streak survived startup: ReadFails %d, FailSince %v", a.ReadFails, a.FailSince)
	}
}
