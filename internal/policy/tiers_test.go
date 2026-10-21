package policy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// D33 (docs/PROFILES.md §10): a profile uses its pools in the order it
// lists them. An account in a later pool is chosen only when no account in
// an earlier one is usable; within a pool today's choice stands (room, then
// priority, or prefer). Only the choice of the next account changes, never
// when a profile rotates.

// tierCfg lists the team accounts first in priority, so a test that picks
// an own account shows the pool order, not the priority, deciding.
func tierCfg() *config.Config {
	return &config.Config{
		SwitchAt: 85, HardFloor: 96,
		Cooldown: config.Duration{Duration: 10 * time.Minute},
		Priority: []string{"team-1", "team-2", "own-1", "own-2"},
		Accounts: []config.Account{{ID: "own-1"}, {ID: "own-2"}, {ID: "team-1"}, {ID: "team-2"}},
	}
}

// ownThenTeam is a profile listing pools = ["own", "team"].
var ownThenTeam = [][]string{{"own-1", "own-2"}, {"team-1", "team-2"}}

func tiered(active string, accts map[string]*state.Account) Input {
	return Input{Cfg: tierCfg(), Now: now, St: st(active, accts),
		Pool: []string{"own-1", "own-2", "team-1", "team-2"}, Tiers: ownThenTeam}
}

func TestDecideTakesTheFirstPoolsAccountOverARoomierLaterOne(t *testing.T) {
	in := tiered("own-1", map[string]*state.Account{
		"own-1":  reading(91, 30, now.Add(time.Hour)),
		"own-2":  reading(50, 30, now.Add(time.Hour)),
		"team-1": reading(5, 5, now.Add(time.Hour)),
		"team-2": reading(5, 5, now.Add(time.Hour)),
	})
	if d := Decide(in); d.Kind != Switch || d.Target != "own-2" {
		t.Fatalf("got %v, want own-2: the own pool comes first", d)
	}
}

// With nothing usable in the first pool the next one is used, and inside
// it room decides as it always has, ahead of the priority order.
func TestDecideMovesToTheNextPoolOnlyWhenTheFirstHasNothingUsable(t *testing.T) {
	for _, c := range []struct {
		name  string
		own2  *state.Account
		label string
	}{
		{"over its trigger", reading(95, 30, now.Add(time.Hour)), ""},
		{"refused", func() *state.Account {
			a := reading(10, 10, now.Add(time.Hour))
			a.BurntTil, a.BurntWin = now.Add(time.Hour), "five_hour"
			return a
		}(), ""},
		{"never read", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			accts := map[string]*state.Account{
				"own-1":  reading(91, 30, now.Add(time.Hour)),
				"team-1": reading(30, 30, now.Add(time.Hour)),
				"team-2": reading(10, 10, now.Add(time.Hour)),
			}
			if c.own2 != nil {
				accts["own-2"] = c.own2
			}
			if d := Decide(tiered("own-1", accts)); d.Kind != Switch || d.Target != "team-2" {
				t.Fatalf("got %v, want team-2: own has nothing usable, and team-2 has the most room in team", d)
			}
		})
	}
}

// The landing margin is part of choosing: an own account short of it is
// not where rotation lands while a team account clears it. Holding until
// the hard floor happens exactly when it would with one pool: when no
// account anywhere clears the margin.
func TestDecidePassesOverAFirstPoolAccountShortOfTheMargin(t *testing.T) {
	in := tiered("own-1", map[string]*state.Account{
		"own-1":  reading(91, 30, now.Add(time.Hour)),
		"own-2":  reading(80, 30, now.Add(time.Hour)), // 5 points of room, margin 10
		"team-1": reading(40, 30, now.Add(time.Hour)),
	})
	if d := Decide(in); d.Kind != Switch || d.Target != "team-1" {
		t.Fatalf("got %v, want team-1, the first account that clears the margin", d)
	}
}

func TestDecideHoldsShortOfTheMarginOnlyWhenNoPoolClearsIt(t *testing.T) {
	accts := map[string]*state.Account{
		"own-1":  reading(91, 30, now.Add(time.Hour)),
		"own-2":  reading(80, 30, now.Add(time.Hour)), // 5 points
		"team-1": reading(78, 30, now.Add(time.Hour)), // 7 points
		"team-2": reading(99, 30, now.Add(time.Hour)),
	}
	if d := Decide(tiered("own-1", accts)); d.Kind != Stay || !strings.Contains(d.Reason, "hard floor") {
		t.Fatalf("got %v, want a hold until the hard floor, as with one pool", d)
	}
	// Past the hard floor the best short account is taken: the first pool's,
	// though the team account has more room.
	accts["own-1"] = reading(97, 30, now.Add(time.Hour))
	d := Decide(tiered("own-1", accts))
	if d.Kind != Switch || d.Target != "own-2" || !d.Forced {
		t.Fatalf("got %v, want a forced switch to own-2", d)
	}
}

func TestDecideWithNoActiveAccountTakesTheFirstPool(t *testing.T) {
	in := tiered("", map[string]*state.Account{
		"own-2":  reading(50, 30, now.Add(time.Hour)),
		"team-1": reading(5, 5, now.Add(time.Hour)),
	})
	if d := Decide(in); d.Kind != Switch || d.Target != "own-2" {
		t.Fatalf("got %v, want own-2", d)
	}
}

func TestBlindFailoverTakesTheFirstPoolsHealthyAccount(t *testing.T) {
	in := tiered("own-1", map[string]*state.Account{
		"own-1":  blindActive(3, 40),
		"own-2":  reading(50, 30, now.Add(time.Hour)),
		"team-1": reading(5, 5, now.Add(time.Hour)),
	})
	in.Cfg.PollActive = config.Duration{Duration: 2 * time.Minute}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "own-2" || !d.Failover {
		t.Fatalf("got %v, want a failover to own-2", d)
	}
	// A first-pool account that is not readable itself is no failover
	// target; the next pool's healthy one is.
	in.St.Accounts["own-2"].ReadFails = 1
	if d := Decide(in); d.Kind != Switch || d.Target != "team-1" {
		t.Fatalf("got %v, want a failover to team-1", d)
	}
}

// A profile with one pool decides exactly as before tiers existed.
func TestOneTierDecidesAsWithoutTiers(t *testing.T) {
	worlds := []map[string]*state.Account{
		{"own-1": reading(91, 30, now.Add(time.Hour)), "own-2": reading(50, 30, now.Add(time.Hour)),
			"team-1": reading(5, 5, now.Add(time.Hour))},
		{"own-1": reading(91, 30, now.Add(time.Hour)), "own-2": reading(80, 30, now.Add(time.Hour)),
			"team-1": reading(78, 30, now.Add(time.Hour))},
		{"own-1": reading(97, 30, now.Add(time.Hour)), "own-2": reading(80, 30, now.Add(time.Hour)),
			"team-1": reading(78, 30, now.Add(time.Hour))},
		{"own-1": blindActive(3, 40), "own-2": reading(50, 30, now.Add(time.Hour))},
	}
	all := []string{"own-1", "own-2", "team-1", "team-2"}
	for i, w := range worlds {
		with := tiered("own-1", w)
		with.Tiers = [][]string{all}
		with.Cfg.PollActive = config.Duration{Duration: 2 * time.Minute}
		without := with
		without.Tiers = nil
		if a, b := Decide(with), Decide(without); !reflect.DeepEqual(a, b) {
			t.Errorf("world %d: one tier %v, no tiers %v", i, a, b)
		}
		_, va := Explain(with)
		_, vb := Explain(without)
		if !reflect.DeepEqual(va, vb) {
			t.Errorf("world %d: verdicts differ:\n%+v\n%+v", i, va, vb)
		}
	}
}

// `why` lists accounts in the order the chooser weighs them: the active
// one, then each pool in turn, by room within it. The first ready account
// is the one rotation takes.
func TestExplainListsAccountsPoolByPool(t *testing.T) {
	in := tiered("own-1", map[string]*state.Account{
		"own-1":  reading(91, 30, now.Add(time.Hour)),
		"own-2":  reading(50, 30, now.Add(time.Hour)),
		"team-1": reading(20, 20, now.Add(time.Hour)),
		"team-2": reading(5, 5, now.Add(time.Hour)),
	})
	dec, vs := Explain(in)
	var order []string
	for _, v := range vs {
		order = append(order, v.ID)
	}
	if want := []string{"own-1", "own-2", "team-2", "team-1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	if dec.Target != "own-2" || !vs[1].Eligible {
		t.Fatalf("decision %v, verdicts %+v: the first ready account must be the target", dec, vs)
	}
}

// "Switch to best" is rotation's own pick, so it follows the pools too; a
// profile on its first pool's account is already on the best while that
// account has room, however roomy a later pool is.
func TestChooseFollowsThePoolOrder(t *testing.T) {
	in := tiered("team-1", map[string]*state.Account{
		"own-1":  reading(40, 30, now.Add(time.Hour)),
		"own-2":  reading(60, 30, now.Add(time.Hour)),
		"team-1": reading(10, 10, now.Add(time.Hour)),
		"team-2": reading(5, 5, now.Add(time.Hour)),
	})
	c := Choose(in, "default")
	if c.ID != "own-1" || c.OnBest {
		t.Fatalf("Choose = %+v, want own-1 and not on the best (team-1 is a later pool's)", c)
	}
	in = tiered("own-1", map[string]*state.Account{
		"own-1":  reading(40, 30, now.Add(time.Hour)),
		"own-2":  reading(95, 30, now.Add(time.Hour)),
		"team-1": reading(5, 5, now.Add(time.Hour)),
	})
	if c := Choose(in, "default"); c.ID != "team-1" || !c.OnBest {
		t.Fatalf("Choose = %+v, want team-1 offered but own-1 already the best", c)
	}
}

// The runway spends the pools as rotation would: the first pool's eligible
// accounts, then the next pool's.
func TestRunwaySpendsThePoolsInOrder(t *testing.T) {
	in := runwayWorld(burning(75, 10, 2, 2*time.Minute), map[string]*state.Account{
		"work-team": weekly(65, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)), // 20 points
		"research":  weekly(55, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)), // 30 points
	})
	in.Tiers = [][]string{{"work-1", "work-team"}, {"research"}}
	r := Forecast(in)
	want := map[string]time.Duration{"work-1": 10 * time.Minute, "work-team": 30 * time.Minute, "research": 60 * time.Minute}
	for id, d := range want {
		if at, ok := r.TriggerAt[id]; !ok || !at.Equal(now.Add(d)) {
			t.Fatalf("trigger_at %s = %v (%v), want +%s", id, at, ok, d)
		}
	}
	if r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %+v, want dry in 1h", r)
	}
}
