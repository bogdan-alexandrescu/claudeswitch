package detector

import (
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Startup seeding walks the whole transcripts tree: 13,756 files and 12 GB
// in the default profile on 2026-10-20. It walked it three times, the first
// holding the lock LastActivity takes, so the daemon's first "is this
// profile busy?" waited for the walk: 6s with a warm cache, 24-77s cold,
// 3m47s with the machine busy, and nothing read or rotated meanwhile.

func seededTree(t *testing.T) (string, time.Time) {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-someone-project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	newest := time.Now().Add(-time.Minute).Truncate(time.Second)
	for i, at := range []time.Time{newest.Add(-time.Hour), newest} {
		p := filepath.Join(proj, []string{"old.jsonl", "new.jsonl"}[i])
		if err := os.WriteFile(p, []byte(`{"cwd":"/work/here"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return root, newest
}

func quietDetector(root string) *Detector {
	return New(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// withWalk replaces the walk for one test.
func withWalk(t *testing.T, w func(string, fs.WalkDirFunc) error) {
	t.Helper()
	old := walkDir
	walkDir = w
	t.Cleanup(func() { walkDir = old })
}

func TestSeedingDoesNotHoldTheLock(t *testing.T) {
	root, newest := seededTree(t)
	entered, release := make(chan struct{}), make(chan struct{})
	withWalk(t, func(r string, fn fs.WalkDirFunc) error {
		close(entered)
		<-release
		return filepath.WalkDir(r, fn)
	})
	d := quietDetector(root)
	stop := make(chan struct{})
	defer close(stop)
	go func() { _ = d.Run(stop) }()
	<-entered

	got := make(chan time.Time, 1)
	go func() { got <- d.LastActivity() }()
	select {
	case at := <-got:
		if !at.IsZero() {
			t.Errorf("LastActivity %v before the walk finished, want zero", at)
		}
	case <-time.After(time.Second):
		t.Fatal("LastActivity waited for the startup walk")
	}
	if d.Seeded() {
		t.Error("Seeded before the walk finished")
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for !d.Seeded() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !d.Seeded() {
		t.Fatal("never seeded")
	}
	if at := d.LastActivity(); !at.Equal(newest) {
		t.Errorf("LastActivity %v, want the newest transcript's %v", at, newest)
	}
	if cwd := d.CurrentDir(); cwd != "/work/here" {
		t.Errorf("CurrentDir %q, want the newest transcript's", cwd)
	}
}

func TestStartupWalksTheTreeOnce(t *testing.T) {
	root, _ := seededTree(t)
	var walks atomic.Int32
	withWalk(t, func(r string, fn fs.WalkDirFunc) error {
		walks.Add(1)
		return filepath.WalkDir(r, fn)
	})
	d := quietDetector(root)
	stop := make(chan struct{})
	defer close(stop)
	go func() { _ = d.Run(stop) }()
	deadline := time.Now().Add(2 * time.Second)
	for !d.Seeded() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := walks.Load(); n != 1 {
		t.Errorf("startup walked the tree %d times, want once", n)
	}
	d.mu.Lock()
	n := len(d.offsets)
	d.mu.Unlock()
	if n != 2 {
		t.Errorf("%d offsets seeded, want both files'", n)
	}
}
