package main

import (
	"fmt"
	"io"
	"os"
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
		Cfg: v.cfg, St: st, Now: now, LastSwitch: v.ist.LastSwitch, Pinned: v.ist.Pinned, PinHard: v.ist.PinHard,
		Lookahead: lookahead(v.cfg),
	}
	if v.declared {
		in.Pool, in.Live = v.pool, v.ist
	}
	return in
}

// runway is the profile's pool runway forecast (IMPROVEMENTS F2), over the
// accounts "Switch to best" may offer: one live in another profile, or
// needing a sign-in, is out of the pool's reach.
func (v profileView) runway(cfg *config.Config, st *state.State, now time.Time) policy.Runway {
	in := v.input(st, now)
	in.Unavailable = bestExclusions(cfg, st, v.in.Name, now)
	return policy.Forecast(in)
}

// addRunwayJSON adds a profile's runway (IMPROVEMENTS F2): pool_dry_at,
// when its pool has no eligible account left at this pace (null when not
// forecast); pool_forecast, which of dry, refills, idle or unknown the
// forecast is; and pool_refills_at, the reset that gives the pool room back
// before it would run dry (null unless refills).
func addRunwayJSON(m map[string]any, r policy.Runway) {
	m["pool_dry_at"] = timeOrNull(r.DryAt)
	m["pool_forecast"] = string(r.Kind)
	m["pool_refills_at"] = timeOrNull(r.RefillsAt)
}

// triggerAtJSON is an account's trigger_at in a runway: when it reaches its
// trigger at this pace, null when that is not forecast.
func triggerAtJSON(r policy.Runway, id string) any {
	if at, ok := r.TriggerAt[id]; ok {
		return at.UTC()
	}
	return nil
}

// runways is every profile's runway, keyed by profile, and every account's
// trigger_at from the runway of the profile whose pool holds it.
type runways struct {
	profiles map[string]map[string]any
	accounts map[string]any
}

func runwayJSON(cfg *config.Config, st *state.State, now time.Time) runways {
	out := runways{profiles: map[string]map[string]any{}, accounts: map[string]any{}}
	for _, v := range profileViews(cfg, st, "") {
		r := v.runway(cfg, st, now)
		m := map[string]any{}
		addRunwayJSON(m, r)
		out.profiles[v.in.Name] = m
		for _, id := range v.in.Pool {
			out.accounts[id] = triggerAtJSON(r, id)
		}
	}
	return out
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
		r := v.runway(cfg, st, now)
		o.Runway = &r
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
		r := views[0].runway(cfg, st, now)
		m := map[string]any{"decision": decisionJSON(dec), "accounts": verdictsJSON(verdicts, views[0].cfg, st, now, r)}
		addBest(m, in, "", cfg, st, now)
		addRunwayJSON(m, r)
		return m
	}
	var list []map[string]any
	for _, v := range views {
		in := v.input(st, now)
		dec, verdicts := policy.Explain(in)
		r := v.runway(cfg, st, now)
		m := map[string]any{
			"profile": v.in.Name, "pool": v.pool, "thresholds": thresholdsJSON(v.cfg),
			"decision": decisionJSON(dec), "accounts": verdictsJSON(verdicts, v.cfg, st, now, r),
		}
		addBest(m, in, v.in.Name, cfg, st, now)
		addRunwayJSON(m, r)
		markCurrent(m, cfg, v.in.Name)
		list = append(list, m)
	}
	return map[string]any{"profiles": list}
}

// addBest adds "best" and "best_why" for one profile's policy input, and
// M12's "on_best", "active_room" and "best_room" (policy.Choose; a room is
// null when unknown, never 0).
func addBest(m map[string]any, in policy.Input, name string, cfg *config.Config, st *state.State, now time.Time) {
	in.Unavailable = bestExclusions(cfg, st, name, now)
	c := policy.Choose(in, name)
	m["best"], m["best_why"] = orNull(c.ID), orNull(c.Why)
	m["on_best"], m["active_room"], m["best_room"] = c.OnBest, roomJSON(c.ActiveRoom), roomJSON(c.BestRoom)
}

// roomJSON is a room for JSON: its points, or null when unknown.
func roomJSON(r *float64) any {
	if r == nil {
		return nil
	}
	return *r
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
		if st.Accounts[a.ID].NeedsSignIn(now) {
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

// headerOn says whether to open a view with the brand header. Only on a
// terminal: piped output and --json stay exactly as they were.
var headerOn = func() bool { return isTTY(os.Stdout) }

// printHeader writes the brand header for status, why and doctor, when
// headerOn allows it.
func printHeader(w io.Writer, st *state.State) {
	if headerOn() {
		fmt.Fprint(w, brandHeader(st, daemonRunning(), time.Now()))
	}
}

// brandHeader is the header itself: version, the daemon's real state, and the
// age of the newest reading of any account.
func brandHeader(st *state.State, running bool, now time.Time) string {
	daemon := "daemon not running"
	var polled time.Time
	if st != nil {
		if running {
			daemon = "daemon dry-run"
			if st.DaemonLive {
				daemon = "daemon live"
			}
		}
		for _, a := range st.Accounts {
			if a != nil && a.LastAt.After(polled) {
				polled = a.LastAt
			}
		}
	} else if running {
		daemon = "daemon running"
	}
	return render.Header(version, daemon, polled, now)
}
