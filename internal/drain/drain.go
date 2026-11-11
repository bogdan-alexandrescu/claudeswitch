// Package drain is the drain forecast (IMPROVEMENTS "Drain forecast"): how
// fast each account's week climbs while it is being spent, and how many hours
// of use it has left before its switch point at that speed.
//
// The daemon keeps a Meter of every account's week figures for the last day
// and writes each account's Speed into its state record; the views read the
// record with For and Pool. Nothing here guesses: an account nobody has
// measured lately is Unknown, and one that has not climbed is Idle.
package drain

import (
	"math"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

const (
	// Lookback is how far back a speed is measured.
	Lookback = 24 * time.Hour
	// StretchGap is the longest wait between two rises of one stretch of
	// use. Past it, the account was resting, and that time is not use; an
	// account that has not risen for longer is not being spent now (Idle).
	StretchGap = time.Hour
	// HalfLife is how fast old use stops counting: a rise HalfLife ago
	// weighs half what one now does, so the speed follows the pace of late
	// rather than averaging a slow night with a busy morning.
	HalfLife = 2 * time.Hour
	// FreshFor is how old a measured speed may be and still be used: the
	// daemon measures at every save, so an older one means no daemon.
	FreshFor = 30 * time.Minute
	// ElsewhereFor is how recent a rise must be for an account live in no
	// profile here to count as used elsewhere.
	ElsewhereFor = 20 * time.Minute
)

type point struct {
	at time.Time
	v  float64
}

// Meter keeps each account's week figures for the last Lookback.
type Meter struct {
	pts map[string][]point
}

// NewMeter is an empty Meter.
func NewMeter() *Meter { return &Meter{pts: map[string][]point{}} }

// Add records account's week figure v, read at at. A figure no newer than
// the account's last is ignored: the daemon hands the same reading over many
// times.
func (m *Meter) Add(account string, at time.Time, v float64) {
	ps := m.pts[account]
	if n := len(ps); n > 0 && !at.After(ps[n-1].at) {
		return
	}
	m.pts[account] = append(ps, point{at, v})
}

// Speed is an account's week speed.
type Speed struct {
	// PerHour is week points an hour of use; 0 when it has not climbed
	// within Lookback.
	PerHour float64
	// LastRise is the newest rise within Lookback, zero for none.
	LastRise time.Time
}

// Speed measures account's speed at now from its figures within Lookback,
// and forgets older ones.
//
// The time counted is rise to rise within a stretch of use (rises at most
// StretchGap apart): whole-point readings of a slow burn come in level pairs,
// so a pair-by-pair rate would read a point every ten minutes as a point a
// minute. The first rise of a stretch counts neither its points nor its time,
// since nobody saw when that use began, and a fall (the week clearing) ends
// a stretch. Each rise's points and time weigh by its age (HalfLife).
func (m *Meter) Speed(account string, now time.Time) Speed {
	ps := m.pts[account]
	cut := 0
	for cut < len(ps) && now.Sub(ps[cut].at) > Lookback {
		cut++
	}
	ps = ps[cut:]
	m.pts[account] = ps

	var s Speed
	var points, mins float64
	var rise time.Time // the last rise of the current stretch
	for i := 1; i < len(ps); i++ {
		p, prev := ps[i], ps[i-1]
		switch {
		case p.v > prev.v:
			if !rise.IsZero() && p.at.Sub(rise) <= StretchGap {
				w := math.Exp2(-now.Sub(p.at).Hours() / HalfLife.Hours())
				points += w * (p.v - prev.v)
				mins += w * p.at.Sub(rise).Minutes()
			}
			rise, s.LastRise = p.at, p.at
		case p.v < prev.v:
			rise = time.Time{}
		}
	}
	if mins > 0 {
		s.PerHour = points / mins * 60
	}
	return s
}

// Kind is what an estimate could say.
type Kind string

const (
	// Hours: Left hours of use before the switch point.
	Hours Kind = "hours"
	// ToReset: the account outlasts its week: Left is longer than the time
	// until the week clears.
	ToReset Kind = "lasts_to_reset"
	// Idle: it is not being spent: no rise within StretchGap (∞).
	Idle Kind = "idle"
	// Drained: at or past its switch point, or refused on its week.
	Drained Kind = "drained"
	// Unknown: no reading, or no speed measured lately (no daemon).
	Unknown Kind = "unknown"
)

// Estimate is one account's drain forecast.
type Estimate struct {
	Kind Kind
	// Left is the hours of use before the switch point; Hours and ToReset.
	Left time.Duration
	// PerHour is the speed it runs at, when one was measured lately.
	PerHour float64
	// Elsewhere: it climbed within ElsewhereFor while no profile here has
	// it live: someone on another machine is spending it.
	Elsewhere bool
}

// fresh reports whether a's speed was measured recently enough to use.
func fresh(a *state.Account, now time.Time) bool {
	return !a.WeekSpeedAt.IsZero() && now.Sub(a.WeekSpeedAt) <= FreshFor
}

// Measured reports whether a's speed was measured lately enough to use: by
// a running daemon, within FreshFor.
func Measured(a *state.Account, now time.Time) bool { return a != nil && fresh(a, now) }

// moving reports whether a is being spent now: a speed, and a rise within
// StretchGap. A speed from this morning says how it was spent, not that it is.
func moving(a *state.Account, now time.Time) bool {
	return a.WeekSpeed > 0 && !a.WeekRiseAt.IsZero() && now.Sub(a.WeekRiseAt) <= StretchGap
}

// room is how many points a has before switchAt, and whether it counts at
// all: read, under its switch point and not refused on its week.
func room(a *state.Account, switchAt float64, now time.Time) (float64, bool) {
	if a == nil || a.Last == nil || !a.Last.SevenDay.Known() {
		return 0, false
	}
	if a.BurntWin == usage.SevenDayKey && now.Before(a.BurntTil) {
		return 0, false
	}
	left := switchAt - a.Last.SevenDay.Pct()
	return left, left > 0
}

// hoursOf is points at perHour, to the second.
func hoursOf(points, perHour float64) time.Duration {
	return time.Duration(math.Round(points/perHour*3600)) * time.Second
}

// For is account a's estimate at now. switchAt is where it leaves rotation
// (its ignore_weekly_gte, or the week's trigger), and live whether a profile
// here has it live.
func For(a *state.Account, switchAt float64, live bool, now time.Time) Estimate {
	if a == nil || a.Last == nil || !a.Last.SevenDay.Known() {
		return Estimate{Kind: Unknown}
	}
	var e Estimate
	measured := fresh(a, now)
	if measured {
		e.PerHour = a.WeekSpeed
		e.Elsewhere = !live && !a.WeekRiseAt.IsZero() && now.Sub(a.WeekRiseAt) <= ElsewhereFor
	}
	left, ok := room(a, switchAt, now)
	switch {
	case !ok:
		e.Kind = Drained
	case !measured:
		e.Kind = Unknown
	case !moving(a, now):
		e.Kind = Idle
	default:
		e.Kind, e.Left = Hours, hoursOf(left, a.WeekSpeed)
		if r := a.Last.SevenDay.ResetsAt; r != nil && !r.IsZero() && e.Left >= r.Sub(now) {
			e.Kind = ToReset
		}
	}
	return e
}

// Member is an account of a pool and its switch point.
type Member struct {
	Account  *state.Account
	SwitchAt float64
}

// PoolEstimate is a pool's drain forecast: how long the room its accounts
// have left lasts at the live account's speed.
type PoolEstimate struct {
	// Kind is Hours, Idle (the live account is not being spent), Drained (no
	// account has room) or Unknown (no speed measured lately).
	Kind Kind
	// Left is the hours of use the pool's room lasts; Hours only.
	Left time.Duration
	// Accounts is how many have room: read, under their switch point, and
	// not refused.
	Accounts int
	// PerHour is the live account's speed.
	PerHour float64
}

// Pool is the forecast for the pool members at the speed live (its live
// account, nil for none) is being spent at. Refused, unread and drained
// accounts are left out of the room: what they come back with is the
// refills', not this forecast's.
func Pool(live *state.Account, members []Member, now time.Time) PoolEstimate {
	var p PoolEstimate
	var points float64
	for _, m := range members {
		if m.Account != nil && now.Before(m.Account.BurntTil) {
			continue
		}
		if left, ok := room(m.Account, m.SwitchAt, now); ok {
			points += left
			p.Accounts++
		}
	}
	switch {
	case p.Accounts == 0:
		p.Kind = Drained
	case live == nil || !fresh(live, now):
		p.Kind = Unknown
	case !moving(live, now):
		p.Kind = Idle
	default:
		p.Kind, p.PerHour, p.Left = Hours, live.WeekSpeed, hoursOf(points, live.WeekSpeed)
	}
	return p
}
