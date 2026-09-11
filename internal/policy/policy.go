// Package policy decides whether to rotate accounts.
//
// It is a pure function of (config, observed state, clock). No I/O, no clock
// reads of its own, no hidden state — so every rule below is testable as a
// table, and a surprising rotation can be reproduced exactly from the audit log.
package policy

import (
	"fmt"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
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
	worst float64
}

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
		if c, ok := firstEligible(all, in); ok {
			return Decision{Kind: Switch, Target: c.acct.ID, Reason: "no active account yet"}
		}
		return waiting(all, in, "no account is currently usable")
	}

	// An account that has been refused must be left alone until it resets, no
	// matter what an older usage reading said.
	activeBurnt := active.avail == state.Burnt
	overTrigger := active.obs != nil && active.obs.Last != nil && active.worst >= in.Cfg.SwitchAt
	forced := active.obs != nil && active.obs.Last != nil && active.worst >= in.Cfg.HardFloor

	if !activeBurnt && !overTrigger {
		if active.obs == nil || active.obs.Last == nil {
			// Unknown is not "fine". But rotating away from a working session on
			// no evidence is worse, so hold and let the poller catch up.
			return Decision{Kind: Stay, Reason: "active account's usage is unknown; holding until it can be read"}
		}
		return Decision{Kind: Stay, Reason: fmt.Sprintf("active account at %.0f%%, under the %.0f%% trigger",
			active.worst, in.Cfg.SwitchAt)}
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

	c, ok := firstEligible(all, in)
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

	reason := fmt.Sprintf("active account at %.0f%%, over the %.0f%% trigger", active.worst, in.Cfg.SwitchAt)
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
					_, c.worst = obs.Last.Worst()
				}
				out = append(out, c)
				continue
			}
			if obs.Last != nil {
				// Use the projection rather than the raw reading: a reading is a
				// lower bound, and under heavy use it can be ten points low by
				// the time it is acted on. Projected() equals the reading when
				// no burn rate is known, so this is conservative by default.
				c.worst = obs.Projected(in.Now.Add(in.Lookahead))
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

// firstEligible walks priority order and returns the first account that is
// usable: readable, not burnt, not over its reserve, not itself already past the
// trigger, and permitted in this directory.
func firstEligible(all []candidate, in Input) (candidate, bool) {
	ordered := preferredOrder(all, in)
	for _, c := range ordered {
		if c.acct.ID == in.St.Active {
			continue
		}
		if c.avail != state.Available {
			continue
		}
		if c.worst >= in.Cfg.SwitchAt {
			continue
		}
		if !in.Cfg.ScopeAllowed(c.acct.Scope, in.Dir) {
			continue
		}
		return c, true
	}
	return candidate{}, false
}

// preferredOrder applies a project's scope preference ahead of the global
// priority, without forbidding anything the rule allows. A stable partition
// rather than a sort, so the configured order survives within each group.
func preferredOrder(all []candidate, in Input) []candidate {
	pr, ok := in.Cfg.ProjectFor(in.Dir)
	if !ok || len(pr.Prefer) == 0 {
		return all
	}
	rank := map[string]int{}
	for i, scope := range pr.Prefer {
		rank[scope] = i
	}
	groups := make([][]candidate, len(pr.Prefer)+1)
	for _, c := range all {
		if i, named := rank[c.acct.Scope]; named {
			groups[i] = append(groups[i], c)
		} else {
			groups[len(pr.Prefer)] = append(groups[len(pr.Prefer)], c)
		}
	}
	out := make([]candidate, 0, len(all))
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// scopeBlocked names an account that would otherwise have served, but is not
// permitted in this directory.
func scopeBlocked(all []candidate, in Input) string {
	for _, c := range all {
		if c.acct.ID == in.St.Active || c.avail != state.Available || c.worst >= in.Cfg.SwitchAt {
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
		case c.obs.Last != nil && c.worst >= in.Cfg.SwitchAt:
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
	ordered := preferredOrder(all, in)

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
			if c.worst >= in.Cfg.SwitchAt {
				v.Why = fmt.Sprintf("at %.0f%%, over the %.0f%% trigger", c.worst, in.Cfg.SwitchAt)
			} else {
				v.Why = fmt.Sprintf("at %.0f%%, %.0f points of room", c.worst, in.Cfg.SwitchAt-c.worst)
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
		case c.worst >= in.Cfg.SwitchAt:
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
