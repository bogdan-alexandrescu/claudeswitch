package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// IMPROVEMENTS I6: `models` makes a model's weekly limit count like the
// weekly window for triggering and eligibility. Empty is today's behaviour
// exactly.

// withModel adds a per-model weekly limit to an observed account. A nil pct is
// a limit the API reported without a figure.
func withModel(a *state.Account, name string, pct *float64) *state.Account {
	r := now.Add(2 * 24 * time.Hour)
	n := name
	a.Last.Limits = append(a.Last.Limits, usage.Limit{
		Kind: "weekly_scoped", Group: "weekly", Severity: "normal", Percent: pct, ResetsAt: &r,
		Scope: &usage.LimitScope{Model: &usage.LimitModel{DisplayName: &n}},
	})
	return a
}

func pctOf(v float64) *float64 { return &v }

func modelsCfg(models ...string) *config.Config {
	c := cfg()
	c.SwitchAtWeekly = 95
	c.HardFloor = 98
	c.Models = models
	return c
}

func TestModelLimitsAreIgnoredByDefault(t *testing.T) {
	in := Input{Cfg: modelsCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": withModel(reading(20, 50, now.Add(time.Hour)), "Modelname", pctOf(99)),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("with no models configured a model limit must change nothing, got %v", d)
	}
}

func TestACountedModelLimitTriggersARotation(t *testing.T) {
	in := Input{Cfg: modelsCfg("Modelname"), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": withModel(reading(20, 50, now.Add(time.Hour)), "Modelname", pctOf(96)),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want a switch to work-b: the counted model is past the weekly trigger", d)
	}
	if !strings.Contains(d.Reason, "Modelname") {
		t.Errorf("the reason must name the model whose limit triggered it: %q", d.Reason)
	}
}

func TestACountedModelLimitRulesOutATarget(t *testing.T) {
	in := Input{Cfg: modelsCfg("Modelname"), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(90, 30, now.Add(time.Hour)),
		"work-b":   withModel(reading(10, 10, now.Add(time.Hour)), "Modelname", pctOf(97)),
		"personal": reading(30, 30, now.Add(time.Hour)), // less room than work-b would have
	})}
	d, vs := Explain(in)
	if d.Kind != Switch || d.Target != "personal" {
		t.Fatalf("got %v, want personal: work-b's counted model is past the weekly trigger", d)
	}
	for _, v := range vs {
		if v.ID == "work-b" {
			if v.Eligible {
				t.Error("work-b is marked eligible")
			}
			if !strings.Contains(v.Why, "Modelname") {
				t.Errorf("work-b's verdict must name the model: %q", v.Why)
			}
		}
	}
}

// Unknown is not fine (DESIGN 4.4): a counted model whose limit the API
// reports without a figure makes the account unknown, never a target.
func TestAnUnreadableCountedModelLimitIsUnknown(t *testing.T) {
	in := Input{Cfg: modelsCfg("Modelname"), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(90, 30, now.Add(time.Hour)),
		"work-b":   withModel(reading(1, 1, now.Add(time.Hour)), "Modelname", nil),
		"personal": reading(30, 30, now.Add(time.Hour)), // less room than work-b would have
	})}
	d, vs := Explain(in)
	if d.Target != "personal" {
		t.Fatalf("got %v, want personal: work-b's counted limit is unreadable", d)
	}
	for _, v := range vs {
		if v.ID == "work-b" && (v.Eligible || !strings.Contains(v.Why, "Modelname")) {
			t.Errorf("work-b verdict = eligible %v, %q", v.Eligible, v.Why)
		}
	}
}

func TestAnActiveAccountWithAnUnreadableCountedModelHolds(t *testing.T) {
	in := Input{Cfg: modelsCfg("Modelname"), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": withModel(reading(20, 30, now.Add(time.Hour)), "Modelname", nil),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Stay || !strings.Contains(d.Reason, "Modelname") {
		t.Fatalf("got %v, want a hold naming the unreadable model limit", d)
	}
}

// An account with no limit for a counted model is held to its weekly window.
func TestAnAccountWithoutTheCountedModelIsJudgedOnItsWeeklyWindow(t *testing.T) {
	in := Input{Cfg: modelsCfg("Modelname"), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b", d)
	}
}
