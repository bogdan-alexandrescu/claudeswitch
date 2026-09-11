package main

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// The one thing this program exists to do: cross the trigger, move to an account
// with room. Every failure to rotate on 2026-09-10 had a different cause, so the
// decision is pinned here directly rather than only through its parts.

func testCfg() *config.Config {
	return &config.Config{
		SwitchAt: 85, HardFloor: 96,
		Cooldown:      config.Duration{Duration: 10 * time.Minute},
		MaxSwitchWait: config.Duration{Duration: 90 * time.Second},
		RefreshWindow: config.Duration{Duration: time.Hour},
		PollActive:    config.Duration{Duration: time.Minute},
		PollHot:       config.Duration{Duration: 30 * time.Second},
		PollIdle:      config.Duration{Duration: 10 * time.Minute},
		APIBudget:     12,
		Priority:      []string{"a", "b"},
		Accounts: []config.Account{
			{ID: "a", Scope: "work"},
			{ID: "b", Scope: "work"},
		},
	}
}

func at(five, seven float64, when time.Time) *state.Account {
	f, s := five, seven
	r := when.Add(time.Hour)
	return &state.Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: &f, ResetsAt: &r},
			SevenDay: usage.Window{Utilization: &s, ResetsAt: &r},
		},
		LastAt: when,
	}
}

func TestRotatesOnceTheTriggerIsCrossed(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name       string
		active     float64
		wantSwitch bool
	}{
		{"well under", 40, false},
		{"approaching", 84, false},
		{"exactly at the trigger", 85, true},
		{"over", 91, true},
		{"past the hard floor", 97, true},
	} {
		in := policy.Input{
			Cfg: testCfg(), Now: now,
			St: &state.State{Active: "a", Accounts: map[string]*state.Account{
				"a": at(tc.active, 10, now),
				"b": at(5, 5, now),
			}},
		}
		d := policy.Decide(in)
		got := d.Kind == policy.Switch
		if got != tc.wantSwitch {
			t.Errorf("%s (%.0f%%): got %v, want switch=%v", tc.name, tc.active, d, tc.wantSwitch)
		}
		if got && d.Target != "b" {
			t.Errorf("%s: switched to %q, want b", tc.name, d.Target)
		}
	}
}

// Past the hard floor the swap must not wait for an idle gap.
func TestHardFloorForcesImmediately(t *testing.T) {
	now := time.Now()
	in := policy.Input{
		Cfg: testCfg(), Now: now, LastSwitch: now.Add(-time.Second),
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": at(97, 10, now), "b": at(5, 5, now),
		}},
	}
	d := policy.Decide(in)
	if d.Kind != policy.Switch || !d.Forced {
		t.Fatalf("got %v, want a forced switch", d)
	}
}

// The weekly window triggers a rotation exactly as the five-hour one does. It is
// the window that has actually been blocking work, and an earlier version
// watched only the five-hour.
func TestWeeklyWindowAlsoTriggers(t *testing.T) {
	now := time.Now()
	in := policy.Input{
		Cfg: testCfg(), Now: now,
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": at(10, 88, now), "b": at(5, 5, now),
		}},
	}
	if d := policy.Decide(in); d.Kind != policy.Switch || d.Target != "b" {
		t.Fatalf("got %v, want switch to b on the weekly window", d)
	}
}

// A stale reading must not hide a crossing: the projection carries it over.
func TestStaleReadingStillTriggersViaProjection(t *testing.T) {
	now := time.Now()
	a := at(80, 10, now.Add(-4*time.Minute))
	a.PrevWorst, a.PrevAt = 68, now.Add(-8*time.Minute) // 3 points/min
	in := policy.Input{
		Cfg: testCfg(), Now: now,
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": a, "b": at(5, 5, now),
		}},
	}
	// 80% four minutes ago at 3%/min is about 92% now.
	if d := policy.Decide(in); d.Kind != policy.Switch {
		t.Fatalf("got %v: a stale reading must not mask a crossing", d)
	}
}

// And the honest opposite: over the trigger with nowhere to go is a wait that
// names the recovery, not a silent stay.
func TestNowhereToGoReportsRecovery(t *testing.T) {
	now := time.Now()
	in := policy.Input{
		Cfg: testCfg(), Now: now,
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": at(99, 10, now), "b": at(100, 10, now),
		}},
	}
	d := policy.Decide(in)
	if d.Kind != policy.Wait {
		t.Fatalf("got %v, want wait", d)
	}
	if d.RecoversAccount == "" || d.RecoversAt.IsZero() {
		t.Fatal("a wait must say which account recovers and when")
	}
}

// Deciding on where an account is *now* is always late: the reading is a lower
// bound, the next poll is an interval away, and the swap may wait for an idle
// gap. Observed on 2026-09-10 crossing at 88% and switching at 100%.
//
// With a lookahead, an account burning fast enough to cross before the next
// decision rotates while it still has room.
func TestRotatesBeforeCrossingWhenBurningFast(t *testing.T) {
	now := time.Now()
	cfg := testCfg()
	// Stated outright rather than derived, so the arithmetic below is checkable.
	const look = time.Minute

	// 80%, rising 3 points a minute: 83% one minute out. Still short of 85.
	a := at(80, 10, now)
	a.PrevWorst, a.PrevAt = 74, now.Add(-2*time.Minute)
	in := policy.Input{
		Cfg: cfg, Now: now, Lookahead: look,
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": a, "b": at(5, 5, now),
		}},
	}
	if d := policy.Decide(in); d.Kind == policy.Switch {
		t.Fatalf("83%% a minute out does not reach the 85%% trigger: %v", d)
	}

	// 80%, rising 6 points a minute: 86% one minute out. Over the line, so the
	// decision is taken now, while the account still has room.
	b := at(80, 10, now)
	b.PrevWorst, b.PrevAt = 68, now.Add(-2*time.Minute)
	in.St.Accounts["a"] = b
	d := policy.Decide(in)
	if d.Kind != policy.Switch {
		t.Fatalf("86%% a minute out crosses the trigger and must rotate now: %v", d)
	}
	if d.Target != "b" {
		t.Fatalf("rotated to %q, want b", d.Target)
	}
}

// Without a lookahead the same account waits until it has already crossed —
// which is what produced a switch at 100%% after detecting 88%%.
func TestWithoutLookaheadTheDecisionComesLate(t *testing.T) {
	now := time.Now()
	a := at(80, 10, now)
	a.PrevWorst, a.PrevAt = 68, now.Add(-2*time.Minute) // 6 points/min
	in := policy.Input{
		Cfg: testCfg(), Now: now, // no Lookahead
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": a, "b": at(5, 5, now),
		}},
	}
	if d := policy.Decide(in); d.Kind == policy.Switch {
		t.Fatalf("without a horizon this reads 80%% and stays: %v", d)
	}
}

// A quiet account must not be dragged over the line by a stale burn rate.
func TestLookaheadDoesNotInventUsageOnAnIdleAccount(t *testing.T) {
	now := time.Now()
	cfg := testCfg()
	// The target was burning fast while it was active, but is idle now.
	b := at(70, 10, now)
	b.PrevWorst, b.PrevAt = 40, now.Add(-5*time.Minute) // 6 points/min, historic
	in := policy.Input{
		Cfg: cfg, Now: now, Lookahead: 5 * time.Minute,
		St: &state.State{Active: "a", Accounts: map[string]*state.Account{
			"a": at(90, 10, now), "b": b,
		}},
	}
	if d := policy.Decide(in); d.Kind != policy.Switch || d.Target != "b" {
		t.Fatalf("an idle account is not being spent and must stay eligible: %v", d)
	}
}
