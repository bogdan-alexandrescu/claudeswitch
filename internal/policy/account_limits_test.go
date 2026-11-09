package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// An account may carry an ignore_weekly_gte and a pick_priority. With neither
// set, nothing about a decision changes.

func TestDecideLeavesAnAccountAtItsOwnWeeklyLimit(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.SwitchAtWeekly = 98
	c.Accounts[0].IgnoreWeeklyGTE = 65
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(10, 66, later),
		"work-b": reading(10, 10, later),
	})})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v %s, want a switch to work-b at the account's own limit", d.Kind, d.Reason)
	}
	if !strings.Contains(d.Reason, "65%") {
		t.Errorf("the reason must quote the limit that applied: %s", d.Reason)
	}
}

func TestDecideStaysUnderAnAccountsOwnWeeklyLimit(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.SwitchAtWeekly = 98
	c.Accounts[0].IgnoreWeeklyGTE = 65
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(10, 64, later),
		"work-b": reading(10, 10, later),
	})})
	if d.Kind != Stay {
		t.Fatalf("got %v %s, want stay under the limit", d.Kind, d.Reason)
	}
}

func TestAnAccountWithNoLimitGoesByTheProfilesTriggers(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.SwitchAtWeekly = 98
	c.Accounts[1].IgnoreWeeklyGTE = 65 // another account's limit
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(10, 66, later),
		"work-b": reading(10, 10, later),
	})})
	if d.Kind != Stay {
		t.Fatalf("got %v %s, want stay: work-a has no limit of its own", d.Kind, d.Reason)
	}
}

func TestDecideDoesNotLandOnAnAccountOverItsOwnLimit(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.SwitchAtWeekly = 98
	c.Accounts[1].IgnoreWeeklyGTE = 65
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, later),
		"work-b":   reading(1, 70, later), // most session room, over its own week
		"personal": reading(20, 30, later),
	})})
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v %q (%s), want personal", d.Kind, d.Target, d.Reason)
	}
}

func TestIgnoreWeeklyGTECountsExactlyAtTheFigure(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.SwitchAtWeekly = 98
	c.Accounts[1].IgnoreWeeklyGTE = 65
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, later),
		"work-b":   reading(1, 65, later),
		"personal": reading(20, 30, later),
	})})
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v %q (%s), want personal: 65 is 65 or above", d.Kind, d.Target, d.Reason)
	}
}

func TestAnAccountWithAPickPriorityIsPickedAfterEveryOneWithout(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.Accounts[2].PickPriority = 1 // personal, the one with the most room
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, later),
		"work-b":   reading(50, 50, later),
		"personal": reading(1, 1, later),
	})})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v %q (%s), want work-b ahead of the protected account", d.Kind, d.Target, d.Reason)
	}
}

func TestAPickPriorityAccountIsStillPickedWhenNothingElseCan(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.Accounts[2].PickPriority = 1
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, later),
		"work-b":   reading(90, 50, later),
		"personal": reading(1, 1, later),
	})})
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v %q (%s), want personal: nothing else can serve", d.Kind, d.Target, d.Reason)
	}
}

func TestALowerPickPriorityNumberGoesFirst(t *testing.T) {
	later := now.Add(time.Hour)
	c := cfg()
	c.Accounts[1].PickPriority = 2 // work-b, with more room
	c.Accounts[2].PickPriority = 1
	d := Decide(Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, later),
		"work-b":   reading(5, 5, later),
		"personal": reading(40, 40, later),
	})})
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v %q (%s), want personal: 1 goes before 2", d.Kind, d.Target, d.Reason)
	}
}

// "Last of the possible": a protected account in the first pool still waits
// for an unprotected one in a later pool.
func TestPickPriorityOutranksPoolOrder(t *testing.T) {
	later := now.Add(time.Hour)
	in := tiered("own-1", map[string]*state.Account{
		"own-1":  reading(91, 30, later),
		"own-2":  reading(1, 1, later),
		"team-1": reading(50, 50, later),
		"team-2": reading(60, 60, later),
	})
	in.Cfg.Accounts[1].PickPriority = 1 // own-2
	if d := Decide(in); d.Kind != Switch || d.Target != "team-1" {
		t.Fatalf("got %v %q (%s), want team-1", d.Kind, d.Target, d.Reason)
	}
}
