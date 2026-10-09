package policy

import (
	"sort"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// RunwayKind is what the pool runway forecast (IMPROVEMENTS F2) could say.
type RunwayKind string

const (
	// RunwayDry: at this pace the pool has no eligible account left at
	// DryAt (now, when it already has none).
	RunwayDry RunwayKind = "dry"
	// RunwayRefills: a window resets (or a refusal clears) before the pool
	// would run dry, giving room back, so it does not run dry at this pace
	// before RefillsAt — and what comes after is not forecast.
	RunwayRefills RunwayKind = "refills"
	// RunwayIdle: the account in use is not being spent (its last two
	// readings are level).
	RunwayIdle RunwayKind = "idle"
	// RunwayUnknown: too few recent readings to forecast from, or an
	// account in the pool whose room nobody knows. Never a guess.
	RunwayUnknown RunwayKind = "unknown"
)

// Runway is the pool runway forecast for one profile (IMPROVEMENTS F2).
type Runway struct {
	Kind RunwayKind
	// DryAt is when the pool has no eligible account left; set only for
	// RunwayDry.
	DryAt time.Time
	// RefillsAt and RefillsAccount are the first reset that gives the pool
	// room back before it would run dry; set only for RunwayRefills.
	RefillsAt      time.Time
	RefillsAccount string
	// Pace is the burn rate the forecast runs at, in points a minute: the
	// account in use's. Zero unless Dry or Refills.
	Pace float64
	// TriggerAt is when each account reaches its trigger at this pace,
	// spent in the order rotation would spend them: the account in use
	// first, then each eligible account. Only known times are present: an
	// account already past its trigger, one rotation would not land on, or
	// one whose time lies past RefillsAt has none.
	TriggerAt map[string]time.Time
}

// ForecastMinWindow is how recent the readings a forecast runs on must be:
// the newer no older than this (or three poll_active intervals, whichever is
// longer), and the pair no further apart. A rate across a longer gap is an
// average over time nobody watched.
const ForecastMinWindow = 15 * time.Minute

// forecastWindow is ForecastMinWindow, stretched for a slow poll_active.
func forecastWindow(in Input) time.Duration {
	if w := 3 * in.Cfg.PollActive.Duration; w > ForecastMinWindow {
		return w
	}
	return ForecastMinWindow
}

// pace is how fast the account in use is being spent, in points a minute,
// from its last two readings — or, when they are level, the rate of the last
// rise if that rise is itself recent (whole-point readings of a slow burn
// come in level pairs). known is false when the readings cannot say: fewer
// than two, either too old, too far apart, or a window reset between them.
// Zero and known means level: not being spent.
func pace(a *state.Account, in Input) (rate float64, known bool) {
	win := forecastWindow(in)
	if a == nil || a.Last == nil || a.LastAt.IsZero() || a.PrevAt.IsZero() ||
		in.Now.Sub(a.LastAt) > win || a.ExpiredAt(in.Now) {
		return 0, false
	}
	span := a.LastAt.Sub(a.PrevAt)
	if span <= 0 || span > win {
		return 0, false
	}
	_, worst := a.Last.Worst()
	switch d := worst - a.PrevWorst; {
	case d > 0:
		return d / span.Minutes(), true
	case d < 0:
		return 0, false // a reset: one reading of the window there is now
	}
	if a.RiseRate > 0 && !a.RiseAt.IsZero() && in.Now.Sub(a.RiseAt) <= win {
		return a.RiseRate, true
	}
	return 0, true
}

// Forecast is the pool runway (IMPROVEMENTS F2): when each account reaches
// its trigger, and when the profile's pool has no eligible account left, at
// the pace the account in use is burning now.
//
// The pool is spent as rotation would spend it: the account in use to its
// trigger, then the account rotation would take (bestEligible's test and
// order, the landing margin and prefer included) to its trigger, and so on.
// Every account is assumed to burn at the same pace in points: plans differ,
// and the forecast says "at this pace" because that is all it knows. Accounts
// in in.Unavailable are out of reach and not counted.
//
// Unknown stays unknown: no forecast without a recent pair of readings of the
// account in use (pace), and none while an account in the pool has no usable
// reading — its room is not known. A window reset (or a refusal clearing)
// before the pool would run dry gives room back, so the answer is then
// RunwayRefills at that reset, not a time past it.
func Forecast(in Input) Runway {
	unknown := Runway{Kind: RunwayUnknown}
	var all []candidate
	for _, c := range gather(in) {
		if _, out := in.Unavailable[c.acct.ID]; out && c.acct.ID != in.active() {
			continue
		}
		all = append(all, c)
	}
	active, ok := find(all, in.active())
	if !ok || active.obs == nil || active.obs.Last == nil || active.avail == state.Unknown {
		return unknown
	}
	for _, c := range all {
		if c.avail == state.Unknown {
			return unknown
		}
	}

	var queue []candidate
	for _, c := range all {
		if usable(c, in) && sessionRoom(c, in) >= in.Cfg.Margin() {
			queue = append(queue, c)
		}
	}
	sort.SliceStable(queue, func(i, j int) bool { return better(queue[i], queue[j], in) })

	// Over its trigger with nowhere to go: dry now, whatever the pace.
	if (active.over || active.avail == state.Burnt) && len(queue) == 0 {
		return Runway{Kind: RunwayDry, DryAt: in.Now}
	}

	rate, known := pace(active.obs, in)
	switch {
	case !known:
		return unknown
	case rate <= 0:
		return Runway{Kind: RunwayIdle}
	}
	minutes := func(points float64) time.Duration {
		return time.Duration(points / rate * float64(time.Minute))
	}

	r := Runway{Kind: RunwayDry, Pace: rate, TriggerAt: map[string]time.Time{}}
	// starts is when each account in the sequence begins being spent.
	starts := map[string]time.Time{active.acct.ID: in.Now}
	t := in.Now
	if active.avail != state.Burnt {
		// From the reading itself, not gather's projection: the forecast is
		// the projection, carried to the trigger.
		_, _, exceed := active.obs.Last.WorstAgainst(in.Cfg.TriggerFor(usage.FiveHourKey), in.Cfg.TriggerFor(usage.SevenDayKey))
		if at := active.obs.LastAt.Add(minutes(-exceed)); exceed < 0 && at.After(in.Now) {
			r.TriggerAt[active.acct.ID] = at
			t = at
		}
	}
	for _, c := range queue {
		starts[c.acct.ID] = t
		t = t.Add(minutes(-c.exceedance))
		r.TriggerAt[c.acct.ID] = t
	}

	if at, id := firstRefill(all, starts, in); !at.IsZero() && at.Before(t) {
		for id, when := range r.TriggerAt {
			if when.After(at) {
				delete(r.TriggerAt, id)
			}
		}
		r.Kind, r.RefillsAt, r.RefillsAccount = RunwayRefills, at, id
		return r
	}
	r.DryAt = t
	return r
}

// firstRefill is the earliest moment, after now, an account in the pool gets
// back room the forecast counts as gone: a refusal clearing, or the reset of
// the window that binds it (the one closest to its own trigger) — for an
// account rotation cannot use now, at any time; for one in the sequence
// (starts), only once it has begun being spent. A reset before rotation
// reaches an account gives back what it used before, which the forecast
// never counted as spent, so it only makes the forecast early by that much.
// A window with nothing used gives nothing back.
func firstRefill(all []candidate, starts map[string]time.Time, in Input) (time.Time, string) {
	var at time.Time
	var who string
	note := func(t time.Time, id string) {
		if start, spent := starts[id]; spent && t.Before(start) {
			return
		}
		if t.After(in.Now) && (at.IsZero() || t.Before(at)) {
			at, who = t, id
		}
	}
	for _, c := range all {
		if c.avail == state.Burnt {
			note(c.obs.BurntTil, c.acct.ID)
			continue
		}
		if c.obs == nil || c.obs.Last == nil {
			continue
		}
		w := c.obs.Last.FiveHour
		if c.window == usage.SevenDayKey {
			w = c.obs.Last.SevenDay
		}
		if w.ResetsAt != nil && w.Pct() > 0 {
			note(*w.ResetsAt, c.acct.ID)
		}
	}
	return at, who
}
