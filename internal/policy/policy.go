// Package policy decides whether to rotate accounts.
//
// It is a pure function of (config, observed state, clock). No I/O, no clock
// reads of its own, no hidden state — so every rule below is testable as a
// table, and a surprising rotation can be reproduced exactly from the audit log.
package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

type Kind string

const (
	// Stay: the active account is fine, or we are deliberately holding.
	Stay Kind = "stay"
	// Switch: rotate to Target.
	Switch Kind = "switch"
	// Wait: nothing is eligible. Report when the first account recovers rather
	// than failing silently.
	Wait Kind = "wait"
)

type Decision struct {
	Kind   Kind
	Target string
	Reason string

	// Forced means the active account is past the hard floor, so the swap must
	// happen even mid-turn rather than waiting for an idle gap.
	Forced bool

	// For Wait: which account recovers first, and when.
	RecoversAccount string
	RecoversAt      time.Time
}

func (d Decision) String() string {
	switch d.Kind {
	case Switch:
		f := ""
		if d.Forced {
			f = " (forced, past hard floor)"
		}
		return fmt.Sprintf("switch to %s: %s%s", d.Target, d.Reason, f)
	case Wait:
		if d.RecoversAccount != "" {
			return fmt.Sprintf("wait: %s (%s recovers %s)", d.Reason, d.RecoversAccount,
				d.RecoversAt.Format("15:04"))
		}
		return "wait: " + d.Reason
	default:
		return "stay: " + d.Reason
	}
}

// Input is everything the decision depends on.
type Input struct {
	Cfg *config.Config
	St  *state.State
	Now time.Time

	// LastSwitch feeds the anti-flap cooldown. Zero means "never switched".
	LastSwitch time.Time

	// Pinned suspends automatic rotation (`claudeswitch use` sets it).
	Pinned string

	// Lookahead is how far forward to project when deciding. Rotating only once
	// a reading has crossed the trigger is always late: the reading is a lower
	// bound, polling is periodic, and the swap then waits for an idle gap. By
	// the time all three have played out the account can be well past the line —
	// observed crossing at 88% and switching at 100%.
	//
	// Projecting one poll interval plus the idle wait forward means the decision
	// is made while there is still room, which is the whole point of a threshold
	// below 100.
	Lookahead time.Duration

	// Dir is where work is currently happening. Project rules are applied
	// against it, so work quota need not silently fund personal work. Empty
	// means unknown, which imposes no restriction — refusing to rotate because
	// we cannot tell where we are would be worse than the thing it prevents.
	Dir string
}

// candidate pairs a configured account with its observed state.
type candidate struct {
	acct  config.Account
	obs   *state.Account
	avail state.Availability
	// worst is the utilization of whichever window is closest to its own
	// trigger, and window names that window. over says it has reached it.
	//
	// These are kept as three fields rather than one number compared against one
	// threshold because the two windows are held to different lines: the
	// comparison has to happen where the window is still known.
	worst  float64
	window string
	over   bool
	// exceedance is points past the trigger, negative when there is room. It
	// orders candidates by how much trouble they are in, across windows.
	exceedance float64
}

// trigger is the threshold governing whichever window this candidate is judged
// on, for use in messages that quote it.
func (c candidate) trigger(cfg *config.Config) float64 { return cfg.TriggerFor(c.window) }

// Decide returns what to do now.
func Decide(in Input) Decision {
	if in.Pinned != "" {
		return Decision{Kind: Stay, Reason: fmt.Sprintf("pinned to %s; automatic rotation is off", in.Pinned)}
	}

	all := gather(in)
	if len(all) == 0 {
		return Decision{Kind: Wait, Reason: "no accounts configured"}
	}

	active, hasActive := find(all, in.St.Active)

	// With no active account yet, take the first eligible one.
	if !hasActive {
		if c, ok := bestEligible(all, in); ok {
			return Decision{Kind: Switch, Target: c.acct.ID, Reason: "no active account yet"}
		}
		return waiting(all, in, "no account is currently usable")
	}

	// An account that has been refused must be left alone until it resets, no
	// matter what an older usage reading said.
	activeBurnt := active.avail == state.Burnt
	overTrigger := active.obs != nil && active.obs.Last != nil && active.over
	forced := active.obs != nil && active.obs.Last != nil && active.worst >= in.Cfg.HardFloor

	if !activeBurnt && !overTrigger {
		if active.obs == nil || active.obs.Last == nil {
			// Unknown is not "fine". But rotating away from a working session on
			// no evidence is worse, so hold and let the poller catch up.
			return Decision{Kind: Stay, Reason: "active account's usage is unknown; holding until it can be read"}
		}
		return Decision{Kind: Stay, Reason: fmt.Sprintf("active account at %.0f%%, under the %.0f%% trigger",
			active.worst, active.trigger(in.Cfg))}
	}

	// Cooldown damps flapping between two marginal accounts — but never delays
	// getting off an account that has actually been refused, or one past the
	// hard floor.
	if !activeBurnt && !forced && !in.LastSwitch.IsZero() {
		if elapsed := in.Now.Sub(in.LastSwitch); elapsed < in.Cfg.Cooldown.Duration {
			return Decision{Kind: Stay, Reason: fmt.Sprintf(
				"active account at %.0f%% but only %s since the last switch (cooldown %s)",
				active.worst, elapsed.Round(time.Second), in.Cfg.Cooldown.Duration)}
		}
	}

	c, ok := bestEligible(all, in)
	if !ok {
		why := fmt.Sprintf("active account at %.0f%% and nothing else is usable", active.worst)
		if activeBurnt {
			why = "active account was refused and nothing else is usable"
		}
		// Distinguish "everything is exhausted" from "the accounts with room are
		// not allowed here" — they call for completely different responses.
		if blocked := scopeBlocked(all, in); blocked != "" {
			why += fmt.Sprintf("; %s has room but this directory only allows %s",
				blocked, strings.Join(allowedScopes(in), ", "))
		}
		return waiting(all, in, why)
	}

	reason := fmt.Sprintf("active account at %.0f%% of its %s, over the %.0f%% trigger",
		active.worst, windowName(active.window), active.trigger(in.Cfg))
	if activeBurnt {
		reason = fmt.Sprintf("active account was refused on its %s window", active.obs.BurntWin)
	}
	return Decision{Kind: Switch, Target: c.acct.ID, Reason: reason, Forced: forced}
}

func gather(in Input) []candidate {
	var out []candidate
	for _, a := range in.Cfg.Ordered() {
		obs := in.St.Accounts[a.ID]
		c := candidate{acct: a, obs: obs, avail: state.Unknown}
		if obs != nil {
			c.avail = obs.AvailabilityAt(a.Reserve, in.Now)
			// Only the ACTIVE account is projected forward. An idle account is
			// not being spent, so projecting it would invent usage and rule out
			// a perfectly good target.
			if a.ID != in.St.Active {
				if obs.Last != nil {
					c.window, c.worst, c.exceedance = obs.Last.WorstAgainst(
						in.Cfg.TriggerFor(usage.FiveHourKey), in.Cfg.TriggerFor(usage.SevenDayKey))
					c.over = c.exceedance >= 0
				}
				out = append(out, c)
				continue
			}
			if obs.Last != nil {
				// Use the projection rather than the raw reading: a reading is a
				// lower bound, and under heavy use it can be ten points low by
				// the time it is acted on. Projected() equals the reading when
				// no burn rate is known, so this is conservative by default.
				c.window, c.worst, c.exceedance = obs.Last.WorstAgainst(
					in.Cfg.TriggerFor(usage.FiveHourKey), in.Cfg.TriggerFor(usage.SevenDayKey))
				// The projection applies to the window we are judging, so carry
				// the same amount of growth onto the exceedance.
				if proj := obs.Projected(in.Now.Add(in.Lookahead)); proj > c.worst {
					c.exceedance += proj - c.worst
					c.worst = proj
				}
				c.over = c.exceedance >= 0
			}
		}
		out = append(out, c)
	}
	return out
}

func find(all []candidate, id string) (candidate, bool) {
	for _, c := range all {
		if c.acct.ID == id {
			return c, true
		}
	}
	return candidate{}, false
}

// bestEligible returns the account worth rotating into: among those that are
// usable — readable, not burnt, not over its reserve, not itself past the
// trigger, and permitted in this directory — the one with the most room.
//
// It used to take the first such account in priority order, which treats one
// point below the trigger as equivalent to ninety. That is how a rotation lands
// on an account it must immediately leave: the swap happens, the point is
// spent, the trigger is met, and the anti-flap cooldown then holds the session
// there for its full duration with no headroom at all. Observed with an account
// at 97% against a 98% weekly trigger, sitting first in priority while a pool
// that had just reset to 0% sat third.
//
// Two things still outrank headroom, because both express which account SHOULD
// serve rather than how much is left in it:
//
//   - a project's scope preference, which says what may serve this directory;
//   - the scope's own position in the priority list. Putting "personal" last is
//     how someone says "do not spend my own account while work accounts have
//     room", and that is not a statement about headroom — a personal account is
//     usually the emptiest precisely because it is the one held back. Ordering
//     purely by room would spend it first, which inverts the instruction.
//
// So headroom decides between accounts of the same scope, and the configured
// order decides between scopes. Within a group the priority order survives as
// the tie-break: candidates arrive in that order and only a strictly better one
// displaces the incumbent.
func bestEligible(all []candidate, in Input) (candidate, bool) {
	tier := scopeTiers(all)
	var best candidate
	found := false
	for _, c := range all {
		if c.acct.ID == in.St.Active {
			continue
		}
		if c.avail != state.Available {
			continue
		}
		if c.over {
			continue
		}
		if !in.Cfg.ScopeAllowed(c.acct.Scope, in.Dir) {
			continue
		}
		if found && !better(c, best, in, tier) {
			continue
		}
		best, found = c, true
	}
	return best, found
}

// better reports whether a ranks ahead of b for selection: preferred scope
// first, then scope order, then room. Equal on all three means the configured
// priority decides, which is the order candidates already arrive in — so this
// answers false for a tie and the incumbent keeps its place.
//
// bestEligible and Explain share it, because `why` claiming to list accounts
// "in order" while ordering them by something else is worse than not saying so:
// it showed an account with one point of room at the top, marked ready, when a
// pool that had just reset would actually have been chosen.
func better(a, b candidate, in Input, tier map[string]int) bool {
	if ra, rb := scopeRank(a, in), scopeRank(b, in); ra != rb {
		return ra < rb
	}
	if ta, tb := tier[a.acct.Scope], tier[b.acct.Scope]; ta != tb {
		return ta < tb
	}
	return a.exceedance < b.exceedance
}

// scopeTiers ranks each scope by where it first appears in the configured
// order, so "work" before "personal" falls out of the priority list itself
// rather than from any meaning attached to those particular words.
func scopeTiers(all []candidate) map[string]int {
	tier := map[string]int{}
	for _, c := range all {
		if _, seen := tier[c.acct.Scope]; !seen {
			tier[c.acct.Scope] = len(tier)
		}
	}
	return tier
}

// scopeRank is which of a project's preferred scopes an account belongs to,
// lower being more preferred. A scope the project does not name ranks after
// every scope it does, so an unlisted account is a fallback rather than a
// forbidden one — ScopeAllowed is what forbids.
func scopeRank(c candidate, in Input) int {
	pr, ok := in.Cfg.ProjectFor(in.Dir)
	if !ok || len(pr.Prefer) == 0 {
		return 0
	}
	for i, scope := range pr.Prefer {
		if c.acct.Scope == scope {
			return i
		}
	}
	return len(pr.Prefer)
}

// scopeBlocked names an account that would otherwise have served, but is not
// permitted in this directory.
func scopeBlocked(all []candidate, in Input) string {
	for _, c := range all {
		if c.acct.ID == in.St.Active || c.avail != state.Available || c.over {
			continue
		}
		if !in.Cfg.ScopeAllowed(c.acct.Scope, in.Dir) {
			return c.acct.ID
		}
	}
	return ""
}

func allowedScopes(in Input) []string {
	pr, ok := in.Cfg.ProjectFor(in.Dir)
	if !ok || len(pr.Eligible) == 0 {
		return []string{"any scope"}
	}
	return pr.Eligible
}

// waiting reports the earliest recovery so the daemon can say something useful
// instead of just refusing.
func waiting(all []candidate, in Input, why string) Decision {
	d := Decision{Kind: Wait, Reason: why}
	for _, c := range all {
		var at time.Time
		switch {
		case c.obs == nil:
			continue
		case c.avail == state.Burnt:
			at = c.obs.BurntTil
		case c.obs.Last != nil && c.over:
			if r := earliestReset(c); !r.IsZero() {
				at = r
			}
		}
		if at.IsZero() {
			continue
		}
		if d.RecoversAt.IsZero() || at.Before(d.RecoversAt) {
			d.RecoversAt = at
			d.RecoversAccount = c.acct.ID
		}
	}
	return d
}

// earliestReset is when the account's more-used window clears.
func earliestReset(c candidate) time.Time {
	if c.obs == nil || c.obs.Last == nil {
		return time.Time{}
	}
	u := c.obs.Last
	which, _ := u.Worst()
	w := u.FiveHour
	if which == "seven_day" {
		w = u.SevenDay
	}
	if w.ResetsAt == nil {
		return time.Time{}
	}
	return *w.ResetsAt
}

// Verdict is why one account was or was not chosen. The decision itself says
// what happened; this says what was considered, which is what you need when the
// answer is surprising.
type Verdict struct {
	ID       string
	Active   bool
	Eligible bool
	Why      string
	Worst    float64
	Window   string
	ClearsAt time.Time
}

// Explain runs the same reasoning as Decide and reports what it found for every
// account, in the order they were considered.
//
// Every failure worth debugging in this program has been "why did it not
// switch?", and answering it has meant reading state JSON and the audit log by
// hand. This is that, built in.
func Explain(in Input) (Decision, []Verdict) {
	dec := Decide(in)
	all := gather(in)
	ordered := explainOrder(all, in)

	out := make([]Verdict, 0, len(ordered))
	for _, c := range ordered {
		v := Verdict{
			ID:     c.acct.ID,
			Active: c.acct.ID == in.St.Active,
			Worst:  c.worst,
		}
		if c.obs != nil && c.obs.Last != nil {
			v.Window, _ = c.obs.Last.Worst()
			if r := earliestReset(c); !r.IsZero() {
				v.ClearsAt = r
			}
		}
		if !c.obs.HasReading() {
			v.Why = "never read"
			out = append(out, v)
			continue
		}

		switch {
		case v.Active:
			v.Eligible = true
			if c.over {
				v.Why = fmt.Sprintf("at %.0f%%, over the %.0f%% %s trigger",
					c.worst, c.trigger(in.Cfg), windowName(c.window))
			} else {
				v.Why = fmt.Sprintf("at %.0f%%, %.0f points of room", c.worst, -c.exceedance)
			}
		case c.avail == state.Burnt:
			v.Why = "refused on its " + c.obs.BurntWin + " window"
		case c.avail == state.Unknown:
			v.Why = "no usable reading"
			if c.obs != nil && c.obs.ExpiredAt(in.Now) {
				v.Why = "its window reset — being re-read"
			}
		case c.avail == state.Reserved:
			v.Why = fmt.Sprintf("held in reserve above %.0f%%", c.acct.Reserve)
		case c.over:
			v.Why = fmt.Sprintf("at %.0f%%, no headroom", c.worst)
		case !in.Cfg.ScopeAllowed(c.acct.Scope, in.Dir):
			v.Why = fmt.Sprintf("scope %q not allowed in this directory", c.acct.Scope)
		default:
			v.Eligible = true
			v.Why = fmt.Sprintf("at %.0f%%, ready", c.worst)
		}
		out = append(out, v)
	}
	return dec, out
}

// explainOrder lists accounts the way the chooser weighs them, so "considered,
// in order" is a true statement. The active account stays first — it is the one
// the reader is asking about — and the rest follow in selection order, which is
// stable for ties because sort.SliceStable preserves the configured priority.
func explainOrder(all []candidate, in Input) []candidate {
	out := append([]candidate(nil), all...)
	tier := scopeTiers(all)
	sort.SliceStable(out, func(i, j int) bool {
		if a := out[i].acct.ID == in.St.Active; a != (out[j].acct.ID == in.St.Active) {
			return a
		}
		return better(out[i], out[j], in, tier)
	})
	return out
}

// windowName is how a window is referred to in something a person reads.
func windowName(key string) string {
	if key == usage.SevenDayKey {
		return "weekly"
	}
	return "session"
}
