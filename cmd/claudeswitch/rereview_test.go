package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// --- MAJOR 1: the hold must not outlive its purpose ---

func holdFixture() (*config.Config, *state.State) {
	next := testCfg() // poll_active 1m
	next.Accounts = []config.Account{{ID: "b", Scope: "work"}}
	return next, &state.State{Active: "a", Accounts: map[string]*state.Account{"a": {}, "b": {}}}
}

// Removing the account whose credential is still live leaves nothing to
// attribute it to: PollActive files it as "active" for good. A hold that waits
// for attribution then vetoes every switch until the daemon restarts.
func TestHoldLiftsAfterTwoActivePollsWhenNothingCanBeAttributed(t *testing.T) {
	cfg, st := holdFixture()
	clock := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	log, buf := captureLog()
	h := activeHold{now: func() time.Time { return clock }, log: log}

	before := st.Active
	st.Reconcile(pinnedOf(cfg))
	h.afterReload(before, st)
	st.SetActive(state.Unattributed) // what PollActive does with a credential nobody owns

	want := policy.Decision{Kind: policy.Switch, Target: "b", Reason: "no active account yet"}
	clock = clock.Add(cfg.PollActive.Duration)
	if d := h.gate(want, st, cfg); d.Kind == policy.Switch {
		t.Fatal("inside the bound the hold still applies")
	}
	clock = clock.Add(cfg.PollActive.Duration + time.Second)
	if d := h.gate(want, st, cfg); d != want {
		t.Fatalf("after two active polls the hold must lift and normal policy apply, got %v", d)
	}
	if !strings.Contains(buf.String(), "lifted") || !strings.Contains(buf.String(), "removed_account=a ") {
		t.Errorf("lifting must be logged with which account it was for:\n%s", buf)
	}
	if h.holding() {
		t.Error("a lifted hold stays lifted")
	}
}

func TestHoldLiftsAtOnceWhenTheLiveCredentialIsAttributed(t *testing.T) {
	cfg, st := holdFixture()
	log, buf := captureLog()
	h := activeHold{log: log}
	before := st.Active
	st.Reconcile(pinnedOf(cfg))
	h.afterReload(before, st)
	st.SetActive("b")
	want := policy.Decision{Kind: policy.Stay, Reason: "fine"}
	if d := h.gate(want, st, cfg); d != want || h.holding() {
		t.Fatalf("got %v, holding=%v", d, h.holding())
	}
	if !strings.Contains(buf.String(), "lifted") || !strings.Contains(buf.String(), "b") {
		t.Errorf("the lift must be logged with what it was attributed to:\n%s", buf)
	}
}

// --- MAJOR 2: a symlinked config ---

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("no reload signal: %s", what)
	}
}

// writeConfigFile renames inside the target's directory, so a watch on the
// link's directory alone never sees an edit made through the link.
func TestWatchConfigSeesEditsThroughASymlink(t *testing.T) {
	root := t.TempDir()
	linkDir, targetDir, otherDir := filepath.Join(root, "conf"), filepath.Join(root, "dotfiles"), filepath.Join(root, "other")
	for _, d := range []string{linkDir, targetDir, otherDir} {
		_ = os.MkdirAll(d, 0o700)
	}
	target := filepath.Join(targetDir, "config.toml")
	_ = os.WriteFile(target, []byte(baseConfig), 0o600)
	link := filepath.Join(linkDir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	defer close(stop)
	log, _ := captureLog()
	changed, err := watchConfig(link, 50*time.Millisecond, stop, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendAccount(link, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, changed, "an edit through the link lands in the target's directory")

	// The link can be pointed somewhere else; the watch must follow it.
	other := filepath.Join(otherDir, "config.toml")
	_ = os.WriteFile(other, []byte(baseConfig), 0o600)
	_ = os.Remove(link)
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, changed, "retargeting the link is a change")
	time.Sleep(150 * time.Millisecond)
	if err := appendAccount(link, "work-c", strings.ReplaceAll(newBlock, "work-b", "work-c")); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, changed, "an edit in the new target's directory")
}

// --- minors ---

// A crash between creating the temporary file and renaming it leaves one
// behind. They are cleaned up the next time the config is written, once old
// enough that they cannot be another writer's file in flight.
func TestConfigWritesRemoveStaleTemporaryFiles(t *testing.T) {
	path := writeConfig(t, baseConfig)
	stale := path + ".claudeswitch-new-123"
	fresh := path + ".claudeswitch-new-456"
	for _, f := range []string{stale, fresh} {
		_ = os.WriteFile(f, []byte("x"), 0o600)
	}
	old := time.Now().Add(-2 * time.Minute)
	_ = os.Chtimes(stale, old, old)
	if err := appendAccount(path, "work-b", newBlock); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a temporary file over a minute old must be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent temporary file may belong to another writer and must be left")
	}
}

// --- owner decision: a fast poll_hot is kept, and warned about ---

func TestDoctorWarnsAboutAFastHotCadence(t *testing.T) {
	cfg := testCfg()
	cfg.PollHot = config.Duration{Duration: 20 * time.Second}
	out := strings.Join(pollCadenceLines(cfg), "\n")
	for _, want := range []string{
		"[warn] poll cadence  hot 20s drains an account's ~25-call allowance in ~8 min",
		"fix: cs config set poll_hot 60s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("a fast cadence is a warning, not a failure:\n%s", out)
	}
}

func TestDoctorIsQuietAboutTheDefaultHotCadence(t *testing.T) {
	cfg := testCfg()
	cfg.PollHot = config.Duration{Duration: time.Minute}
	out := strings.Join(pollCadenceLines(cfg), "\n")
	if strings.Contains(out, "[warn]") || !strings.Contains(out, "[ok  ] poll cadence") {
		t.Errorf("got:\n%s", out)
	}
}
