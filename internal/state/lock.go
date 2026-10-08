package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Two locks, two jobs.
//
// daemon.lock is held for the daemon's whole life: it enforces one daemon per
// machine, and lets the CLI tell whether a daemon is running (if the CLI can
// take it, nobody is home).
//
// state.lock is taken briefly around any read-modify-write of state.json, so a
// CLI mutation and a daemon save cannot interleave into a lost update.
const (
	daemonLockName = "daemon.lock"
	stateLockName  = "state.lock"
)

type Lock struct {
	f    *os.File
	path string
}

func lockPath(name string) string {
	return filepath.Join(filepath.Dir(DefaultPath()), name)
}

// tryLock takes an exclusive flock without blocking.
func tryLock(path string) (*Lock, error) { return tryLockMode(path, syscall.LOCK_EX) }

// tryLockMode takes a flock of the given mode (LOCK_EX or LOCK_SH) without
// blocking.
func tryLockMode(path string, mode int) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock held by another process: %w", err)
	}
	return &Lock{f: f, path: path}, nil
}

// daemonLockWait is how long AcquireDaemonLock waits out a lock held only
// for a moment, by a DaemonRunning probe, before calling it a daemon.
const (
	daemonLockWait  = 500 * time.Millisecond
	daemonLockRetry = 25 * time.Millisecond
)

// blockingLock waits for the lock. Only used for the brief state lock.
func blockingLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f, path: path}, nil
}

func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}

// AcquireDaemonLock is how the daemon claims sole ownership. A second daemon
// gets an error instead of quietly fighting the first over state.json.
//
// A `status` probing the lock (DaemonRunning) holds it for moments; failing on
// that made a daemon starting at the same instant exit, to be restarted by
// launchd half a minute later. So a lock that is busy is retried for
// daemonLockWait before it counts as another daemon.
func AcquireDaemonLock() (*Lock, error) {
	deadline := time.Now().Add(daemonLockWait)
	for {
		l, err := tryLock(lockPath(daemonLockName))
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return nil, fmt.Errorf("another claudeswitch daemon is already running: %w", err)
		}
		time.Sleep(daemonLockRetry)
	}
}

// ErrDaemonRunning is TryDaemonLock's answer when a daemon holds the lock,
// as distinct from failing to take it at all.
var ErrDaemonRunning = errors.New("a claudeswitch daemon is running")

// TryDaemonLock takes the daemon lock without waiting, for a command that
// must keep any daemon from starting while it rewrites state (rename). It
// fails at once if a daemon holds it; a daemon starting meanwhile waits its
// short bound and exits, to be restarted by its service manager.
func TryDaemonLock() (*Lock, error) {
	l, err := tryLock(lockPath(daemonLockName))
	if err != nil && errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, fmt.Errorf("%w: %v", ErrDaemonRunning, err)
	}
	return l, err
}

// DaemonRunning reports whether a daemon holds the lock. It works by trying to
// take it, shared: a daemon's exclusive lock refuses that, while two probes
// sharing it never take each other for a daemon.
func DaemonRunning() bool {
	l, err := tryLockMode(lockPath(daemonLockName), syscall.LOCK_SH)
	if err != nil {
		return true
	}
	l.Release()
	return false
}
