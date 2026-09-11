package credstore

import (
	"testing"
	"time"
)

// storeUsable reports whether this machine has a credential store a test can
// actually exercise. A hosted CI runner has a login keychain that blocks on an
// approval nobody can answer, which is a property of the runner rather than a
// bug in the code under test — so the tests below skip there instead of failing
// and teaching everyone to ignore a red build.
func storeUsable(t *testing.T) {
	t.Helper()
	start := time.Now()
	_, err := Read("claudeswitch:__definitely_not_present")
	if err == nil {
		t.Fatal("a service that should not exist was readable")
	}
	// A working store answers "no such item" immediately. One that sits there
	// until the timeout is not available to this process at all.
	if time.Since(start) > 5*time.Second {
		t.Skipf("no usable credential store here (a read of a missing item took %s)",
			time.Since(start).Round(time.Second))
	}
}

// A refresh revokes the old token the moment it succeeds, so the store has to
// be proven writable *before* that call. This is the proof.
func TestCheckWritableRoundTripsAndCleansUp(t *testing.T) {
	storeUsable(t)
	if err := CheckWritable(); err != nil {
		t.Fatalf("CheckWritable on a healthy store: %v", err)
	}
	if _, err := Read(probeService); err == nil {
		t.Fatal("the probe item survived the check; it must not be left behind")
	}
}

// Running it twice has to work: a probe left over from a previous call would
// otherwise collide, and a refusal here would block every refresh.
func TestCheckWritableIsRepeatable(t *testing.T) {
	storeUsable(t)
	for i := 0; i < 3; i++ {
		if err := CheckWritable(); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}
