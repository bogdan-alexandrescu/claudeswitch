package policy

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// D1: a profile rotates only within its own pool. An account outside it is
// not a candidate however much room it has.
func TestDecideRestrictsCandidatesToThePool(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pool: []string{"work-a", "personal"},
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(91, 30, now.Add(time.Hour)),
			"work-b":   reading(1, 1, now.Add(time.Hour)), // emptiest, not in the pool
			"personal": reading(20, 20, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want a switch to personal, the only other account in the pool", d)
	}
}

// A pool with no room waits; it does not borrow from outside.
func TestDecideWaitsRatherThanLeaveThePool(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pool: []string{"work-a"},
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(99, 30, now.Add(time.Hour)),
			"work-b": reading(1, 1, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind == Switch {
		t.Fatalf("got %v; work-b is not in the pool", d)
	}
}

// The decision is about the live credential it is given, not the default
// profile's.
func TestDecideJudgesTheGivenProfilesActive(t *testing.T) {
	s := st("work-a", map[string]*state.Account{
		"work-a":   reading(10, 10, now.Add(time.Hour)),
		"work-b":   reading(91, 30, now.Add(time.Hour)),
		"personal": reading(20, 20, now.Add(time.Hour)),
	})
	other := &state.ProfileState{Active: "work-b"}
	d := Decide(Input{Cfg: cfg(), Now: now, St: s, Live: other, Pool: []string{"work-b", "personal"}})
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want work-b (this profile's active, over) to move to personal", d)
	}
}
