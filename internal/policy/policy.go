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

	// Pool restricts the candidates to these account ids, in config order: the
	// pool of the profile being decided for (docs/PROFILES.md §8 D1).
	// Nil means every configured account, which is the single-profile case.
	Pool []string

	// Live is the live credential's state being decided about: which account
	// it holds. Nil means the default profile's, St.Default(). LastSwitch and
	// Pinned stay separate fields, as before.
	Live *state.ProfileState

	// SessionBusy says the profile's transcripts show a turn in progress
	// (no idle gap). It decides one thing: whether an expired access token
	// explains an unreadable active account. Idle, it does — Claude Code
	// refreshes the token on the next message — so blind failover holds.
	// False when unknown (the CLI has no detector), which errs toward holding.
	SessionBusy bool
}

// active is the account the live credential being decided about holds.
func (in Input) active() string {
	if in.Live != nil {
		return in.Live.Active
	}
	return in.St.Default().Active
}

// inPool reports whether an account is a candidate at all.
func (in Input) inPool(id string) bool {
	if in.Pool == nil {
		return true
	}
	for _, p := range in.Pool {
		if p == id {
			return true
		}
	}
	return false
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

	active, hasActive := find(all, in.active())

	// With no active account yet, take the best eligible one — short of the
	// landing margin if need be, since there is nothing to stay on.
	if !hasActive {
		if c, ok := bestEligible(all, in); ok {
			return Decision{Kind: Switch, Target: c.acct.ID, Reason: "no active account yet"}
		}
		if c, ok := bestWithin(all, in, 0, false); ok {
			return Decision{Kind: Switch, Target: c.acct.ID,
				Reason: "no active account yet" + shortNote(c, in)}
		}
		return waiting(all, in, "no account is currently usable")
	}

	// An account that has been refused must be left alone until it resets, no
	// matter what an older usage reading said.
	activeBurnt := active.avail == state.Burnt
	overTrigger := active.obs != nil && active.obs.Last != nil && active.over
	forced := active.obs != nil && active.obs.Last != nil && active.worst >= in.Cfg.HardFloor

	if !activeBurnt && !overTrigger {
		// Unknown is not "fine", and the hold below has a limit: an active
		// account unreadable for long enough is failed over from (A2).
		switch blind, held := blindness(active, in); {
		case blind:
			return failover(all, in, active)
		case held != "":
			return Decision{Kind: Stay, Reason: held}
		}
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
	note := ""
	if !ok {
		// Accounts with room, but less than the landing margin (A1). Landing
		// on one means rotating away again almost at once, so a normal
		// rotation holds — as a Stay, not a Wait: this is not "every account
		// is out". Past the hard floor, or refused, staying is the worse of
		// the two, and the best of them is taken.
		if short, has := bestWithin(all, in, 0, false); has {
			if !forced && !activeBurnt {
				return Decision{Kind: Stay, Reason: fmt.Sprintf(
					"active account at %.0f%%, over the %.0f%% trigger, but %s; holding until the %.0f%% hard floor",
					active.worst, active.trigger(in.Cfg), shortList(all, in), in.Cfg.HardFloor)}
			}
			c, ok, note = short, true, shortNote(short, in)
		}
	}
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
	return Decision{Kind: Switch, Target: c.acct.ID, Reason: reason + note, Forced: forced}
}

// room is how many points a candidate sits below its own trigger, on
// whichever window is closest to its line.
func (c candidate) room() float64 { return -c.exceedance }

// shortNote is appended to a switch that lands inside the landing margin.
func shortNote(c candidate, in Input) string {
	return fmt.Sprintf("; %s has only %.0f points below its %.0f%% %s trigger, short of the %g-point landing margin, but it is the best there is",
		c.acct.ID, c.room(), c.trigger(in.Cfg), windowName(c.window), in.Cfg.Margin())
}

// shortList names the candidates the landing margin excluded, for a Stay.
func shortList(all []candidate, in Input) string {
	var parts []string
	for _, c := range all {
		if usable(c, in) && c.room() < in.Cfg.Margin() {
			parts = append(parts, fmt.Sprintf("%s has only %.0f points below its %.0f%% %s trigger",
				c.acct.ID, c.room(), c.trigger(in.Cfg), windowName(c.window)))
		}
	}
	return strings.Join(parts, " and ") + fmt.Sprintf(", short of the %g-point landing margin", in.Cfg.Margin())
}

// blindness reports whether the active account has been unreadable long
// enough to fail over from (IMPROVEMENTS A2), or, when it has but the cause is
// benign, the reason to hold instead.
//
// Blind means all of:
//   - blind_failover_polls is not 0, and the account's last ReadFails polls in
//     a row failed (the poller does not count a 429 or a declined poll);
//   - its last good reading, if any, is at least that many poll_active
//     intervals old — so a burst of fast hot-polling failures is not taken for
//     minutes of blindness;
//   - it is not an access token that expired on an idle session with a live
//     refresh token: Claude Code refreshes that on the next message, and the
//     daemon re-captures it.
func blindness(active candidate, in Input) (blind bool, hold string) {
	n := in.Cfg.BlindPolls()
	o := active.obs
	if n <= 0 || o == nil || o.ReadFails < n {
		return false, ""
	}
	if iv := in.Cfg.PollActive.Duration; iv > 0 && o.Last != nil &&
		in.Now.Sub(o.LastAt) < time.Duration(n)*iv {
		return false, ""
	}
	expired := !o.TokenExpiry.IsZero() && !in.Now.Before(o.TokenExpiry)
	refreshable := o.RefreshExpiry.IsZero() || in.Now.Before(o.RefreshExpiry)
	if expired && refreshable && !in.SessionBusy {
		return false, fmt.Sprintf("active account unreadable for %d polls, but its access token expired "+
			"on an idle session; Claude Code refreshes it on the next message, so holding", o.ReadFails)
	}
	return true, ""
}

// failover moves off a blind active account to a healthy one: readable (its
// own last poll succeeded) and under its trigger by the landing margin. It is
// an ordinary rotation for timing — not forced, so it waits for an idle gap,
// and it respects the cooldown. With no healthy account it holds, as a Stay:
// a working session on an unreadable account beats one on a doubtful account.
func failover(all []candidate, in Input, active candidate) Decision {
	why := fmt.Sprintf("active account unreadable for %d consecutive polls", active.obs.ReadFails)
	if e := active.obs.LastErr; e != "" {
		why += " (" + e + ")"
	}
	if !in.LastSwitch.IsZero() {
		if elapsed := in.Now.Sub(in.LastSwitch); elapsed < in.Cfg.Cooldown.Duration {
			return Decision{Kind: Stay, Reason: fmt.Sprintf("%s, but only %s since the last switch (cooldown %s)",
				why, elapsed.Round(time.Second), in.Cfg.Cooldown.Duration)}
		}
	}
	c, ok := bestWithin(all, in, in.Cfg.Margin(), true)
	if !ok {
		return Decision{Kind: Stay, Reason: why + "; no healthy account to fail over to, so holding"}
	}
	return Decision{Kind: Switch, Target: c.acct.ID, Reason: why + "; failing over to a healthy account"}
}

func gather(in Input) []candidate {
	var out []candidate
	for _, a := range in.Cfg.Ordered() {
		if !in.inPool(a.ID) {
			continue
		}
		obs := in.St.Accounts[a.ID]
		c := candidate{acct: a, obs: obs, avail: state.Unknown}
		if obs != nil {
			c.avail = obs.AvailabilityAt(a.Reserve, in.Now)
			// Only the ACTIVE account is projected forward. An idle account is
			// not being spent, so projecting it would invent usage and rule out
			// a perfectly good target.
			if a.ID != in.active() {
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
// Only a project's scope preference outranks room, because that says which
// accounts may serve this directory at all rather than how much is left in
// them. Everything else is decided by headroom, with the configured priority
// surviving as the tie-break: candidates arrive in that order and only a
// strictly better one displaces the incumbent.
//
// Note what this means for an account placed last on purpose. A held-back
// account is usually the emptiest precisely because it is held back, so it
// becomes the preferred target as soon as it is eligible. Position in the
// priority list no longer keeps it in reserve; use `reserve` for that, or a
// scope the working directory does not allow.
//
// A target must also clear the landing margin (IMPROVEMENTS A1): at least
// landing_margin points below its own trigger, on the same figures as above.
// One point of room is not a landing place either; it is the same failure one
// rotation later.
func bestEligible(all []candidate, in Input) (candidate, bool) {
	return bestWithin(all, in, in.Cfg.Margin(), false)
}

// usable is everything bestEligible asks of a candidate except the margin.
func usable(c candidate, in Input) bool {
	return c.acct.ID != in.active() && c.avail == state.Available && !c.over &&
		in.Cfg.ScopeAllowed(c.acct.Scope, in.Dir)
}

// bestWithin is bestEligible with the margin given: zero for the hard-floor
// and refusal fallbacks. readable also requires the candidate's own last poll
// to have succeeded, for failing over from a blind account onto a seeing one.
func bestWithin(all []candidate, in Input, margin float64, readable bool) (candidate, bool) {
	var best candidate
	found := false
	for _, c := range all {
		if !usable(c, in) || c.room() < margin {
			continue
		}
		if readable && c.obs.ReadFails > 0 {
			continue
		}
		if found && !better(c, best, in) {
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
func better(a, b candidate, in Input) bool {
	if ra, rb := scopeRank(a, in), scopeRank(b, in); ra != rb {
		return ra < rb
	}
	// exceedance is points PAST the trigger, so it falls as room grows.
	return a.exceedance < b.exceedance
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
		if c.acct.ID == in.active() || c.avail != state.Available || c.over {
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
			Active: c.acct.ID == in.active(),
			Worst:  c.worst,
		}
		if c.obs != nil && c.obs.Last != nil {
			v.Window, _ = c.obs.Last.Worst()
			if r := earliestReset(c); !r.IsZero() {
				v.ClearsAt = r
			}
		}
		if v.Active {
			if blind, held := blindness(c, in); blind || held != "" {
				v.Eligible = true
				v.Why = fmt.Sprintf("unreadable for %d polls; failing over when a healthy account is free", c.obs.ReadFails)
				if held != "" {
					v.Why = fmt.Sprintf("unreadable for %d polls, but its token only expired on an idle session; holding",
						c.obs.ReadFails)
				}
				out = append(out, v)
				continue
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
		case c.room() < in.Cfg.Margin():
			v.Why = fmt.Sprintf("at %.0f%%, only %.0f points below its %.0f%% %s trigger — short of the %g-point landing margin; a target only past the hard floor or after a refusal",
				c.worst, c.room(), c.trigger(in.Cfg), windowName(c.window), in.Cfg.Margin())
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
	sort.SliceStable(out, func(i, j int) bool {
		if a := out[i].acct.ID == in.active(); a != (out[j].acct.ID == in.active()) {
			return a
		}
		return better(out[i], out[j], in)
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
