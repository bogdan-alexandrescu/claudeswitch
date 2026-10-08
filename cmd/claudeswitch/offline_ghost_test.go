package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Owner decision (lane 7): a profile removed or re-pointed while no daemon
// ran must still be guarded. State records each profile's resolved item;
// at daemon startup and in the CLI's checks, a profile state names with a
// real active account but the config no longer has (or has on another dir)
// becomes a ghost — from the recorded item, or, with none recorded, guarding
// the account by name until `cs profile forget`.

var withoutWork = strings.Replace(ghostTwo,
	"\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1)

func recordWork(st *state.State, item *state.ItemRef) {
	st.Get("w1")
	st.Profile("work").SetActive("w1")
	st.Profile("work").Item = item
}

// stop, edit, start: the daemon that starts after work was removed offline
// guards work's old item.
func TestDaemonStartupMakesAGhostOfAProfileRemovedOffline(t *testing.T) {
	r := newRig(t, loadOrFail(t, writeConfig(t, withoutWork)), true)
	put(r.d.st, "w1", 10)
	recordWork(r.d.st, &state.ItemRef{Service: "item-work", Dir: "~/.claude-work"})

	r.d.adoptOfflineGhosts()

	gs := r.d.st.GhostList()
	if len(gs) != 1 || gs[0].Profile != "work" || gs[0].Service != "item-work" || gs[0].Account != "w1" {
		t.Fatalf("ghosts at startup: %+v", gs)
	}
	if a := r.d.st.Profile("work").Active; a != "" {
		t.Errorf("work's record still names %q, so a forget would not stick", a)
	}
	if !strings.Contains(r.logs.String(), "level=WARN") || !strings.Contains(r.logs.String(), "profile=work") {
		t.Errorf("no WARN naming work:\n%s", r.logs)
	}
	if other, _ := r.d.liveElsewhere(context.Background(), r.prof("default"), "w1"); other == "" {
		t.Fatal("w1 installable in default while it may be live in work's old item")
	}
}

// Re-pointed offline: the old item is a ghost and the profile holds.
func TestDaemonStartupMakesAGhostOfAProfileRepointedOffline(t *testing.T) {
	r := newRig(t, loadOrFail(t, writeConfig(t, strings.Replace(ghostTwo, "~/.claude-work", "~/.claude-w2", 1))), true)
	put(r.d.st, "w1", 10)
	recordWork(r.d.st, &state.ItemRef{Service: "item-old-work", Dir: "~/.claude-work"})

	r.d.adoptOfflineGhosts()

	gs := r.d.st.GhostList()
	if len(gs) != 1 || gs[0].Why != state.GhostRepointed || gs[0].Service != "item-old-work" {
		t.Fatalf("ghosts: %+v", gs)
	}
	if r.d.st.Profile("work").Active != "" || !r.prof("work").hold.holding() {
		t.Error("the re-pointed profile must forget its old account and hold")
	}
}

// The daemon records each profile's resolved item, so an offline edit later
// still knows it.
func TestDaemonRecordsEachProfilesItem(t *testing.T) {
	r := newRig(t, loadOrFail(t, writeConfig(t, ghostTwo)), false)
	r.d.start(context.Background())
	it := r.d.st.Profile("work").Item
	if it == nil || it.Service != "item-work" || it.Dir != "~/.claude-work" || it.FromEnv {
		t.Fatalf("work's recorded item: %+v", it)
	}
}

// No daemon: the CLI's own checks find the offline removal.
func TestCLIWithNoDaemonRefusesAnAccountOfAProfileRemovedOffline(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, withoutWork))
	st, _ := state.Load(t.TempDir()+"/s.json", cfg.ProfileNames()...)
	recordWork(st, &state.ItemRef{Service: "item-work", Dir: "~/.claude-work"})
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = rigLiveFor

	other, why := liveConflict(cfg, st, &fakeVault{}, "default", "w1")
	if other == "" || !strings.Contains(why, "removed profile work") {
		t.Fatalf("use/login: %q %q", other, why)
	}
}

func rigLiveFor(in config.Profile) (keychain.Live, error) {
	return fakeLive{name: "item-" + in.Name}, nil
}

// Never resolved by a daemon that records items: the account is guarded by
// name — refused anywhere, refresh and remove included — until forgotten.
func TestAProfileWithNoRecordedItemGuardsItsAccountByName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeConfig(t, withoutWork)
	cfg := loadOrFail(t, path)
	st, _ := state.Load("", cfg.ProfileNames()...)
	recordWork(st, nil)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = rigLiveFor

	if other, why := liveConflict(cfg, st, &fakeVault{}, "default", "w1"); other == "" || !strings.Contains(why, "work") {
		t.Fatalf("use/login: %q %q", other, why)
	}
	if name, _, live := liveHolder(cliGhostTargets(cfg, st), &fakeVault{}, "w1"); !live || !strings.Contains(name, "work") {
		t.Errorf("refresh/remove: %q %v", name, live)
	}

	// The daemon too, and it never releases a by-name ghost on its own.
	r := newRig(t, cfg, true)
	put(r.d.st, "w1", 10)
	recordWork(r.d.st, nil)
	r.d.adoptOfflineGhosts()
	r.d.releaseGhosts(context.Background())
	if gs := r.d.st.GhostList(); len(gs) != 1 || gs[0].Service != "" || gs[0].Account != "w1" {
		t.Fatalf("daemon ghosts: %+v", gs)
	}
	if other, _ := r.d.liveElsewhere(context.Background(), r.prof("default"), "w1"); other == "" {
		t.Fatal("daemon ignores the by-name guard")
	}

	// forget releases it, for good.
	if err := cmdProfile([]string{"forget", "work"}); err != nil {
		t.Fatal(err)
	}
	after, _ := state.Load("", cfg.ProfileNames()...)
	if other, _ := liveConflict(cfg, after, &fakeVault{}, "default", "w1"); other != "" {
		t.Errorf("still guarded after forget: %q", other)
	}
}
