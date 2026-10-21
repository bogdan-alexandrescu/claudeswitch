package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// D34: one machine-wide switch lock, held from the §3 check through the
// live write and its verify by every command that writes a live
// credential, as by the daemon. Waiting for it is bounded; a writer that
// waited checks §3 against state as it is once it holds the lock.

// useWorld is one implicit profile holding x and y, x vaulted, and the
// profile's item a fake.
func useWorld(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	store := fakeVaultStore(t)
	store[keychain.VaultService("x")] = &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-x"}}
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return fakeLive{name: "item-default"}, nil }
	return writeConfig(t, "[[account]]\nid = \"x\"\n\n[[account]]\nid = \"y\"\n")
}

func TestUseRefusesWhenTheSwitchLockStaysBusy(t *testing.T) {
	path := useWorld(t)
	old := cliSwitchWait
	t.Cleanup(func() { cliSwitchWait = old })
	cliSwitchWait = 100 * time.Millisecond
	held, err := state.AcquireSwitchLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	err = cmdUse([]string{"x", "--config", path, "--json"})
	if got := errCode(t, err); got != codeSwitchBusy {
		t.Fatalf("code %q (%v), want %s", got, err, codeSwitchBusy)
	}
	if !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("refusal %q does not say nothing was changed", err)
	}
	// A dry run writes nothing, so it does not wait for the lock.
	if err := cmdUse([]string{"x", "--config", path, "--dry-run", "--json"}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
}

// Another writer finished while `use` waited: x is now guarded by a ghost.
// `use` reads state again once it holds the lock, and refuses.
func TestUseChecksStateReadOnceItHoldsTheSwitchLock(t *testing.T) {
	path := useWorld(t)
	old := acquireSwitchLock
	t.Cleanup(func() { acquireSwitchLock = old })
	acquireSwitchLock = func(wait time.Duration) (*state.Lock, error) {
		st, err := state.Load("")
		if err != nil {
			t.Fatal(err)
		}
		st.AddGhost(&state.Ghost{Profile: "old", Why: state.GhostRemoved, Service: "item-old", Account: "x",
			Since: time.Now()})
		if err := st.Save(); err != nil {
			t.Fatal(err)
		}
		return old(wait)
	}
	err := cmdUse([]string{"x", "--config", path})
	if err == nil || !strings.Contains(err.Error(), "removed profile old") {
		t.Fatalf("use = %v, want a refusal naming the ghost recorded while it waited", err)
	}
}

// restoreRec is the vault as switching back after a login uses it.
type restoreRec struct {
	fakeVault
	swaps     []string
	lockHeld  bool
	lockError error
}

func (r *restoreRec) SwapToWith(_ context.Context, _ keychain.Live, id, _ string, _ vault.SwapOptions) (*vault.SwapResult, error) {
	r.swaps = append(r.swaps, id)
	if l, err := state.AcquireSwitchLock(0); err == nil {
		l.Release()
	} else if errors.Is(err, state.ErrSwitchBusy) {
		r.lockHeld = true
	} else {
		r.lockError = err
	}
	return &vault.SwapResult{AccountID: id}, nil
}

const restoreShared = `
[[account]]
id = "home"

[[account]]
id = "new"

[[pool]]
name     = "team"
accounts = ["home", "new"]

[[profile]]
name  = "default"
pools = ["team"]

[[profile]]
name  = "work"
dir   = "~/.claude-work"
pools = ["team"]
`

func restoreWorld(t *testing.T) (*config.Config, *state.State) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(in config.Profile) (keychain.Live, error) { return fakeLive{name: "item-" + in.Name}, nil }
	cfg := loadOrFail(t, writeConfig(t, restoreShared))
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	// The login just vaulted new through work's item; home was live there.
	st.Profile("work").SetActive("new")
	return cfg, st
}

// While the interactive login ran, home was live nowhere, and default took
// it. Switching work back would put it in two profiles: it is not done.
func TestRestoreAfterLoginSkipsAnAccountNowLiveElsewhere(t *testing.T) {
	cfg, st := restoreWorld(t)
	disk, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		t.Fatal(err)
	}
	disk.Profile("default").SetActive("home")
	if err := disk.SaveAs(state.OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	rec := &restoreRec{}

	restoreActive(rec, cfg, st, "work", fakeLive{name: "item-work"}, "home")

	if len(rec.swaps) != 0 {
		t.Fatalf("switched work back to %v although home is live in default", rec.swaps)
	}
	if got := st.Profile("work").Active; got != "new" {
		t.Errorf("work's active = %q, want new: nothing was written", got)
	}
}

// Otherwise it switches back, holding the switch lock for the write.
func TestRestoreAfterLoginHoldsTheSwitchLock(t *testing.T) {
	cfg, st := restoreWorld(t)
	rec := &restoreRec{}

	restoreActive(rec, cfg, st, "work", fakeLive{name: "item-work"}, "home")

	if len(rec.swaps) != 1 || rec.swaps[0] != "home" {
		t.Fatalf("swaps = %v, want home", rec.swaps)
	}
	if !rec.lockHeld || rec.lockError != nil {
		t.Fatalf("switched back without the switch lock (err %v)", rec.lockError)
	}
	if got := st.Profile("work").Active; got != "home" {
		t.Errorf("work's active = %q, want home", got)
	}
}

// A seed writes the new profile's credential under the switch lock.
func TestSeedWritesUnderTheSwitchLock(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, false, time.Second)
	inner := createLiveItem
	held := false
	createLiveItem = func(svc, file string, b *keychain.Blob) error {
		if l, err := state.AcquireSwitchLock(0); err == nil {
			l.Release()
		} else {
			held = errors.Is(err, state.ErrSwitchBusy)
		}
		return inner(svc, file, b)
	}
	if err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"}); err != nil {
		t.Fatal(err)
	}
	if !rec.created || !held {
		t.Fatalf("created %v, under the switch lock %v", rec.created, held)
	}
}

// Busy past the wait, the seed is refused and the profile is left made,
// as when the daemon never loads it.
func TestSeedRefusesWhenTheSwitchLockStaysBusy(t *testing.T) {
	createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, false, time.Second)
	old := cliSwitchWait
	t.Cleanup(func() { cliSwitchWait = old })
	cliSwitchWait = 100 * time.Millisecond
	held, err := state.AcquireSwitchLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	err = profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
	if got := errCode(t, err); got != codeSwitchBusy {
		t.Fatalf("code %q (%v), want %s", got, err, codeSwitchBusy)
	}
	if rec.created {
		t.Fatal("seeded without the switch lock")
	}
	if _, ok := loadOrFail(t, path).ProfileNamed("work"); !ok {
		t.Error("the profile itself should still be made")
	}
}
