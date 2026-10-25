package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/clean"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// The daemon's cleanup (owner decisions 2026-10-25): with clean on, every
// clean_every, on a goroutine of its own so polling never waits, one at a
// time, and never in the first minutes after the daemon starts. A dry-run
// daemon only plans. Each run is audited.

type cleanRecorder struct {
	mu     sync.Mutex
	runs   []bool // apply, per run
	events []audit.Event
	gate   chan struct{}
}

func (r *cleanRecorder) Write(e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func cleanDaemon(t *testing.T, on, live bool) (*daemon, *cleanRecorder) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	setCCDir(t, "")
	b := on
	cfg := &config.Config{Clean: &b}
	rec := &cleanRecorder{}
	d := &daemon{cfg: cfg, aud: rec, live: live, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.cleaner.started = time.Now().Add(-time.Hour)
	d.cleaner.do = func(_ context.Context, _ clean.Options, apply bool) (*clean.Plan, *clean.Run, error) {
		if rec.gate != nil {
			<-rec.gate
		}
		rec.mu.Lock()
		rec.runs = append(rec.runs, apply)
		rec.mu.Unlock()
		p := &clean.Plan{Items: []clean.Item{{Kind: clean.KindWorktree, Path: "/w", Remove: true, Bytes: 2 << 30}}}
		if !apply {
			return p, nil, nil
		}
		return p, &clean.Run{ID: "r1", Done: []clean.Entry{{Item: p.Items[0]}}}, nil
	}
	return d, rec
}

func (r *cleanRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

func TestTheDaemonCleansUpWhenOnAndDue(t *testing.T) {
	d, rec := cleanDaemon(t, true, true)
	d.maybeClean(context.Background())
	d.cleaner.wait()
	if rec.count() != 1 || !rec.runs[0] {
		t.Fatalf("runs %v", rec.runs)
	}
	if len(rec.events) != 1 || rec.events[0].Kind != "clean" || rec.events[0].Reason == "" {
		t.Fatalf("audit %+v", rec.events)
	}
	// Not again before clean_every.
	d.maybeClean(context.Background())
	d.cleaner.wait()
	if rec.count() != 1 {
		t.Fatalf("ran again: %v", rec.runs)
	}
	d.cleaner.last = time.Now().Add(-25 * time.Hour)
	d.maybeClean(context.Background())
	d.cleaner.wait()
	if rec.count() != 2 {
		t.Fatalf("not run after clean_every: %v", rec.runs)
	}
}

func TestTheDaemonDoesNotCleanUpWhenOffOrJustStarted(t *testing.T) {
	d, rec := cleanDaemon(t, false, true)
	d.maybeClean(context.Background())
	d.cleaner.wait()
	if rec.count() != 0 {
		t.Fatal("cleaned with clean off")
	}
	d, rec = cleanDaemon(t, true, true)
	d.cleaner.started = time.Now()
	d.maybeClean(context.Background())
	d.cleaner.wait()
	if rec.count() != 0 {
		t.Fatal("cleaned right after starting")
	}
}

func TestTheDaemonRunsOneCleanupAtATimeAndOnlyPlansInDryRun(t *testing.T) {
	d, rec := cleanDaemon(t, true, false)
	rec.gate = make(chan struct{})
	d.maybeClean(context.Background())
	d.cleaner.last = time.Time{} // due again, but one is running
	d.maybeClean(context.Background())
	close(rec.gate)
	d.cleaner.wait()
	if rec.count() != 1 || rec.runs[0] {
		t.Fatalf("runs %v: want one plan, no apply", rec.runs)
	}
}
