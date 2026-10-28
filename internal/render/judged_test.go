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

// The compact table judges each account as the daemon does: by the window
// nearest its own trigger (WorstAgainst), on the figure it shows, and with
// the landing margin a target must clear.

// judgedWorld is two accounts under triggers 85 (session) and 98 (week):
// "live" active and burning, "idle" read an hour ago.
func judgedWorld() (*config.Config, *state.State) {
	SetColor(false)
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 99,
		PollActive: config.Duration{Duration: 2 * time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "live"}, {ID: "idle"}},
		Priority:   []string{"live", "idle"},
	}
	five, week := time.Now().Add(3*time.Hour+10*time.Minute+30*time.Second), time.Now().Add(4*24*time.Hour)
	st := &state.State{
		Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "live"}},
		Accounts: map[string]*state.Account{
			"live": {Last: &usage.Usage{
				FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &five},
				SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &week},
			}, LastAt: time.Now()},
			"idle": {Last: &usage.Usage{
				FiveHour: usage.Window{Utilization: ptr(90), ResetsAt: &five},
				SevenDay: usage.Window{Utilization: ptr(95), ResetsAt: &week},
			}, LastAt: time.Now().Add(-time.Hour)},
		},
	}
	return cfg, st
}

func judgedRow(t *testing.T, cfg *config.Config, st *state.State, id string, dec *policy.Decision) (string, string) {
	t.Helper()
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Decision: dec})
	out := b.String()
	for _, line := range strings.Split(out, "\n") {
		// A table row: the name after the marker column, never the headline.
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "▸"))
		if len(f) > 1 && f[0] == id && strings.Contains(line, "%") && f[1] != "at" {
			return line, out
		}
	}
	t.Fatalf("no row for %q:\n%s", id, out)
	return "", out
}

// At 90% of the session and 95% of the week, against 85 and 98, the session
// is over its trigger; the week's 95% is the higher figure but has room. The
// row read "available" because it judged the higher figure.
func TestStateJudgesTheWindowNearestItsTrigger(t *testing.T) {
	cfg, st := judgedWorld()
	row, out := judgedRow(t, cfg, st, "idle", nil)
	if !strings.Contains(row, "no headroom") || strings.Contains(row, "available") {
		t.Errorf("90%% of the session against 85 is no headroom:\n%s", out)
	}
	// CLEARS is the judged window's reset: the session's, in 3h 10m, not the
	// week's four days out.
	if !strings.Contains(row, "3h 10m") || strings.Contains(row, "4d") {
		t.Errorf("CLEARS must be the session's reset:\n%s", out)
	}
}

// The figure carried forward is the one shown, marked "~", and the one the
// verdict and the headline use. The row showed the reading with a "~" while
// the verdict and headline used the projection, so 84.4% read "84%" beside
// "no headroom" at a trigger of 85.
func TestTheProjectedFigureIsTheOneShownAndJudged(t *testing.T) {
	cfg, st := judgedWorld()
	live := st.Accounts["live"]
	live.Last.FiveHour.Utilization = ptr(84.4)
	read := time.Now().Add(-1 * time.Minute)
	live.LastAt, live.PrevAt, live.PrevWorst = read, read.Add(-time.Minute), 83.7
	row, out := judgedRow(t, cfg, st, "live", nil)
	if !strings.Contains(row, "~85%") {
		t.Errorf("the session carried forward to 85%% must show as ~85%%:\n%s", out)
	}
	if !strings.Contains(row, "no headroom") {
		t.Errorf("at ~85%% against 85 the verdict is no headroom:\n%s", out)
	}
	if !strings.Contains(out, "live at 85%") {
		t.Errorf("the headline must give the same figure:\n%s", out)
	}

	// Carried forward by less than shows, the reading stands unmarked.
	live.Last.FiveHour.Utilization = ptr(40)
	live.PrevWorst = 39.9
	live.LastAt, live.PrevAt = time.Now(), time.Now().Add(-10*time.Minute)
	row, out = judgedRow(t, cfg, st, "live", nil)
	if strings.Contains(row, "~") || !strings.Contains(row, "40%") {
		t.Errorf("a projection that does not change the figure is not marked:\n%s", out)
	}
}

// A target must clear the landing margin (policy.go, Explain): an idle
// account with room under its triggers but less than landing_margin points
// below the session trigger is not one rotation will take, so it is not
// "available".
func TestStateSaysWhenAnAccountIsShortOfTheLandingMargin(t *testing.T) {
	cfg, st := judgedWorld()
	idle := st.Accounts["idle"]
	idle.Last.FiveHour.Utilization, idle.Last.SevenDay.Utilization = ptr(80), ptr(30)
	row, out := judgedRow(t, cfg, st, "idle", nil)
	if !strings.Contains(row, "short of margin") || strings.Contains(row, "available") {
		t.Errorf("80%% of the session with a 10-point margin under 85 is short of margin:\n%s", out)
	}
	// The live account is no landing place to judge: it stays available.
	st.Accounts["live"].Last.FiveHour.Utilization = ptr(80)
	row, out = judgedRow(t, cfg, st, "live", nil)
	if strings.Contains(row, "short of margin") {
		t.Errorf("the margin applies to targets, not to the account in use:\n%s", out)
	}
}

// WeeklyPace carried an idle account forward with a rate measured long
// before, so `why` showed "98% used" beside the verdict "at 97%, ready".
// Only an account some profile has live, or one read within poll_active,
// is projected, as everywhere else.
func TestWeeklyPaceDoesNotProjectAnIdleAccount(t *testing.T) {
	cfg, st := judgedWorld()
	idle := st.Accounts["idle"]
	idle.Last.FiveHour.Utilization, idle.Last.SevenDay.Utilization = ptr(10), ptr(97)
	idle.PrevAt, idle.PrevWorst = idle.LastAt.Add(-10*time.Minute), 96
	dec, vs := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: vs})
	out := b.String()
	if !strings.Contains(out, "97% used") || strings.Contains(out, "100% used") {
		t.Errorf("an idle account's weekly pace must use its reading:\n%s", out)
	}
	b.Reset()
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: true})
	if out := b.String(); !strings.Contains(out, "pace: 97% used") {
		t.Errorf("status --detail's pace must use the reading too:\n%s", out)
	}
}
