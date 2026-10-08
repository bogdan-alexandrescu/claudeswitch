package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// A1 (docs/IMPROVEMENTS.md): a switch must not land on an account that is
// itself about to be rotated away from. The landing margin is room below the
// target's OWN trigger, judged on the same figures eligibility uses.

func marginCfg(margin float64) *config.Config {
	c := cfg()
	c.LandingMargin = &margin
	return c
}

func TestTheDefaultLandingMarginIsTen(t *testing.T) {
	if got := cfg().Margin(); got != 10 {
		t.Fatalf("Margin() on a config that never set it = %v, want 10", got)
	}
	zero := 0.0
	c := cfg()
	c.LandingMargin = &zero
	if got := c.Margin(); got != 0 {
		t.Fatalf("an explicit landing_margin = 0 read back as %v; zero must mean off, not default", got)
	}
}

// Over the trigger but not forced, and the only account with room is five
// points short of its own trigger: rotating would land on an account that is
// rotated away from almost at once. Hold, and say why — this is not "every
// account is out", so it must not be a Wait.
func TestMarginKeepsANormalRotationOffANearlyFullAccount(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(80, 20, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Stay {
		t.Fatalf("got %v, want stay: work-b has 5 points of room against a 10-point landing margin", d)
	}
	if !strings.Contains(d.Reason, "landing margin") || !strings.Contains(d.Reason, "work-b") {
		t.Errorf("reason %q does not name the candidate the margin excluded", d.Reason)
	}
}

// Owner decision 2026-10-07: the margin applies to the 5-hour window only.
// A weekly window close to its trigger is still a valid landing place as long
// as it is under it — the weekly trigger is near 100 on purpose, and a margin
// there would rule out accounts with days of work left.
func TestTheMarginDoesNotApplyToTheWeeklyWindow(t *testing.T) {
	c := cfg()
	c.SwitchAtWeekly = 98
	c.HardFloor = 99
	in := Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(30, 98.4, now.Add(time.Hour)),
		"work-b": reading(10, 92, now.Add(time.Hour)), // 6 points under its weekly trigger
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b now: the margin is a session-window rule", d)
	}
	_, verdicts := Explain(in)
	for _, v := range verdicts {
		if v.ID == "work-b" && (!v.Eligible || strings.Contains(v.Why, "margin")) {
			t.Errorf("work-b verdict %q (eligible %v): the weekly window has no margin", v.Why, v.Eligible)
		}
	}
}

// The session window is held to the margin even when the weekly window is the
// one closest to its own trigger.
func TestTheSessionMarginAppliesWhicheverWindowBinds(t *testing.T) {
	c := cfg()
	c.SwitchAtWeekly = 98
	c.HardFloor = 99
	in := Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(80, 96, now.Add(time.Hour)), // session 5 under, weekly 2 under
	})}
	d := Decide(in)
	if d.Kind != Stay || !strings.Contains(d.Reason, "landing margin") {
		t.Fatalf("got %v, want stay: work-b's session window is inside the margin", d)
	}
	if !strings.Contains(d.Reason, "session") {
		t.Errorf("reason %q should name the session window", d.Reason)
	}
}

func TestMarginExactlyMetIsEnough(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(75, 20, now.Add(time.Hour)), // exactly 10 below 85
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b (exactly the margin)", d)
	}
}

func TestAZeroMarginRestoresTheOldRule(t *testing.T) {
	in := Input{Cfg: marginCfg(0), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(84, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b with the margin off", d)
	}
}

// Past the hard floor, staying is worse than landing anywhere below the
// trigger: the active account is about to refuse work. The margin yields.
func TestHardFloorStillLandsOnAnAccountShortOfTheMargin(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(97, 30, now.Add(time.Hour)),
		"work-b": reading(80, 20, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" || !d.Forced {
		t.Fatalf("got %v, want a forced switch to work-b", d)
	}
	if !strings.Contains(d.Reason, "landing margin") {
		t.Errorf("reason %q should say the target is short of the landing margin", d.Reason)
	}
}

// Within the margin, an account with more room still beats one with less.
func TestHardFloorFallbackStillPicksTheMostRoom(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(97, 30, now.Add(time.Hour)),
		"work-b":   reading(83, 20, now.Add(time.Hour)),
		"personal": reading(78, 20, now.Add(time.Hour)), // reserve lifted below
	})}
	in.Cfg.Accounts[2].Reserve = 0
	d := Decide(in)
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want personal (7 points of room beats work-b's 2)", d)
	}
}

// A refused account must be left whatever the margin says (DESIGN 4.6).
func TestARefusalStillLandsOnAnAccountShortOfTheMargin(t *testing.T) {
	a := reading(40, 30, now.Add(time.Hour))
	a.BurntTil, a.BurntWin = now.Add(time.Hour), "five_hour"
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(80, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b: the active account was refused", d)
	}
}

// With nothing below the trigger at all, it is still Wait with a recovery
// time — the margin does not change what "nowhere to go" means.
func TestNothingBelowTheTriggerIsStillAWait(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(90, 20, now.Add(2*time.Hour)),
	})}
	if d := Decide(in); d.Kind != Wait {
		t.Fatalf("got %v, want wait", d)
	}
}

func TestWhySaysWhenTheMarginExcludedACandidate(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(91, 30, now.Add(time.Hour)),
		"work-b": reading(80, 20, now.Add(time.Hour)),
	})}
	_, verdicts := Explain(in)
	for _, v := range verdicts {
		if v.ID != "work-b" {
			continue
		}
		if v.Eligible {
			t.Errorf("work-b reported eligible; the margin excludes it")
		}
		if !strings.Contains(v.Why, "landing margin") {
			t.Errorf("work-b verdict %q does not mention the landing margin", v.Why)
		}
		return
	}
	t.Fatal("no verdict for work-b")
}

// Per profile, like switch_at: ForProfile carries the override, and an
// profile that sets none inherits the global value.
func TestLandingMarginOverridePerProfile(t *testing.T) {
	five, zero := 5.0, 0.0
	c := cfg()
	c.LandingMargin = &five
	c.Profiles = []config.Profile{
		{Name: "default", Pool: []string{"work-a", "work-b", "personal"}},
		{Name: "loose", Dir: "~/.claude-loose", LandingMargin: &zero},
	}
	if got := c.ForProfile("default").Margin(); got != 5 {
		t.Errorf("default inherits %v, want the global 5", got)
	}
	if got := c.ForProfile("loose").Margin(); got != 0 {
		t.Errorf("loose = %v, want its own 0", got)
	}
	if got := c.Margin(); got != 5 {
		t.Errorf("ForProfile changed the global margin to %v", got)
	}
}
