package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 7 re-review, MAJOR M1: consolidating to one profile must not switch
// the §3 check off while a ghost of the removed profile still guards an
// account. `use` and `login` ran the check only with several profiles.
func TestUseWithOneProfileLeftStillHonoursAGhost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("x")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-x"}}
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return fakeLive{name: "item-default"}, nil }
	path := writeConfig(t, "[[account]]\nid = \"x\"\n\n[[account]]\nid = \"y\"\n")

	st, _ := state.Load("")
	st.AddGhost(&state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "item-old", Account: "x", Since: time.Now()})
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	err := cmdUse([]string{"x", "--config", path})
	if err == nil || !strings.Contains(err.Error(), "removed profile old") {
		t.Fatalf("use installed x while it may still be live in the removed profile: %v", err)
	}
}

// The same check, as login runs it before signing in.
func TestLoginConflictWithOneProfileLeftHonoursAGhost(t *testing.T) {
	cfg := loadOrFail(t, writeConfig(t, "[[account]]\nid = \"x\"\n"))
	st, _ := state.Load(t.TempDir()+"/s.json", cfg.ProfileNames()...)
	st.AddGhost(&state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "item-old", Account: "x", Since: time.Now()})
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return fakeLive{name: "item-default"}, nil }

	other, why := liveConflict(cfg, st, &fakeVault{}, "default", "x")
	if other == "" || !strings.Contains(why, "removed profile old") {
		t.Fatalf("login's §3 check ignored the ghost: %q %q", other, why)
	}
	if other, _ := liveConflict(cfg, st, &fakeVault{}, "default", "y"); other != "" {
		t.Errorf("an account no ghost holds was refused: %q", other)
	}
}

// Rename hardening: a failed state save leaves both vault items and says how
// to finish; re-running the rename completes what a crash left undone.
func TestRenameKeepsTheOldItemWhenStateCannotBeSaved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-w"},
		Meta: &keychain.Meta{AccountUUID: "u1", OrgID: "o1"}}
	path := writeConfig(t, badNameConfig)
	old := saveRenameState
	t.Cleanup(func() { saveRenameState = old })
	saveRenameState = func(*state.State) error { return errors.New("disk full") }

	err := cmdRename([]string{"--config", path, "me+work", "me-work"})
	if err == nil || !strings.Contains(err.Error(), "cs rename me+work me-work") {
		t.Fatalf("want an error with the command that finishes it, got %v", err)
	}
	if store[keychain.VaultService("me+work")] == nil || store[keychain.VaultService("me-work")] == nil {
		t.Fatal("a rename whose state save failed deleted a vault item")
	}

	// Re-running finishes it.
	saveRenameState = old
	if err := cmdRename([]string{"--config", path, "me+work", "me-work"}); err != nil {
		t.Fatalf("re-running the rename did not complete it: %v", err)
	}
	if store[keychain.VaultService("me+work")] != nil {
		t.Error("the old item survived the completed rename")
	}
	if got := store[keychain.VaultService("me-work")]; got == nil || got.ClaudeAIOAuth.AccessToken != "tok-w" {
		t.Errorf("new item: %+v", got)
	}
	if _, err := config.Load(path); err != nil {
		t.Errorf("config: %v", err)
	}
}

// A resumed rename never merges two different credentials.
func TestRenameResumeRefusesTwoDifferentCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-a"}}
	store[keychain.VaultService("me-work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-b"}}
	path := writeConfig(t, strings.ReplaceAll(badNameConfig, "me+work", "me-work"))
	if err := cmdRename([]string{"--config", path, "me+work", "me-work"}); err == nil {
		t.Fatal("resumed over a different credential")
	}
	if store[keychain.VaultService("me+work")] == nil {
		t.Fatal("deleted a credential")
	}
}

// The rename holds the daemon lock throughout, so a daemon cannot start and
// write state in the middle of it.
func TestRenameHoldsTheDaemonLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-w"}}
	path := writeConfig(t, badNameConfig)
	old := saveRenameState
	t.Cleanup(func() { saveRenameState = old })
	acquired := false
	saveRenameState = func(st *state.State) error {
		if l, err := state.AcquireDaemonLock(); err == nil {
			acquired = true
			l.Release()
		}
		return st.Save()
	}
	if err := cmdRename([]string{"--config", path, "me+work", "me-work"}); err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Error("a daemon could take its lock in the middle of a rename")
	}
	// And a running daemon refuses the rename outright.
	l, err := state.AcquireDaemonLock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if err := cmdRename([]string{"--config", path, "me-work", "me-w2"}); err == nil ||
		!strings.Contains(err.Error(), "daemon") {
		t.Errorf("renamed while a daemon ran: %v", err)
	}
}

// Re-pointing twice before the first new item is attributed keeps guarding
// the middle item: its account is the one the hold carries forward.
func TestRepointingTwiceKeepsGuardingTheMiddleItem(t *testing.T) {
	path := writeConfig(t, ghostTwo)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, true)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	base := r.d.resolve
	r.d.resolve = func(in config.Profile) (keychain.Live, error) {
		switch in.Dir {
		case "~/.claude-w2":
			return fakeLive{name: "item-w2"}, nil
		case "~/.claude-w3":
			return fakeLive{name: "item-w3"}, nil
		}
		return base(in)
	}
	put(r.d.st, "w1", 10)
	r.d.st.Profile("work").SetActive("w1")

	rewrite(t, path, strings.Replace(ghostTwo, "~/.claude-work", "~/.claude-w2", 1))
	r.d.reload(context.Background())
	rewrite(t, path, strings.Replace(ghostTwo, "~/.claude-work", "~/.claude-w3", 1))
	r.d.reload(context.Background())

	got := map[string]string{}
	for _, g := range r.d.st.GhostList() {
		got[g.Service] = g.Account
	}
	if got["item-work"] != "w1" || got["item-w2"] != "w1" {
		t.Fatalf("ghosts after two re-points: %v", got)
	}
}

// A profile declared again on the same item drops its ghost: the live
// profile's own check covers that item now.
func TestRedeclaringAProfileOnItsOldItemDropsItsGhost(t *testing.T) {
	path := writeConfig(t, ghostTwo)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, true)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	put(r.d.st, "w1", 10)
	r.d.st.Profile("work").SetActive("w1")

	without := strings.Replace(ghostTwo, "\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1)
	rewrite(t, path, without)
	r.d.reload(context.Background())
	if len(r.d.st.GhostList()) != 1 {
		t.Fatalf("setup: no ghost: %+v", r.d.st.GhostList())
	}
	rewrite(t, path, ghostTwo)
	r.d.reload(context.Background())
	if gs := r.d.st.GhostList(); len(gs) != 0 {
		t.Errorf("the ghost of work's own item survived work being declared again: %+v", gs)
	}
}
