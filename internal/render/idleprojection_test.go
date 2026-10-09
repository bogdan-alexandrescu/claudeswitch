package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// 0.6.1 (decided 2026-10-08): `cs status` shows a projected "~" figure only
// for the live account of some profile or an account read within
// poll_active. An idle account with a remembered burn rate and a flat pair
// of readings must show its reading as it is, not a figure that climbs.
func TestStatusDoesNotProjectIdleAccounts(t *testing.T) {
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 95, HardFloor: 96,
		PollActive: config.Duration{Duration: 2 * time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "live"}, {ID: "spare"}},
		Priority:   []string{"live", "spare"},
	}
	far := time.Now().Add(72 * time.Hour)
	burning := func() *state.Account {
		read := time.Now().Add(-10 * time.Minute)
		return &state.Account{
			Last: &usage.Usage{
				FiveHour: usage.Window{Utilization: ptr(40), ResetsAt: &far},
				SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &far},
			},
			LastAt: read, PrevWorst: 40, PrevAt: read.Add(-10 * time.Minute), LastRate: 2,
		}
	}
	st := &state.State{
		Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "live"}},
		Accounts: map[string]*state.Account{"live": burning(), "spare": burning()},
	}
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true})
	rows := map[string]string{}
	for _, line := range strings.Split(buf.String(), "\n") {
		for _, id := range []string{"live", "spare"} {
			if strings.Contains(line, id) && strings.Contains(line, "●") && rows[id] == "" {
				rows[id] = line
			}
		}
	}
	if !strings.Contains(rows["live"], "~") {
		t.Errorf("the live account's row must be projected:\n%s", buf.String())
	}
	if strings.Contains(rows["spare"], "~") {
		t.Errorf("an idle account's row must not be projected:\n%s", buf.String())
	}

	buf.Reset()
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: true})
	if n := strings.Count(buf.String(), "burning"); n != 1 {
		t.Errorf("only the live account may say it is burning (%d lines do):\n%s", n, buf.String())
	}
}
