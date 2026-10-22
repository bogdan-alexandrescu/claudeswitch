package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// IMPROVEMENTS phase 1 R, re-aimed after phase 3 C0 (owner decision
// 2026-10-22): a new Claude in Chrome connection needs a browser signed in as
// the live account itself (GROUND_TRUTH §56), so a switch that goes during a
// claude.ai hold (past the hard floor, or off a refused account) prefers an
// account with a Chrome profile of its own, short of the landing margin if
// need be. With none that has room, it goes to the best account anywhere,
// and says a new connection needs a browser signed in as it. A switch the
// hold delays, and any switch with no hold, choose as before.

func holdCfg() *config.Config {
	c := cfg()
	c.Priority = []string{"work-a", "work-b", "lab"}
	c.Accounts = []config.Account{{ID: "work-a"}, {ID: "work-b"}, {ID: "lab"}}
	return c
}

// pastFloor is work-a live past the 96% hard floor, work-b at workB with a
// Chrome profile of its own, and lab nearly empty with none.
func pastFloor(workB float64) *state.State {
	s := st("work-a", map[string]*state.Account{
		"work-a": reading(97, 30, now.Add(time.Hour)),
		"work-b": reading(workB, 20, now.Add(time.Hour)),
		"lab":    reading(5, 5, now.Add(time.Hour)),
	})
	s.SetChrome("work-b", "claudeswitch-work-b", now)
	return s
}

var aMinuteAgo = now.Add(-time.Minute)

func TestAForcedSwitchDuringAHoldPrefersAnAccountWithItsOwnBrowser(t *testing.T) {
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || !d.Forced || d.Target != "work-b" {
		t.Fatalf("got %v, want a forced switch to work-b, which has a browser of its own", d)
	}
	if !strings.Contains(d.Reason, "Chrome profile of its own") {
		t.Errorf("reason %q does not say why work-b", d.Reason)
	}
}

// Room at the hard floor is any room: short of the landing margin with a
// browser beats plenty of room without one.
func TestAForcedSwitchDuringAHoldTakesAnOwnBrowserShortOfTheMargin(t *testing.T) {
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: pastFloor(80), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b, 5 points short of the margin but with its own browser", d)
	}
}

func TestAForcedSwitchDuringAHoldGoesAnywhereWhenNoOwnBrowserHasRoom(t *testing.T) {
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: pastFloor(90), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || !d.Forced || d.Target != "lab" {
		t.Fatalf("got %v, want a forced switch to lab", d)
	}
	if !strings.Contains(d.Reason, "a new Claude in Chrome connection needs a browser signed in as lab") {
		t.Errorf("reason %q does not say what a new connection needs", d.Reason)
	}
}

// A Chrome profile shared by several accounts is nobody's own.
func TestASharedChromeProfileIsNoBrowserOfItsOwn(t *testing.T) {
	s := pastFloor(60)
	s.SetChromeExisting("work-b", "Profile 25", now)
	s.SetChromeExisting("lab", "Profile 25", now)
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: s, ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Target != "lab" || !strings.Contains(d.Reason, "browser signed in as lab") {
		t.Fatalf("got %v, want lab, the most room, and what a new connection needs", d)
	}
}

func TestASwitchOffARefusedAccountDuringAHoldPrefersAnOwnBrowser(t *testing.T) {
	s := pastFloor(60)
	s.Accounts["work-a"] = reading(50, 30, now.Add(time.Hour))
	s.Accounts["work-a"].BurntTil, s.Accounts["work-a"].BurntWin = now.Add(time.Hour), "five_hour"
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: s, ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b", d)
	}
}

// Without a hold, or once it has run out, or with it turned off, the switch
// chooses as before, and says nothing of browsers.
func TestAForcedSwitchWithoutAHoldChoosesAsBefore(t *testing.T) {
	off := holdCfg()
	off.HoldForClaudeAI = &config.Duration{}
	for name, in := range map[string]Input{
		"no call":         {Cfg: holdCfg(), Now: now, St: pastFloor(60)},
		"hold run out":    {Cfg: holdCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: now.Add(-6 * time.Minute)},
		"hold turned off": {Cfg: off, Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo},
	} {
		d := Decide(in)
		if d.Kind != Switch || d.Target != "lab" || strings.Contains(d.Reason, "Chrome") {
			t.Errorf("%s: got %v, want lab, the most room, as before", name, d)
		}
	}
}

// A switch the hold delays goes when the hold has run out, to the best
// account then: its target is not narrowed.
func TestASwitchTheHoldDelaysIsNotNarrowed(t *testing.T) {
	s := pastFloor(60)
	s.Accounts["work-a"] = reading(90, 30, now.Add(time.Hour))
	d := Decide(Input{Cfg: holdCfg(), Now: now, St: s, ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Forced || d.Held == "" || d.Target != "lab" {
		t.Fatalf("got %v held %q, want a held switch to lab", d, d.Held)
	}
}

// `cs why` lists the accounts in the order the switch weighs them: those
// with a browser of their own first while it prefers them.
func TestExplainListsOwnBrowsersFirstWhilePreferred(t *testing.T) {
	d, vs := Explain(Input{Cfg: holdCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo})
	if d.Target != "work-b" {
		t.Fatalf("target %s, want work-b", d.Target)
	}
	var order []string
	for _, v := range vs {
		order = append(order, v.ID)
	}
	if strings.Join(order, ",") != "work-a,work-b,lab" {
		t.Fatalf("order %v, want work-a, work-b, lab", order)
	}
}
