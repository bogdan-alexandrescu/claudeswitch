package credstore

import "testing"

// A refresh revokes the old token the moment it succeeds, so the store has to
// be proven writable *before* that call. This is the proof.
func TestCheckWritableRoundTripsAndCleansUp(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		if err := CheckWritable(); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}
