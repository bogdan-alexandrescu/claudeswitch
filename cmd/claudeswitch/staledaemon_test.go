package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The upgrade gap D10 opens: a daemon from before the multi-profile release
// reads state fields this binary no longer writes. Every command that touches
// state says so when the running daemon is older than itself.

func stamp(ver, rev string, at time.Time) buildStamp {
	return buildStamp{Version: ver, Revision: rev, Time: at}
}

func daemonState(ver, rev string, at time.Time) *state.State {
	return &state.State{DaemonVersion: ver, DaemonBuild: rev, DaemonBuildTime: at,
		DaemonSince: time.Date(2026, 10, 7, 9, 30, 0, 0, time.Local)}
}

func TestStaleDaemonLine(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(48 * time.Hour)
	me := stamp("dev", "bbbbbbbbbbbb", t1)
	cases := []struct {
		name    string
		st      *state.State
		me      buildStamp
		running bool
		stale   bool
	}{
		{"no daemon running", &state.State{}, me, false, false},
		{"daemon from before the field", &state.State{}, me, true, true},
		{"same build", daemonState("dev", "bbbbbbbbbbbb", t1), me, true, false},
		{"older commit", daemonState("dev", "aaaaaaaaaaaa", t0), me, true, true},
		{"newer commit", daemonState("dev", "cccccccccccc", t1.Add(time.Hour)), me, true, false},
		{"older release", daemonState("0.4.8", "", time.Time{}), stamp("0.5.0", "", time.Time{}), true, true},
		{"newer release", daemonState("0.5.1", "", time.Time{}), stamp("0.5.0", "", time.Time{}), true, false},
		// dev builds without VCS information cannot be ordered: no warning
		// rather than a false one on every command.
		{"dev without vcs", daemonState("dev", "", time.Time{}), stamp("dev", "", time.Time{}), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := staleDaemonLine(c.st, c.me, c.running, "darwin")
			if (got != "") != c.stale {
				t.Fatalf("staleDaemonLine = %q, want stale=%v", got, c.stale)
			}
			if !c.stale {
				return
			}
			for _, want := range []string{"! the running daemon (", "is older than this cs (",
				"restart it: launchctl kickstart -k gui/$UID/xyz.claudeswitch.daemon"} {
				if !strings.Contains(got, want) {
					t.Errorf("line %q lacks %q", got, want)
				}
			}
		})
	}
	if got := staleDaemonLine(&state.State{}, me, true, "linux"); !strings.Contains(got,
		"systemctl --user restart claudeswitch.service") {
		t.Fatalf("linux restart hint: %q", got)
	}
}

// The daemon stamps its own build into state when it starts.
func TestDaemonStartRecordsItsBuild(t *testing.T) {
	old := currentBuild
	t.Cleanup(func() { currentBuild = old })
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	currentBuild = func() buildStamp { return stamp("0.5.0", "abc123", at) }

	r := newRig(t, twoProfiles(), false)
	r.d.start(context.Background())
	st := r.d.st
	if st.DaemonVersion != "0.5.0" || st.DaemonBuild != "abc123" || !st.DaemonBuildTime.Equal(at) {
		t.Fatalf("daemon recorded %q %q %v", st.DaemonVersion, st.DaemonBuild, st.DaemonBuildTime)
	}
}

// staleWorld runs a command against a state file written by a daemon that
// predates the field, with a daemon reported running.
func staleWorld(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldRun, oldOut := daemonRunning, staleWarnOut
	t.Cleanup(func() { daemonRunning, staleWarnOut = oldRun, oldOut })
	daemonRunning = func() bool { return true }
	var b bytes.Buffer
	staleWarnOut = &b
	st, _ := state.Load("")
	if err := st.SaveAs(state.OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	return &b
}

func TestLoadWarnsOnceAboutAStaleDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b := staleWorld(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[account]]\nid = \"a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := load(path); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "is older than this cs"); n != 1 {
		t.Fatalf("stderr = %q, want the warning once", b.String())
	}
}

func TestDoctorFailsOnAStaleDaemon(t *testing.T) {
	w := newDoctorWorld(t, doctorBaseConfig)
	staleWorld(t)
	out, err := w.run(t, false)
	if !strings.Contains(out, "[FAIL] daemon") || err == nil {
		t.Fatalf("a stale daemon must be a doctor FAIL (err %v):\n%s", err, out)
	}
}
