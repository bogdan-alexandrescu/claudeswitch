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

// D34: an account two profiles share is used by one at a time. Every other
// profile that may use it shows it "in use by" the one it is live in, never
// as ready.
func sharedWorld() (*config.Config, *state.State) {
	resets := time.Now().Add(2 * time.Hour)
	reading := func() *state.Account {
		return &state.Account{LastAt: time.Now(), Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &resets},
			SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &resets},
		}}
	}
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 95, HardFloor: 96,
		PollActive: config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "own-1"}, {ID: "team-1"}},
		Priority:   []string{"own-1", "team-1"},
		Pools: []config.Pool{{Name: "own", Accounts: []string{"own-1"}},
			{Name: "team", Accounts: []string{"team-1"}}},
		Profiles: []config.Profile{{Name: "default", Pools: []string{"own", "team"}},
			{Name: "work", Dir: "~/.claude-work", Pools: []string{"team"}}},
	}
	st := &state.State{
		Profiles: map[string]*state.ProfileState{"default": {Active: "own-1"}, "work": {Active: "team-1"}},
		Accounts: map[string]*state.Account{"own-1": reading(), "team-1": reading()},
	}
	return cfg, st
}

func rowOf(out, id string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}
	return ""
}

func TestStatusShowsAnAccountLiveInAnotherProfileAsInUse(t *testing.T) {
	cfg, st := sharedWorld()
	for _, detail := range []bool{false, true} {
		var buf bytes.Buffer
		Status(&buf, Options{Cfg: cfg, St: st, Profile: "default", Pool: []string{"own-1", "team-1"},
			Detail: detail, Block: true})
		row := rowOf(buf.String(), "team-1")
		if !strings.Contains(row, "in use by work") || strings.Contains(row, "available") {
			t.Errorf("detail %v: team-1's row in default %q, want in use by work:\n%s", detail, row, buf.String())
		}
		if own := rowOf(buf.String(), "own-1"); strings.Contains(own, "in use by") {
			t.Errorf("detail %v: default's own live account %q", detail, own)
		}
	}
	// In the profile it is live in, it is the active account as ever.
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, Profile: "work", Pool: []string{"team-1"}, Block: true})
	if row := rowOf(buf.String(), "team-1"); strings.Contains(row, "in use by") {
		t.Errorf("team-1 in work: %q", row)
	}
}

func TestWhyShowsAnAccountLiveInAnotherProfileAsInUse(t *testing.T) {
	cfg, st := sharedWorld()
	var buf bytes.Buffer
	Why(&buf, WhyOptions{Cfg: cfg, St: st, Profile: "default",
		Decision: policy.Decision{Kind: policy.Stay, Reason: "active account at 10%"},
		Verdicts: []policy.Verdict{{ID: "own-1", Active: true, Eligible: true, Why: "active, 10%"},
			{ID: "team-1", Eligible: true, Why: "available, 10%"}}})
	row := rowOf(buf.String(), "team-1")
	if !strings.Contains(row, "in use by work") || !strings.Contains(row, "✗") || strings.Contains(row, "✓") {
		t.Errorf("team-1 in default's why: %q\n%s", row, buf.String())
	}
}
