package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// IMPROVEMENTS phase 1 R (owner decision 2026-10-21): Claude in Chrome
// connects only to a Claude Code signed in to the extension's organization,
// so a switch that goes during a claude.ai hold (past the hard floor, or off
// a refused account) stays in the live account's organization. When no
// account there has room, it goes to the best account in another one and
// says Claude in Chrome may need /chrome. A switch the hold delays, and any
// switch with no hold, choose as before.

// orgCfg has work-a and work-b in one organization and lab in another.
func orgCfg() *config.Config {
	c := cfg()
	c.Priority = []string{"work-a", "work-b", "lab"}
	c.Accounts = []config.Account{
		{ID: "work-a", OrgID: "org-acme"},
		{ID: "work-b", OrgID: "org-acme"},
		{ID: "lab", OrgID: "org-lab"},
	}
	return c
}

// pastFloor is work-a live past the 96% hard floor, work-b in its
// organization at workB, and lab, in another, nearly empty.
func pastFloor(workB float64) *state.State {
	return st("work-a", map[string]*state.Account{
		"work-a": reading(97, 30, now.Add(time.Hour)),
		"work-b": reading(workB, 20, now.Add(time.Hour)),
		"lab":    reading(5, 5, now.Add(time.Hour)),
	})
}

var aMinuteAgo = now.Add(-time.Minute)

func TestAForcedSwitchDuringAHoldStaysInTheOrganization(t *testing.T) {
	d := Decide(Input{Cfg: orgCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || !d.Forced || d.Target != "work-b" {
		t.Fatalf("got %v, want a forced switch to work-b, in work-a's organization", d)
	}
	if strings.Contains(d.Reason, "/chrome") {
		t.Errorf("reason %q speaks of /chrome for a switch within the organization", d.Reason)
	}
}

// Room at the hard floor is any room: short of the landing margin in the
// organization beats plenty of room outside it.
func TestAForcedSwitchDuringAHoldTakesTheOrganizationShortOfTheMargin(t *testing.T) {
	d := Decide(Input{Cfg: orgCfg(), Now: now, St: pastFloor(80), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b, 5 points short of the margin but in the organization", d)
	}
}

func TestAForcedSwitchDuringAHoldLeavesTheOrganizationWhenNothingThereHasRoom(t *testing.T) {
	d := Decide(Input{Cfg: orgCfg(), Now: now, St: pastFloor(90), ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || !d.Forced || d.Target != "lab" {
		t.Fatalf("got %v, want a forced switch to lab", d)
	}
	if !strings.Contains(d.Reason, "Claude in Chrome may need /chrome") {
		t.Errorf("reason %q does not say Claude in Chrome may need /chrome", d.Reason)
	}
}

func TestASwitchOffARefusedAccountDuringAHoldStaysInTheOrganization(t *testing.T) {
	s := pastFloor(60)
	s.Accounts["work-a"] = reading(50, 30, now.Add(time.Hour))
	s.Accounts["work-a"].BurntTil, s.Accounts["work-a"].BurntWin = now.Add(time.Hour), "five_hour"
	d := Decide(Input{Cfg: orgCfg(), Now: now, St: s, ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b", d)
	}
}

// Without a hold, or once it has run out, or with it turned off, the switch
// chooses as before.
func TestAForcedSwitchWithoutAHoldChoosesAsBefore(t *testing.T) {
	off := orgCfg()
	off.HoldForClaudeAI = &config.Duration{}
	for name, in := range map[string]Input{
		"no call":         {Cfg: orgCfg(), Now: now, St: pastFloor(60)},
		"hold run out":    {Cfg: orgCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: now.Add(-6 * time.Minute)},
		"hold turned off": {Cfg: off, Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo},
	} {
		d := Decide(in)
		if d.Kind != Switch || d.Target != "lab" || strings.Contains(d.Reason, "/chrome") {
			t.Errorf("%s: got %v, want lab, the most room, as before", name, d)
		}
	}
}

// A switch the hold delays goes when the hold has run out, to the best
// account then: its target is not narrowed.
func TestASwitchTheHoldDelaysIsNotNarrowed(t *testing.T) {
	s := pastFloor(60)
	s.Accounts["work-a"] = reading(90, 30, now.Add(time.Hour))
	d := Decide(Input{Cfg: orgCfg(), Now: now, St: s, ClaudeAIAt: aMinuteAgo})
	if d.Kind != Switch || d.Forced || d.Held == "" || d.Target != "lab" {
		t.Fatalf("got %v held %q, want a held switch to lab", d, d.Held)
	}
}

// The live account's organization is its pinned one, or else the one its
// last reading recorded. Unknown, nothing is narrowed and nothing is said.
func TestTheLiveOrganizationComesFromThePinOrTheReading(t *testing.T) {
	c := orgCfg()
	c.Accounts[0].OrgID = ""
	s := pastFloor(60)
	s.Accounts["work-a"].OrgID = "org-acme"
	if d := Decide(Input{Cfg: c, Now: now, St: s, ClaudeAIAt: aMinuteAgo}); d.Target != "work-b" {
		t.Errorf("organization from the reading: got %v, want work-b", d)
	}
	s.Accounts["work-a"].OrgID = ""
	if d := Decide(Input{Cfg: c, Now: now, St: s, ClaudeAIAt: aMinuteAgo}); d.Target != "lab" ||
		strings.Contains(d.Reason, "/chrome") {
		t.Errorf("organization unknown: got %v, want lab and nothing said", d)
	}
}

// `cs why` lists the accounts in the order the switch weighs them: the
// organization's first while it is narrowed.
func TestExplainListsTheOrganizationFirstWhileNarrowed(t *testing.T) {
	d, vs := Explain(Input{Cfg: orgCfg(), Now: now, St: pastFloor(60), ClaudeAIAt: aMinuteAgo})
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
