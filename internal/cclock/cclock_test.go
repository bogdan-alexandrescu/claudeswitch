package cclock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These pin the protocol read from Claude Code 2.1.293 (GROUND_TRUTH §43):
// two mkdir locks, <dir>/.oauth_refresh.lock then <realpath(dir)>.lock, a
// lock whose mtime is over 60 s old is stale, and a held lock's mtime is
// touched while it is held.

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func claudeDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), ".claude")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAcquireTakesBothLocksAndReleaseRemovesThem(t *testing.T) {
	d := claudeDir(t)
	h, err := Acquire(context.Background(), d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	primary, legacy := Paths(d)
	if primary != filepath.Join(d, ".oauth_refresh.lock") {
		t.Errorf("primary lock %s", primary)
	}
	if !exists(primary) || !exists(legacy) {
		t.Fatalf("held: primary %v, legacy %v; want both", exists(primary), exists(legacy))
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	if exists(primary) || exists(legacy) {
		t.Fatal("a lock outlived its release")
	}
}

// The legacy lock is the realpath of the dir plus ".lock", as Claude Code
// takes it: a dir reached through a symlink locks the real one.
func TestLegacyLockFollowsTheRealPath(t *testing.T) {
	real := claudeDir(t)
	link := filepath.Join(t.TempDir(), "via-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, legacy := Paths(link)
	want, _ := filepath.EvalSymlinks(real)
	if legacy != want+".lock" {
		t.Fatalf("legacy = %s, want %s.lock", legacy, want)
	}
}

func TestAFreshLockIsWaitedForThenGivenUp(t *testing.T) {
	d := claudeDir(t)
	primary, _ := Paths(d)
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := Acquire(context.Background(), d, 300*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if waited := time.Since(start); waited < 300*time.Millisecond || waited > 3*time.Second {
		t.Errorf("waited %s, want about the bound", waited)
	}
	if !exists(primary) {
		t.Error("someone else's lock was removed")
	}
}

func TestALockReleasedWhileWaitingIsTaken(t *testing.T) {
	d := claudeDir(t)
	primary, _ := Paths(d)
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.Remove(primary)
	}()
	h, err := Acquire(context.Background(), d, 3*time.Second)
	if err != nil {
		t.Fatalf("got %v, want the lock once it was released", err)
	}
	_ = h.Release()
}

func TestAStaleLockIsTakenOver(t *testing.T) {
	d := claudeDir(t)
	primary, legacy := Paths(d)
	for _, p := range []string{primary, legacy} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-61 * time.Second)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Acquire(context.Background(), d, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("got %v; a lock untouched for over 60 s is stale", err)
	}
	_ = h.Release()
}

// The second lock held: the first is let go again, so a half-taken pair never
// blocks Claude Code.
func TestAHeldLegacyLockReleasesThePrimary(t *testing.T) {
	d := claudeDir(t)
	primary, legacy := Paths(d)
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Acquire(context.Background(), d, 200*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if exists(primary) {
		t.Error("the primary lock was left held after the legacy one was refused")
	}
}

// A held lock is touched, so a slow write is never taken for a dead holder.
func TestAHeldLockIsTouched(t *testing.T) {
	old := touchEvery
	touchEvery = 20 * time.Millisecond
	t.Cleanup(func() { touchEvery = old })

	d := claudeDir(t)
	h, err := Acquire(context.Background(), d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release()
	primary, legacy := Paths(d)
	past := time.Now().Add(-50 * time.Second)
	for _, p := range []string{primary, legacy} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(150 * time.Millisecond)
	for _, p := range []string{primary, legacy} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(fi.ModTime()) > 10*time.Second {
			t.Errorf("%s not touched while held: mtime %s", p, fi.ModTime())
		}
	}
}

// No directory means no Claude Code using it: nothing to lock, nothing created.
func TestAMissingDirNeedsNoLock(t *testing.T) {
	d := filepath.Join(t.TempDir(), "never-created")
	h, err := Acquire(context.Background(), d, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	if exists(d) {
		t.Error("acquiring created the directory")
	}
}

func TestACancelledWaitStops(t *testing.T) {
	d := claudeDir(t)
	primary, _ := Paths(d)
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Acquire(ctx, d, 5*time.Second); err == nil {
		t.Fatal("acquired a held lock")
	}
}
