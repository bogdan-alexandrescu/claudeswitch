package state

import (
	"math"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// IMPROVEMENTS I8: the weekly pace view. Display only.

func paceAccount(seven float64, resets time.Time, readAt time.Time) *Account {
	five := 10.0
	r5 := readAt.Add(time.Hour)
	return &Account{
		Last: &usage.Usage{
			FiveHour: usage.Window{Utilization: &five, ResetsAt: &r5},
			SevenDay: usage.Window{Utilization: &seven, ResetsAt: &resets},
		},
		LastAt: readAt,
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

// Four days into the week (three left), 20% used: 57% of the week has gone,
// so it is 37 points behind pace; at the four-day average it reaches 35% by
// the reset and 65% expires unused.
func TestWeeklyPaceBehind(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := paceAccount(20, now.Add(3*24*time.Hour), now)
	p, ok := a.WeeklyPace(now)
	if !ok {
		t.Fatal("no pace for a readable weekly window with a reset")
	}
	if !near(p.Expected, 400.0/7) {
		t.Errorf("Expected = %v, want %v (4 of 7 days)", p.Expected, 400.0/7)
	}
	if p.Actual != 20 {
		t.Errorf("Actual = %v, want 20", p.Actual)
	}
	if !p.Estimated {
		t.Fatal("four days in is enough to estimate")
	}
	if !near(p.AtReset, 35) || !near(p.Unused, 65) {
		t.Errorf("AtReset/Unused = %v/%v, want 35/65", p.AtReset, p.Unused)
	}
}

// Ahead of pace, the projection caps at 100 and nothing expires.
func TestWeeklyPaceAheadCapsAtTheLimit(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := paceAccount(90, now.Add(3*24*time.Hour), now)
	p, ok := a.WeeklyPace(now)
	if !ok || !p.Estimated {
		t.Fatal("no estimate")
	}
	if p.AtReset != 100 || p.Unused != 0 {
		t.Errorf("AtReset/Unused = %v/%v, want 100/0", p.AtReset, p.Unused)
	}
	if p.Actual <= p.Expected {
		t.Errorf("90%% four days in must read as ahead of pace (%v vs %v)", p.Actual, p.Expected)
	}
}

// Too early in the week, one busy morning extrapolates to anything; the pace
// is shown but no expiry estimate is made.
func TestWeeklyPaceTooEarlyToEstimate(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := paceAccount(5, now.Add(7*24*time.Hour-6*time.Hour), now)
	p, ok := a.WeeklyPace(now)
	if !ok {
		t.Fatal("pace must still be shown early in the week")
	}
	if p.Estimated {
		t.Errorf("six hours in must not estimate expiry, got AtReset %v", p.AtReset)
	}
}

func TestWeeklyPaceNeedsAKnownWindowAndReset(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if _, ok := (&Account{}).WeeklyPace(now); ok {
		t.Error("an account with no reading has no pace")
	}
	a := paceAccount(20, now.Add(3*24*time.Hour), now)
	a.Last.SevenDay.ResetsAt = nil
	if _, ok := a.WeeklyPace(now); ok {
		t.Error("no reset means no window to measure pace against")
	}
	a = paceAccount(20, now.Add(-time.Hour), now.Add(-2*time.Hour))
	if _, ok := a.WeeklyPace(now); ok {
		t.Error("a reading from a week that has since reset has no pace")
	}
	a = paceAccount(20, now.Add(3*24*time.Hour), now)
	a.Last.SevenDay.Utilization = nil
	if _, ok := a.WeeklyPace(now); ok {
		t.Error("an unknown weekly window has no pace")
	}
}

// The projection to the reset uses the existing projection to bring the
// figure up to now when the weekly window is the one burning.
func TestWeeklyPaceUsesTheProjectionForAnOldWeeklyReading(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	read := now.Add(-10 * time.Minute)
	a := paceAccount(50, now.Add(3*24*time.Hour), read)
	*a.Last.FiveHour.Utilization = 5 // the weekly window is the worse one
	a.PrevWorst, a.PrevAt = 49, read.Add(-10*time.Minute)
	p, ok := a.WeeklyPace(now)
	if !ok {
		t.Fatal("no pace")
	}
	if p.Actual <= 50 {
		t.Errorf("Actual = %v; a weekly reading burning 0.1/min, 10 minutes old, projects past 50", p.Actual)
	}
}

// Counting a model's weekly limit replaces the weekly figure in a copy; the
// burn rate must not jump because the figure it is measured against changed.
func TestAccountWithModelsKeepsTheBurnRate(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := paceAccount(40, now.Add(3*24*time.Hour), now)
	*a.Last.FiveHour.Utilization = 5
	pct, r := 90.0, now.Add(2*24*time.Hour)
	a.Last.Limits = []usage.Limit{{Kind: "weekly_scoped", Group: "weekly", Percent: &pct, ResetsAt: &r,
		Scope: &usage.LimitScope{Model: &usage.LimitModel{DisplayName: strPtr("Modelname")}}}}
	a.PrevWorst, a.PrevAt = 39, now.Add(-10*time.Minute)
	before := a.BurnRate()

	cp, from, unreadable := a.WithModels([]string{"Modelname"})
	if from != "Modelname" || unreadable != "" {
		t.Fatalf("from/unreadable = %q/%q", from, unreadable)
	}
	if cp == a {
		t.Fatal("WithModels must return a copy when the figure changes")
	}
	if cp.Last.SevenDay.Pct() != 90 || a.Last.SevenDay.Pct() != 40 {
		t.Errorf("copy weekly %v (want 90), original %v (want 40)", cp.Last.SevenDay.Pct(), a.Last.SevenDay.Pct())
	}
	if after := cp.BurnRate(); !near(after, before) {
		t.Errorf("burn rate changed from %v to %v by counting a model limit", before, after)
	}
	if same, _, _ := a.WithModels(nil); same != a {
		t.Error("no models must return the account itself")
	}
}

func strPtr(s string) *string { return &s }
