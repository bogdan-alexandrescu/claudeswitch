package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 7 security review, MAJOR 1 (owner decision: ghosts). Removing an
// profile from the config, or changing its dir, used to drop its live item
// from every §3 check: the account live there could then be installed in a
// second profile, and whichever refreshed first logged the other out. The
// old item is now kept as a ghost that every check consults until it no
// longer holds that account, or `cs profile forget`.

const ghostTwo = `
switch_at = 85
priority = ["a", "b", "w1"]

[[account]]
id = "a"
[[account]]
id = "b"
[[account]]
id = "w1"

[[profile]]
name = "default"
pool = ["a", "b"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

// B's pool merged into A: A must not install the account still live in B's
// old item.
func TestGhostStopsMergingAPoolIntoAnotherFromDoublingACredential(t *testing.T) {
	path := writeConfig(t, ghostTwo)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, true)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	st := r.d.st
	put(st, "a", 95)
	put(st, "b", 95)
	put(st, "w1", 10)
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")

	merged := strings.Replace(ghostTwo, "\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1)
	merged = strings.Replace(merged, `pool = ["a", "b"]`, `pool = ["a", "b", "w1"]`, 1)
	rewrite(t, path, merged)
	r.d.reload(context.Background())

	if n := len(r.v.ops("swap")); n != 0 {
		t.Fatalf("swapped %d times: w1 may still be live in work's old item", n)
	}
	gs := st.GhostList()
	if len(gs) != 1 || gs[0].Profile != "work" || gs[0].Account != "w1" || gs[0].Service != "item-work" {
		t.Fatalf("ghosts: %+v", gs)
	}
	logs := r.logs.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "ghost") {
		t.Errorf("no WARN when the ghost was made:\n%s", logs)
	}
	errs := r.aud.kind("error")
	if len(errs) == 0 || !strings.Contains(errs[0].Err, "may still be live in removed profile work") {
		t.Errorf("the refusal must name the ghost: %+v", errs)
	}
}

// A re-pointed profile's old item is a ghost too: the account it held may
// still be live there, so nothing else may install it.
func TestGhostGuardsARepointedProfilesOldItem(t *testing.T) {
	path := writeConfig(t, ghostTwo)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, true)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	old := r.d.resolve
	r.d.resolve = func(in config.Profile) (keychain.Live, error) {
		if in.Dir == "~/.claude-work2" {
			return fakeLive{name: "item-work2"}, nil
		}
		return old(in)
	}
	put(r.d.st, "w1", 10)
	r.d.st.Profile("work").SetActive("w1")

	rewrite(t, path, strings.Replace(ghostTwo, "~/.claude-work", "~/.claude-work2", 1))
	r.d.reload(context.Background())

	gs := r.d.st.GhostList()
	if len(gs) != 1 || gs[0].Why != state.GhostRepointed || gs[0].Service != "item-work" {
		t.Fatalf("ghosts: %+v", gs)
	}
	// The re-pointed profile itself may not swap w1 into its new item.
	other, why, _ := r.d.liveElsewhere(context.Background(), r.prof("work"), "w1")
	if other == "" || !strings.Contains(why, "work") {
		t.Fatalf("w1 installable while it may still be live in work's old item: %q %q", other, why)
	}
}

// Ghosts are persisted: a daemon started later still honours them.
func TestGhostSurvivesADaemonRestart(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, strings.Replace(ghostTwo,
		"\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1)))
	statePath := filepath.Join(t.TempDir(), "state.json")
	st, _ := state.Load(statePath, cfg.ProfileNames()...)
	st.AddGhost(&state.Ghost{Profile: "work", Why: state.GhostRemoved, Service: "item-work", Account: "w1",
		Since: time.Now()})
	if err := st.SaveAs(state.OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	r := newRig(t, cfg, true)
	fresh, err := state.Load(statePath, cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	r.d.st, r.p.st = fresh, fresh
	if other, _, _ := r.d.liveElsewhere(context.Background(), r.prof("default"), "w1"); other == "" {
		t.Fatal("a restarted daemon forgot the ghost")
	}
}

// The ghost is released once its item no longer holds the account (checked
// as HoldsAccount does; unknown keeps guarding, D18), and logs INFO.
func TestGhostIsReleasedWhenTheOldItemNoLongerHoldsTheAccount(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, ghostTwo))
	r := newRig(t, cfg, true)
	r.d.st.AddGhost(&state.Ghost{Profile: "gone", Why: state.GhostRemoved, Service: "item-gone", Account: "w1",
		Since: time.Now()})

	r.v.holds = map[string]string{"item-gone/w1": "unknown"}
	r.d.releaseGhosts(context.Background())
	if len(r.d.st.GhostList()) != 1 {
		t.Fatal("released while unknown")
	}
	r.v.holds = map[string]string{"item-gone/w1": "yes"}
	r.d.releaseGhosts(context.Background())
	if len(r.d.st.GhostList()) != 1 {
		t.Fatal("released while it still holds the account")
	}
	r.v.holds = nil
	r.d.releaseGhosts(context.Background())
	if len(r.d.st.GhostList()) != 0 {
		t.Fatal("not released once the item no longer holds the account")
	}
	if !strings.Contains(r.logs.String(), "level=INFO") || !strings.Contains(r.logs.String(), "ghost released") {
		t.Errorf("no INFO on release:\n%s", r.logs)
	}
}

// `cs profile forget <name>` releases a ghost by hand.
func TestProfileForgetReleasesTheGhost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeConfig(t, ghostTwo)
	st, _ := state.Load("", "default", "work")
	st.AddGhost(&state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "svc", Account: "w1", Since: time.Now()})
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if err := cmdProfile([]string{"forget", "nothing", "--config", path}); err == nil {
		t.Error("forgetting a profile with no ghost succeeded")
	}
	if err := cmdProfile([]string{"forget", "old", "--config", path}); err != nil {
		t.Fatal(err)
	}
	after, _ := state.Load("", "default", "work")
	if len(after.GhostList()) != 0 {
		t.Fatalf("ghosts after forget: %+v", after.GhostList())
	}
}

// The CLI's §3 checks (use, login) and its live-holder checks (refresh,
// remove) consult ghosts too.
func TestCLIChecksConsultGhosts(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, ghostTwo))
	st, _ := state.Load(filepath.Join(t.TempDir(), "s.json"), "default", "work")
	st.AddGhost(&state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "item-old", Account: "w1", Since: time.Now()})
	mk := func(service, file string) keychain.Live { return fakeLive{name: service} }
	targets := withGhosts(st, []liveTarget{{name: "default", live: fakeLive{name: "item-default"}}}, mk)

	other, why, _ := liveElsewhereOf(context.Background(), &fakeVault{}, cfg, st, "default", targets, "w1")
	if other == "" || !strings.Contains(why, "may still be live in removed profile old") {
		t.Fatalf("use/login: %q %q", other, why)
	}
	// Another account in the ghost's item (a hand login there) is found too.
	v := &fakeVault{holds: map[string]string{"item-old/b": "yes"}}
	if other, _, _ := liveElsewhereOf(context.Background(), v, cfg, st, "default", targets, "b"); other == "" {
		t.Error("an account in the ghost's item was not found")
	}
	if name, _, live := liveHolder(targets, v, "b"); !live || !strings.Contains(name, "old") {
		t.Errorf("refresh/remove: %q %v", name, live)
	}
}

// status and doctor list ghosts still guarding.
func TestStatusAndDoctorListGhosts(t *testing.T) {
	g := &state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "item-old", Account: "w1",
		Since: time.Now().Add(-3 * time.Hour)}
	lines := ghostLines([]*state.Ghost{g})
	for _, want := range []string{"old", "w1", "cs profile forget old"} {
		if !strings.Contains(lines, want) {
			t.Errorf("status ghost lines lack %q:\n%s", want, lines)
		}
	}
	if ghostLines(nil) != "" {
		t.Error("ghost lines with no ghosts")
	}
	w := newDoctorWorld(t, doctorBaseConfig)
	out, err := w.runWith(t, func(d *doctorDeps) {
		d.ghosts = func() []*state.Ghost { return []*state.Ghost{g} }
	})
	if err != nil {
		t.Fatalf("a ghost is a warning: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn] ghost") || !strings.Contains(out, "cs profile forget old") {
		t.Errorf("doctor:\n%s", out)
	}
}

// A profile declared again after a removal keeps no stale active account
// from state: it is cleared and held until attributed, like a re-pointed one.
func TestReaddedProfileForgetsItsStaleActiveAccount(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	put(r.d.st, "w2", 10)
	r.d.st.Profile("third").SetActive("w2")
	rewrite(t, path, reloadTwoProfiles+thirdProfile)
	r.d.reload(context.Background())
	if a := r.d.st.Profile("third").Active; a != "" {
		t.Errorf("third came back believing %q is live", a)
	}
	if !r.prof("third").hold.holding() {
		t.Error("third must hold until attributed")
	}
}
