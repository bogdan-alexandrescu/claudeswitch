package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lagDaemon makes the daemon's lock say `before` for the first n probes and
// `after` from then on: a daemon that takes, or lets go of, its lock a moment
// after launchctl or systemctl has returned.
func lagDaemon(t *testing.T, n int, before, after bool) *int {
	t.Helper()
	old := daemonRunning
	t.Cleanup(func() { daemonRunning = old })
	probes := 0
	daemonRunning = func() bool {
		probes++
		if probes <= n {
			return before
		}
		return after
	}
	return &probes
}

// The app's Live toggle runs `daemon dry-run`, which reloads the service and
// answers with the status. launchctl returns once the job is loaded, before
// the new daemon has taken its lock, so read at once the daemon was "loaded,
// not running", and the app showed that, with Start where Stop belonged,
// while the dry-run daemon ran (observed 2026-10-21, renaming an account).
func TestDaemonVerbReportsADaemonStillStarting(t *testing.T) {
	serviceWorld(t, "darwin")
	daemonJSON(t, "install", "live")
	for _, verb := range []string{"dry-run", "live", "restart", "start"} {
		lagDaemon(t, 3, false, true)
		if m := daemonJSON(t, verb, ""); m["running"] != true {
			t.Errorf("%s answered running=%v for a daemon that took its lock a moment later", verb, m["running"])
		}
	}
}

// And the other way: a daemon that lets go of its lock a moment after
// launchctl unload returned is stopped, and stop says so.
func TestDaemonStopReportsADaemonStillExiting(t *testing.T) {
	serviceWorld(t, "darwin")
	daemonJSON(t, "install", "live")
	lagDaemon(t, 3, true, false)
	if m := daemonJSON(t, "stop", ""); m["running"] != false {
		t.Errorf("stop answered running=%v for a daemon that exited a moment later", m["running"])
	}
}

// A daemon that never takes its lock (it fails at start, and launchd retries
// half a minute later) is reported not running once the wait runs out; the
// verb does not hang on it.
func TestDaemonVerbStopsWaitingForADaemonThatNeverStarts(t *testing.T) {
	serviceWorld(t, "darwin")
	daemonJSON(t, "install", "live")
	oldWait, oldStep := daemonSettleWait, daemonSettleStep
	t.Cleanup(func() { daemonSettleWait, daemonSettleStep = oldWait, oldStep })
	daemonSettleWait, daemonSettleStep = 30*time.Millisecond, time.Millisecond
	lagDaemon(t, 1<<30, false, true)
	start := time.Now()
	m := daemonJSON(t, "restart", "")
	if m["running"] != false {
		t.Errorf("restart answered running=%v for a daemon that never started", m["running"])
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("restart took %s waiting for a daemon that never started", d)
	}
}

// A loaded job whose daemon has exited cleanly stays down: KeepAlive restarts
// it only after a failure. Start has to start it, not just see it is loaded.
func TestDaemonStartStartsALoadedJobThatIsNotRunning(t *testing.T) {
	f, home, _ := serviceWorld(t, "darwin")
	daemonJSON(t, "install", "live")
	started := false
	run := serviceSeams.run
	serviceSeams.run = func(name string, args ...string) (string, error) {
		if filepath.Base(name) == "launchctl" && strings.Join(args, " ") == "start "+serviceLabel {
			started = true
		}
		return run(name, args...)
	}
	old := daemonRunning
	t.Cleanup(func() { daemonRunning = old })
	daemonRunning = func() bool { return started }
	f.calls = nil
	m := daemonJSON(t, "start", "")
	if !started {
		t.Errorf("start ran %v and left the loaded job down", f.calls)
	}
	if m["running"] != true {
		t.Errorf("start answered %v", m)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "launchctl load") {
		t.Errorf("start reloaded a job already loaded: %v (plist in %s)", f.calls, home)
	}
}
