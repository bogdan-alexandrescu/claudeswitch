package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// Lane 6 at the daemon and `use`: every swap names the account it is about
// to overwrite, so the vault can capture it (I1); a target about to expire is
// refreshed first, unless it may be live anywhere (I3); and Claude Code
// holding its credential lock is a retry, not a failure (I2).

// workOverTrigger is twoProfiles with work on w1 at 90%, so work rotates to w2.
func workOverTrigger(t *testing.T) *rig {
	t.Helper()
	cfg := twoProfiles()
	cfg.Accounts[2].AccountUUID, cfg.Accounts[2].OrgID = "u-w1", "o-w"
	r := newRig(t, cfg, true)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 5)
	put(st, "w1", 90)
	put(st, "w2", 20)
	return r
}

func TestDaemonSwapNamesTheOutgoingAccountForCapture(t *testing.T) {
	r := workOverTrigger(t)
	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if len(r.v.swapOpts) != 1 {
		t.Fatalf("swaps %+v, want one", r.v.ops("swap"))
	}
	o := r.v.swapOpts[0]
	if o.Profile != "work" || o.Outgoing != "w1" || o.OutgoingSeat != "u-w1@o-w" {
		t.Errorf("swap options %+v, want work's outgoing w1 and its seat", o)
	}
	if o.SeatOwner == nil || o.SeatOwner("u-w1@o-w") != "w1" {
		t.Error("the swap cannot attribute a live credential to a configured account")
	}
}

func TestDaemonFreshensAnExpiringTargetBeforeSwapping(t *testing.T) {
	r := workOverTrigger(t)
	r.v.needsRefresh["w2"] = true
	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	var order []string
	for _, c := range r.v.calls {
		if c.op == "refresh" || c.op == "swap" {
			order = append(order, c.op+":"+c.account)
			if c.op == "refresh" && c.item != nil {
				t.Errorf("the target was refreshed as a live account (holder %v)", c.item.Name())
			}
		}
	}
	if strings.Join(order, ",") != "refresh:w2,swap:w2" {
		t.Fatalf("calls %v, want w2 refreshed, then swapped in", order)
	}
	if r.v.refreshWindow != 10*time.Minute {
		t.Errorf("freshen window %s, want 10m", r.v.refreshWindow)
	}
}

// A refresh revokes the token wherever it is live, so a target that is, or
// may be, live in its own profile's item is never refreshed (D18).
func TestDaemonNeverFreshensATargetThatMayBeLive(t *testing.T) {
	for _, answer := range []string{"yes", "unknown"} {
		t.Run(answer, func(t *testing.T) {
			r := workOverTrigger(t)
			r.v.needsRefresh["w2"] = true
			r.v.holds = map[string]string{"item-work/w2": answer}
			r.d.evaluate(context.Background(), r.prof("work"), "poll")
			if refs := r.v.ops("refresh"); len(refs) != 0 {
				t.Fatalf("refreshed %+v although it may be live", refs)
			}
		})
	}
}

// The target live in this profile under a token the vault never saw (Claude
// Code refreshed it, or a hand login): a token comparison misses it, the seat
// does not. Never refreshed.
func TestDaemonNeverFreshensATargetLiveUnderAnotherToken(t *testing.T) {
	for _, answer := range []string{"yes", "unknown"} {
		t.Run(answer, func(t *testing.T) {
			r := workOverTrigger(t)
			r.v.needsRefresh["w2"] = true
			r.v.seatHolds = map[string]string{"item-work/w2": answer}
			r.d.evaluate(context.Background(), r.prof("work"), "poll")
			if refs := r.v.ops("refresh"); len(refs) != 0 {
				t.Fatalf("refreshed %+v although its seat may be live", refs)
			}
		})
	}
}

// Claude Code holding its lock is a tick to skip, not a failed swap: no error
// audited, nothing recorded as switched, and the next tick tries again.
func TestDaemonRetriesASwapTheLockHeldOff(t *testing.T) {
	r := workOverTrigger(t)
	r.v.swapErr = fmt.Errorf("claude code is refreshing: %w", vault.ErrLockBusy)

	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	r.d.evaluate(context.Background(), r.prof("work"), "poll")

	if n := len(r.v.ops("swap")); n != 2 {
		t.Fatalf("%d swap attempts, want one per tick", n)
	}
	if errs := r.aud.kind("error"); len(errs) != 0 {
		t.Errorf("a busy lock was audited as an error: %+v", errs)
	}
	if got := r.d.st.Profile("work").Active; got != "w1" {
		t.Errorf("work recorded as on %q though nothing was written", got)
	}
}

// staleDaemon makes the running daemon older than this binary.
func staleDaemon(t *testing.T) {
	t.Helper()
	oldR, oldB := daemonRunning, currentBuild
	t.Cleanup(func() { daemonRunning, currentBuild = oldR, oldB })
	daemonRunning = func() bool { return true }
	currentBuild = func() buildStamp { return stamp("0.9.0", "ffffffffffff", time.Now()) }
}

func freshDaemon(t *testing.T) {
	t.Helper()
	oldR := daemonRunning
	t.Cleanup(func() { daemonRunning = oldR })
	daemonRunning = func() bool { return false }
}

// The status line marks an outdated daemon, and changes by nothing else.
func TestPinSingleProfileStatuslineStaleDaemon(t *testing.T) {
	path, _, _ := pinWorld(t, 90)
	staleDaemon(t)
	got := captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	checkGolden(t, "statusline_stale", got)

	plain, err := os.ReadFile(filepath.Join("testdata", "pin", "statusline.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(plain)+"  ⚠ daemon outdated" {
		t.Fatalf("stale status line is not the plain one plus the marker:\n%q", got)
	}
}

func TestStatuslineHasNoMarkerWithACurrentDaemon(t *testing.T) {
	path, _, _ := pinWorld(t, 90)
	freshDaemon(t)
	got := captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	if strings.Contains(got, "daemon outdated") {
		t.Fatalf("marker without a stale daemon: %q", got)
	}
}

func TestStatuslineMarksAnOutdatedDaemonOnEveryShape(t *testing.T) {
	path, _, st := pinWorld(t, 90)
	st.Default().SetActive("")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	staleDaemon(t)
	got := captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	if !strings.HasSuffix(got, "  ⚠ daemon outdated") {
		t.Fatalf("no marker on the no-account line: %q", got)
	}
}

func TestContextTellsTheUserToRestartAnOutdatedDaemon(t *testing.T) {
	path, _, _ := pinWorld(t, 40)
	staleDaemon(t)
	got := captureStdout(t, func() error { return cmdContext([]string{"--config", path}) })
	var hits []string
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.Contains(l, "daemon is older than this claudeswitch") {
			hits = append(hits, l)
		}
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "[claudeswitch] ") ||
		!strings.Contains(hits[0], restartHint(runtime.GOOS)) {
		t.Fatalf("context = %q, want one line saying to restart the daemon", got)
	}

	freshDaemon(t)
	got = captureStdout(t, func() error { return cmdContext([]string{"--config", path}) })
	if strings.Contains(got, "older than this claudeswitch") {
		t.Fatalf("restart line without a stale daemon: %q", got)
	}
}
