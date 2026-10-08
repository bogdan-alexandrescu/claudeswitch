package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The header states the daemon's real condition and the newest reading's age.
func TestBrandHeaderStates(t *testing.T) {
	render.SetColor(false)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	st := &state.State{Accounts: map[string]*state.Account{
		"a": {LastAt: now.Add(-3 * time.Minute)},
		"b": {LastAt: now.Add(-70 * time.Second)},
		"c": {},
	}}
	old := version
	t.Cleanup(func() { version = old })
	version = "1.4.0"

	st.DaemonLive = true
	if got := brandHeader(st, true, now); got != "  ◎ claudeswitch 1.4.0 · daemon live · polled 1m ago\n" {
		t.Errorf("live = %q", got)
	}
	st.DaemonLive = false
	if got := brandHeader(st, true, now); !strings.Contains(got, "· daemon dry-run ·") {
		t.Errorf("dry-run = %q", got)
	}
	// DaemonLive is left over from the last daemon; with none running it
	// says nothing about now.
	st.DaemonLive = true
	if got := brandHeader(st, false, now); !strings.Contains(got, "· daemon not running ·") {
		t.Errorf("not running = %q", got)
	}
	if got := brandHeader(&state.State{}, false, now); !strings.HasSuffix(got, "· not polled yet\n") {
		t.Errorf("unpolled = %q", got)
	}
}

// On a terminal the view opens with the header and a blank line; piped, or
// with --json, it does not change at all.
func TestHeaderOnlyOnATerminal(t *testing.T) {
	path, _, _ := pinWorld(t, 40)
	oldOn, oldRun := headerOn, daemonRunning
	t.Cleanup(func() { headerOn, daemonRunning = oldOn, oldRun })
	daemonRunning = func() bool { return false }

	headerOn = func() bool { return false }
	piped := captureStdout(t, func() error { return cmdWhy([]string{"--config", path}) })
	if strings.Contains(piped, render.Mark) {
		t.Errorf("piped output must have no header:\n%s", piped)
	}

	headerOn = func() bool { return true }
	tty := captureStdout(t, func() error { return cmdWhy([]string{"--config", path}) })
	lines := strings.Split(tty, "\n")
	if !strings.HasPrefix(lines[0], "  ◎ claudeswitch ") || !strings.Contains(lines[0], "daemon not running") {
		t.Errorf("first line should be the header, got %q", lines[0])
	}
	if lines[1] != "" {
		t.Errorf("a blank line should follow the header, got %q", lines[1])
	}
	if tty[len(lines[0])+1:] != piped {
		t.Errorf("the header must be the only difference:\n%s\n---\n%s", tty, piped)
	}

	js := captureStdout(t, func() error { return cmdWhy([]string{"--config", path, "--json"}) })
	if strings.Contains(js, render.Mark) {
		t.Errorf("--json must have no header:\n%s", js)
	}
}
