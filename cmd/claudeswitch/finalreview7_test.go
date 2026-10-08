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

const legacyUpgradeConfig = `
[[account]]
id = "x"
[[account]]
id = "w1"

[[profile]]
name = "personal"
pool = ["x"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

func fakeEnvItem(t *testing.T) {
	t.Helper()
	old := envItemRef
	t.Cleanup(func() { envItemRef = old })
	envItemRef = func() (string, string, bool) { return "item-env", "", true }
}

// Final review MAJOR 1: a 0.4.9 state (its active account migrated to
// profiles["default"], no item recorded) under a config that names the
// CLAUDE_CONFIG_DIR-unset profile anything but "default" must not guard the
// account by name forever: the legacy record used the environment's item, so
// the ghost is that item, filtered against the profile using it now and
// released like any other.
func TestLegacyDefaultRecordGuardsTheEnvironmentsItemNotTheName(t *testing.T) {
	fakeEnvItem(t)
	cfg := loadOrFail(t, writeConfig(t, legacyUpgradeConfig))
	st, _ := state.Load(t.TempDir()+"/s.json", cfg.ProfileNames()...)
	st.Get("x")
	st.Default().SetActive("x")

	gs := offlineGhosts(cfg, st)
	if len(gs) != 1 || gs[0].Service != "item-env" || gs[0].Account != "x" {
		t.Fatalf("ghosts: %+v", gs)
	}
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) {
		if in.Name == "personal" {
			return fakeLive{name: "item-env"}, nil // no dir: the item the legacy default used
		}
		return fakeLive{name: "item-" + in.Name}, nil
	}
	if other, why := liveConflict(cfg, st, &fakeVault{}, "personal", "x"); other != "" {
		t.Fatalf("x refused in the profile it is live in: %q %q", other, why)
	}

	// The daemon adopts it as an item ghost and releases it once the item no
	// longer holds x.
	r := newRig(t, cfg, true)
	put(r.d.st, "x", 10)
	r.d.st.Default().SetActive("x")
	r.d.adoptOfflineGhosts()
	if gs := r.d.st.GhostList(); len(gs) != 1 || gs[0].Service != "item-env" {
		t.Fatalf("daemon ghosts: %+v", gs)
	}
	r.d.releaseGhosts(context.Background())
	if gs := r.d.st.GhostList(); len(gs) != 0 {
		t.Errorf("a legacy default's ghost is never released: %+v", gs)
	}
}

// Final review MAJOR 2: renaming a stray vault entry onto a configured
// account with no vault entry of its own is not a resume; it would hand that
// account another credential.
func TestRenameRefusesAStrayOntoAConfiguredAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("stray")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-stray"}}
	path := writeConfig(t, "[[account]]\nid = \"existing\"\naccount_uuid = \"u1\"\norg_id = \"o1\"\n")
	if err := cmdRename([]string{"--config", path, "stray", "existing"}); err == nil {
		t.Fatal("renamed a stray credential onto a configured account")
	}
	if store[keychain.VaultService("existing")] != nil || store[keychain.VaultService("stray")] == nil {
		t.Error("a refused rename moved a credential")
	}
}

// (a) A by-name ghost is released when its profile is declared again.
func TestByNameGhostIsReleasedWhenItsProfileIsDeclaredAgain(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	r.d.st.AddGhost(&state.Ghost{Profile: "third", Why: state.GhostRemoved, Account: "w2", Since: time.Now()})
	rewrite(t, path, reloadTwoProfiles+thirdProfile)
	r.d.reload(context.Background())
	if gs := r.d.st.GhostList(); len(gs) != 0 {
		t.Errorf("by-name ghost survived its profile being declared again: %+v", gs)
	}
	// And the CLI ignores one whose profile is declared.
	cfg := loadOrFail(t, writeConfig(t, reloadTwoProfiles+thirdProfile))
	st, _ := state.Load(t.TempDir()+"/s.json", cfg.ProfileNames()...)
	st.AddGhost(&state.Ghost{Profile: "third", Why: state.GhostRemoved, Account: "w2", Since: time.Now()})
	if gs := allGhosts(cfg, st); len(gs) != 0 {
		t.Errorf("CLI still honours a by-name ghost of a declared profile: %+v", gs)
	}
}

// (b) A CLI save never writes back a stale item: the daemon owns it.
func TestCLISaveTakesTheItemFromDisk(t *testing.T) {
	path := t.TempDir() + "/state.json"
	t.Setenv("HOME", t.TempDir())
	daemon, _ := state.Load(path, "work")
	daemon.Profile("work").Item = &state.ItemRef{Service: "old"}
	_ = daemon.SaveAs(state.OwnerDaemon)
	cli, _ := state.Load(path, "work")
	daemon.Profile("work").Item = &state.ItemRef{Service: "new"}
	_ = daemon.SaveAs(state.OwnerDaemon)
	_ = cli.Save()
	after, _ := state.Load(path, "work")
	if it := after.Profile("work").Item; it == nil || it.Service != "new" {
		t.Errorf("a CLI save wrote back a stale item: %+v", it)
	}
}

// (c) A held daemon lock is reported as a running daemon, by type.
func TestTryDaemonLockSaysADaemonIsRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := state.AcquireDaemonLock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, err := state.TryDaemonLock(); !errors.Is(err, state.ErrDaemonRunning) {
		t.Errorf("got %v, want ErrDaemonRunning", err)
	}
}

// (d) After a failed state save, the rename says not to start the daemon
// until it is re-run.
func TestRenameSaveFailureSaysNotToStartTheDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := fakeVaultStore(t)
	store[keychain.VaultService("me+work")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-w"}}
	path := writeConfig(t, badNameConfig)
	old := saveRenameState
	t.Cleanup(func() { saveRenameState = old })
	saveRenameState = func(*state.State) error { return errors.New("disk full") }
	err := cmdRename([]string{"--config", path, "me+work", "me-work"})
	if err == nil || !strings.Contains(err.Error(), "do not start the daemon") {
		t.Errorf("error: %v", err)
	}
}

// (e) refresh --allow-active blocked by a by-name ghost says where, not
// "could not be looked up".
func TestRefreshBlockedByAGhostSaysWhere(t *testing.T) {
	err := liveHolderUnknown("removed profile old", "w1")
	if err == nil || !strings.Contains(err.Error(), "may be live in removed profile old") ||
		strings.Contains(err.Error(), "looked up") {
		t.Errorf("ghost wording: %v", err)
	}
	if err := liveHolderUnknown("work", "w1"); err == nil || !strings.Contains(err.Error(), "looked up") {
		t.Errorf("profile wording: %v", err)
	}
}
