package policy

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// M12 (owner, 2026-10-08): "already on the best" is measured by the
// policy's room — points short of each window's own trigger, as Best ranks —
// not by raw utilization, so the app's button and the CLI never disagree.

func roomCfg() *Input {
	c := cfg()
	c.SwitchAtWeekly = 98
	return &Input{Cfg: c, Now: now}
}

func eq(p *float64, want float64) bool { return p != nil && *p == want }

// The case raw utilization gets wrong: the active session is at 80% against
// an 85% trigger (5 points of room) while the best account's week is at 85%
// against a 98% trigger (13 points). By the higher raw percentage the best
// looks worse (85 >= 80); by room it is better, and Best would move there.
func TestChooseRoomBeatsRawUtilization(t *testing.T) {
	in := *roomCfg()
	in.St = st("work-a", map[string]*state.Account{
		"work-a": reading(80, 30, now.Add(time.Hour)),
		"work-b": reading(10, 85, now.Add(time.Hour)),
	})
	c := Choose(in, "default")
	if c.ID != "work-b" {
		t.Fatalf("Choose = %+v, want work-b", c)
	}
	if !eq(c.ActiveRoom, 5) || !eq(c.BestRoom, 13) {
		t.Fatalf("rooms = %v, %v; want 5 and 13", deref(c.ActiveRoom), deref(c.BestRoom))
	}
	if c.OnBest {
		t.Fatal("on_best with the best account 8 points roomier")
	}
}

// No more room than the active account, by the binding window, is on the
// best; equal room is too (better breaks no tie in the best's favour).
func TestChooseOnBestWhenTheBestHasNoMoreRoom(t *testing.T) {
	for _, tc := range []struct{ active, best float64 }{{30, 30}, {30, 55}} {
		in := *roomCfg()
		in.St = st("work-a", map[string]*state.Account{
			"work-a": reading(tc.active, 10, now.Add(time.Hour)),
			"work-b": reading(tc.best, 10, now.Add(time.Hour)),
		})
		c := Choose(in, "default")
		if c.ID != "work-b" || !c.OnBest {
			t.Errorf("active %v, best %v: %+v, want on_best", tc.active, tc.best, c)
		}
		if !eq(c.ActiveRoom, 85-tc.active) || !eq(c.BestRoom, 85-tc.best) {
			t.Errorf("rooms = %v, %v", deref(c.ActiveRoom), deref(c.BestRoom))
		}
	}
}

// Unknown is never 0 and never on the best.
func TestChooseUnknownActiveRoomIsNull(t *testing.T) {
	in := *roomCfg()
	in.St = st("work-a", map[string]*state.Account{
		"work-a": {},
		"work-b": reading(70, 10, now.Add(time.Hour)),
	})
	c := Choose(in, "default")
	if c.ID != "work-b" || c.ActiveRoom != nil || !eq(c.BestRoom, 15) || c.OnBest {
		t.Fatalf("Choose = %+v (active room %v), want work-b, null active room, not on best", c, deref(c.ActiveRoom))
	}
	// An expired reading (taken before a reset that has since passed) is
	// unknown too.
	old := reading(10, 10, now.Add(-time.Hour))
	old.LastAt = now.Add(-2 * time.Hour)
	in.St = st("work-a", map[string]*state.Account{
		"work-a": old,
		"work-b": reading(70, 10, now.Add(time.Hour)),
	})
	if c := Choose(in, "default"); c.ActiveRoom != nil || c.OnBest {
		t.Fatalf("expired: %+v (active room %v)", c, deref(c.ActiveRoom))
	}
}

// With no best account there is no best room, and nothing to be on.
func TestChooseNoBestHasNullBestRoom(t *testing.T) {
	in := *roomCfg()
	in.St = st("work-a", map[string]*state.Account{
		"work-a": reading(10, 10, now.Add(time.Hour)),
		"work-b": reading(90, 10, now.Add(time.Hour)),
	})
	in.Pool = []string{"work-a", "work-b"}
	c := Choose(in, "default")
	if c.ID != "" || c.BestRoom != nil || c.OnBest || !eq(c.ActiveRoom, 75) {
		t.Fatalf("Choose = %+v", c)
	}
}

// A refused active account has no room whatever its reading says.
func TestChooseRefusedActiveIsNotOnBest(t *testing.T) {
	in := *roomCfg()
	a := reading(10, 10, now.Add(time.Hour))
	a.BurntTil = now.Add(time.Hour)
	in.St = st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(50, 10, now.Add(time.Hour)),
	})
	if c := Choose(in, "default"); c.ID != "work-b" || c.OnBest {
		t.Fatalf("Choose = %+v, want work-b and not on best", c)
	}
}

// Best and Choose agree.
func TestBestIsChoose(t *testing.T) {
	in := *roomCfg()
	in.St = st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(80, 30, now.Add(time.Hour)),
	})
	id, why := Best(in, "default")
	c := Choose(in, "default")
	if id != c.ID || why != c.Why {
		t.Fatalf("Best = %q, %q; Choose = %+v", id, why, c)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
