package usage

import (
	"testing"
	"time"
)

// The deadlock this guards against: every account runs out of quota, the 429s
// that follow arm a twenty-minute lockout, and `cs add` — the one command that
// relieves the shortage — is refused for the whole of it. The credential it
// would have vaulted came from an interactive login, so the refusal does not
// merely delay the work, it discards it.
func TestAnInteractiveCallIsNotRefusedByOurOwnLockout(t *testing.T) {
	b := NewBudget()
	b.Penalize(20 * time.Minute)

	if ok, reason := b.Allow(Swap); ok || reason != ReasonLockout {
		t.Fatalf("precondition: a swap call should be locked out, got ok=%v %q", ok, reason)
	}
	if ok, reason := b.Allow(Interactive); !ok {
		t.Errorf("an interactive call must go through a lockout, got %q", reason)
	}
}

// Nor by a spent allowance: the window is a burst guard against loops, and a
// person running `add` is not a loop.
func TestAnInteractiveCallIsNotRefusedByASpentAllowance(t *testing.T) {
	b := NewBudget()
	for i := 0; i < DefaultAllowance; i++ {
		b.Allow(Swap)
	}
	if ok, _ := b.Allow(Swap); ok {
		t.Fatal("precondition: the allowance should be spent")
	}
	if ok, reason := b.Allow(Interactive); !ok {
		t.Errorf("an interactive call must go through a spent allowance, got %q", reason)
	}
}

// Bypassing the refusal is not the same as being invisible. The call still
// happened, so it still counts against the window — otherwise a bypass would
// hide spend from every other caller and the budget would stop adding up.
func TestAnInteractiveCallIsStillRecorded(t *testing.T) {
	b := NewBudget()
	before := len(b.calls)
	if ok, _ := b.Allow(Interactive); !ok {
		t.Fatal("should be allowed")
	}
	if len(b.calls) != before+1 {
		t.Errorf("the call must be recorded: %d → %d", before, len(b.calls))
	}
}

// The reserve exists so a rotation can always be checked. Only the daemon's own
// scheduled polling is held back from it.
func TestOnlyScheduledPollingKeepsTheReserve(t *testing.T) {
	b := NewBudget()
	for i := 0; i < DefaultAllowance-ReservedForSwap; i++ {
		if ok, _ := b.Allow(Scheduled); !ok {
			t.Fatalf("call %d should fit under the reserve", i)
		}
	}
	if ok, reason := b.Allow(Scheduled); ok || reason != ReasonReserved {
		t.Errorf("scheduled polling must stop at the reserve, got ok=%v %q", ok, reason)
	}
	if ok, _ := b.Allow(Swap); !ok {
		t.Error("a swap call must be able to spend the reserve")
	}
}
