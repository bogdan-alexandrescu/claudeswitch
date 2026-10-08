package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// profileView is one profile as the read-only views (status, top, why, plan,
// the status line, the session context) see it: its effective thresholds (D4),
// its pool and its live state.
//
// With no [[profile]] blocks there is one view, built so every view is
// exactly what it was before profiles: the config itself, no pool limit, and
// the default profile's state.
type profileView struct {
	in       config.Profile
	cfg      *config.Config // cfg.ForProfile(name); read only
	ist      *state.ProfileState
	pool     []string // nil: every account
	declared bool     // false for the implicit profile of a config without blocks
}

func viewOf(cfg *config.Config, st *state.State, in config.Profile) profileView {
	if len(cfg.Profiles) == 0 {
		return profileView{in: in, cfg: cfg, ist: st.Default()}
	}
	return profileView{in: in, cfg: cfg.ForProfile(in.Name), ist: st.Profile(in.Name),
		pool: append([]string{}, in.Pool...), declared: true}
}

// profileViews is one view per effective profile, in config order, or only
// the one named (an --profile flag, already checked by pickProfile).
func profileViews(cfg *config.Config, st *state.State, only string) []profileView {
	var out []profileView
	for _, in := range cfg.EffectiveProfiles() {
		if only == "" || in.Name == only {
			out = append(out, viewOf(cfg, st, in))
		}
	}
	return out
}

// input is the policy question for this profile, asked as the daemon asks it.
func (v profileView) input(st *state.State, now time.Time) policy.Input {
	in := policy.Input{
		Cfg: v.cfg, St: st, Now: now, LastSwitch: v.ist.LastSwitch, Pinned: v.ist.Pinned,
		Lookahead: lookahead(v.cfg),
	}
	if v.declared {
		in.Pool, in.Live = v.pool, v.ist
	}
	return in
}

// options narrows a status view to this profile.
func (v profileView) options(base render.Options, st *state.State) render.Options {
	o := base
	o.Cfg, o.St = v.cfg, st
	if v.declared {
		o.Profile, o.Pool = v.in.Name, v.pool
	}
	return o
}

// launchedAs says how Claude Code is started for a profile.
func launchedAs(in config.Profile) string {
	switch {
	case in.FromEnv:
		return "as this environment's CLAUDE_CONFIG_DIR says"
	case in.Dir == "":
		return "CLAUDE_CONFIG_DIR unset"
	}
	return "CLAUDE_CONFIG_DIR=" + in.Dir
}

// heading introduces one profile's block: its name, how it is launched, its
// pool and its effective thresholds (D4).
func (v profileView) heading(w io.Writer) {
	fmt.Fprintf(w, "\n  %s %s %s  %s\n", render.Bold("profile"), render.Bold(v.in.Name),
		fmt.Sprintf("(switch ≥%.0f%% / ≥%.0f%%)", v.cfg.TriggerFor(usage.FiveHourKey), v.cfg.TriggerFor(usage.SevenDayKey)),
		render.Grey(launchedAs(v.in)))
	pool := "empty — nothing to rotate to"
	if len(v.pool) > 0 {
		pool = strings.Join(v.pool, ", ")
	}
	fmt.Fprintf(w, "  %s\n", render.Grey(fmt.Sprintf("pool %s · hard floor ≥%.0f%%", pool, v.cfg.HardFloor)))
}

// activeIn names the profile whose live credential holds accountID, or "".
// With no profile config it is the default profile's, as before profiles.
func activeIn(cfg *config.Config, st *state.State, accountID string) string {
	for _, v := range profileViews(cfg, st, "") {
		if v.ist.Active == accountID && accountID != "" {
			return v.in.Name
		}
	}
	return ""
}

// addProfileJSON adds, with more than one profile, the profile whose pool
// holds an account and the one it is live in. Nothing with one profile.
func addProfileJSON(m map[string]any, cfg *config.Config, st *state.State, accountID string) {
	if !multiProfile(cfg) {
		return
	}
	if owner, ok := cfg.ProfileOf(accountID); ok {
		m["profile"] = owner
	}
	if in := activeIn(cfg, st, accountID); in != "" {
		m["active_in"] = in
	}
}

// switchesFor keeps the switches made in one profile. Events from before
// profiles name none, and were the default profile's.
func switchesFor(evs []audit.Event, name string) []audit.Event {
	var out []audit.Event
	for _, e := range evs {
		if e.Profile == name || (e.Profile == "" && name == config.DefaultProfile) {
			out = append(out, e)
		}
	}
	return out
}

// renderStatus writes `status` (and each frame of `top`): with more than one
// profile, one block per profile, each with its own pool, active account,
// decision and thresholds; otherwise the single view, unchanged.
func renderStatus(w io.Writer, cfg *config.Config, st *state.State, base render.Options,
	switches []audit.Event, now time.Time, only string) {
	views := profileViews(cfg, st, only)
	multi := multiProfile(cfg)
	for _, v := range views {
		o := v.options(base, st)
		dec := policy.Decide(v.input(st, now))
		o.Decision = &dec
		o.Switches = switches
		if multi {
			v.heading(w)
			o.Switches = switchesFor(switches, v.in.Name)
			o.Block = true
		}
		render.Status(w, o)
	}
	// Owner decision 2026-10-07: with several profiles the footer and legend
	// come once, at the end; each block's header carries its thresholds.
	if multi && !base.Detail {
		o := base
		o.Cfg, o.St = cfg, st
		render.SharedFooter(w, o)
	}
}

// renderWhy writes `why`, one block per profile when there is more than one.
func renderWhy(w io.Writer, cfg *config.Config, st *state.State, now time.Time, only string) {
	for _, v := range profileViews(cfg, st, only) {
		if multiProfile(cfg) {
			v.heading(w)
		}
		dec, verdicts := policy.Explain(v.input(st, now))
		render.Why(w, render.WhyOptions{Cfg: v.cfg, St: st, Decision: dec, Verdicts: verdicts})
	}
}

// whyJSON is `why --json`: with one profile {"decision", "accounts",
// "best", "best_why"}, and with more a "profiles" list holding each one's
// decision, accounts and best account.
//
// best is the account the app's "Switch to best" switches the profile to
// now (policy.Best), null when there is none; best_why says why there is
// none, null when there is one (lane 16).
func whyJSON(cfg *config.Config, st *state.State, now time.Time, only string) map[string]any {
	views := profileViews(cfg, st, only)
	if !multiProfile(cfg) {
		in := views[0].input(st, now)
		dec, verdicts := policy.Explain(in)
		m := map[string]any{"decision": decisionJSON(dec), "accounts": verdictsJSON(verdicts, views[0].cfg, st, now)}
		addBest(m, in, "", cfg, st, now)
		return m
	}
	var list []map[string]any
	for _, v := range views {
		in := v.input(st, now)
		dec, verdicts := policy.Explain(in)
		m := map[string]any{
			"profile": v.in.Name, "pool": v.pool, "thresholds": thresholdsJSON(v.cfg),
			"decision": decisionJSON(dec), "accounts": verdictsJSON(verdicts, v.cfg, st, now),
		}
		addBest(m, in, v.in.Name, cfg, st, now)
		markCurrent(m, cfg, v.in.Name)
		list = append(list, m)
	}
	return map[string]any{"profiles": list}
}

// addBest adds "best" and "best_why" for one profile's policy input.
func addBest(m map[string]any, in policy.Input, name string, cfg *config.Config, st *state.State, now time.Time) {
	in.Unavailable = bestExclusions(cfg, st, name, now)
	best, why := policy.Best(in, name)
	m["best"], m["best_why"] = orNull(best), orNull(why)
}

// bestExclusions are the accounts "Switch to best" must never offer in
// profile name (owner decision, lane 16): live in another profile, or maybe
// live there (a ghost guards it, D18/D22), and accounts only a sign-in
// brings back. From config and state alone, like the rest of `why`.
func bestExclusions(cfg *config.Config, st *state.State, name string, now time.Time) map[string]string {
	if name == "" {
		name = state.DefaultProfile
	}
	out := map[string]string{}
	for _, a := range cfg.Accounts {
		if acct := st.Accounts[a.ID]; acct != nil &&
			(render.NeedsLogin(acct.LastErr) || (!acct.RefreshExpiry.IsZero() && now.After(acct.RefreshExpiry))) {
			out[a.ID] = "needs a sign-in"
		}
	}
	for _, v := range profileViews(cfg, st, "") {
		if a := v.ist.Active; a != "" && a != state.Unattributed && v.in.Name != name {
			out[a] = "live in " + v.in.Name
		}
	}
	for _, g := range allGhosts(cfg, st) {
		if g.Account != "" && g.Profile != name {
			out[g.Account] = "may still be signed in in the old profile " + g.Profile
		}
	}
	return out
}

// markCurrent flags the profile this process's CLAUDE_CONFIG_DIR belongs
// to, which is the one `use` acts on with no flag. A skill running inside a
// session reads it to find its own block.
func markCurrent(m map[string]any, cfg *config.Config, name string) {
	if in, err := pickProfile(cfg, ""); err == nil && in.Name == name {
		m["current"] = true
	}
}

// thresholdsJSON is a profile's effective thresholds.
func thresholdsJSON(c *config.Config) map[string]any {
	return map[string]any{
		"switch_at": c.SwitchAt, "switch_at_weekly": c.SwitchAtWeekly, "hard_floor": c.HardFloor,
		"landing_margin": c.Margin(),
	}
}

// statuslineView picks the profile a status line belongs to: its own
// session's, from the CLAUDE_CONFIG_DIR Claude Code ran it with.
func statuslineView(cfg *config.Config, st *state.State) (profileView, error) {
	in, err := pickProfile(cfg, "")
	if err != nil {
		return profileView{}, err
	}
	return viewOf(cfg, st, in), nil
}
