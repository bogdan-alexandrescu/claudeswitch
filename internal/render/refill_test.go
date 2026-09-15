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

// statusRow renders a one-account table and returns the line for that account,
// so a test can assert on the verdict without matching the whole view.
func statusRow(t *testing.T, id string, five, seven usage.Window) string {
	t.Helper()
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 95, HardFloor: 96,
		PollActive: config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "active"}, {ID: id}},
		Priority:   []string{"active", id},
	}
	far := time.Now().Add(72 * time.Hour)
	st := &state.State{Active: "active", Accounts: map[string]*state.Account{
		"active": {
			Last: &usage.Usage{
				FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &far},
				SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &far},
			},
			LastAt: time.Now(),
		},
		id: {Last: &usage.Usage{FiveHour: five, SevenDay: seven}, LastAt: time.Now()},
	}}
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true})
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}
	t.Fatalf("no row for %q in:\n%s", id, buf.String())
	return ""
}

func at(d time.Duration) *time.Time { t := time.Now().Add(d); return &t }

// "Out of room" and "out of room for the next forty minutes" are different
// pieces of news, and the table said both the same way — in red, with a suffix
// naming the full window rather than when it empties.
func TestAnAccountAboutToRefillSaysWhenRatherThanNoHeadroom(t *testing.T) {
	row := statusRow(t, "work-b",
		// A whisker over, because shortDur floors to the minute the same way the
		// CLEARS column does; a flat 44m renders as "43m" and so would the clock.
		usage.Window{Utilization: ptr(85), ResetsAt: at(44*time.Minute + 30*time.Second)},
		usage.Window{Utilization: ptr(27), ResetsAt: at(5 * 24 * time.Hour)})
	if !strings.Contains(row, "refills in 44m") {
		t.Errorf("a five-hour window 44m from resetting must say so:\n%s", row)
	}
	if strings.Contains(row, "no headroom") {
		t.Errorf("an account that comes back on its own is not out of headroom:\n%s", row)
	}
}

// The refill line is only earned by a reset that is genuinely near. Everything
// beyond the hour keeps the verdict it had, which is the honest one: you are
// not waiting this one out.
func TestARefillBeyondAnHourIsStillNoHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   time.Duration
	}{
		{"just over the hour", 61 * time.Minute},
		{"later today", 2*time.Hour + 34*time.Minute},
		{"days out", 47 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := statusRow(t, "work-c",
				usage.Window{Utilization: ptr(3), ResetsAt: at(time.Hour)},
				usage.Window{Utilization: ptr(95), ResetsAt: at(tc.in)})
			if !strings.Contains(row, "no headroom · 7d") {
				t.Errorf("a reset %s out is not a refill worth waiting for:\n%s", tc.in, row)
			}
		})
	}
}

// Refilling the five-hour does nothing for an account that is out on its
// weekly, so the countdown must come from the window that did the blocking.
func TestTheCountdownComesFromTheWindowThatBlocked(t *testing.T) {
	row := statusRow(t, "work-d",
		usage.Window{Utilization: ptr(5), ResetsAt: at(40 * time.Minute)},
		usage.Window{Utilization: ptr(96), ResetsAt: at(3 * 24 * time.Hour)})
	if strings.Contains(row, "refills in") {
		t.Errorf("the five-hour clearing does not unblock a spent weekly:\n%s", row)
	}
	if !strings.Contains(row, "no headroom · 7d") {
		t.Errorf("want the weekly verdict:\n%s", row)
	}
}

// A configuration that does not do what its numbers say has to be visible where
// the numbers are. The footer prints both thresholds side by side, so without
// this the contradiction is on screen with no hint of its consequence.
func TestAContradictoryConfigIsWarnedAboutInStatus(t *testing.T) {
	far := time.Now().Add(72 * time.Hour)
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 96,
		Cooldown:   config.Duration{Duration: 10 * time.Minute},
		PollActive: config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "work-a"}},
		Priority:   []string{"work-a"},
	}
	st := &state.State{Active: "work-a", Accounts: map[string]*state.Account{
		"work-a": {Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &far},
			SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &far},
		}, LastAt: time.Now()},
	}}
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true})
	out := buf.String()
	if !strings.Contains(out, "WARNINGS") || !strings.Contains(out, "hard_floor (96)") {
		t.Errorf("the mismatch must be reported where the thresholds are shown:\n%s", out)
	}
	if !strings.Contains(out, "switch ≥85% session / ≥98% weekly") {
		t.Errorf("footer must quote the configured thresholds:\n%s", out)
	}
}
