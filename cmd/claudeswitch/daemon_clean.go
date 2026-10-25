package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/clean"
)

// The daemon's cleanup (owner decisions 2026-10-25): with clean on, once
// every clean_every, judged and applied as `cs clean --apply` would, on a
// goroutine of its own so polling and rotation never wait for a scan. A
// dry-run daemon only plans. Each run is audited (kind "clean").

// cleanStartDelay keeps the first cleanup away from the daemon's start: a
// login, a reboot or an upgrade is not the moment to scan every repository.
const cleanStartDelay = 10 * time.Minute

// daemonCleaner is the daemon's cleanup schedule. Only run's goroutine
// touches started and last.
type daemonCleaner struct {
	started time.Time
	last    time.Time
	running atomic.Bool
	wg      sync.WaitGroup
	// do makes one plan and, with apply, acts on it. A seam; nil is the
	// real thing.
	do func(ctx context.Context, opt clean.Options, apply bool) (*clean.Plan, *clean.Run, error)
}

// wait is for tests: until the cleanup started last has finished.
func (c *daemonCleaner) wait() { c.wg.Wait() }

func cleanOnce(ctx context.Context, opt clean.Options, apply bool) (*clean.Plan, *clean.Run, error) {
	plan, err := clean.Scan(ctx, opt)
	if err != nil || !apply {
		return plan, nil, err
	}
	run, err := clean.Apply(ctx, plan, opt)
	return plan, run, err
}

// maybeClean starts a cleanup when clean is on, the daemon has been up for
// cleanStartDelay, clean_every has passed since the last run (the newest
// journal, across restarts) and none is running.
func (d *daemon) maybeClean(ctx context.Context) {
	if d.cfg == nil || !d.cfg.CleanOn() {
		return
	}
	c := &d.cleaner
	now := time.Now()
	if c.started.IsZero() {
		c.started = now
	}
	if now.Sub(c.started) < cleanStartDelay {
		return
	}
	opt, err := cleanOptions(d.cfg, now)
	if err != nil {
		d.log.Warn("cleanup: cannot read what to look at", "err", err)
		return
	}
	if c.last.IsZero() {
		if runs, _ := clean.Runs(opt); len(runs) > 0 {
			c.last = runs[0].At
		}
	}
	if !c.last.IsZero() && now.Sub(c.last) < d.cfg.CleanInterval() {
		return
	}
	if !c.running.CompareAndSwap(false, true) {
		return
	}
	c.last = now
	do := c.do
	if do == nil {
		do = cleanOnce
	}
	apply := d.live
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.running.Store(false)
		plan, run, err := do(ctx, opt, apply)
		ev := audit.Event{Kind: "clean", DryRun: !apply}
		switch {
		case err != nil:
			ev.Err = err.Error()
			d.log.Warn("cleanup failed", "err", err)
		case !apply:
			var n int
			var bytes int64
			for _, it := range plan.Removable() {
				n++
				bytes += it.Bytes
			}
			ev.Reason = fmt.Sprintf("would move %d to the Trash (%s); dry run", n, clean.Human(bytes))
			d.log.Info("cleanup planned (dry run)", "items", n, "bytes", bytes)
		default:
			ev.Reason = cleanSummary(run)
			d.log.Info("cleaned up after Claude", "run", run.ID, "removed", len(run.Done),
				"bytes", run.Bytes(), "skipped", len(run.Skipped), "failed", len(run.Failed))
		}
		_ = d.aud.Write(ev)
	}()
}

// cleanSummary is a run in one line, for the audit.
func cleanSummary(run *clean.Run) string {
	counts := map[clean.Kind]int{}
	for _, e := range run.Done {
		counts[e.Kind]++
	}
	var parts []string
	for _, k := range kindOrderCLI {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, kindTitle[k]))
		}
	}
	if len(parts) == 0 {
		return "run " + run.ID + ": nothing to remove"
	}
	s := fmt.Sprintf("run %s moved %s to the Trash: %s", run.ID, clean.Human(run.Bytes()), strings.Join(parts, ", "))
	if n := len(run.Skipped); n > 0 {
		s += fmt.Sprintf("; %d changed since the plan, left", n)
	}
	if n := len(run.Failed); n > 0 {
		s += fmt.Sprintf("; %d failed", n)
	}
	return s + "; cs clean undo " + run.ID + " puts it back"
}
