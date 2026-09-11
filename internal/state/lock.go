package state

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
func tryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock held by another process: %w", err)
	}
	return &Lock{f: f, path: path}, nil
}

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
func AcquireDaemonLock() (*Lock, error) {
	l, err := tryLock(lockPath(daemonLockName))
	if err != nil {
		return nil, fmt.Errorf("another claudeswitch daemon is already running: %w", err)
	}
	return l, nil
}

// DaemonRunning reports whether a daemon holds the lock. It works by trying to
// take it: if we can, nobody else has it.
func DaemonRunning() bool {
	l, err := tryLock(lockPath(daemonLockName))
	if err != nil {
		return true
	}
	l.Release()
	return false
}
