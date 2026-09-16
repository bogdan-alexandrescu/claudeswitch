package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readBack(t *testing.T, path string) (string, map[string]any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("wrote invalid JSON: %v\n%s", err, b)
	}
	return string(b), m
}

func TestStatuslineInstallIntoMissingFile(t *testing.T) {
	path := writeSettings(t, "")
	if _, err := installStatusline(path, false); err != nil {
		t.Fatal(err)
	}
	_, m := readBack(t, path)
	sl, _ := m["statusLine"].(map[string]any)
	if sl["command"] != statuslineCommand || sl["type"] != "command" {
		t.Errorf("statusLine = %v", m["statusLine"])
	}
	if _, err := os.Stat(path + ".claudeswitch.bak"); err == nil {
		t.Error("backed up a file that did not exist")
	}
}

// Someone's hand-edited settings must come back with every key, in its order,
// and the original kept beside it.
func TestStatuslineInstallKeepsKeysOrderAndBackup(t *testing.T) {
	orig := `{"zeta": 1, "hooks": {"Stop": [{"hooks": []}]}, "alpha": "x"}`
	path := writeSettings(t, orig)
	if _, err := installStatusline(path, false); err != nil {
		t.Fatal(err)
	}
	body, m := readBack(t, path)
	if m["zeta"] != float64(1) || m["alpha"] != "x" || m["hooks"] == nil {
		t.Errorf("lost a key: %v", m)
	}
	iz, ih, ia, is := strings.Index(body, `"zeta"`), strings.Index(body, `"hooks"`),
		strings.Index(body, `"alpha"`), strings.Index(body, `"statusLine"`)
	if !(iz < ih && ih < ia && ia < is) {
		t.Errorf("key order changed:\n%s", body)
	}
	if bak, err := os.ReadFile(path + ".claudeswitch.bak"); err != nil || string(bak) != orig {
		t.Errorf("backup = %q, %v", bak, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the original 0600", fi.Mode().Perm())
	}
}

func TestStatuslineInstallLeavesSomeoneElsesAlone(t *testing.T) {
	orig := `{"statusLine": {"type": "command", "command": "ccstatus"}}`
	path := writeSettings(t, orig)
	if _, err := installStatusline(path, false); err == nil {
		t.Fatal("replaced another status line without --force")
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Errorf("file changed: %s", b)
	}
	if _, err := installStatusline(path, true); err != nil {
		t.Fatal(err)
	}
	if ok := statuslineCommandOf(mustGet(t, path, "statusLine")); ok != statuslineCommand {
		t.Errorf("--force left %q", ok)
	}
}

func TestStatuslineInstallIsIdempotent(t *testing.T) {
	for _, cmd := range []string{"claudeswitch statusline", "cs statusline", "/Users/x/.local/bin/claudeswitch statusline --config y"} {
		orig := `{"statusLine": {"type": "command", "command": "` + cmd + `"}}`
		path := writeSettings(t, orig)
		if _, err := installStatusline(path, false); err != nil {
			t.Errorf("%q: %v", cmd, err)
		}
		if b, _ := os.ReadFile(path); string(b) != orig {
			t.Errorf("%q: rewrote a file that was already right: %s", cmd, b)
		}
	}
}

func TestStatuslineUninstallOnlyRemovesOurs(t *testing.T) {
	theirs := `{"statusLine": {"type": "command", "command": "ccstatus"}}`
	path := writeSettings(t, theirs)
	if _, err := uninstallStatusline(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != theirs {
		t.Errorf("removed someone else's status line: %s", b)
	}

	path = writeSettings(t, `{"model": "opus", "statusLine": {"type": "command", "command": "cs statusline"}}`)
	if _, err := uninstallStatusline(path); err != nil {
		t.Fatal(err)
	}
	_, m := readBack(t, path)
	if _, ok := m["statusLine"]; ok || m["model"] != "opus" {
		t.Errorf("after uninstall: %v", m)
	}
}

func TestStatuslineRefusesInvalidJSON(t *testing.T) {
	orig := `{"model": "opus",}`
	path := writeSettings(t, orig)
	if _, err := installStatusline(path, false); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Errorf("touched an unparseable file: %s", b)
	}
}

func mustGet(t *testing.T, path, key string) json.RawMessage {
	t.Helper()
	s, err := readSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.get(key)
	return v
}

func renderContextString(cfg *config.Config, st *state.State, now time.Time, daemon bool) string {
	var b strings.Builder
	renderContext(&b, cfg, st, now, daemon, "")
	return b.String()
}

func TestContextNormalCaseIsShort(t *testing.T) {
	now := time.Now()
	st := &state.State{Active: "a", DaemonLive: true, Accounts: map[string]*state.Account{
		"a": at(40, 30, now), "b": at(5, 5, now),
	}}
	out := renderContextString(testCfg(), st, now, true)
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("normal case is %d lines, want 2:\n%s", n, out)
	}
	for _, want := range []string{"active a", "session 40%", "week 30%", "rotates automatically"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "[claudeswitch] ") {
			t.Errorf("unprefixed line %q", line)
		}
	}
}

func TestContextSaysWhenToSwitch(t *testing.T) {
	now := time.Now()
	st := &state.State{Active: "a", DaemonLive: true, Accounts: map[string]*state.Account{
		"a": at(91, 30, now), "b": at(5, 5, now),
	}}
	out := renderContextString(testCfg(), st, now, true)
	if !strings.Contains(out, "claudeswitch use b") {
		t.Errorf("over the trigger but no switch suggested:\n%s", out)
	}
}

func TestContextNamesADaemonThatWillNotAct(t *testing.T) {
	now := time.Now()
	st := &state.State{Active: "a", Accounts: map[string]*state.Account{"a": at(10, 10, now)}}
	if out := renderContextString(testCfg(), st, now, false); !strings.Contains(out, "NOT running") {
		t.Errorf("stopped daemon not reported:\n%s", out)
	}
	if out := renderContextString(testCfg(), st, now, true); !strings.Contains(out, "dry-run") {
		t.Errorf("dry-run daemon not reported:\n%s", out)
	}
}

func TestContextBeforeSetup(t *testing.T) {
	now := time.Now()
	if out := renderContextString(&config.Config{}, nil, now, false); !strings.Contains(out, "claudeswitch setup") {
		t.Errorf("no accounts: %s", out)
	}
	if out := renderContextString(testCfg(), &state.State{}, now, false); !strings.Contains(out, "no account selected") {
		t.Errorf("no active: %s", out)
	}
	st := &state.State{Active: "a", Accounts: map[string]*state.Account{"a": {}}}
	if out := renderContextString(testCfg(), st, now, false); !strings.Contains(out, "no usage reading") {
		t.Errorf("no reading: %s", out)
	}
}

// A routine backoff on the active account must not reach every session; a
// poller that has stopped must.
func TestContextStaleOnlyPastTenMinutes(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		age  time.Duration
		want bool
	}{{4 * time.Minute, false}, {9 * time.Minute, false}, {12 * time.Minute, true}} {
		st := &state.State{Active: "a", DaemonLive: true, Accounts: map[string]*state.Account{
			"a": at(40, 30, now.Add(-tc.age)), "b": at(5, 5, now),
		}}
		out := renderContextString(testCfg(), st, now, true)
		if got := strings.Contains(out, "reading "); got != tc.want {
			t.Errorf("reading %s old: stale note = %v, want %v\n%s", tc.age, got, tc.want, out)
		}
	}
}
