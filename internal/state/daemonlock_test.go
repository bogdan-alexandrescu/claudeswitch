package state

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// holdShared takes the daemon lock the way a status probe does, shared.
func holdShared(t *testing.T) func() {
	t.Helper()
	p := lockPath(daemonLockName)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
}

// A `cs status` probing the lock while the daemon starts must not make the
// daemon exit: the probe holds it for moments, and the acquire waits that out.
func TestAcquireDaemonLockWaitsOutAProbe(t *testing.T) {
	isolate(t)
	probe, err := tryLock(lockPath(daemonLockName)) // the old, exclusive probe
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		probe.Release()
	}()
	l, err := AcquireDaemonLock()
	if err != nil {
		t.Fatalf("a 50ms probe made the daemon's acquire fail: %v", err)
	}
	l.Release()
}

// A real daemon still keeps a second one out.
func TestAcquireDaemonLockRefusesARealHolder(t *testing.T) {
	isolate(t)
	first, err := AcquireDaemonLock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	start := time.Now()
	if l, err := AcquireDaemonLock(); err == nil {
		l.Release()
		t.Fatal("a second daemon acquired the lock")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("the refusal took %s; it must give up within about half a second", waited)
	}
	if !DaemonRunning() {
		t.Fatal("DaemonRunning must see a daemon holding the lock")
	}
}

// Two probes never take each other for a daemon.
func TestDaemonRunningProbesDoNotCollide(t *testing.T) {
	isolate(t)
	release := holdShared(t)
	defer release()
	if DaemonRunning() {
		t.Fatal("another probe holding the lock was taken for a running daemon")
	}
}
