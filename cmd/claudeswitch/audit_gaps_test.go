package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/poller"
)

// runCLI runs this test binary as the claudeswitch CLI (TestMain hands
// argv to main when cliEnv is set), so a command whose flag set exits the
// process can be checked from outside. It inherits testshim's PATH and
// HOME.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), cliEnv+"="+strings.Join(args, cliSep))
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// docs/APP_CLI.md: "`--config PATH` is accepted by every command, as
// everywhere else." These five did not define it.
func TestEveryCommandAcceptsConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[account]]\nid = \"work-1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ccDir := t.TempDir() // statusline uninstall: a settings.json-less profile
	t.Setenv("CLAUDE_CONFIG_DIR", ccDir)
	for _, args := range [][]string{
		{"history", "--days", "1", "--config", path},
		{"audit", "--config", path},
		{"statusline", "uninstall", "--config", path},
		{"version", "--config", path, "--json"},
	} {
		out, code := runCLI(t, args...)
		if strings.Contains(out, "flag provided but not defined") || code == 2 {
			t.Errorf("%v: --config refused (exit %d):\n%s", args, code, out)
		}
	}

	// daemon, in-process, against the fake service manager.
	serviceWorld(t, "linux")
	out := captureStdout(t, func() error {
		return cmdDaemon([]string{"status", "--config", path, "--json"})
	})
	if m := decodeJSON(t, []byte(out)); m["action"] != "status" {
		t.Errorf("daemon status --config answered %v", m)
	}
}

// docs/APP_CLI.md error codes: bad arguments are `usage`, a name the
// validator refuses is `invalid_value` — on add and login as on rename and
// profile create, not `failed`.
func TestAddAndLoginArgumentErrorsCarryTheirCodes(t *testing.T) {
	none := func() (string, string, string) { return "", "", "" }
	ask := func(string, string) string { return "" }
	_, err := chooseAddName([]string{"work-1", "personal"}, false, none, nil, ask)
	if got := errCode(t, err); got != codeUsage {
		t.Errorf("add with two names: %q, want usage", got)
	}
	_, err = chooseAddName([]string{"work..1"}, false, none, nil, ask)
	if got := errCode(t, err); got != codeInvalidValue {
		t.Errorf("add with a bad name: %q, want invalid_value", got)
	}

	path := appWorld(t, "[[account]]\nid = \"work-1\"\n")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--direct", "--no-open", "--json"}, codeUsage},
		{[]string{"work-1", "personal", "--direct", "--no-open", "--json"}, codeUsage},
		{[]string{"work..1", "--direct", "--no-open", "--json"}, codeInvalidValue},
		{[]string{"work..1", "--code", "x", "--json"}, codeInvalidValue},
	} {
		err := cmdLogin(append(c.args, "--config", path))
		if got := errCode(t, err); got != c.want {
			t.Errorf("login %v: %q, want %q", c.args, got, c.want)
		}
	}
}

// An account in no profile's pool (profiles declared, no default, the
// account disabled) is live in no profile, so it cannot be pinned; the
// refusal used to name profile "" ("live in profile , so…"), and its JSON
// form would have answered `"profile": ""`.
func TestPinOfAnAccountInNoPoolIsRefused(t *testing.T) {
	path := appWorld(t, `[[account]]
id = "work-1"

[[account]]
id = "research"
enabled = false

[[profile]]
name = "work"
dir = "~/.claude-work"
pool = ["work-1"]
`)
	var buf bytes.Buffer
	err := accountPin(&buf, path, "research", false, true)
	if got := errCode(t, err); got != codeNotActive {
		t.Errorf("pin of an account in no pool: %q, want not_active", got)
	}
	if msg := err.Error(); strings.Contains(msg, "profile ,") || !strings.Contains(msg, "no profile") {
		t.Errorf("the refusal does not say the account is in no profile: %q", msg)
	}
}

// D15: "busy" for D3 is a transcript write within poll_active (the
// poller's default when unset).
func TestDaemonBusyIsATranscriptWriteWithinPollActive(t *testing.T) {
	det := &fakeDet{}
	cfg := testCfg()
	cfg.PollActive = config.Duration{Duration: 3 * time.Minute}
	d := &daemon{cfg: cfg, profs: []*profileLoop{{name: "work", det: det}}}

	if d.busy("work") {
		t.Error("a profile with no transcript write is not busy")
	}
	det.last = time.Now().Add(-2 * time.Minute)
	if !d.busy("work") {
		t.Error("a write 2m ago is within poll_active (3m): busy")
	}
	det.last = time.Now().Add(-(3*time.Minute + 30*time.Second))
	if d.busy("work") {
		t.Error("a write 3m30s ago is past poll_active (3m): not busy")
	}
	if d.busy("default") {
		t.Error("a profile the daemon does not run is not busy")
	}

	cfg.PollActive = config.Duration{}
	det.last = time.Now().Add(-poller.ActiveInterval + 30*time.Second)
	if !d.busy("work") {
		t.Error("with no poll_active the poller's own active interval is the window")
	}
}
