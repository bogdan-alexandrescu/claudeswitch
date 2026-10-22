// Package policy decides whether to rotate accounts.
//
// It is a pure function of (config, observed state, clock). No I/O, no clock
// reads of its own, no hidden state — so every rule below is testable as a
// table, and a surprising rotation can be reproduced exactly from the audit log.
package policy

import (
	"fmt"
	"math"
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

	// Failover marks a switch away from an active account whose usage could
	// not be read (IMPROVEMENTS A2). It happens only in an idle gap: the
	// daemon never gives up waiting and swaps mid-turn for one.
	Failover bool

	// Held says why a switch waits even in an idle gap: a session of the
	// profile called a claude.ai tool (Claude in Chrome, an artifact, a Doc)
	// within hold_for_claude_ai (IMPROVEMENTS P4, phase 1 H), as "session
	// busy (claude.ai work 2m ago)". HeldUntil is when that hold runs out,
	// unless the session makes another such call.
	// Empty on anything but a switch that is neither forced nor off a refused
	// account. The daemon waits it out (max_switch_wait does not cut it
	// short) and `cs why` says it in the same words (WaitingFor).
	Held      string
	HeldUntil time.Time

	// For Wait: which account recovers first, and when.
	RecoversAccount string
	RecoversAt      time.Time

	// ownFirst says a switch during a claude.ai hold preferred accounts with
	// a browser of their own (R, ownBrowserFirst), for Explain's order.
	ownFirst bool

	// Unpin is set when the pin was lifted to reach this decision (the pin
	// safety valve, IMPROVEMENTS F3): "pin on work-1 lifted: it was
	// refused". The decision is then the one rotation makes unpinned, and
	// the daemon clears the pin, saying this in its log, a notification
	// and the audit log (kind unpin).
	Unpin string
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
	// PinHard keeps the pin even when the pinned account can no longer
	// serve (`cs account pin --hard`); without it PinLifted lifts the pin.
	PinHard bool

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

	// Pool restricts the candidates to these account ids, in config order: the
	// pool of the profile being decided for (docs/PROFILES.md §8 D1).
	// Nil means every configured account, which is the single-profile case.
	Pool []string

	// Tiers is the profile's pools in the order it lists them (D33,
	// config.Profile.Tiers): a candidate in a later tier is chosen only when
	// no candidate in an earlier one passes the same test. Within a tier the
	// choice is today's (better). Nil, or one tier, is one pool: exactly the
	// choice before shared pools. An account in Pool but in no tier counts as
	// the last.
	Tiers [][]string

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

	// ClaudeAIAt is the newest claude.ai call the profile's sessions have
	// made, zero when none is known (IMPROVEMENTS P4, phase 1 H): the
	// daemon's detector or, for the CLI, what the daemon recorded in state.
	// A switch within hold_for_claude_ai of it is Held.
	ClaudeAIAt time.Time

	// Unavailable names accounts that are not candidates, each with why:
	// live or maybe live in another profile ("in use by work"), or, for
	// Best, needing a sign-in. Since shared pools (D34) Decide, failover
	// and Explain pass them over for the next candidate, as Best and
	// Forecast always have, rather than choosing one for the daemon's §3
	// check to refuse. It never applies to the active account, which is
	// this profile's own.
	Unavailable map[string]string
}

// tierOf is the tier an account is in: its pool's place in the profile's
// order (D33). Without tiers every account is in tier 0.
func (in Input) tierOf(id string) int {
	for i, t := range in.Tiers {
		for _, a := range t {
			if a == id {
				return i
			}
		}
	}
	if len(in.Tiers) == 0 {
		return 0
	}
	return len(in.Tiers)
}

// unavailable reports whether a candidate other than the active account is
// out of reach (Input.Unavailable).
func (in Input) unavailable(id string) bool {
	_, out := in.Unavailable[id]
	return out && id != in.active()
}

// unavailableNote names the accounts of this pool passed over as
// unavailable, for a decision that found nothing else: " (t2: in use by
// work)". "" when none were.
func unavailableNote(all []candidate, in Input) string {
	var parts []string
	for _, c := range all {
		if in.unavailable(c.acct.ID) {
			parts = append(parts, c.acct.ID+": "+in.Unavailable[c.acct.ID])
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
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
	// model names the counted model whose weekly limit stands in for the
	// weekly window (cfg.Models, IMPROVEMENTS I6); "" when it is the window
	// itself. unreadable names a counted model whose limit has no figure,
	// which makes the account unknown (DESIGN 4.4).
	model      string
	unreadable string
	// tier is the candidate's pool's place in the profile's order (D33).
	tier int
}

// windowLabel is windowName with the counted model, when its limit is the
// weekly figure: "Modelname weekly".
func (c candidate) windowLabel() string {
	if c.model != "" && c.window == usage.SevenDayKey {
		return c.model + " weekly"
	}
	return windowName(c.window)
}

// trigger is the threshold governing whichever window this candidate is judged
// on, for use in messages that quote it.
func (c candidate) trigger(cfg *config.Config) float64 { return cfg.TriggerFor(c.window) }

// Decide returns what to do now.
func Decide(in Input) Decision {
	if in.Pinned != "" {
		why := ""
		if !in.PinHard {
			why = PinLifted(in.St.Accounts[in.Pinned], in.Now)
		}
		if why == "" {
			return Decision{Kind: Stay, Reason: fmt.Sprintf("pinned to %s; automatic rotation is off", in.Pinned)}
		}
		lift := fmt.Sprintf("pin on %s lifted: %s", in.Pinned, why)
		in.Pinned = ""
		d := Decide(in)
		d.Unpin, d.Reason = lift, lift+"; "+d.Reason
		return d
	}

	all := gather(in)
	if len(all) == 0 {
		return Decision{Kind: Wait, Reason: "no accounts configured"}
	}

	active, hasActive := find(all, in.active())

	// With no active account yet, take the best eligible one — short of the
	// landing margin if need be, since there is nothing to stay on.
	if !hasActive {
		why := "no active account yet"
		if a := in.active(); a != "" && a != state.Unattributed && !in.inPool(a) {
			// Live here but listed in another profile's accounts (moved, or
			// saved from here into another profile by `add --from`): this
			// profile moves off it so the other can use it.
			why = a + " is not in this profile's accounts"
		}
		if c, ok := bestEligible(all, in); ok {
			return in.held(Decision{Kind: Switch, Target: c.acct.ID, Reason: why}, false)
		}
		if c, ok := bestWithin(all, in, 0, false); ok {
			return in.held(Decision{Kind: Switch, Target: c.acct.ID, Reason: why + shortNote(c, in)}, false)
		}
		return waiting(all, in, "no account is currently usable"+unavailableNote(all, in))
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
			return in.held(failover(all, in, active), false)
		case held != "":
			return Decision{Kind: Stay, Reason: held}
		}
		if active.unreadable != "" {
			// A counted model's limit with no figure: the account's weekly
			// standing is unknown, which holds like any unknown reading.
			return Decision{Kind: Stay, Reason: fmt.Sprintf(
				"active account's %s weekly limit has no figure; holding until it can be read", active.unreadable)}
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
		return waiting(all, in, why+unavailableNote(all, in))
	}

	// A switch that goes now, during a claude.ai hold, prefers an account
	// with a browser of its own (R). One the hold delays is chosen when it
	// goes.
	ownFirst := false
	if forced || activeBurnt {
		c, note, ownFirst = ownBrowserFirst(all, in, c, note)
	}

	reason := fmt.Sprintf("active account at %.0f%% of its %s, over the %.0f%% trigger",
		active.worst, active.windowLabel(), active.trigger(in.Cfg))
	if activeBurnt {
		reason = fmt.Sprintf("active account was refused on its %s window", active.obs.BurntWin)
	}
	if e := expiringNote(c, in); e != "" {
		note += "; " + e
	}
	return in.held(Decision{Kind: Switch, Target: c.acct.ID, Reason: reason + note, Forced: forced, ownFirst: ownFirst}, activeBurnt)
}

// ownBrowserFirst is R, re-aimed after phase 3 C0 (owner decision
// 2026-10-22): a new Claude in Chrome connection needs a browser signed in as
// the live account itself, not another of its organization (GROUND_TRUTH
// §56), so a switch that goes during a claude.ai hold prefers an account with
// a browser of its own (state.OwnBrowser), short of the landing margin if
// need be, since this is a switch past the hard floor or off a refused
// account. With none that has room, c stands, and the reason says what a new
// connection needs. An open connection survives any switch; this is for the
// sessions that connect after it. It returns the choice, its note, and
// whether accounts with their own browser were put first.
func ownBrowserFirst(all []candidate, in Input, c candidate, note string) (candidate, string, bool) {
	if _, holding := in.holdAgo(); !holding {
		return c, note, false
	}
	own := func(x candidate) bool { _, ok := in.St.OwnBrowser(x.acct.ID); return ok }
	if own(c) {
		return c, note, true
	}
	var mine []candidate
	for _, x := range all {
		if own(x) {
			mine = append(mine, x)
		}
	}
	const why = "an account with a Chrome profile of its own, during claude.ai work, so a new Claude in Chrome " +
		"connection finds its browser"
	if s, ok := bestEligible(mine, in); ok {
		return s, "; " + why, true
	}
	if s, ok := bestWithin(mine, in, 0, false); ok {
		return s, fmt.Sprintf("; %s, short of the %g-point landing margin; %s", shortOf(s, in), in.Cfg.Margin(), why), true
	}
	return c, note + "; a new Claude in Chrome connection needs a browser signed in as " + c.acct.ID +
		" (cs chrome add " + c.acct.ID + ")", false
}

// held marks a switch that a recent claude.ai call holds (IMPROVEMENTS P4,
// phase 1 H): the session counts as busy, as a mid-turn one does, and the
// switch waits for the hold to run out. Not a forced switch: past the hard
// floor the wall is worse than a broken browser bridge or a split artifact.
// Not one off a refused account either: that account serves nothing, the
// claude.ai work included, so the switch keeps a refusal's timing.
func (in Input) held(d Decision, refused bool) Decision {
	if d.Kind != Switch || d.Forced || refused {
		return d
	}
	ago, holding := in.holdAgo()
	if !holding {
		return d
	}
	d.Held = "session busy (claude.ai work " + agoText(ago) + " ago)"
	d.HeldUntil = in.ClaudeAIAt.Add(in.Cfg.ClaudeAIHold())
	return d
}

// holdAgo is how long ago the profile's last claude.ai call was, and whether
// it is recent enough to hold a rotation: within hold_for_claude_ai, which
// is not off.
func (in Input) holdAgo() (time.Duration, bool) {
	if in.ClaudeAIAt.IsZero() {
		return 0, false
	}
	ago := max(in.Now.Sub(in.ClaudeAIAt), 0)
	hold := in.Cfg.ClaudeAIHold()
	return ago, hold > 0 && ago < hold
}

// agoText is a short time since: seconds under a minute, whole minutes after.
func agoText(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// WaitingFor is how a switch waiting on a busy session reads, in the daemon's
// log and in `cs why` alike: "switch wanted, session busy (claude.ai work 2m
// ago) — waiting for an idle gap".
func WaitingFor(busy string) string {
	return "switch wanted, " + busy + " — waiting for an idle gap"
}

// PinLifted says why a pin on this account no longer holds (the pin safety
// valve, IMPROVEMENTS F3), "" while the account can still serve:
//
//   - "it was refused": a refusal (a 429 in the transcripts) holds until its
//     reset;
//   - "it is out of quota": a reading, still current, at 100% on either
//     window;
//   - "it needs a sign-in": its refresh token has expired, or its last
//     error is one only a sign-in cures and was counted as a failed read.
//     An uncounted one is a stale vault copy the daemon re-captures
//     (DESIGN 4.4), and says nothing about the account.
//
// No record is no evidence. A hard pin (Input.PinHard) is never lifted.
func PinLifted(a *state.Account, now time.Time) string {
	switch {
	case a == nil:
		return ""
	case a.AvailabilityAt(0, now) == state.Burnt:
		return "it was refused"
	case a.Last != nil && !a.ExpiredAt(now) && (a.Last.FiveHour.Pct() >= 100 || a.Last.SevenDay.Pct() >= 100):
		return "it is out of quota"
	case !a.RefreshExpiry.IsZero() && now.After(a.RefreshExpiry),
		state.NeedsLoginErr(a.LastErr) && a.ReadFails > 0:
		return "it needs a sign-in"
	}
	return ""
}

// sessionRoom is how many points a candidate's 5-hour window sits below the
// session trigger, on the same reading eligibility uses. The landing margin
// is judged on it alone (owner decision 2026-10-07): the weekly trigger sits
// near 100 on purpose, and a margin there would rule out accounts with days
// of work left, so a weekly window only has to be under its trigger. A
// window with no reading imposes no margin.
func sessionRoom(c candidate, in Input) float64 {
	if c.obs == nil || c.obs.Last == nil || !c.obs.Last.FiveHour.Known() {
		return math.Inf(1)
	}
	return in.Cfg.TriggerFor(usage.FiveHourKey) - c.obs.Last.FiveHour.Pct()
}

// shortOf says how a candidate falls short of the landing margin.
func shortOf(c candidate, in Input) string {
	return fmt.Sprintf("%s's session window has only %.0f points below its %.0f%% trigger",
		c.acct.ID, sessionRoom(c, in), in.Cfg.TriggerFor(usage.FiveHourKey))
}

// shortNote is appended to a switch that lands inside the landing margin.
func shortNote(c candidate, in Input) string {
	return fmt.Sprintf("; %s, short of the %g-point landing margin, but it is the best there is",
		shortOf(c, in), in.Cfg.Margin())
}

// shortList names the candidates the landing margin excluded, for a Stay.
func shortList(all []candidate, in Input) string {
	var parts []string
	for _, c := range all {
		if usable(c, in) && sessionRoom(c, in) < in.Cfg.Margin() {
			parts = append(parts, shortOf(c, in))
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
//   - the failure streak itself has lasted at least that many poll_active
//     intervals, measured from FailSince — so a burst of fast hot-polling
//     failures, or a few failed polls in the seconds after waking from sleep
//     (when the last reading is hours old but the reads have only just begun
//     failing), is not taken for minutes of blindness. A streak with no
//     recorded start proves no duration;
//   - it is not an access token that expired on an idle session with a live
//     refresh token: Claude Code refreshes that on the next message, and the
//     daemon re-captures it.
func blindness(active candidate, in Input) (blind bool, hold string) {
	n := in.Cfg.BlindPolls()
	o := active.obs
	if n <= 0 || o == nil || o.ReadFails < n || o.FailSince.IsZero() {
		return false, ""
	}
	if iv := in.Cfg.PollActive.Duration; iv > 0 && in.Now.Sub(o.FailSince) < time.Duration(n)*iv {
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
// own last poll succeeded), under its triggers, and clear of the landing
// margin. It is never forced and is marked Failover: the daemon swaps only in
// an idle gap, never after max_switch_wait mid-turn (owner decision
// 2026-10-07) — moving a working session on no evidence of trouble with the
// work itself is not worth splitting a turn. It respects the cooldown. With no healthy account it holds, as a Stay:
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
		return Decision{Kind: Stay, Reason: why + "; no healthy account to fail over to, so holding" +
			unavailableNote(all, in)}
	}
	return Decision{Kind: Switch, Target: c.acct.ID, Reason: why + "; failing over to a healthy account",
		Failover: true}
}

func gather(in Input) []candidate {
	var out []candidate
	for _, a := range in.Cfg.Ordered() {
		if !in.inPool(a.ID) {
			continue
		}
		obs := in.St.Accounts[a.ID]
		c := candidate{acct: a, obs: obs, avail: state.Unknown, tier: in.tierOf(a.ID)}
		if obs != nil {
			// Counted models' weekly limits stand in for the weekly window
			// where higher (I6). With none configured obs is unchanged.
			obs, c.model, c.unreadable = obs.WithModels(in.Cfg.Models)
			c.obs = obs
			c.avail = obs.AvailabilityAt(a.Reserve, in.Now)
			if c.unreadable != "" && c.avail != state.Burnt {
				// Unknown is not "fine": an account whose counted limit
				// cannot be read is never a target.
				c.avail = state.Unknown
			}
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
// Headroom decides, with the configured priority surviving as the
// tie-break: candidates arrive in that order and only a strictly better one
// displaces the incumbent.
//
// Note what this means for an account placed last on purpose. A held-back
// account is usually the emptiest precisely because it is held back, so it
// becomes the preferred target as soon as it is eligible. Position in the
// priority list no longer keeps it in reserve; use `reserve` for that, or
// another profile's pool.
//
// A target must also clear the landing margin (IMPROVEMENTS A1): its 5-hour
// window at least landing_margin points below the session trigger, on the
// same figures as above. One point of session room is not a landing place;
// it is the same failure one rotation later. The weekly window only has to be
// under its trigger (owner decision 2026-10-07; see sessionRoom).
func bestEligible(all []candidate, in Input) (candidate, bool) {
	return bestWithin(all, in, in.Cfg.Margin(), false)
}

// usable is everything bestEligible asks of a candidate except the margin.
// An account live in another profile is not (D34): one credential is live
// in at most one profile (§3), so a shared account in use elsewhere waits
// its turn rather than being chosen for the daemon's check to refuse.
func usable(c candidate, in Input) bool {
	return c.acct.ID != in.active() && c.avail == state.Available && !c.over && !in.unavailable(c.acct.ID)
}

// bestWithin is bestEligible with the margin given: zero for the hard-floor
// and refusal fallbacks. readable also requires the candidate's own last poll
// to have succeeded, for failing over from a blind account onto a seeing one.
//
// Pool by pool (D33): of the candidates passing the test, those in the
// profile's earliest pool are preferred, and among them better decides as
// it always has. So a later pool's account is chosen only when no earlier
// pool's passes this same test, and whether there is anything to choose at
// all — which is what decides when a profile rotates or holds — is
// unchanged by the order.
func bestWithin(all []candidate, in Input, margin float64, readable bool) (candidate, bool) {
	var best candidate
	found := false
	for _, c := range all {
		if !usable(c, in) || sessionRoom(c, in) < margin {
			continue
		}
		if readable && c.obs.ReadFails > 0 {
			continue
		}
		if found && !ahead(c, best, in) {
			continue
		}
		best, found = c, true
	}
	return best, found
}

// ahead reports whether a is chosen before b: an earlier pool first (D33),
// then better. With one pool it is better.
func ahead(a, b candidate, in Input) bool {
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	return better(a, b, in)
}

// better reports whether a ranks ahead of b for selection: by room. Equal
// room means the configured priority decides, which is the order candidates already arrive in — so this
// answers false for a tie and the incumbent keeps its place.
//
// bestEligible and Explain share it (through ahead, which puts an earlier
// pool first), because `why` claiming to list accounts "in order" while
// ordering them by something else is worse than not saying so:
// it showed an account with one point of room at the top, marked ready, when a
// pool that had just reset would actually have been chosen.
//
// With prefer = "expiring" (IMPROVEMENTS F1) the account whose weekly window
// resets soonest while it still has quota unused ranks first, an account
// with no known reset after every one with; equal resets (to the minute)
// fall back to room.
func better(a, b candidate, in Input) bool {
	if in.Cfg.Preference() == config.PreferExpiring {
		ra, oka := expiring(a, in.Now)
		rb, okb := expiring(b, in.Now)
		switch {
		case oka != okb:
			return oka
		case oka && !ra.Equal(rb):
			return ra.Before(rb)
		}
	}
	// exceedance is points PAST the trigger, so it falls as room grows.
	return a.exceedance < b.exceedance
}

// WeeklyUnused is an account's weekly window as prefer = "expiring" weighs
// it (IMPROVEMENTS F1): when it resets and how much of it is unused (100
// less its utilization, a counted model's limit standing in as for every
// decision). False when either is unknown: no reading, no weekly figure or
// reset time, or a reading of a week that has since reset. Never 0 for
// unknown.
func WeeklyUnused(a *state.Account, now time.Time) (resetsAt time.Time, unused float64, ok bool) {
	if a == nil || a.Last == nil || !a.Last.SevenDay.Known() || a.Last.SevenDay.ResetsAt == nil {
		return time.Time{}, 0, false
	}
	r := *a.Last.SevenDay.ResetsAt
	if !now.Before(r) {
		return time.Time{}, 0, false
	}
	return r, 100 - a.Last.SevenDay.Pct(), true
}

// expiring is the key prefer = "expiring" ranks by: the weekly reset, to
// the minute, of a candidate with weekly quota unused.
func expiring(c candidate, now time.Time) (time.Time, bool) {
	r, unused, ok := WeeklyUnused(c.obs, now)
	if !ok || unused <= 0 {
		return time.Time{}, false
	}
	return r.Truncate(time.Minute), true
}

// expiringNote says why prefer = "expiring" put c first: "work-team resets
// in 9h with 40% unused, so it goes first". "" when it did not decide.
func expiringNote(c candidate, in Input) string {
	if in.Cfg.Preference() != config.PreferExpiring {
		return ""
	}
	r, unused, ok := WeeklyUnused(c.obs, in.Now)
	if !ok || unused <= 0 {
		return ""
	}
	return fmt.Sprintf("%s resets in %s with %.0f%% unused, so it goes first", c.acct.ID, inWords(r.Sub(in.Now)), unused)
}

// inWords is a span as a person says it: "40m", "9h", "2d 5h".
func inWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// Best is the account a manual switch of this profile would land on now
// (the app's "Switch to best"), and a warning or why there is none. Owner
// decision (lane 16): it is automatic rotation's own pick — usable
// (readable, not refused, not reserved, under its trigger), clear of the
// landing margin (bestEligible), ranked by ahead: pool by pool (D33), then
// better. When nothing clears the margin, the best account inside it is
// still returned, with a warning of how little room it has: a person may
// move anyway. A pin or the cooldown
// holds automatic rotation only, so neither applies. Accounts in
// in.Unavailable (live elsewhere, needing a sign-in) are never offered.
// name is the profile, for the reasons.
func Best(in Input, name string) (string, string) {
	c := Choose(in, name)
	return c.ID, c.Why
}

// Choice is Best's answer with the measure behind it (IMPROVEMENTS M12).
type Choice struct {
	// ID is Best's account, "" when there is none; Why is Best's warning
	// or its reason for none.
	ID, Why string
	// ActiveRoom and BestRoom are the points the live account and ID sit
	// below the trigger of their binding window (the one closest to its own
	// trigger): the figures better ranks by, the live account's carrying its
	// projection, negative past the trigger. Nil when unknown — no reading,
	// an expired one, an unreadable counted model, or no such account —
	// never 0.
	ActiveRoom, BestRoom *float64
	// OnBest is "Already on the best" (M12, owner 2026-10-08): there is a
	// best account and ahead does not rank it before the live one — an
	// account of a later pool than the live one never does (D33). False
	// whenever either room is unknown, and while the live account is
	// refused (a 429 leaves it no room, whatever its reading says).
	OnBest bool
}

// Choose is Best with the rooms it compares. The app's "Already on the
// best" reads OnBest, so its button and the CLI never disagree.
func Choose(in Input, name string) Choice {
	in.Pinned = ""
	var all []candidate
	var excluded []string
	var out Choice
	active, haveActive := candidate{}, false
	for _, c := range gather(in) {
		if c.acct.ID == in.active() {
			active, haveActive = c, true
			out.ActiveRoom = room(c)
		}
		if why, ok := in.Unavailable[c.acct.ID]; ok && c.acct.ID != in.active() {
			excluded = append(excluded, c.acct.ID+": "+why)
			continue
		}
		all = append(all, c)
	}
	pick := func(c candidate, why string) Choice {
		out.ID, out.Why, out.BestRoom = c.acct.ID, why, room(c)
		out.OnBest = haveActive && active.avail != state.Burnt &&
			out.ActiveRoom != nil && out.BestRoom != nil && !ahead(c, active, in)
		return out
	}
	if c, ok := bestEligible(all, in); ok {
		return pick(c, "")
	}
	if c, ok := bestWithin(all, in, 0, false); ok {
		return pick(c, fmt.Sprintf("%s, short of the %g-point landing margin", shortOf(c, in), in.Cfg.Margin()))
	}
	others := 0
	for _, c := range all {
		if c.acct.ID != in.active() {
			others++
		}
	}
	if name == "" {
		name = "the pool"
	}
	switch {
	case others == 0 && len(excluded) == 0:
		out.Why = name + " has no other account"
		return out
	case others == 0:
		out.Why = "no other account in " + name + " can be switched to (" + strings.Join(excluded, "; ") + ")"
		return out
	}
	out.Why = "no other account in " + name + " has room (each is over its trigger, refused, reserved or unread)"
	if len(excluded) > 0 {
		out.Why += "; not offered: " + strings.Join(excluded, "; ")
	}
	return out
}

// room is how many points c sits below its binding window's trigger: the
// negated exceedance better compares. Nil when that is not known.
func room(c candidate) *float64 {
	if c.window == "" || c.unreadable != "" || c.avail == state.Unknown {
		return nil
	}
	r := -c.exceedance
	return &r
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

	// WeeklyResetsAt and WeeklyUnused are the weekly window as prefer =
	// "expiring" weighs it (WeeklyUnused, IMPROVEMENTS F1): zero and nil
	// when unknown, whatever prefer is.
	WeeklyResetsAt time.Time
	WeeklyUnused   *float64
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
	ordered := explainOrder(all, in, dec.ownFirst)

	out := make([]Verdict, 0, len(ordered))
	first := true
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
		if r, unused, ok := WeeklyUnused(c.obs, in.Now); ok {
			v.WeeklyResetsAt, v.WeeklyUnused = r, &unused
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
		if in.unavailable(c.acct.ID) {
			// In use by another profile (D34), whatever its reading says:
			// never shown as ready, since rotation will not take it.
			v.Why = in.Unavailable[c.acct.ID]
			out = append(out, v)
			continue
		}
		if !c.obs.HasReading() {
			v.Why = "never read"
			out = append(out, v)
			continue
		}

		switch {
		case v.Active:
			v.Eligible = true
			switch {
			case c.unreadable != "":
				v.Why = fmt.Sprintf("its %s weekly limit has no figure; holding until it can be read", c.unreadable)
			case c.over:
				v.Why = fmt.Sprintf("at %.0f%%, over the %.0f%% %s trigger",
					c.worst, c.trigger(in.Cfg), c.windowLabel())
			default:
				v.Why = fmt.Sprintf("at %.0f%%, %.0f points of room", c.worst, -c.exceedance)
			}
		case c.avail == state.Burnt:
			v.Why = "refused on its " + c.obs.BurntWin + " window"
		case c.avail == state.Unknown:
			v.Why = "no usable reading"
			switch {
			case c.obs != nil && c.obs.ExpiredAt(in.Now):
				v.Why = "its window reset — being re-read"
			case c.unreadable != "":
				v.Why = fmt.Sprintf("its %s weekly limit has no figure — unknown, never a target", c.unreadable)
			}
		case c.avail == state.Reserved:
			v.Why = fmt.Sprintf("held in reserve above %.0f%%", c.acct.Reserve)
		case c.over:
			v.Why = fmt.Sprintf("at %.0f%%, no headroom", c.worst)
			if c.model != "" && c.window == usage.SevenDayKey {
				v.Why += " on its " + c.windowLabel() + " limit"
			}
		case sessionRoom(c, in) < in.Cfg.Margin():
			v.Why = fmt.Sprintf("session at %.0f%%, only %.0f points below its %.0f%% trigger — short of the %g-point landing margin; a target only past the hard floor or after a refusal",
				c.obs.Last.FiveHour.Pct(), sessionRoom(c, in), in.Cfg.TriggerFor(usage.FiveHourKey), in.Cfg.Margin())
		default:
			v.Eligible = true
			v.Why = fmt.Sprintf("at %.0f%%, ready", c.worst)
			if first {
				// The first ready account in the order is the one rotation
				// takes; say when prefer = "expiring" is why.
				if e := expiringNote(c, in); e != "" {
					v.Why += "; " + e
				}
				first = false
			}
		}
		out = append(out, v)
	}
	return dec, out
}

// explainOrder lists accounts the way the chooser weighs them, so "considered,
// in order" is a true statement. The active account stays first — it is the one
// the reader is asking about — and the rest follow in selection order, which is
// stable for ties because sort.SliceStable preserves the configured priority.
// With several pools that order is pool by pool (D33), and while a switch
// prefers accounts with a browser of their own (ownFirst, R) those come
// first, so the first ready account listed is still the one rotation takes.
func explainOrder(all []candidate, in Input, ownFirst bool) []candidate {
	out := append([]candidate(nil), all...)
	sort.SliceStable(out, func(i, j int) bool {
		if a := out[i].acct.ID == in.active(); a != (out[j].acct.ID == in.active()) {
			return a
		}
		if ownFirst {
			_, a := in.St.OwnBrowser(out[i].acct.ID)
			if _, b := in.St.OwnBrowser(out[j].acct.ID); a != b {
				return a
			}
		}
		return ahead(out[i], out[j], in)
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
