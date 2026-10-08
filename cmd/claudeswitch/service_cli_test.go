package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `cs daemon …` drives a fake launchctl/systemctl: the real ones would
// reload the real daemon (testshim fails the run if either is executed).
type fakeService struct {
	calls    []string
	relative []string // programs run by a bare name, which PATH would pick
	loaded   bool
	fail     string // a call that fails
}

func (f *fakeService) run(name string, args ...string) (string, error) {
	if !filepath.IsAbs(name) {
		f.relative = append(f.relative, name)
	}
	call := filepath.Base(name) + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.HasPrefix(call, f.fail) {
		return "it went wrong", errors.New("exit status 1")
	}
	switch {
	case strings.HasPrefix(call, "launchctl list"):
		if !f.loaded {
			return "", errors.New("exit status 113")
		}
	case strings.HasPrefix(call, "launchctl load"):
		f.loaded = true
	case strings.HasPrefix(call, "launchctl unload"):
		f.loaded = false
	case strings.HasPrefix(call, "systemctl --user is-active"):
		if !f.loaded {
			return "inactive\n", errors.New("exit status 3")
		}
		return "active\n", nil
	case strings.Contains(call, "enable --now"), strings.Contains(call, "systemctl --user start"),
		strings.Contains(call, "systemctl --user restart"):
		f.loaded = true
	}
	return "", nil
}

func serviceWorld(t *testing.T, goos string) (*fakeService, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	bin := filepath.Join(t.TempDir(), "claudeswitch")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeService{}
	old, oldRunning, oldDurable := serviceSeams, daemonRunning, nonDurable
	serviceSeams = serviceDeps{goos: goos, run: f.run, exe: func() (string, error) { return bin, nil }}
	daemonRunning = func() bool { return false }
	nonDurable = func(string) string { return "" } // t.TempDir is a temporary directory
	t.Cleanup(func() {
		serviceSeams, daemonRunning, nonDurable = old, oldRunning, oldDurable
		if len(f.relative) > 0 {
			t.Errorf("ran %v by a bare name; launchctl and systemctl go by absolute path", f.relative)
		}
	})
	return f, home, bin
}

func daemonJSON(t *testing.T, verb, mode string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := runDaemonCmd(&buf, verb, mode, true); err != nil {
		t.Fatalf("daemon %s: %v", verb, err)
	}
	return decodeJSON(t, buf.Bytes())
}

func TestDaemonInstallLiveDryRunAndUninstallOnLaunchd(t *testing.T) {
	f, home, bin := serviceWorld(t, "darwin")
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	stateDir := filepath.Join(home, ".local", "state", "claudeswitch")

	var buf bytes.Buffer
	if got := errCode(t, runDaemonCmd(&buf, "start", "", true)); got != codeNotInstalled {
		t.Errorf("start before install: %q", got)
	}

	m := daemonJSON(t, "install", "")
	if m["installed"] != true || m["loaded"] != true || m["mode"] != "dry-run" || m["binary"] != bin ||
		m["binary_is_this"] != true || m["platform"] != "launchd" {
		t.Errorf("install answered %v", m)
	}
	if got := readFile(t, plist); got != renderPlist(bin, stateDir, false) {
		t.Errorf("the plist is not install.sh's:\n%s", got)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "launchctl load "+plist) {
		t.Errorf("calls %v", f.calls)
	}

	// live keeps the binary the plist runs, whatever binary asks.
	serviceSeams.exe = func() (string, error) { return filepath.Join(t.TempDir(), "claudeswitch"), nil }
	f.calls = nil
	m = daemonJSON(t, "live", "")
	if m["mode"] != "live" || m["binary"] != bin || m["binary_is_this"] != false {
		t.Errorf("live answered %v", m)
	}
	if got := readFile(t, plist); got != renderPlist(bin, stateDir, true) {
		t.Errorf("live plist:\n%s", got)
	}
	if strings.Join(f.calls, "|") != "launchctl unload "+plist+"|launchctl load "+plist+"|launchctl list "+serviceLabel {
		t.Errorf("live reloads: %v", f.calls)
	}
	// A reinstall keeps the mode unless told.
	if m := daemonJSON(t, "install", ""); m["mode"] != "live" {
		t.Errorf("reinstall changed the mode: %v", m["mode"])
	}
	if m := daemonJSON(t, "install", "dry-run"); m["mode"] != "dry-run" {
		t.Errorf("install --dry-run: %v", m["mode"])
	}
	if m := daemonJSON(t, "stop", ""); m["loaded"] != false {
		t.Errorf("stop answered %v", m)
	}
	if m := daemonJSON(t, "start", ""); m["loaded"] != true {
		t.Errorf("start answered %v", m)
	}
	f.fail = "launchctl load"
	if got := errCode(t, runDaemonCmd(&buf, "restart", "", true)); got != codeServiceFailed {
		t.Errorf("a failing launchctl: %q", got)
	}
	f.fail = ""
	if m := daemonJSON(t, "uninstall", ""); m["installed"] != false {
		t.Errorf("uninstall answered %v", m)
	}
	if _, err := os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plist stayed")
	}
}

func TestDaemonOnSystemd(t *testing.T) {
	f, home, bin := serviceWorld(t, "linux")
	unit := filepath.Join(home, ".config", "systemd", "user", serviceUnit)
	m := daemonJSON(t, "install", "live")
	if m["platform"] != "systemd" || m["mode"] != "live" || m["loaded"] != true {
		t.Errorf("install answered %v", m)
	}
	if got := readFile(t, unit); got != renderUnit(bin, true) {
		t.Errorf("unit:\n%s", got)
	}
	calls := strings.Join(f.calls, "\n")
	for _, want := range []string{"systemctl --user daemon-reload", "systemctl --user enable --now " + serviceUnit} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in %v", want, f.calls)
		}
	}
	if m := daemonJSON(t, "dry-run", ""); m["mode"] != "dry-run" {
		t.Errorf("dry-run answered %v", m)
	}
	if !strings.Contains(readFile(t, unit), "ExecStart="+bin+" watch\n") {
		t.Errorf("dry-run unit:\n%s", readFile(t, unit))
	}
}

func TestDaemonRefusesOtherPlatforms(t *testing.T) {
	serviceWorld(t, "freebsd")
	var buf bytes.Buffer
	if got := errCode(t, runDaemonCmd(&buf, "status", "", true)); got != codeUnsupported {
		t.Errorf("code %q", got)
	}
}

// The files `cs daemon install` writes are the ones install.sh writes, so the
// two routes cannot drift apart.
func TestServiceFilesMatchInstallSh(t *testing.T) {
	sh := readFile(t, filepath.Join("..", "..", "install.sh"))
	heredoc := func(tag string) string {
		m := regexp.MustCompile(`(?s)<<` + tag + `\n(.*?\n)` + tag + `\n`).FindStringSubmatch(sh)
		if m == nil {
			t.Fatalf("no %s heredoc in install.sh", tag)
		}
		return m[1]
	}
	plist, unit := heredoc("PLIST_EOF"), heredoc("UNIT_EOF")
	for _, live := range []bool{false, true} {
		liveArg, liveFlag := "", ""
		if live {
			liveArg, liveFlag = "    <string>--live</string>", " --live"
		}
		want := strings.NewReplacer("${LABEL}", serviceLabel, "${BIN_DIR}/claudeswitch", "/opt/x/claudeswitch",
			"${LIVE_ARG}", liveArg, "${STATE_DIR}", "/st", "\\`", "`").Replace(plist)
		if got := renderPlist("/opt/x/claudeswitch", "/st", live); got != want {
			t.Errorf("plist (live %v) differs from install.sh's:\n%s\n---\n%s", live, got, want)
		}
		want = strings.NewReplacer("${BIN_DIR}/claudeswitch", "/opt/x/claudeswitch", "${LIVE_FLAG}", liveFlag).Replace(unit)
		if got := renderUnit("/opt/x/claudeswitch", live); got != want {
			t.Errorf("unit (live %v) differs from install.sh's:\n%s\n---\n%s", live, got, want)
		}
	}
}
