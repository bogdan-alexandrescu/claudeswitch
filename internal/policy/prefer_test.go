package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// F1: spend expiring quota first (opt-in, prefer = "expiring").

func preferCfg(prefer string) *config.Config {
	return &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 99, Prefer: prefer,
		Cooldown: config.Duration{Duration: 10 * time.Minute},
		Priority: []string{"work-1", "work-team", "research"},
		Accounts: []config.Account{{ID: "work-1"}, {ID: "work-team"}, {ID: "research"}},
	}
}

// weekly builds a reading whose session and weekly windows reset at their
// own times.
func weekly(five, seven float64, sessionResets, weekResets time.Time) *state.Account {
	f, s := five, seven
	r5, r7 := sessionResets, weekResets
	return &state.Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: &f, ResetsAt: &r5},
			SevenDay: usage.Window{Utilization: &s, ResetsAt: &r7},
		},
		LastAt: now,
	}
}

// The world both tests below decide in: work-1 is over its trigger;
// research has the most room but its week resets in six days; work-team
// has less room and its week resets in nine hours with 40% unused.
func expiringWorld(prefer string) Input {
	return Input{Cfg: preferCfg(prefer), Now: now, St: st("work-1", map[string]*state.Account{
		"work-1":    weekly(90, 50, now.Add(time.Hour), now.Add(3*24*time.Hour)),
		"work-team": weekly(30, 60, now.Add(2*time.Hour), now.Add(9*time.Hour)),
		"research":  weekly(5, 10, now.Add(3*time.Hour), now.Add(6*24*time.Hour)),
	})}
}

func TestPreferRoomIsTodaysBehaviour(t *testing.T) {
	for _, p := range []string{"", config.PreferRoom} {
		d := Decide(expiringWorld(p))
		if d.Kind != Switch || d.Target != "research" {
			t.Fatalf("prefer %q: got %v, want the roomiest, research", p, d)
		}
		if strings.Contains(d.Reason, "goes first") {
			t.Fatalf("prefer %q: reason %q mentions the expiring rule", p, d.Reason)
		}
	}
}

func TestPreferExpiringSpendsTheSoonestResetFirst(t *testing.T) {
	d := Decide(expiringWorld(config.PreferExpiring))
	if d.Kind != Switch || d.Target != "work-team" {
		t.Fatalf("got %v, want work-team: its week resets first with quota unused", d)
	}
	if want := "work-team resets in 9h with 40% unused, so it goes first"; !strings.Contains(d.Reason, want) {
		t.Fatalf("reason %q does not say %q", d.Reason, want)
	}
}

// Eligibility is unchanged: an account whose week resets soonest but which
// is short of the landing margin, over its trigger, refused or unread is
// never preferred.
func TestPreferExpiringKeepsTheSameEligibility(t *testing.T) {
	in := expiringWorld(config.PreferExpiring)
	in.St.Accounts["work-team"] = weekly(80, 60, now.Add(2*time.Hour), now.Add(9*time.Hour)) // 5 points of session room
	if d := Decide(in); d.Target != "research" {
		t.Fatalf("got %v, want research: work-team is inside the landing margin", d)
	}
	in = expiringWorld(config.PreferExpiring)
	in.St.Accounts["work-team"].BurntTil, in.St.Accounts["work-team"].BurntWin = now.Add(time.Hour), "five_hour"
	if d := Decide(in); d.Target != "research" {
		t.Fatalf("got %v, want research: work-team was refused", d)
	}
	in = expiringWorld(config.PreferExpiring)
	in.St.Accounts["work-team"] = weekly(30, 98.5, now.Add(2*time.Hour), now.Add(9*time.Hour))
	if d := Decide(in); d.Target != "research" {
		t.Fatalf("got %v, want research: work-team is over its weekly trigger", d)
	}
}

// Ties fall back to room: two weeks resetting at the same minute.
func TestPreferExpiringTiesFallBackToRoom(t *testing.T) {
	in := expiringWorld(config.PreferExpiring)
	in.St.Accounts["research"] = weekly(5, 10, now.Add(3*time.Hour), now.Add(9*time.Hour).Add(20*time.Second))
	if d := Decide(in); d.Target != "research" {
		t.Fatalf("got %v, want research: same reset minute, more room", d)
	}
}

// An account whose weekly reset is unknown ranks after every account with
// one, and among themselves they rank by room.
func TestPreferExpiringRanksAnUnknownResetLast(t *testing.T) {
	in := expiringWorld(config.PreferExpiring)
	in.St.Accounts["work-team"].Last.SevenDay.ResetsAt = nil
	if d := Decide(in); d.Target != "research" {
		t.Fatalf("got %v, want research: work-team's reset is unknown", d)
	}
}

// Best (the app's "Switch to best") and why's order use the same rule.
func TestPreferExpiringAppliesToBestAndWhy(t *testing.T) {
	in := expiringWorld(config.PreferExpiring)
	if id, _ := Best(in, "default"); id != "work-team" {
		t.Fatalf("Best = %q, want work-team", id)
	}
	_, vs := Explain(in)
	if len(vs) < 3 || vs[1].ID != "work-team" || vs[2].ID != "research" {
		t.Fatalf("why order = %v, want work-1, work-team, research", ids(vs))
	}
	if !strings.Contains(vs[1].Why, "resets in 9h with 40% unused, so it goes first") {
		t.Fatalf("work-team's why = %q", vs[1].Why)
	}
	wt := vs[1]
	if !wt.WeeklyResetsAt.Equal(now.Add(9*time.Hour)) || wt.WeeklyUnused == nil || *wt.WeeklyUnused != 40 {
		t.Fatalf("work-team weekly = %v, %v; want resets in 9h, 40 unused", wt.WeeklyResetsAt, wt.WeeklyUnused)
	}
}

// why --json carries weekly_resets_at and weekly_unused whatever prefer is;
// unknown is unknown, never 0.
func TestVerdictWeeklyFieldsAreUnknownWithoutAReading(t *testing.T) {
	in := expiringWorld(config.PreferRoom)
	delete(in.St.Accounts, "research")
	_, vs := Explain(in)
	for _, v := range vs {
		if v.ID == "research" && (v.WeeklyUnused != nil || !v.WeeklyResetsAt.IsZero()) {
			t.Fatalf("research never read, yet weekly = %v, %v", v.WeeklyResetsAt, v.WeeklyUnused)
		}
		if v.ID == "work-1" && (v.WeeklyUnused == nil || *v.WeeklyUnused != 50) {
			t.Fatalf("work-1 weekly_unused = %v, want 50", v.WeeklyUnused)
		}
	}
}

func ids(vs []Verdict) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}
