package credstore

import (
	"errors"
	"testing"
)

// check runs the guard and separates the two ways it can fail. A hosted CI
// runner has a login keychain that blocks every write on an approval nobody can
// answer; that is the machine, not the code, and it surfaces as ErrUnavailable.
// Anything else — a value that reads back wrong, a probe left behind — is a real
// failure of the guard and has to be loud.
//
// Note that a read of a missing item still answers immediately on such a runner,
// so there is no cheaper way to tell: the write has to be attempted.
func check(t *testing.T) {
	t.Helper()
	err := CheckWritable()
	if errors.Is(err, ErrUnavailable) {
		t.Skipf("no usable credential store on this machine: %v", err)
	}
	if err != nil {
		t.Fatalf("CheckWritable on a store that is answering: %v", err)
	}
}

// A refresh revokes the old token the moment it succeeds, so the store has to
// be proven writable *before* that call. This is the proof.
func TestCheckWritableRoundTripsAndCleansUp(t *testing.T) {
	check(t)
	if _, err := Read(probeService); err == nil {
		t.Fatal("the probe item survived the check; it must not be left behind")
	}
}

// Running it twice has to work: a probe left over from a previous call would
// otherwise collide, and a refusal here would block every refresh.
func TestCheckWritableIsRepeatable(t *testing.T) {
	for i := 0; i < 3; i++ {
		check(t)
	}
}
