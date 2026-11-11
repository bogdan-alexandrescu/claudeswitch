package drain

import (
	"math"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

// feed hands m the week figures of one account, one every step from t0.
func feed(m *Meter, id string, step time.Duration, figures ...float64) time.Time {
	at := t0
	for i, v := range figures {
		at = t0.Add(time.Duration(i) * step)
		m.Add(id, at, v)
	}
	return at
}

func near(got, want float64) bool { return math.Abs(got-want) < 0.01 }

func TestASteadyClimbIsItsSpeed(t *testing.T) {
	m := NewMeter()
	last := feed(m, "a", 3*time.Minute, 40, 41, 42, 43, 44)
	s := m.Speed("a", last)
	// The first rise starts the stretch and is not counted: 3 points in 9
	// minutes after it.
	if !near(s.PerHour, 20) {
		t.Errorf("speed %.2f points an hour, want 20", s.PerHour)
	}
	if !s.LastRise.Equal(last) {
		t.Errorf("last rise %v, want %v", s.LastRise, last)
	}
}

// Whole-point readings of a slow burn come in level pairs: the time runs from
// rise to rise, or a point every ten minutes reads as a point a minute.
func TestLevelReadingsBetweenRisesCountAsUse(t *testing.T) {
	m := NewMeter()
	last := feed(m, "a", time.Minute, 40, 40, 40, 40, 40, 41, 41, 41, 41, 41, 42, 42, 42, 42, 42, 43)
	if s := m.Speed("a", last); !near(s.PerHour, 12) {
		t.Errorf("speed %.2f points an hour, want 12 (a point every 5 minutes)", s.PerHour)
	}
}

// A break longer than StretchGap is not use: the stretch after it starts over.
func TestABreakIsNotUse(t *testing.T) {
	m := NewMeter()
	m.Add("a", t0, 10)
	m.Add("a", t0.Add(5*time.Minute), 12)  // first rise: not counted
	m.Add("a", t0.Add(10*time.Minute), 14) // 2 points in 5 minutes
	m.Add("a", t0.Add(3*time.Hour), 20)    // after a break: first rise again
	m.Add("a", t0.Add(3*time.Hour+10*time.Minute), 24)
	// 24 points an hour in both stretches; counting the break would make it
	// 6 points in nearly three hours.
	if s := m.Speed("a", t0.Add(4*time.Hour)); !near(s.PerHour, 24) {
		t.Errorf("speed %.2f points an hour, want 24", s.PerHour)
	}
}

// The speed follows the pace of late: use HalfLife ago weighs half as much.
func TestRecentUseWeighsMore(t *testing.T) {
	now := t0.Add(6 * time.Hour)
	m := NewMeter()
	m.Add("a", now.Add(-4*time.Hour-40*time.Minute), 10)
	m.Add("a", now.Add(-4*time.Hour-30*time.Minute), 11) // first rise
	m.Add("a", now.Add(-4*time.Hour), 12)                // 1 point in 30 minutes, two half-lives ago
	m.Add("a", now.Add(-20*time.Minute), 12)
	m.Add("a", now.Add(-10*time.Minute), 13) // first rise
	m.Add("a", now, 16)                      // 3 points in 10 minutes, now
	// (1/4 + 3) points in (30/4 + 10) minutes; unweighted it would be 6.
	if s := m.Speed("a", now); !near(s.PerHour, 3.25/17.5*60) {
		t.Errorf("speed %.2f points an hour, want %.2f", s.PerHour, 3.25/17.5*60)
	}
}

func TestAnAccountThatDidNotClimbHasNoSpeed(t *testing.T) {
	m := NewMeter()
	last := feed(m, "a", 10*time.Minute, 50, 50, 50, 50)
	if s := m.Speed("a", last); s.PerHour != 0 || !s.LastRise.IsZero() {
		t.Errorf("a level account: %+v", s)
	}
	if s := m.Speed("never", last); s.PerHour != 0 {
		t.Errorf("an account with no figures: %+v", s)
	}
	// One rise is a stretch with nothing counted in it yet.
	m.Add("b", t0, 50)
	m.Add("b", t0.Add(10*time.Minute), 51)
	if s := m.Speed("b", t0.Add(10*time.Minute)); s.PerHour != 0 || s.LastRise.IsZero() {
		t.Errorf("a single rise is no speed, but is a rise: %+v", s)
	}
}

// A fall is the week clearing: the stretch it interrupts ends there.
func TestAClearedWeekEndsTheStretch(t *testing.T) {
	m := NewMeter()
	m.Add("a", t0, 97)
	m.Add("a", t0.Add(5*time.Minute), 98)
	m.Add("a", t0.Add(10*time.Minute), 2) // cleared
	m.Add("a", t0.Add(15*time.Minute), 3) // first rise of a new stretch
	m.Add("a", t0.Add(20*time.Minute), 4)
	if s := m.Speed("a", t0.Add(20*time.Minute)); !near(s.PerHour, 12) {
		t.Errorf("speed %.2f points an hour, want 12 (one point in 5 minutes)", s.PerHour)
	}
}

func TestOnlyTheLastDayCounts(t *testing.T) {
	m := NewMeter()
	feed(m, "a", 3*time.Minute, 10, 11, 12, 13)
	if s := m.Speed("a", t0.Add(Lookback+time.Hour)); s.PerHour != 0 || !s.LastRise.IsZero() {
		t.Errorf("figures over a day old still counted: %+v", s)
	}
}

func TestAnOlderOrRepeatedFigureIsIgnored(t *testing.T) {
	m := NewMeter()
	m.Add("a", t0, 10)
	m.Add("a", t0.Add(3*time.Minute), 11)
	m.Add("a", t0.Add(6*time.Minute), 12)
	m.Add("a", t0.Add(6*time.Minute), 30) // the same reading again
	m.Add("a", t0.Add(time.Minute), 90)   // older than the last
	if s := m.Speed("a", t0.Add(6*time.Minute)); !near(s.PerHour, 20) {
		t.Errorf("speed %.2f points an hour, want 20", s.PerHour)
	}
}

// account is a state record at week figure w, its week clearing in clears,
// measured just now at perHour, last rising rise ago.
func account(w float64, clears time.Duration, perHour float64, rise time.Duration) *state.Account {
	at := t0.Add(clears)
	a := &state.Account{
		Last:        &usage.Usage{SevenDay: usage.Window{Utilization: &w, ResetsAt: &at}},
		LastAt:      t0.Add(-time.Minute),
		WeekSpeed:   perHour,
		WeekSpeedAt: t0.Add(-time.Minute),
	}
	if rise > 0 {
		a.WeekRiseAt = t0.Add(-rise)
	}
	return a
}

func TestHoursOfUseLeftBeforeTheSwitchPoint(t *testing.T) {
	e := For(account(60, 5*24*time.Hour, 10, time.Minute), 98, true, t0)
	if e.Kind != Hours || e.Left != 3*time.Hour+48*time.Minute {
		t.Errorf("38 points at 10 an hour: %+v, want 3h48m", e)
	}
	if e.PerHour != 10 {
		t.Errorf("speed %v, want 10", e.PerHour)
	}
	// ignore_weekly_gte is the account's own switch point.
	if e := For(account(60, 5*24*time.Hour, 10, time.Minute), 65, true, t0); e.Left != 30*time.Minute {
		t.Errorf("5 points at 10 an hour: %+v, want 30m", e)
	}
}

func TestAnAccountThatOutlastsItsWeekLastsToReset(t *testing.T) {
	// 38 points at 2 an hour is 19 hours of use; the week clears in 10.
	if e := For(account(60, 10*time.Hour, 2, time.Minute), 98, true, t0); e.Kind != ToReset || e.Left != 19*time.Hour {
		t.Errorf("%+v, want lasts to reset with 19h left", e)
	}
}

func TestAnIdleAccountIsInfinite(t *testing.T) {
	if e := For(account(60, 5*24*time.Hour, 0, 0), 98, false, t0); e.Kind != Idle {
		t.Errorf("%+v, want idle", e)
	}
	// A speed from this morning says how it was spent, not that it is.
	if e := For(account(60, 5*24*time.Hour, 10, StretchGap+time.Minute), 98, true, t0); e.Kind != Idle {
		t.Errorf("not risen for over an hour: %+v, want idle", e)
	}
}

func TestDrained(t *testing.T) {
	if e := For(account(98, time.Hour, 10, time.Minute), 98, true, t0); e.Kind != Drained {
		t.Errorf("at its switch point: %+v", e)
	}
	refused := account(70, time.Hour, 10, time.Minute)
	refused.BurntTil, refused.BurntWin = t0.Add(time.Hour), usage.SevenDayKey
	if e := For(refused, 98, true, t0); e.Kind != Drained {
		t.Errorf("refused on its week: %+v", e)
	}
	session := account(70, time.Hour, 10, time.Minute)
	session.BurntTil, session.BurntWin = t0.Add(time.Hour), usage.FiveHourKey
	if e := For(session, 98, true, t0); e.Kind == Drained {
		t.Errorf("refused on its session only still has its week: %+v", e)
	}
}

func TestUnknownStaysUnknown(t *testing.T) {
	if e := For(nil, 98, false, t0); e.Kind != Unknown {
		t.Errorf("no record: %+v", e)
	}
	if e := For(&state.Account{}, 98, false, t0); e.Kind != Unknown {
		t.Errorf("no reading: %+v", e)
	}
	stale := account(60, 5*24*time.Hour, 10, time.Minute)
	stale.WeekSpeedAt = t0.Add(-FreshFor - time.Minute)
	if e := For(stale, 98, true, t0); e.Kind != Unknown {
		t.Errorf("a speed nobody has measured lately (no daemon): %+v", e)
	}
	never := account(60, 5*24*time.Hour, 0, 0)
	never.WeekSpeedAt = time.Time{}
	if e := For(never, 98, true, t0); e.Kind != Unknown {
		t.Errorf("never measured: %+v", e)
	}
}

func TestClimbingWhileLiveNowhereHereIsUsedElsewhere(t *testing.T) {
	if e := For(account(60, 5*24*time.Hour, 10, 5*time.Minute), 98, false, t0); !e.Elsewhere {
		t.Errorf("climbing, live in no profile here: %+v", e)
	}
	if e := For(account(60, 5*24*time.Hour, 10, 5*time.Minute), 98, true, t0); e.Elsewhere {
		t.Errorf("live here: %+v", e)
	}
	if e := For(account(60, 5*24*time.Hour, 10, ElsewhereFor+time.Minute), 98, false, t0); e.Elsewhere {
		t.Errorf("its last rise is too old to say: %+v", e)
	}
	// Spending past the switch point is worth seeing too.
	if e := For(account(99, time.Hour, 10, time.Minute), 98, false, t0); !e.Elsewhere || e.Kind != Drained {
		t.Errorf("drained and still climbing: %+v", e)
	}
}

func TestThePoolLastsAtTheLiveAccountsSpeed(t *testing.T) {
	live := account(90, 5*24*time.Hour, 20, time.Minute)
	refused := account(10, time.Hour, 0, 0)
	refused.BurntTil, refused.BurntWin = t0.Add(time.Hour), usage.FiveHourKey
	members := []Member{
		{live, 98},                                // 8 points
		{account(40, 3*24*time.Hour, 0, 0), 98},   // 58
		{account(60, 3*24*time.Hour, 5, 0), 65},   // 5
		{account(99, 3*24*time.Hour, 0, 0), 98},   // drained
		{refused, 98},                             // refused: left out
		{&state.Account{}, 98},                    // unread: left out
		{account(10, 3*24*time.Hour, 0, 0), 98.5}, // 88.5
	}
	p := Pool(live, members, t0)
	// 159.5 points at 20 an hour: 7h58m30s.
	if p.Kind != Hours || p.Accounts != 4 || p.Left != 7*time.Hour+58*time.Minute+30*time.Second {
		t.Errorf("%+v, want 4 accounts and 7h58m30s", p)
	}
}

func TestAPoolNobodyIsSpendingIsInfinite(t *testing.T) {
	live := account(50, 5*24*time.Hour, 0, 0)
	if p := Pool(live, []Member{{live, 98}}, t0); p.Kind != Idle || p.Accounts != 1 {
		t.Errorf("%+v, want idle", p)
	}
	resting := account(50, 5*24*time.Hour, 20, StretchGap+time.Minute)
	if p := Pool(resting, []Member{{resting, 98}}, t0); p.Kind != Idle {
		t.Errorf("the live account has not risen for over an hour: %+v, want idle", p)
	}
}

func TestAPoolWithNoRoomIsDrained(t *testing.T) {
	live := account(98, 5*24*time.Hour, 20, time.Minute)
	if p := Pool(live, []Member{{live, 98}}, t0); p.Kind != Drained {
		t.Errorf("%+v, want drained", p)
	}
}

func TestAPoolWithoutAMeasuredSpeedIsUnknown(t *testing.T) {
	live := account(50, 5*24*time.Hour, 20, time.Minute)
	live.WeekSpeedAt = time.Time{}
	if p := Pool(live, []Member{{live, 98}}, t0); p.Kind != Unknown || p.Accounts != 1 {
		t.Errorf("%+v, want unknown", p)
	}
	if p := Pool(nil, []Member{{account(50, time.Hour, 0, 0), 98}}, t0); p.Kind != Unknown {
		t.Errorf("no live account: %+v, want unknown", p)
	}
}
