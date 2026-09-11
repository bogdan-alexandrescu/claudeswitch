package policy

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

var now = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func cfg() *config.Config {
	return &config.Config{
		SwitchAt:  85,
		HardFloor: 96,
		Cooldown:  config.Duration{Duration: 10 * time.Minute},
		Priority:  []string{"work-a", "work-b", "personal"},
		Accounts: []config.Account{
			{ID: "work-a", Scope: "work"},
			{ID: "work-b", Scope: "work"},
			{ID: "personal", Scope: "personal", Reserve: 70},
		},
	}
}

// reading builds an observed account at a given utilization on both windows.
func reading(five, seven float64, resets time.Time) *state.Account {
	f, s := five, seven
	r := resets
	return &state.Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: &f, ResetsAt: &r},
			SevenDay: usage.Window{Utilization: &s, ResetsAt: &r},
		},
		LastAt: now,
	}
}

func st(active string, accounts map[string]*state.Account) *state.State {
	return &state.State{Active: active, Accounts: accounts}
}

func TestStaysWhenActiveHasHeadroom(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(40, 30, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay", d)
	}
}

func TestSwitchesAtTheTriggerInPriorityOrder(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(91, 30, now.Add(time.Hour)),
		"work-b":   reading(12, 20, now.Add(time.Hour)),
		"personal": reading(5, 5, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b (priority order, personal is last)", d)
	}
	if d.Forced {
		t.Error("91%% is over the trigger but under the hard floor: must not be forced")
	}
}

func TestTheWorseWindowDrivesTheDecision(t *testing.T) {
	// Five-hour is comfortable; the weekly window is what is about to bite.
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(20, 88, now.Add(time.Hour)),
		"work-b": reading(30, 30, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want a switch driven by the seven-day window", d)
	}
}

func TestHardFloorForcesTheSwap(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, LastSwitch: now.Add(-1 * time.Minute),
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(97, 30, now.Add(time.Hour)),
			"work-b": reading(10, 10, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if d.Kind != Switch || !d.Forced {
		t.Fatalf("got %v, want a forced switch past the hard floor even inside the cooldown", d)
	}
}

func TestCooldownDampsFlapping(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, LastSwitch: now.Add(-3 * time.Minute),
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(88, 30, now.Add(time.Hour)),
			"work-b": reading(10, 10, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay: 3m into a 10m cooldown", d)
	}
}

func TestRefusalBeatsTheCooldown(t *testing.T) {
	burnt := reading(50, 50, now.Add(time.Hour))
	burnt.BurntTil = now.Add(2 * time.Hour)
	burnt.BurntWin = "seven_day"
	in := Input{Cfg: cfg(), Now: now, LastSwitch: now.Add(-1 * time.Minute),
		St: st("work-a", map[string]*state.Account{
			"work-a": burnt,
			"work-b": reading(10, 10, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v: a refused account must be left immediately, cooldown or not", d)
	}
}

func TestPersonalReserveBlocksOverflowButNotItsOwnUse(t *testing.T) {
	// Personal at 74% is over its 70% reserve: not available as overflow.
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(90, 30, now.Add(time.Hour)),
		"personal": reading(74, 20, now.Add(3*time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Wait {
		t.Fatalf("got %v, want wait: personal is over its reserve", d)
	}

	// Under the reserve it is fair game.
	in.St = st("work-a", map[string]*state.Account{
		"work-a":   reading(90, 30, now.Add(time.Hour)),
		"personal": reading(68, 20, now.Add(time.Hour)),
	})
	if d := Decide(in); d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want switch to personal at 68%% (under its 70%% reserve)", d)
	}

	// And once personal IS active, the reserve does not evict it: the reserve
	// governs rotating INTO an account, not staying on one.
	in.St = st("personal", map[string]*state.Account{
		"personal": reading(74, 20, now.Add(time.Hour)),
	})
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v: reserve must not evict an already-active personal account", d)
	}
}

func TestUnknownAccountsAreNeverSwitchedTo(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(92, 30, now.Add(time.Hour)),
		"work-b": {}, // never read
	})}
	if d := Decide(in); d.Kind != Wait {
		t.Fatalf("got %v: an unread account must not be a switch target", d)
	}
}

func TestUnknownActiveHoldsRatherThanGuessing(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": {},
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Stay {
		t.Fatalf("got %v: with no reading for the active account, hold rather than rotate on nothing", d)
	}
}

func TestAllBurntReportsTheEarliestRecovery(t *testing.T) {
	a := reading(99, 99, now.Add(time.Hour))
	a.BurntTil = now.Add(3 * time.Hour)
	b := reading(99, 99, now.Add(time.Hour))
	b.BurntTil = now.Add(40 * time.Minute)
	p := reading(99, 99, now.Add(time.Hour))
	p.BurntTil = now.Add(5 * time.Hour)

	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a, "work-b": b, "personal": p,
	})}
	d := Decide(in)
	if d.Kind != Wait {
		t.Fatalf("got %v, want wait", d)
	}
	if d.RecoversAccount != "work-b" {
		t.Fatalf("earliest recovery is work-b at +40m, got %q", d.RecoversAccount)
	}
	if !d.RecoversAt.Equal(now.Add(40 * time.Minute)) {
		t.Fatalf("recovery time wrong: %v", d.RecoversAt)
	}
}

func TestBurnExpiresWithTheClock(t *testing.T) {
	a := reading(90, 30, now.Add(time.Hour))
	b := reading(10, 10, now.Add(time.Hour))
	b.BurntTil = now.Add(30 * time.Minute)
	accounts := map[string]*state.Account{"work-a": a, "work-b": b}

	if d := Decide(Input{Cfg: cfg(), Now: now, St: st("work-a", accounts)}); d.Kind != Wait {
		t.Fatalf("while work-b is burnt: got %v, want wait", d)
	}
	// Same state, later clock: the burn has lapsed.
	later := now.Add(31 * time.Minute)
	if d := Decide(Input{Cfg: cfg(), Now: later, St: st("work-a", accounts)}); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("after the reset: got %v, want switch to work-b", d)
	}
}

func TestPinSuspendsRotation(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pinned: "personal",
		St: st("personal", map[string]*state.Account{
			"personal": reading(99, 99, now.Add(time.Hour)),
			"work-a":   reading(1, 1, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v: a pinned account must not be rotated away from", d)
	}
}

func TestNoActiveAccountTakesTheFirstEligible(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("", map[string]*state.Account{
		"work-a": reading(20, 20, now.Add(time.Hour)),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-a" {
		t.Fatalf("got %v, want switch to work-a (first in priority)", d)
	}
}

func TestNoAccountsConfigured(t *testing.T) {
	in := Input{Cfg: &config.Config{SwitchAt: 85, HardFloor: 96}, Now: now, St: st("", nil)}
	if d := Decide(in); d.Kind != Wait {
		t.Fatalf("got %v, want wait", d)
	}
}

// Scope was written down and never enforced: work quota could silently fund
// personal work and vice versa. For anyone whose employer cares where their
// usage is billed, that is the difference between a usable tool and one that is
// not.
func cfgWithProjects() *config.Config {
	c := cfg()
	c.Projects = map[string]config.Project{
		"/work/**":     {Eligible: []string{"work"}},
		"/personal/**": {Eligible: []string{"personal"}},
		"/either/**":   {Prefer: []string{"personal", "work"}},
	}
	return c
}

func TestScopeRestrictsWhichAccountsMayServeADirectory(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "/work/repo",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"personal": reading(2, 2, now.Add(time.Hour)),
		})}
	// personal has plenty of room but is not allowed in a work directory.
	d := Decide(in)
	if d.Kind != Wait {
		t.Fatalf("got %v, want wait: personal must not serve a work directory", d)
	}
	if !contains(d.Reason, "personal") || !contains(d.Reason, "work") {
		t.Errorf("the reason should explain the scope block, got %q", d.Reason)
	}
}

func TestScopeAllowsAnAccountOfTheRightScope(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "/work/repo",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"work-b":   reading(5, 5, now.Add(time.Hour)),
			"personal": reading(1, 1, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b", d)
	}
}

// An unknown directory must not stop rotation. Refusing to switch because we
// cannot tell where we are is worse than the thing the rule prevents.
func TestUnknownDirectoryImposesNoRestriction(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"personal": reading(2, 2, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch {
		t.Fatalf("got %v, want a switch: an unknown directory restricts nothing", d)
	}
}

// A directory with no rule is unrestricted.
func TestDirectoryWithNoRuleIsUnrestricted(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "/somewhere/else",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"personal": reading(2, 2, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want switch to personal", d)
	}
}

// `prefer` reorders without forbidding: an account outside the preference is
// still usable, just later.
func TestPreferReordersWithoutForbidding(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "/either/repo",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"work-b":   reading(5, 5, now.Add(time.Hour)),
			"personal": reading(6, 6, now.Add(time.Hour)),
		})}
	// Global priority puts work-b first; the project prefers personal.
	if d := Decide(in); d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want switch to personal (preferred here)", d)
	}
}

func TestPreferFallsBackWhenThePreferredScopeIsExhausted(t *testing.T) {
	in := Input{Cfg: cfgWithProjects(), Now: now, Dir: "/either/repo",
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(92, 30, now.Add(time.Hour)),
			"work-b":   reading(5, 5, now.Add(time.Hour)),
			"personal": reading(99, 99, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b: preference is not a requirement", d)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
