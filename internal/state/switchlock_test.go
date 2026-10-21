package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// D34: one machine-wide switch lock, held from the §3 check through the
// live write and its verify, so two writers cannot both pass §3 for one
// account. It sits beside state.lock and daemon.lock.
func TestSwitchLockLivesBesideTheOtherLocks(t *testing.T) {
	isolate(t)
	l, err := AcquireSwitchLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	want := filepath.Join(filepath.Dir(DefaultPath()), "switch.lock")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("no lock file at %s: %v", want, err)
	}
}

// A second writer waits, within its bound, and is refused clearly when the
// first does not let go.
func TestSwitchLockIsExclusiveAndItsWaitIsBounded(t *testing.T) {
	isolate(t)
	first, err := AcquireSwitchLock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	second, err := AcquireSwitchLock(150 * time.Millisecond)
	if err == nil {
		second.Release()
		t.Fatal("a second writer took the switch lock while the first held it")
	}
	if !errors.Is(err, ErrSwitchBusy) {
		t.Fatalf("err = %v, want ErrSwitchBusy", err)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond || waited > 3*time.Second {
		t.Fatalf("waited %s, want about the 150ms bound", waited)
	}

	// Released within the wait, it is taken.
	go func() {
		time.Sleep(50 * time.Millisecond)
		first.Release()
	}()
	third, err := AcquireSwitchLock(2 * time.Second)
	if err != nil {
		t.Fatalf("the lock was released within the wait, yet: %v", err)
	}
	third.Release()
}

// Zero waits not at all: a holder can be detected without blocking.
func TestSwitchLockWithNoWaitAnswersAtOnce(t *testing.T) {
	isolate(t)
	first, err := AcquireSwitchLock(0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	start := time.Now()
	if _, err := AcquireSwitchLock(0); !errors.Is(err, ErrSwitchBusy) {
		t.Fatalf("err = %v, want ErrSwitchBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a zero wait blocked")
	}
}

// Path is where the state was loaded from, so a writer can read it again
// once it holds the switch lock.
func TestStatePathIsWhereItWasLoadedFrom(t *testing.T) {
	path := isolate(t)
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Path() != path {
		t.Fatalf("Path() = %q, want %q", st.Path(), path)
	}
}
