package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 12 security review: account delete re-checks liveness after its
// prompt and holds what it checked (no daemon: the daemon lock; a daemon:
// the config goes first and the credential only once the daemon runs
// without the account).

// deleteWorld is appWorld with w1 vaulted, recorded in state, and a delete
// that counts how often liveness was asked.
func deleteWorld(t *testing.T) (path string, deleted *string, liveCalls *int) {
	t.Helper()
	path = appWorld(t, profilesTOML)
	deleted, liveCalls = new(string), new(int)
	acctSeams.has = func(id string) bool { return id == "w1" }
	acctSeams.identity = func(string) (string, string) { return "person-1@org-1", "org-1" }
	acctSeams.deleteVault = func(id string) error { *deleted = id; return nil }
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		*liveCalls++
		return "", false
	}
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("w1").OrgID = "org-1"
	st.AddVaulted("w1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	oldLock, oldWait := tryDaemonLock, waitDaemonConfig
	t.Cleanup(func() { tryDaemonLock, waitDaemonConfig = oldLock, oldWait })
	waitDaemonConfig = func(string) bool { t.Fatal("waited for a daemon with none running"); return false }
	return path, deleted, liveCalls
}

// daemonHolds makes the delete see a running daemon.
func daemonHolds() {
	tryDaemonLock = func() (*state.Lock, error) {
		return nil, fmt.Errorf("%w: held", state.ErrDaemonRunning)
	}
	daemonRunning = func() bool { return true }
}

func TestDeleteWithNoDaemonHoldsTheDaemonLockThroughout(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	acctSeams.deleteVault = func(id string) error {
		if l, err := state.TryDaemonLock(); err == nil {
			l.Release()
			t.Error("no daemon lock held while the credential was deleted: a daemon could start midway")
		}
		*deleted = id
		return nil
	}
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "w1", true, true); err != nil {
		t.Fatal(err)
	}
	if *deleted != "w1" {
		t.Fatal("not deleted")
	}
	if l, err := state.TryDaemonLock(); err != nil {
		t.Errorf("the lock was not released: %v", err)
	} else {
		l.Release()
	}
}

// The account turns live after the first check (while the prompt waited,
// or the config was edited): nothing is deleted and the config is put back.
func TestDeleteRefusesAnAccountThatBecameLiveMidway(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	before := readFile(t, path)
	calls := 0
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		calls++
		return "work", calls > 1
	}
	var buf bytes.Buffer
	if got := errCode(t, accountDelete(&buf, path, "w1", true, true)); got != codeLive {
		t.Errorf("code %q", got)
	}
	if *deleted != "" {
		t.Error("the credential was deleted")
	}
	if readFile(t, path) != before {
		t.Error("the config was not put back")
	}
	if calls < 2 {
		t.Errorf("liveness asked %d times; it must be asked again before the delete", calls)
	}
}

// Owner decision: delete works while the daemon runs. The config goes
// first; the credential only once the daemon has reloaded without the
// account, and a later daemon save does not bring the record back.
func TestDeleteWithADaemonRunning(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	daemonHolds()
	// The daemon's memory, from its start: the record and the vaulted id.
	dst, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	if dst.Accounts["w1"] == nil || !slices.Contains(dst.Vaulted, "w1") {
		t.Fatal("fixture: the daemon should start knowing w1")
	}
	waited := false
	waitDaemonConfig = func(hash string) bool {
		waited = true
		if *deleted != "" {
			t.Error("the credential went before the daemon reloaded")
		}
		cfg := loadOrFail(t, path)
		if hasAccount(cfg, "w1") {
			t.Error("waited before the config was edited")
		}
		if hash != config.ContentHash([]byte(readFile(t, path))) || hash != cfg.Hash {
			t.Error("waited for another config than the one the delete wrote")
		}
		// The daemon's reload: Reconcile against the new config, record what
		// it loaded, save.
		dst.Reconcile(pinnedOf(cfg))
		dst.DaemonConfigHash = cfg.Hash
		if err := dst.SaveAs(state.OwnerDaemon); err != nil {
			t.Fatal(err)
		}
		return true
	}
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "w1", true, true); err != nil {
		t.Fatal(err)
	}
	if !waited || *deleted != "w1" {
		t.Fatalf("waited %v, deleted %q", waited, *deleted)
	}
	if m := decodeJSON(t, buf.Bytes()); m["daemon_running"] != true {
		t.Errorf("answered %v", m)
	}
	// The daemon saves again, from its memory.
	if err := dst.SaveAs(state.OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if back.Accounts["w1"] != nil || slices.Contains(back.Vaulted, "w1") {
		t.Errorf("a daemon save brought w1 back: record %v, vaulted %v", back.Accounts["w1"], back.Vaulted)
	}
}

func TestDeleteWithADaemonPutsTheConfigBackOnTimeoutOrLive(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	daemonHolds()
	before := readFile(t, path)
	var buf bytes.Buffer

	waitDaemonConfig = func(string) bool { return false }
	if got := errCode(t, accountDelete(&buf, path, "w1", true, true)); got != codeDaemonNotLoaded {
		t.Errorf("timeout: %q", got)
	}
	if *deleted != "" || readFile(t, path) != before {
		t.Error("a timeout must keep the credential and put the config back")
	}

	// Loaded, but live by then.
	waitDaemonConfig = func(string) bool { return true }
	calls := 0
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		calls++
		return "work", calls > 1
	}
	if got := errCode(t, accountDelete(&buf, path, "w1", true, true)); got != codeLive {
		t.Errorf("live after the reload: %q", got)
	}
	if *deleted != "" || readFile(t, path) != before {
		t.Error("turning live must keep the credential and put the config back")
	}
}

// The wait is for the config this CLI wrote, by content: a marker a
// daemon left from an earlier run, or from the config before the edit, is
// not it.
func TestWaitDaemonConfigWantsTheHashOfWhatWasWritten(t *testing.T) {
	appWorld(t, profilesTOML)
	daemonRunning = func() bool { return true }
	oldWait, oldPoll := deleteWait, deletePoll
	deleteWait, deletePoll = 50*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { deleteWait, deletePoll = oldWait, oldPoll })
	write := func(hash string) {
		st, _ := state.Load("")
		st.DaemonConfigHash = hash
		if err := st.SaveAs(state.OwnerDaemon); err != nil {
			t.Fatal(err)
		}
	}
	old, next := config.ContentHash([]byte("before")), config.ContentHash([]byte("after"))
	write("") // a daemon too old to say
	if waitDaemonConfig(next) {
		t.Error("no marker is not \"loaded\"")
	}
	write(old) // stale: a previous run, or the config before the edit
	if waitDaemonConfig(next) {
		t.Error("a stale marker counted as loaded")
	}
	write(next)
	if !waitDaemonConfig(next) {
		t.Error("the daemon reports the config that was written")
	}
	// A CLI save keeps the daemon's marker.
	st, _ := state.Load("")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Load(""); st.DaemonConfigHash != next {
		t.Errorf("a CLI save lost the marker: %q", st.DaemonConfigHash)
	}
	// No daemon any more: nothing to wait for.
	daemonRunning = func() bool { return false }
	write(old)
	if !waitDaemonConfig(next) {
		t.Error("waited for a daemon that is gone")
	}
}

// A delete of an account the config does not name still waits for the
// daemon to run the current config, before the credential goes.
func TestDeleteOfAnUnconfiguredAccountWaitsForTheDaemonToo(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	daemonHolds()
	acctSeams.has = func(id string) bool { return id == "stray" }
	st, _ := state.Load("")
	st.AddVaulted("stray")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	want := config.ContentHash([]byte(readFile(t, path)))
	got := ""
	waitDaemonConfig = func(hash string) bool { got = hash; return true }
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "stray", true, true); err != nil {
		t.Fatal(err)
	}
	if got != want || *deleted != "stray" {
		t.Errorf("waited for %q (want %q), deleted %q", got, want, *deleted)
	}
}

// Putting the config back is a compare-and-swap: a config someone else
// changed meanwhile is left as they made it, and the delete says so.
func TestDeletePutsTheConfigBackOnlyIfUnchanged(t *testing.T) {
	path, deleted, _ := deleteWorld(t)
	daemonHolds()
	waitDaemonConfig = func(string) bool {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("\n# someone else's edit\n")
		f.Close()
		return false
	}
	var buf bytes.Buffer
	err := accountDelete(&buf, path, "w1", true, true)
	if got := errCode(t, err); got != codeConfigChanged {
		t.Errorf("code %q (%v)", got, err)
	}
	text := readFile(t, path)
	if !strings.Contains(text, "someone else's edit") {
		t.Error("the other edit was overwritten")
	}
	if *deleted != "" {
		t.Error("the credential was deleted")
	}
}

// Deleting one of two names for one credential (the twin repair) leaves
// no profile recording the deleted name as live.
func TestDeleteOfATwinClearsItsLiveRecord(t *testing.T) {
	path, _, _ := deleteWorld(t)
	acctSeams.twin = func(*config.Config, *state.State, string) string { return "w2" }
	accountLiveIn = func(*config.Config, *state.State, string, func(string, bool) bool) (string, bool) {
		return "work", true
	}
	st, _ := state.Load("", "default", "work")
	st.Profile("work").SetActive("w1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountDelete(&buf, path, "w1", true, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Load("", "default", "work"); st.Profile("work").Active == "w1" {
		t.Error("work still records the deleted name as live")
	}
}

func TestNaNIsNoNumber(t *testing.T) {
	path := appWorld(t, profilesTOML)
	var buf bytes.Buffer
	for _, v := range []string{"NaN", "nan", "Inf", "-inf"} {
		if got := errCode(t, runConfigCmd(&buf, path, []string{"set", "switch_at", v}, true, "")); got != codeInvalidValue {
			t.Errorf("switch_at %s: %q", v, got)
		}
		if got := errCode(t, runConfigCmd(&buf, path, []string{"set", "landing_margin", v}, true, "")); got != codeInvalidValue {
			t.Errorf("landing_margin %s: %q", v, got)
		}
		if got := errCode(t, profileSet(&buf, path, "work", "hard_floor", v, true)); got != codeInvalidValue {
			t.Errorf("profile hard_floor %s: %q", v, got)
		}
	}
	c := config.Defaults()
	c.HotThreshold = math.NaN()
	if c.Validate() == nil {
		t.Error("Validate passed a NaN")
	}
	c = config.Defaults()
	nan := math.NaN()
	c.Profiles = []config.Profile{{Name: "default", LandingMargin: &nan}}
	if c.Validate() == nil {
		t.Error("Validate passed a NaN profile override")
	}
}

func TestServicePathsAreQuotedOrRefused(t *testing.T) {
	if got := systemdQuote("/opt/a b/%h$HOME/claudeswitch"); got != `"/opt/a b/%%h$$HOME/claudeswitch"` {
		t.Errorf("quoted %s", got)
	}
	dir := t.TempDir()
	unit := filepath.Join(dir, "u.service")
	bin := "/opt/a b/%h$x/claudeswitch"
	if err := os.WriteFile(unit, []byte(renderUnit(bin, true)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, live, _ := installedService("linux", unit); got != bin || !live {
		t.Errorf("read back %q, live %v", got, live)
	}
	_, _, _ = serviceWorld(t, "darwin")
	serviceSeams.exe = func() (string, error) { return "/opt/x\n<evil>/claudeswitch", nil }
	var buf bytes.Buffer
	if got := errCode(t, runDaemonCmd(&buf, "install", "", true)); got != codeInvalidValue {
		t.Errorf("a control character: %q", got)
	}
}

func TestDaemonInstallRefusesATemporaryBinary(t *testing.T) {
	for p, want := range map[string]bool{
		"/private/var/folders/x/T/go-build123/b001/exe/claudeswitch": true,
		"/tmp/claudeswitch":              true,
		"/var/folders/ab/T/claudeswitch": true,
		"/private/var/folders/x/AppTranslocation/1/d/x.app/bin/claudeswitch": true,
		"/home/me/.cache/go-build/ab/claudeswitch":                           true,
		"/opt/go-build-tools/claudeswitch":                                   false,
		"/Users/me/my-go-build/claudeswitch":                                 false,
		"/Users/me/.local/bin/claudeswitch":                                  false,
		"/opt/homebrew/bin/claudeswitch":                                     false,
	} {
		if got := nonDurable(p) != ""; got != want {
			t.Errorf("%s: non-durable %v, want %v", p, got, want)
		}
	}
	f, _, _ := serviceWorld(t, "darwin")
	nonDurable = func(string) string { return "it is in a temporary directory" }
	var buf bytes.Buffer
	if got := errCode(t, runDaemonCmd(&buf, "install", "", true)); got != codeNotDurable {
		t.Errorf("code %q", got)
	}
	if len(f.calls) != 0 {
		t.Errorf("launchctl ran: %v", f.calls)
	}
}

// A command on the old flag parser still answers --json with the error
// object when a flag is wrong, instead of usage text and exit 2.
func TestBadFlagWithJSONAnswersTheErrorObject(t *testing.T) {
	old := exitProcess
	t.Cleanup(func() { exitProcess = old })
	type exited int
	exitProcess = func(code int) { panic(exited(code)) }
	var code exited = -1
	out := captureStdout(t, func() error {
		defer func() {
			if r := recover(); r != nil {
				code = r.(exited)
			}
		}()
		return cmdUse([]string{"a1", "--no-such-flag", "--json"})
	})
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	m := decodeJSON(t, []byte(out))
	if e, _ := m["error"].(map[string]any); e == nil || e["code"] != codeUsage ||
		!strings.Contains(e["message"].(string), "no-such-flag") {
		t.Errorf("answered %v", m)
	}
}

func TestServiceWriteLeavesNoTempFile(t *testing.T) {
	_, home, _ := serviceWorld(t, "darwin")
	paths, err := servicePathsFor("darwin")
	if err != nil {
		t.Fatal(err)
	}
	// The target is a directory: the rename fails.
	if err := os.MkdirAll(filepath.Join(paths.file, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeService("darwin", paths, "/opt/x/claudeswitch", false); err == nil {
		t.Fatal("renaming over a directory must fail")
	}
	if _, err := os.Stat(paths.file + ".claudeswitch-new"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temp file stayed (%v) under %s", err, home)
	}
}
