package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// IMPROVEMENTS I6 (per-model weekly limits) and I8 (weekly pace) in the
// human views.

func modelLimit(name string, pct *float64, resets time.Time) usage.Limit {
	n := name
	return usage.Limit{Kind: "weekly_scoped", Group: "weekly", Severity: "normal", Percent: pct, ResetsAt: &resets,
		Scope: &usage.LimitScope{Model: &usage.LimitModel{DisplayName: &n}}}
}

// modelsWorld: work-a active, four days into its week at 20%, with one model
// limit at 12%, one reported without a figure, and one limit of a kind this
// code does not know.
func modelsWorld(models ...string) (*config.Config, *state.State) {
	SetColor(false)
	week := time.Now().Add(3*24*time.Hour + time.Minute)
	five := time.Now().Add(2 * time.Hour)
	odd := 7.0
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 95, HardFloor: 98,
		PollActive: config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "work-a"}, {ID: "work-b"}},
		Priority:   []string{"work-a", "work-b"},
		Models:     models,
	}
	st := &state.State{Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "work-a"}},
		Accounts: map[string]*state.Account{
			"work-a": {
				Last: &usage.Usage{
					FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &five},
					SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &week},
					Limits: []usage.Limit{
						{Kind: "session", Group: "session", Percent: ptr(10), Severity: "normal", ResetsAt: &five},
						{Kind: "weekly_all", Group: "weekly", Percent: ptr(20), Severity: "normal", ResetsAt: &week},
						modelLimit("Modelname", ptr(12), week),
						modelLimit("Othermodel", nil, week),
						{Kind: "fortnightly_new", Group: "fortnightly", Percent: &odd, Severity: "normal"},
					},
				},
				LastAt: time.Now(),
			},
			"work-b": {
				Last: &usage.Usage{
					FiveHour: usage.Window{Utilization: ptr(5), ResetsAt: &five},
					SevenDay: usage.Window{Utilization: ptr(5), ResetsAt: &week},
				},
				LastAt: time.Now(),
			},
		}}
	return cfg, st
}

func renderStatus(cfg *config.Config, st *state.State, detail bool) string {
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: detail})
	return b.String()
}

func TestDetailShowsPerModelWeeklyAndUnknownLimits(t *testing.T) {
	cfg, st := modelsWorld()
	out := renderStatus(cfg, st, true)
	for _, want := range []string{"Modelname weekly 12%", "Othermodel weekly unknown", "fortnightly_new", "not understood"} {
		if !strings.Contains(out, want) {
			t.Errorf("status --detail lacks %q:\n%s", want, out)
		}
	}
}

func TestDetailShowsWeeklyPace(t *testing.T) {
	cfg, st := modelsWorld()
	out := renderStatus(cfg, st, true)
	if !strings.Contains(out, "pace") || !strings.Contains(out, "57% of the week gone") {
		t.Errorf("status --detail lacks the weekly pace:\n%s", out)
	}
	if !strings.Contains(out, "would expire unused") {
		t.Errorf("status --detail lacks the expiry estimate:\n%s", out)
	}
}

func TestCompactStatusShowsPerModelWeeklyButNeverPace(t *testing.T) {
	cfg, st := modelsWorld()
	out := renderStatus(cfg, st, false)
	if !strings.Contains(out, "Modelname 12%") {
		t.Errorf("status lacks the per-model weekly figure:\n%s", out)
	}
	if !strings.Contains(out, "Othermodel unknown") {
		t.Errorf("a model limit without a figure must read unknown:\n%s", out)
	}
	if strings.Contains(out, "pace") || strings.Contains(out, "expire unused") {
		t.Errorf("the compact status must not carry the pace view:\n%s", out)
	}
	if !strings.Contains(out, "fortnightly_new") {
		t.Errorf("a limit of an unknown kind must be named in the warnings:\n%s", out)
	}
}

func TestCompactStatusWithoutModelLimitsIsUnchanged(t *testing.T) {
	cfg, st := modelsWorld()
	st.Accounts["work-a"].Last.Limits = nil
	out := renderStatus(cfg, st, false)
	if strings.Contains(out, "BY MODEL") {
		t.Errorf("no model limits, no model block:\n%s", out)
	}
}

func TestDetailMarksACountedModel(t *testing.T) {
	cfg, st := modelsWorld("Modelname")
	out := renderStatus(cfg, st, true)
	if !strings.Contains(out, "counted") {
		t.Errorf("a model the config counts must say so:\n%s", out)
	}
}

func TestWhyShowsPaceAndModels(t *testing.T) {
	cfg, st := modelsWorld()
	dec, vs := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: vs})
	out := b.String()
	for _, want := range []string{"WEEKLY", "57% of the week gone", "would expire unused", "Modelname 12%"} {
		if !strings.Contains(out, want) {
			t.Errorf("why lacks %q:\n%s", want, out)
		}
	}
}
