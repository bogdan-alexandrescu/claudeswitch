package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Shared pools (docs/PROFILES.md §10, D31–D34) in the daemon: pools used in
// order, an account live in another profile passed over for the next
// candidate, and one machine-wide switch lock around every live write.

// sharedProfiles is default (pools own, then team) and work (pool team):
// own is a and b, team is t1, t2 and t3. Priority puts team first, so a
// pick of an own account shows the pool order deciding.
func sharedProfiles() *config.Config {
	cfg := testCfg()
	cfg.Priority = []string{"t1", "t2", "t3", "a", "b"}
	cfg.Accounts = []config.Account{{ID: "a"}, {ID: "b"}, {ID: "t1"}, {ID: "t2"}, {ID: "t3"}}
	cfg.Pools = []config.Pool{
		{Name: "own", Accounts: []string{"a", "b"}},
		{Name: "team", Accounts: []string{"t1", "t2", "t3"}},
	}
	cfg.Profiles = []config.Profile{
		{Name: "default", Pools: []string{"own", "team"}},
		{Name: "work", Dir: "~/.claude-work", Pools: []string{"team"}},
	}
	return cfg
}

// sharedRig is a live daemon over sharedProfiles, with its own HOME so the
// switch lock is this test's alone.
func sharedRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return newRig(t, sharedProfiles(), true)
}

// D33: default uses own before team, though team's account has more room.
func TestDaemonUsesItsPoolsInOrder(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("a")
	put(st, "a", 90)
	put(st, "b", 60)
	put(st, "t1", 5)

	r.d.evaluate(context.Background(), r.prof("default"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("swaps = %+v, want b: own comes before team", swaps)
	}
}

// D34: the account with the most room is live in default, as state
// records; work takes the next one, and nothing is refused or alarmed.
func TestDaemonTakesTheNextCandidateWhenOneIsLiveInAnotherProfile(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("t2")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t2", 5)
	put(st, "t3", 30)

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	swaps := r.v.ops("swap")
	if len(swaps) != 1 || swaps[0].account != "t3" || swaps[0].item != r.items["work"] {
		t.Fatalf("swaps = %+v, want t3 into work's item", swaps)
	}
	if errs := r.aud.kind("error"); len(errs) != 0 {
		t.Errorf("error rows %+v; an account in use elsewhere is routine under sharing", errs)
	}
	if logs := r.logs.String(); strings.Contains(logs, "level=ERROR") {
		t.Errorf("logged an error:\n%s", logs)
	}
	if len(r.nt.sent) != 1 || !strings.Contains(r.nt.sent[0], "t1 → t3") {
		t.Errorf("notifications = %q, want the switch alone", r.nt.sent)
	}
}

// With nothing else, work waits, and its decision says which account is in
// use and where.
func TestDaemonWaitsWhenTheOnlyRoomIsLiveInAnotherProfile(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("t2")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t2", 5)
	put(st, "t3", 99)

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swaps = %+v; t2 is live in default", swaps)
	}
	dec := r.aud.kind("decision")
	if len(dec) != 1 || dec[0].Decision != "wait" || !strings.Contains(dec[0].Reason, "t2: in use by default") {
		t.Fatalf("decisions = %+v, want a wait naming t2 in use by default", dec)
	}
	if errs := r.aud.kind("error"); len(errs) != 0 {
		t.Errorf("error rows %+v", errs)
	}
	// Its notice says the pool is in use, not that everything is burnt.
	if len(r.nt.sent) != 1 || !strings.Contains(r.nt.sent[0], "no account free") ||
		!strings.Contains(r.nt.sent[0], "t2: in use by default") || strings.Contains(r.nt.sent[0], "burnt") {
		t.Errorf("notifications = %q, want one saying t2 is in use by default", r.nt.sent)
	}
}

// State has not caught up: default's item holds t2 (a hand login). The §3
// check finds it, and the daemon decides again without it rather than
// stopping there: t3 is taken. Routine under sharing, so INFO, no alarm.
func TestDaemonRedecidesWhenTheCheckFindsTheTargetLiveElsewhere(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t2", 5)
	put(st, "t3", 30)
	r.v.holds = map[string]string{"item-default/t2": "yes"}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	swaps := r.v.ops("swap")
	if len(swaps) != 1 || swaps[0].account != "t3" {
		t.Fatalf("swaps = %+v, want t3: t2 is in default's item", swaps)
	}
	if st.Profile("work").Active != "t3" {
		t.Errorf("work is on %q, want t3", st.Profile("work").Active)
	}
	logs := r.logs.String()
	if strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, "in use in another profile") {
		t.Errorf("want the refusal at INFO, no error:\n%s", logs)
	}
	for _, n := range r.nt.sent {
		if strings.Contains(n, "refused") {
			t.Errorf("notified a routine refusal: %q", n)
		}
	}
}

// Unknown counts as live (D18): an item that cannot say is passed over too.
func TestDaemonPassesOverATargetThatMayBeLiveElsewhere(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t2", 5)
	put(st, "t3", 30)
	r.v.holds = map[string]string{"item-default/t2": "unknown"}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "t3" {
		t.Fatalf("swaps = %+v, want t3", swaps)
	}
}

// D17 under sharing: an account from a pool this profile lists is its own,
// whichever profile lists that pool first.
func TestDaemonDoesNotFlagASharedAccountAsAnotherPools(t *testing.T) {
	r := sharedRig(t)
	r.d.st.Profile("default").SetActive("a")
	r.d.noteCrossPool(r.prof("work"), "t2", "t1")
	if logs := r.logs.String(); strings.Contains(logs, "another profile's pool") {
		t.Fatalf("t2 is in team, which work lists:\n%s", logs)
	}
	// An account from a pool work does not list still is.
	r.d.noteCrossPool(r.prof("work"), "a", "t1")
	if logs := r.logs.String(); !strings.Contains(logs, "another profile's pool") {
		t.Fatalf("a is in own, which work does not list:\n%s", logs)
	}
}

// D34: one machine-wide switch lock. While another writer holds it, the
// daemon does not wait long and writes nothing; the next tick tries again.
func TestDaemonDefersASwapWhileTheSwitchLockIsHeld(t *testing.T) {
	r := sharedRig(t)
	old := daemonSwitchWait
	t.Cleanup(func() { daemonSwitchWait = old })
	daemonSwitchWait = 50 * time.Millisecond
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t3", 30)

	held, err := state.AcquireSwitchLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swapped %+v while another writer held the switch lock", swaps)
	}
	if holds := r.v.ops("holds"); len(holds) != 0 {
		t.Fatalf("ran the §3 check outside the lock: %+v", holds)
	}
	if logs := r.logs.String(); !strings.Contains(logs, "switch lock") || strings.Contains(logs, "level=ERROR") {
		t.Fatalf("want an INFO saying the swap waits for the switch lock:\n%s", logs)
	}
	held.Release()

	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "t3" {
		t.Fatalf("swaps = %+v after the lock was let go, want t3", swaps)
	}
}

// The lock is held from the §3 check through the write and its verify,
// freshening the target included, and let go afterwards.
func TestDaemonHoldsTheSwitchLockFromTheCheckThroughTheSwap(t *testing.T) {
	r := sharedRig(t)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("t1")
	put(st, "t1", 90)
	put(st, "t3", 30)
	r.v.needsRefresh["t3"] = true // freshened first: HoldsAccount, then RefreshIn

	unlocked := map[string]int{}
	r.v.onCall = func(op string) {
		l, err := state.AcquireSwitchLock(0)
		if err == nil {
			l.Release()
			unlocked[op]++
		} else if !errors.Is(err, state.ErrSwitchBusy) {
			t.Errorf("%s: %v", op, err)
		}
	}

	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "t3" {
		t.Fatalf("swaps = %+v, want t3", swaps)
	}
	if len(r.v.ops("refresh")) != 1 {
		t.Fatalf("the target was not freshened: %+v", r.v.calls)
	}
	if len(unlocked) != 0 {
		t.Fatalf("ran without the switch lock: %v", unlocked)
	}
	l, err := state.AcquireSwitchLock(0)
	if err != nil {
		t.Fatalf("the daemon kept the switch lock after the swap: %v", err)
	}
	l.Release()
}
