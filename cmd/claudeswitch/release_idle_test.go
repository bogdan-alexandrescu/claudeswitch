package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// Owner request and decisions 2026-10-23: work-team was live in the work
// profile, which nobody was using, so the default profile could not have it
// until the owner moved work by hand. One account cannot be live in two
// profiles (a refresh in one logs the other out), but an idle profile need
// not hold it: when another profile wants an account live in a profile whose
// sessions have been idle for release_idle_after (30m), claudeswitch first
// moves that profile to the best other account of its own pools. A pinned
// profile, recent activity, or no other account with room keeps it.

// releaseRig: default uses own [a] then team [t1]; work uses team [t1] then
// spare [w1]. Default's a is over its trigger, and t1, its only way out, is
// live in work.
func releaseRig(t *testing.T) *rig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := testCfg()
	cfg.Priority = []string{"a", "t1", "w1"}
	cfg.Accounts = []config.Account{{ID: "a"}, {ID: "t1"}, {ID: "w1"}}
	cfg.Pools = []config.Pool{
		{Name: "own", Accounts: []string{"a"}},
		{Name: "team", Accounts: []string{"t1"}},
		{Name: "spare", Accounts: []string{"w1"}},
	}
	cfg.Profiles = []config.Profile{
		{Name: "default", Pools: []string{"own", "team"}},
		{Name: "work", Dir: "~/.claude-work", Pools: []string{"team", "spare"}},
	}
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("t1")
	put(st, "a", 90)
	put(st, "t1", 5)
	put(st, "w1", 20)
	return r
}

func TestDaemonTakesAnAccountFromAnIdleProfile(t *testing.T) {
	r := releaseRig(t)
	r.d.st.Profile("work").ActivityAt = time.Now().Add(-2 * time.Hour)
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	swaps := r.v.ops("swap")
	if len(swaps) != 2 || swaps[0].item != r.prof("work").live || swaps[0].account != "w1" ||
		swaps[1].item != r.prof("default").live || swaps[1].account != "t1" {
		t.Fatalf("swaps = %+v, want work onto w1, then default onto t1", swaps)
	}
	if a := r.d.st.Profile("work").Active; a != "w1" {
		t.Errorf("work is on %q, want w1", a)
	}
	var released bool
	for _, e := range r.aud.kind("switch") {
		if e.Profile == "work" && e.From == "t1" && e.To == "w1" && strings.Contains(e.Reason, "released t1 for default") {
			released = true
		}
	}
	if !released {
		t.Errorf("no audit row for the release: %+v", r.aud.kind("switch"))
	}
}

func TestDaemonLeavesAnAccountWithARecentlyActiveProfile(t *testing.T) {
	r := releaseRig(t)
	r.d.st.Profile("work").ActivityAt = time.Now().Add(-5 * time.Minute)
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swaps = %+v; work was active five minutes ago", swaps)
	}
}

func TestDaemonLeavesAnAccountWithAPinnedProfile(t *testing.T) {
	r := releaseRig(t)
	r.d.st.Profile("work").ActivityAt = time.Now().Add(-2 * time.Hour)
	r.d.st.Profile("work").Pinned = "t1"
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swaps = %+v; work is pinned to t1", swaps)
	}
}

func TestDaemonLeavesAnAccountWhenTheIdleProfileHasNowhereToGo(t *testing.T) {
	r := releaseRig(t)
	r.d.st.Profile("work").ActivityAt = time.Now().Add(-2 * time.Hour)
	put(r.d.st, "w1", 99)
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swaps = %+v; work had no other account with room", swaps)
	}
}

func TestReleaseIdleAfterZeroNeverReleases(t *testing.T) {
	r := releaseRig(t)
	r.d.cfg.ReleaseIdleAfter = &config.Duration{}
	for _, il := range r.d.profs {
		il.cfg.ReleaseIdleAfter = &config.Duration{}
	}
	r.d.st.Profile("work").ActivityAt = time.Now().Add(-2 * time.Hour)
	r.d.evaluate(context.Background(), r.prof("default"), "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swaps = %+v with release_idle_after = 0", swaps)
	}
}
