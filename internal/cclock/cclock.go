// Package cclock takes Claude Code's own credential locks, so a write to an
// profile's live credential never interleaves with Claude Code refreshing it.
//
// The protocol is Claude Code's, read from 2.1.293 (docs/GROUND_TRUTH.md §43):
// proper-lockfile directory locks — mkdir to take, rmdir to release, EEXIST
// meaning held — first <dir>/.oauth_refresh.lock, then <realpath(dir)>.lock.
// A lock whose mtime is over 60 s old is stale and may be removed; a holder
// touches its locks every 5 s. No owner record is written, so Claude Code
// never takes these over: it waits, then skips its refresh this time.
package cclock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// Stale is how old a lock's mtime must be before it is taken for a dead
	// holder's (Claude Code: stale 60000).
	Stale = 60 * time.Second
	// DefaultWait is how long Acquire waits for a held lock by default. Claude
	// Code holds them for one token exchange, normally well under this.
	DefaultWait = 5 * time.Second
)

// ErrBusy means the lock was still held when the wait ran out. Nothing was
// written; try again later.
var ErrBusy = errors.New("Claude Code's credential lock is held")

// Seams.
var (
	// touchEvery is how often a held lock's mtime is refreshed (Claude Code:
	// update 5000).
	touchEvery = 5 * time.Second
	// pollEvery is how often a held lock is retried while waiting.
	pollEvery = 100 * time.Millisecond
)

// Paths are the two lock directories for a secure-storage dir, in the order
// they are taken.
func Paths(dir string) (primary, legacy string) {
	primary = filepath.Join(dir, ".oauth_refresh.lock")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	return primary, real + ".lock"
}

// Held is a pair of held locks. The zero value holds nothing.
type Held struct {
	paths []string
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

// Acquire takes both locks for dir, waiting up to wait for a held one. A dir
// that does not exist has no Claude Code using it, and needs no lock.
func Acquire(ctx context.Context, dir string, wait time.Duration) (*Held, error) {
	if dir == "" {
		return &Held{}, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return &Held{}, nil
	}
	if wait <= 0 {
		wait = DefaultWait
	}
	primary, legacy := Paths(dir)
	deadline := time.Now().Add(wait)
	for {
		held, busy, err := tryBoth(primary, legacy)
		if err != nil {
			return nil, err
		}
		if !busy {
			h := &Held{paths: held, stop: make(chan struct{}), done: make(chan struct{})}
			go h.touch()
			return h, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w (%s, still held after %s); nothing was written",
				ErrBusy, busy2(primary, legacy), wait)
		}
		t := time.NewTimer(min(pollEvery, time.Until(deadline)))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("waiting for Claude Code's credential lock: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// busy2 names whichever lock is held, for the error.
func busy2(primary, legacy string) string {
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	return legacy
}

// tryBoth makes one attempt at both locks, in Claude Code's order. With the
// second held, the first is released again.
func tryBoth(primary, legacy string) (held []string, busy bool, err error) {
	ok, err := tryOne(primary)
	if err != nil || !ok {
		return nil, !ok && err == nil, err
	}
	ok, err = tryOne(legacy)
	if err != nil || !ok {
		_ = os.Remove(primary)
		return nil, !ok && err == nil, err
	}
	return []string{primary, legacy}, false, nil
}

// tryOne is proper-lockfile's acquire: mkdir; on EEXIST, a lock untouched for
// longer than Stale is removed and the mkdir tried once more.
func tryOne(path string) (bool, error) {
	err := os.Mkdir(path, 0o700)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("taking Claude Code's credential lock %s: %w", path, err)
	}
	fi, serr := os.Stat(path)
	if serr != nil {
		if errors.Is(serr, os.ErrNotExist) { // released between the two calls
			return tryOnce(path)
		}
		return false, fmt.Errorf("checking Claude Code's credential lock %s: %w", path, serr)
	}
	if time.Since(fi.ModTime()) <= Stale {
		return false, nil
	}
	if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return false, nil // someone else is dealing with it
	}
	return tryOnce(path)
}

// tryOnce is a single mkdir with no stale check.
func tryOnce(path string) (bool, error) {
	err := os.Mkdir(path, 0o700)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrExist):
		return false, nil
	}
	return false, fmt.Errorf("taking Claude Code's credential lock %s: %w", path, err)
}

// touch keeps the held locks' mtimes current until Release, so a slow write
// is never taken for a dead holder.
func (h *Held) touch() {
	defer close(h.done)
	t := time.NewTicker(touchEvery)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			now := time.Now()
			for _, p := range h.paths {
				_ = os.Chtimes(p, now, now)
			}
		}
	}
}

// Release lets both locks go, the second first, as Claude Code does. It is
// safe to call more than once.
func (h *Held) Release() error {
	if h == nil || h.stop == nil {
		return nil
	}
	var first error
	h.once.Do(func() {
		close(h.stop)
		<-h.done
		for i := len(h.paths) - 1; i >= 0; i-- {
			if err := os.Remove(h.paths[i]); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
				first = fmt.Errorf("releasing Claude Code's credential lock %s: %w", h.paths[i], err)
			}
		}
	})
	return first
}
