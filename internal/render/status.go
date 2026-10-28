// Package render draws the human-facing status view.
//
// Rules this view must never break: a number is shown only if it was actually
// observed, its age is visible when it is stale, and an account that could not
// be read reads "unknown" rather than blank or zero.
package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

type Options struct {
	Cfg         *config.Config
	St          *state.State
	Budget      *usage.Budget
	Degraded    bool
	DegradedWhy string
	// DaemonOwns means a daemon is running and is responsible for polling, so
	// this view reports what it observed rather than a fresh read of our own.
	DaemonOwns bool
	// Switches are the most recent rotations, newest last, read from the audit
	// log. The audit log is the single record of what happened — state and the
	// log disagreeing produced a footer reading "last switch never" directly
	// beneath a list of switches.
	Switches []audit.Event
	// Decision is what the policy engine says right now, so the headline can
	// state what happens next rather than leaving it to be inferred.
	Decision *policy.Decision
	// Known marks account ids still present in the config, so a switch involving
	// one that has since been renamed or removed can say so rather than look
	// like bad data.
	Known map[string]bool
	// Detail restores the per-account continuation lines. The default is one
	// line per account: the table is read far more often than it is
	// interrogated, and the bars took most of the width to say what the number
	// already said.
	Detail bool
	// Plans maps account id to its rendered subscription, e.g. "Max 20x". A 5x
	// seat exhausts four times sooner than a 20x one, so it belongs next to the
	// numbers rather than a command away.
	Plans map[string]string
	// Vaulted lists accounts that have a stored credential. Without it, an
	// account that is vaulted but not yet polled is indistinguishable from one
	// that was never set up — and telling the user "no credential" when there is
	// one is worse than saying nothing.
	Vaulted map[string]bool
	// Profile, when set, is the Claude Code profile this view is for: its
	// active account is the one marked and headlined. Empty means the default
	// profile, which is every view from before profiles.
	Profile string
	// Pool, when non-nil, limits the accounts shown to that profile's pool.
	Pool []string
	// Pools is how many pools the profile lists (D31); with more than one,
	// the runway is theirs together, and the line says so.
	Pools int
	// Runway is the profile's pool runway forecast (IMPROVEMENTS F2): one
	// line under the table when it forecasts something, and each account's
	// ETA under --detail. Nil says nothing.
	Runway *policy.Runway
	// Block marks this view as one of several, one per profile: it ends
	// without the footer and legend, which SharedFooter prints once after the
	// last block.
	Block bool
}

// active is the account the view's live credential holds.
func (o Options) active() string {
	if o.Profile == "" {
		return o.St.Default().Active
	}
	if in := o.St.Profiles[o.Profile]; in != nil {
		return in.Active
	}
	return ""
}

// inUseBy names the other profile an account is live in, "" when none. A
// shared account (D31) is used by one profile at a time (D34), so every other
// profile that may use it shows it as in use there, never as ready.
func (o Options) inUseBy(id string) string {
	return InUseBy(o.Cfg, o.St, o.Profile, id)
}

// InUseBy names the profile other than self (default when "") that state
// records id live in, "" when none.
func InUseBy(cfg *config.Config, st *state.State, self, id string) string {
	if self == "" {
		self = state.DefaultProfile
	}
	if st == nil || id == "" {
		return ""
	}
	for _, name := range cfg.ProfileNames() {
		if ps := st.Profiles[name]; name != self && ps != nil && ps.Active == id {
			return name
		}
	}
	return ""
}

// judgement is an account as the policy judges it: a counted model's weekly
// limit standing in for the week (IMPROVEMENTS I6), the window nearest its
// own trigger (usage.WorstAgainst), and that window's figure carried
// forward by the burn rate when the account may be (State.Projects). The
// figure shown, the verdict beside it and the headline all use fig, so the
// three cannot disagree.
type judgement struct {
	eff       *state.Account // with counted models; acct itself without
	model     string         // the counted model setting the weekly figure, or ""
	which     string         // usage.FiveHourKey or usage.SevenDayKey; "" with no figure
	read      float64        // which's figure as read
	fig       float64        // which's figure now: read, carried forward when projected
	projected bool           // fig, rounded as shown, differs from read
}

func judge(cfg *config.Config, st *state.State, id string, acct *state.Account, now time.Time) judgement {
	eff, model, _ := acct.WithModels(cfg.Models)
	j := judgement{eff: eff, model: model}
	j.which, j.read, _ = eff.Last.WorstAgainst(cfg.TriggerFor(usage.FiveHourKey), cfg.TriggerFor(usage.SevenDayKey))
	j.fig = j.read
	if st.Projects(id, now, cfg.PollActive.Duration) {
		j.fig = max(eff.ProjectedIn(j.which, now), j.read)
	}
	// Marked only when carrying it forward changes what is shown: a "~"
	// beside the figure as read says it was projected when it was not.
	j.projected = fmt.Sprintf("%.0f", j.fig) != fmt.Sprintf("%.0f", j.read)
	return j
}

// resetOf is when the judged window clears.
func (j judgement) resetOf() *time.Time {
	if j.which == usage.SevenDayKey {
		return j.eff.Last.SevenDay.ResetsAt
	}
	return j.eff.Last.FiveHour.ResetsAt
}

// accounts are the configured accounts this view shows, in priority order.
func (o Options) accounts() []config.Account {
	all := o.Cfg.Ordered()
	if o.Pool == nil {
		return all
	}
	in := map[string]bool{}
	for _, id := range o.Pool {
		in[id] = true
	}
	var out []config.Account
	for _, a := range all {
		if in[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

func bar(pct float64, w int) string {
	f := int(pct/100*float64(w) + 0.5)
	if f > w {
		f = w
	}
	if f < 0 {
		f = 0
	}
	return "[" + strings.Repeat("#", f) + strings.Repeat(".", w-f) + "]"
}

func until(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return shortDur(time.Until(*t))
}

// shortDur renders a duration the way someone reads a clock, not the way a
// computer counts. "81h49m" is arithmetic; "3d 10h" is an answer.
func shortDur(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// span is a configured span as a person reads it: shortDur, with zero as
// "0s" rather than "now", since it is a length, not a moment.
func span(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return shortDur(d)
}

// windowFlag is a detail window's flag: " over" at or past its trigger,
// " reserve" past the account's reserve, "" otherwise.
func windowFlag(w usage.Window, cfg *config.Config, reserve float64, weekly bool) string {
	if !w.Known() {
		return ""
	}
	trigger := cfg.TriggerFor(usage.FiveHourKey)
	if weekly {
		trigger = cfg.TriggerFor(usage.SevenDayKey)
	}
	switch p := w.Pct(); {
	case p >= trigger:
		return " over"
	case reserve > 0 && p > reserve:
		return " reserve"
	}
	return ""
}

// windowCell is a detail window: a bar of barW, the figure, and its flag.
func windowCell(w usage.Window, cfg *config.Config, reserve float64, weekly bool, barW int) string {
	if !w.Known() {
		return strings.Repeat(" ", barW+2) + "     -"
	}
	return fmt.Sprintf("%s %5.1f%%%s", bar(w.Pct(), barW), w.Pct(), windowFlag(w, cfg, reserve, weekly))
}

// Status writes the whole view.
func Status(out io.Writer, o Options) {
	if o.Detail {
		statusDetailed(out, o)
		return
	}
	statusCompact(out, o)
}

// pct renders a utilization, marking the ones that need a second look: "!" when
// the API itself flags the window, "~" when the figure is projected forward from
// an older reading rather than freshly observed.
func pct(v float64, flagged, projected bool, switchAt float64, severity string) string {
	s := fmt.Sprintf("%.0f%%", v)
	if projected {
		s = "~" + s
	}
	// The flag has a column of its own after the figure, blank when there
	// is none: taking one from the figure's left put "97%!" a column left of
	// every unflagged figure. Pad before colouring: escape codes have no
	// width, so a coloured cell padded by Printf comes out short.
	flag := " "
	if flagged {
		flag = "!"
	}
	return paint(levelFor(v, switchAt, severity), fmt.Sprintf("%5s%s", s, flag))
}

// severityName returns the API's own grading for a window, which outranks our
// arithmetic: it comes from the system that will do the refusing.
func severityName(u *usage.Usage, which string) string {
	if u == nil {
		return ""
	}
	for _, l := range u.Limits {
		if which == "five_hour" && l.Group == "session" {
			return l.Severity
		}
		if which == "seven_day" && l.Group == "weekly" && l.Class() != usage.LimitModelWeekly {
			return l.Severity
		}
	}
	return ""
}

// critical names a window the API grades critical while it is under its
// trigger, "" for none: its figure is red, and its state may still be
// available.
func critical(u *usage.Usage, cfg *config.Config) string {
	for _, key := range []string{usage.FiveHourKey, usage.SevenDayKey} {
		w := u.FiveHour
		if key == usage.SevenDayKey {
			w = u.SevenDay
		}
		if strings.EqualFold(severityName(u, key), "critical") && w.Known() && w.Pct() < cfg.TriggerFor(key) {
			return WindowName(key)
		}
	}
	return ""
}

// severityOf reports whether the API flagged the window that drives this
// account's state.
func severityOf(u *usage.Usage, which string) bool {
	if u == nil {
		return false
	}
	for _, l := range u.Limits {
		if l.Severity == "" || l.Severity == "normal" {
			continue
		}
		if which == "five_hour" && l.Group == "session" {
			return true
		}
		if which == "seven_day" && l.Group == "weekly" && l.Class() != usage.LimitModelWeekly {
			return true
		}
	}
	return false
}

// lockedAccounts names how many accounts are backing off. The budget knows
// credentials, not account names, and a count is all it can honestly give.
func lockedAccounts(n int) string {
	if n == 1 {
		return "one account"
	}
	return fmt.Sprintf("%d accounts", n)
}

// backingOff reports whether an account's last error is a refusal it is now
// waiting out. Locks are per account, so another account's lock does not
// explain this one's old figures.
func backingOff(lastErr string) bool {
	return strings.Contains(lastErr, "rate limited") || strings.Contains(lastErr, "after a 429")
}

// holdingOffReason describes a deliberate pause on calling the usage API, or ""
// when there is none. The daemon backs off when the API refuses it, and during
// that window every reading legitimately goes stale.
func holdingOffReason(b *usage.Budget) string {
	if b == nil {
		return ""
	}
	til, n := b.AnyLocked()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("holding off after the API refused %s, resuming by %s",
		lockedAccounts(n), When(til, time.Now()))
}

func statusCompact(out io.Writer, o Options) {
	cfg, st := o.Cfg, o.St
	fmt.Fprintln(out)

	accounts := o.accounts()
	if len(accounts) == 0 {
		fmt.Fprintf(out, "  no accounts configured. See %s\n\n", cfg.Path)
		return
	}

	nameW := len("ACCOUNT")
	for _, a := range accounts {
		if n := len(a.Name()); n > nameW {
			nameW = n
		}
	}
	// STATE is sized to what it will say, so the optional columns are chosen
	// against the table as it will be drawn: a fixed guess let a long state
	// push the table past the terminal.
	stateW := len("STATE")
	for _, a := range accounts {
		stateW = max(stateW, visibleLen(o.stateCell(a, false)))
	}
	lay := layoutFor(terminalWidth(), nameW, stateW)

	for _, l := range Wrap(headline(o), terminalWidth()-2) {
		fmt.Fprintf(out, "  %s\n", l)
	}
	fmt.Fprintln(out)

	// Columns are assembled to fit the terminal. A table that wraps is worse
	// than a narrower one: wrapping destroys the alignment the eye relies on.
	head := []string{"", "ACCOUNT"}
	right := map[int]bool{}
	if lay.plan {
		head = append(head, "PLAN")
	}
	head = append(head, "SESSION")
	right[len(head)-1] = true
	if lay.dots > 0 {
		head = append(head, "")
	}
	head = append(head, "WEEK")
	right[len(head)-1] = true
	if lay.dots > 0 {
		head = append(head, "")
	}
	if lay.clears {
		head = append(head, "CLEARS")
		right[len(head)-1] = true
	}
	head = append(head, "STATE")
	t := &table{head: head, right: right}
	var warnings []string
	holdingOff := holdingOffReason(o.Budget)

	for _, a := range accounts {
		acct := st.Accounts[a.ID]
		plan := nonEmpty(o.Plans[a.ID], "-")

		marker := " "
		nameCell := a.Name()
		if a.ID == o.active() {
			// Which account you are on should be visible without reading words.
			marker = paint(glacier, "▸")
			nameCell = paint(bold, nameCell)
		}

		stateText := o.stateCell(a, lay.burn)
		if acct == nil || acct.Last == nil {
			switch {
			case o.inUseBy(a.ID) != "":
				// Another profile's for now; its STATE says so.
			case !o.Vaulted[a.ID]:
				warnings = append(warnings, fmt.Sprintf("%s is not set up: claudeswitch login %s --direct", a.ID, a.ID))
			case acct != nil && acct.LastErr != "":
				warnings = append(warnings,
					fmt.Sprintf("%s could not be read: %s", a.ID, truncate(acct.LastErr, 60)))
			}
			row := []string{marker, nameCell}
			if lay.plan {
				row = append(row, plan)
			}
			row = append(row, "-")
			if lay.dots > 0 {
				row = append(row, "")
			}
			row = append(row, "-")
			if lay.dots > 0 {
				row = append(row, "")
			}
			if lay.clears {
				row = append(row, "-")
			}
			t.add(append(row, colorState(stateText))...)
			continue
		}

		j := judge(cfg, st, a.ID, acct, time.Now())
		// The judged window shows the figure carried forward, when that
		// changes it; the other window shows its reading.
		fiveV, sevenV := acct.Last.FiveHour.Pct(), acct.Last.SevenDay.Pct()
		if j.projected && j.which == usage.FiveHourKey {
			fiveV = j.fig
		}
		if j.projected && j.which == usage.SevenDayKey {
			sevenV = j.fig
		}

		five := pct(fiveV, severityOf(acct.Last, "five_hour"),
			j.projected && j.which == usage.FiveHourKey, cfg.TriggerFor(usage.FiveHourKey),
			severityName(acct.Last, "five_hour"))
		seven := pct(sevenV, severityOf(acct.Last, "seven_day"),
			j.projected && j.which == usage.SevenDayKey, cfg.TriggerFor(usage.SevenDayKey),
			severityName(acct.Last, "seven_day"))

		// CLEARS is the reset of the window the account is judged on — the
		// one STATE speaks of — or, for a refused account, when the refusal
		// lifts.
		clears := until(j.resetOf())
		if acct.Availability(a.Reserve) == state.Burnt {
			clears = until(&acct.BurntTil)
		}

		// Figures for an account that cannot be read again are leftovers. They
		// are kept, because knowing where it stood is useful, but greyed: shown
		// in the same colours as a live account they read as current, and a
		// dead account looked like a healthy one with room to spare.
		frozen := needsLogin(acct.LastErr)
		if frozen {
			five, seven = paint(grey, stripColor(five)), paint(grey, stripColor(seven))
		}
		bar := func(pctVal, trigger float64, which string) string {
			return dotBar(pctVal, trigger, lay.dots,
				levelFor(pctVal, trigger, severityName(acct.Last, which)), frozen)
		}

		row := []string{marker, nameCell}
		if lay.plan {
			row = append(row, plan)
		}
		row = append(row, five)
		if lay.dots > 0 {
			row = append(row, bar(acct.Last.FiveHour.Pct(), cfg.TriggerFor(usage.FiveHourKey), "five_hour"))
		}
		row = append(row, seven)
		if lay.dots > 0 {
			row = append(row, bar(acct.Last.SevenDay.Pct(), cfg.TriggerFor(usage.SevenDayKey), "seven_day"))
		}
		if lay.clears {
			row = append(row, clears)
		}
		t.add(append(row, colorState(stateText))...)

		// Anything the reader should act on goes to the warnings block rather
		// than crowding the row it belongs to.
		// "the poller is behind" is only true when the poller is trying and not
		// keeping up. When the last attempt failed, the daemon already knows
		// exactly why, and printing a generic lag message instead throws away
		// the one line that tells the reader what to do about it.
		// An account needing a sign-in is reported whatever the age of its
		// figures. Hanging this off staleness meant the one condition a person
		// has to act on appeared only once a timer had also elapsed, and the
		// row meanwhile showed a headroom verdict computed from numbers that
		// could no longer be refreshed.
		// Red beside "available" needs saying: the API grades the window
		// critical while it is under the trigger rotation goes by.
		if w := critical(acct.Last, cfg); w != "" {
			warnings = append(warnings, fmt.Sprintf("%s's %s, which the API grades critical, is under its trigger; "+
				"rotation goes by the trigger", a.ID, w))
		}
		if fix := loginFix(acct.LastErr); fix != "" {
			warnings = append(warnings, fmt.Sprintf("%s needs sign-in: %s", a.ID, fmt.Sprintf(fix, a.ID)))
		} else if age := time.Since(acct.LastAt); age > staleAfter(cfg, a.ID == o.active()) {
			switch {
			case holdingOff != "" && backingOff(acct.LastErr):
				// Deliberately not polling is not the same as failing to keep
				// up, and showing it as a fault sends the reader looking for a
				// broken daemon. This is the one case where the figures being
				// old is the system working.
				warnings = append(warnings, fmt.Sprintf(
					"%s reading is %s old — %s", a.ID, shortDur(age), holdingOff))
			case acct.LastErr != "":
				warnings = append(warnings, fmt.Sprintf("%s has not been readable for %s — %s",
					a.ID, shortDur(age), firstLine(acct.LastErr)))
			default:
				warnings = append(warnings, fmt.Sprintf("%s reading is %s old — the poller is behind",
					a.ID, shortDur(age)))
			}
		}
		if !acct.RefreshExpiry.IsZero() && time.Until(acct.RefreshExpiry) < 7*24*time.Hour {
			warnings = append(warnings, fmt.Sprintf(
				"%s's sign-in expires in %s; sign in again before then: claudeswitch login %s --direct",
				a.ID, shortDur(time.Until(acct.RefreshExpiry)), a.ID))
		}
		warnings = append(warnings, unknownLimitWarnings(a.ID, acct.Last)...)
	}

	fmt.Fprint(out, t.render("  "))
	modelBlock(out, o, accounts)
	if line := poolLine(o, false, time.Now()); line != "" {
		fmt.Fprintln(out)
		for _, l := range Wrap(line, terminalWidth()-2) {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}

	if len(o.Switches) > 0 {
		fmt.Fprintf(out, "\n  %s\n", paint(dim, "RECENT SWITCHES"))
		// The reason goes last and is cut to the terminal's width.
		st := (&table{head: []string{"", "", "", ""}}).Fit(terminalWidth())
		now := time.Now()
		// Newest first: the most recent rotation is the one being looked for.
		for i := len(o.Switches) - 1; i >= 0; i-- {
			e := o.Switches[i]
			// One format for every row, with the date once it is past a
			// week: "Mon 15:04" for a switch twelve days old named a day it
			// was not.
			when := When(e.At, now)
			name := func(id string) string {
				if id == "" {
					return "?"
				}
				if o.Known != nil && !o.Known[id] {
					// Renamed or removed since. Say so, rather than leave a
					// reader wondering why an unknown account is listed.
					return id + paint(grey, " (gone)")
				}
				return id
			}
			reason := paint(grey, shortReason(e.Reason))
			if e.Forced {
				// Ahead of the reason, which is what a narrow terminal cuts.
				reason = paint(orange, "forced") + paint(grey, " · ") + reason
			}
			st.add(paint(grey, when), name(e.From)+" → "+paint(bold, name(e.To)), reason)
		}
		// No header row for this one: the columns speak for themselves.
		st.head = nil
		fmt.Fprint(out, st.render("  "))
	}

	// A configuration that does not do what its numbers say belongs here rather
	// than in a log nobody reads: the footer prints both thresholds side by
	// side, so the contradiction is on screen already and only the consequence
	// is missing.
	warnings = append(warnings, cfg.Warnings()...)
	if o.Degraded {
		warnings = append(warnings,
			"usage API shape changed — predictive rotation DISABLED, reactive only: "+truncate(o.DegradedWhy, 50))
	}
	if o.Budget != nil {
		if til, n := o.Budget.AnyLocked(); n > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"usage API refused %s; readings resume within %s (figures above are the last good ones)",
				lockedAccounts(n), shortDur(time.Until(til))))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(out, "\n  %s\n", paint(dim, "WARNINGS"))
		for _, w := range warnings {
			// Each to the terminal's width, its later lines under its first.
			for i, l := range Wrap(w, terminalWidth()-4) {
				if i == 0 {
					fmt.Fprintf(out, "  %s %s\n", paint(orange, "⚠"), l)
				} else {
					fmt.Fprintf(out, "    %s\n", l)
				}
			}
		}
	}

	fmt.Fprintln(out)
	if o.Block {
		return // SharedFooter closes the whole view once
	}
	fmt.Fprintf(out, "  %s\n", paint(grey, daemonLine(o)))
	footer(out, o)
}

// stateCell is an account's STATE as its row shows it; withBurn adds the burn
// rate, where there is room for it. The marker says which account is active,
// so STATE does not have to.
func (o Options) stateCell(a config.Account, withBurn bool) string {
	cfg, st := o.Cfg, o.St
	acct := st.Accounts[a.ID]
	if elsewhere := o.inUseBy(a.ID); elsewhere != "" {
		// Another profile's for now (D34), whatever its reading says.
		return "in use by " + elsewhere
	}
	if acct == nil || acct.Last == nil {
		switch {
		case !o.Vaulted[a.ID]:
			return "not set up"
		case acct != nil && acct.LastErr != "":
			return "unreadable"
		}
		return "not polled yet"
	}
	// Only a live account, or one read within poll_active, projects: an
	// idle account's figure must not climb while nobody spends it. The
	// landing margin is asked of a target only, and the active account is
	// not one.
	projects := st.Projects(a.ID, time.Now(), cfg.PollActive.Duration)
	s := stateOf(acct, a, cfg, judge(cfg, st, a.ID, acct, time.Now()), a.ID != o.active(), withBurn && projects)
	if bill := acct.Last.Billing(); bill != "" {
		s += " · " + paint(amber, bill)
	}
	return s
}

// SharedFooter closes a view made of several Block views, once: the daemon
// note, when a rotation goes and the budget, and the legend. Each block's
// thresholds and hard floor are in its own header, so they are not repeated
// here.
func SharedFooter(out io.Writer, o Options) {
	fmt.Fprintf(out, "  %s\n", paint(grey, daemonLine(o)))
	parts := []string{"rotates " + Timing(o.Cfg)}
	if o.Budget != nil {
		parts = append(parts, spare(o.Budget))
	}
	fmt.Fprint(out, Joined(parts, "  ", terminalWidth()))
	legend(out)
}

// spare is the calls the budget has spare, in words.
func spare(b *usage.Budget) string {
	return Plural(b.Remaining(), "API call", "API calls") + " spare"
}

// poolLine is the profile's runway (IMPROVEMENTS F2) in one line: "work
// pool: runs dry Thu 14:00 at this pace". The compact view says it only when
// there is a forecast; detail also says why there is none. "" for nothing.
func poolLine(o Options, detail bool, now time.Time) string {
	r := o.Runway
	if r == nil {
		return ""
	}
	name, runs, doesNot := "pool", "runs dry", "does not run dry"
	if o.Profile != "" {
		name = o.Profile + " pool"
	}
	if o.Pools > 1 {
		// "default pool" named one pool of several.
		name, runs, doesNot = o.Profile+"'s pools", "run dry", "do not run dry"
	}
	say := ""
	switch {
	case r.Kind == policy.RunwayDry && r.DryAt.After(now):
		say = runs + " " + clockAt(r.DryAt, now) + " at this pace"
	case r.Kind == policy.RunwayRefills:
		say = fmt.Sprintf("%s before %s resets %s at this pace", doesNot, r.RefillsAccount, clockAt(r.RefillsAt, now))
	case !detail:
		return ""
	case r.Kind == policy.RunwayDry:
		say = "no eligible account left"
	case r.Kind == policy.RunwayIdle:
		say = "not being spent"
	default:
		say = "runway unknown (too few recent readings)"
	}
	return name + ": " + say
}

// clockAt is a time a person reads off a clock: "today 14:00", "Thu 14:00",
// "28 Oct 14:00" (When).
func clockAt(t, now time.Time) string { return When(t, now) }

// headline is the single line worth reading first: where you stand, and what
// happens next.
func headline(o Options) string {
	st, cfg := o.St, o.Cfg
	acct := st.Accounts[o.active()]
	if o.active() == "" || acct == nil || acct.Last == nil {
		return paint(dim, "no reading for the active account yet")
	}
	// As its row judges it: the window nearest its own trigger, a counted
	// model standing in for the week, the figure carried forward.
	j := judge(cfg, st, o.active(), acct, time.Now())
	acct, which, proj, model := j.eff, j.which, j.fig, j.model
	window := WindowName(which)
	if which == usage.SevenDayKey && model != "" {
		window = model + " week"
	}

	level := levelFor(proj, cfg.TriggerFor(which), severityName(acct.Last, which))
	head := fmt.Sprintf("%s at %s of its %s", paint(bold, o.active()),
		paint(level, fmt.Sprintf("%.0f%%", proj)), window)

	// Only a live daemon acts on the decision: under a dry-run one, or none,
	// "rotating to" would be untrue.
	mode := o.daemonMode()
	switch {
	case o.Decision == nil:
		return head
	case o.Decision.Kind == "switch" && o.Decision.Held != "":
		// claude.ai work holds the switch (IMPROVEMENTS P4): it is wanted,
		// not under way.
		return head + paint(grey, " · ") + paint(amber, "waiting to rotate to "+o.Decision.Target) +
			paint(grey, " while claude.ai work goes on")
	case o.Decision.Kind == "switch" && mode == daemonLive:
		return head + paint(grey, " · ") + paint(glacier, "rotating to "+o.Decision.Target)
	case o.Decision.Kind == "switch" && mode == daemonDryRun:
		return head + paint(grey, " · ") + paint(glacier, "would rotate to "+o.Decision.Target) +
			paint(grey, " (the daemon is in dry-run)")
	case o.Decision.Kind == "switch":
		return head + paint(grey, " · ") + paint(glacier, "would rotate to "+o.Decision.Target) +
			paint(grey, " (no daemon running)")
	case o.Decision.Kind == "wait" && !o.Decision.RecoversAt.IsZero():
		return head + paint(grey, " · ") +
			paint(red, "every account is out") +
			paint(grey, fmt.Sprintf(" · %s returns in %s",
				o.Decision.RecoversAccount, shortDur(time.Until(o.Decision.RecoversAt))))
	case o.Decision.Kind == "wait":
		return head + paint(grey, " · ") + paint(red, "every account is out")
	}

	// Staying put. A pin turns rotation off, so room before a rotation that
	// will not come is no answer.
	if ps := o.profileState(); ps != nil && ps.Pinned != "" {
		return head + paint(grey, " · ") + paint(amber, "pinned") + paint(grey, "; automatic rotation is off")
	}
	// Otherwise say how much room is left, which is the reassuring form of
	// the same fact; only a live daemon rotates.
	left := cfg.TriggerFor(which) - proj
	switch {
	case left > 0 && mode == daemonLive:
		return head + paint(grey, fmt.Sprintf(" · %.0f points before it rotates", left))
	case left > 0:
		return head + paint(grey, fmt.Sprintf(" · %.0f points before its trigger", left))
	}
	// Over its trigger and staying: the cooldown, the landing margin or an
	// unreadable account holds it, and why says which.
	return head + paint(grey, " · over its trigger, staying for now (claudeswitch why says why)")
}

// The daemon's modes, as the headline and the footer speak of them.
const (
	daemonLive    = "live"
	daemonDryRun  = "dry-run"
	daemonStopped = "stopped"
)

// daemonMode is what acts on this view: a live daemon, one in dry-run, or
// none.
func (o Options) daemonMode() string {
	switch {
	case !o.DaemonOwns:
		return daemonStopped
	case o.St != nil && o.St.DaemonLive:
		return daemonLive
	}
	return daemonDryRun
}

// daemonLine is the footer's account of the daemon. Piped output has no
// brand header, so this is the one place it says whether anything acts on
// what the view shows.
func daemonLine(o Options) string {
	switch o.daemonMode() {
	case daemonLive:
		return "daemon live: it polls and rotates; these are its readings"
	case daemonDryRun:
		return "daemon in dry-run: it rotates nothing, only logs (claudeswitch daemon live)"
	}
	return "no daemon running: nothing rotates (claudeswitch daemon start)"
}

// profileState is the state of the profile this view is for.
func (o Options) profileState() *state.ProfileState {
	if o.St == nil {
		return nil
	}
	if o.Profile == "" {
		return o.St.Default()
	}
	return o.St.Profiles[o.Profile]
}

// shortReason trims the boilerplate out of a switch reason. "requested with
// `cs use`" five times in a row is not information.
func shortReason(r string) string {
	switch {
	case strings.Contains(r, "cs use"):
		return "manual"
	case strings.Contains(r, "restored after"):
		return "restored"
	case strings.Contains(r, "over the"):
		if i := strings.Index(r, "at "); i >= 0 {
			return r[i:]
		}
	}
	return r
}

// refillSoon is how near a reset has to be before "out" is better said as
// "back shortly". An hour is the span over which waiting is a real option: it
// is inside one sitting, and it is short enough that the number does not go
// stale between two glances at the table.
const refillSoon = time.Hour

// refillsIn is how long until the window that put this account over its trigger
// clears, and whether that is soon enough to be worth saying.
//
// Being over the trigger is not one fact but two. A five-hour window forty
// minutes from resetting comes back on its own before you have finished what
// you are doing; a weekly two days out does not. Both read "no headroom", in
// red, and the only thing separating them was a "5h"/"7d" suffix that says
// which window is full rather than when it empties. CLEARS carried the number
// all along — but it is the first column dropped on a narrow terminal, and a
// verdict that reads as terminal sends you hunting for another account you did
// not need.
//
// The window asked about is the one that put the account over, not whichever
// the API marks active: refilling the five-hour does nothing for an account
// that is out on its weekly.
func refillsIn(acct *state.Account, which string, now time.Time) (time.Duration, bool) {
	if acct.Last == nil {
		return 0, false
	}
	w := acct.Last.FiveHour
	if which == usage.SevenDayKey {
		w = acct.Last.SevenDay
	}
	if w.ResetsAt == nil {
		return 0, false
	}
	// A reset already in the past means the reading has outlived its window;
	// that is ExpiredAt's story to tell, and it is told before this is reached.
	d := w.ResetsAt.Sub(now)
	if d <= 0 || d > refillSoon {
		return 0, false
	}
	return d, true
}

// stateOf is the one-phrase verdict for an account, judged as j says. target
// asks the landing margin of it: an account rotation could land on.
func stateOf(acct *state.Account, a config.Account, cfg *config.Config, j judgement, target, withBurn bool) string {
	which, proj := j.which, j.fig
	window := WindowName(which)
	avail := acct.Availability(a.Reserve)
	switch {
	case needsLogin(acct.LastErr):
		// Nothing else in this row can be trusted: the figures are whatever was
		// last readable, and no amount of waiting will refresh them.
		return "needs sign-in"
	case acct.ExpiredAt(time.Now()):
		// The window this reading describes has since refilled, so the figure
		// says nothing about the account now.
		return "window reset · re-reading"
	case avail == state.Burnt:
		return "refused · " + WindowName(acct.BurntWin)
	case avail == state.Reserved:
		return "reserved · " + window
	case proj >= cfg.TriggerFor(which):
		if d, ok := refillsIn(acct, which, time.Now()); ok {
			return "refills in " + shortDur(d)
		}
		return "no headroom · " + window
	case target && avail == state.Available && shortOfMargin(acct, cfg):
		// Under its triggers, but a rotation will not land here: one point
		// of session room is the same failure one rotation later (the
		// policy's landing margin, IMPROVEMENTS A1).
		return "short of margin"
	}
	s := string(avail)
	if withBurn {
		if rate := acct.BurnRate(); rate > 0 {
			s += paint(grey, fmt.Sprintf(" · %.1f%%/min", rate))
		}
	}
	return s
}

// shortOfMargin reports whether an account's session window sits less than
// landing_margin points below the session trigger, on its reading, as the
// policy judges a target (policy.sessionRoom). A session with no figure
// imposes no margin.
func shortOfMargin(acct *state.Account, cfg *config.Config) bool {
	if acct.Last == nil || !acct.Last.FiveHour.Known() {
		return false
	}
	return cfg.TriggerFor(usage.FiveHourKey)-acct.Last.FiveHour.Pct() < cfg.Margin()
}

func footer(out io.Writer, o Options) {
	fmt.Fprint(out, Joined(rotationParts(o.Cfg, o.Budget), "  ", terminalWidth()))
	legend(out)
}

// rotationParts are the footer's account of when the daemon rotates: the
// triggers, the timing, the hard floor and the calls spare.
func rotationParts(cfg *config.Config, b *usage.Budget) []string {
	parts := []string{Thresholds(cfg), Timing(cfg), HardFloor(cfg)}
	if b != nil {
		parts = append(parts, spare(b))
	}
	return parts
}

func legend(out io.Writer) {
	// Every mark the table draws, said once.
	legend := strings.TrimRight(Joined([]string{"▸ the live account", dotLit + " used", dotThreshold + " its trigger",
		"! flagged by the API", "~ carried forward from the last reading"}, "  ", terminalWidth()), "\n")
	if useColor {
		// These four are colour swatches, not words to read in sequence. Without
		// a label and separators they run together into "ok climbing close act
		// now", which is what the line looks like the moment colour is stripped
		// — by a pipe, a paste, or a terminal that does not support it.
		legend += "\n  colour:  " + paint(meltwater, "ok") + "  ·  " + paint(amber, "climbing") +
			"  ·  " + paint(orange, "close") + "  ·  " + paint(red, "act now")
	}
	fmt.Fprintln(out, legend)
	fmt.Fprintln(out)
}

// detailRow is one account in status --detail: its cells, and the notes
// under it.
type detailRow struct {
	name       string
	five, week *usage.Window // nil: no reading
	reserve    float64
	clears     string
	state      string
	notes      []string
}

// statusDetailed is the previous view, kept behind --detail for when a number
// needs interrogating rather than glancing at.
func statusDetailed(out io.Writer, o Options) {
	cfg, st := o.Cfg, o.St
	width := terminalWidth()
	fmt.Fprintln(out)

	if o.Degraded {
		fmt.Fprintf(out, "  ⚠ usage API returned an unexpected shape. Predictive rotation is DISABLED;\n")
		fmt.Fprintf(out, "    running reactive-only, so brief pauses at limits are possible.\n")
		for _, l := range Wrap("detail: "+o.DegradedWhy, width-4) {
			fmt.Fprintf(out, "    %s\n", l)
		}
		fmt.Fprintf(out, "    run `claudeswitch doctor` for the full picture.\n\n")
	}
	if o.Budget != nil {
		if til, n := o.Budget.AnyLocked(); n > 0 {
			fmt.Fprintf(out, "  ⚠ usage API refused %s; readings resume within %s (this is expected,\n",
				lockedAccounts(n), shortDur(time.Until(til)))
			fmt.Fprintf(out, "    not a failure — figures below are the last good ones).\n\n")
		}
	}

	accounts := o.accounts()
	if len(accounts) == 0 {
		fmt.Fprintf(out, "  no accounts configured. See %s\n\n", cfg.Path)
		return
	}

	var rows []detailRow
	// The live credential may not match any configured account. Show it plainly
	// rather than filing it under a guess.
	if un, ok := st.Accounts[Unattributed]; ok && un.Last != nil {
		rows = append(rows, detailRow{name: "(live)", five: &un.Last.FiveHour, week: &un.Last.SevenDay,
			clears: until(un.Last.FiveHour.ResetsAt), state: "ACTIVE, unattributed",
			notes: []string{fmt.Sprintf("org %s is not in your config. Add org_id = %q to the account it "+
				"belongs to, so its usage is tracked by name.", shortID(un.OrgID), un.OrgID)}})
	}
	for _, a := range accounts {
		rows = append(rows, o.detailRowOf(a))
	}

	// Columns sized to what they hold, and the bars to the room left, so
	// the view fits the terminal: at a fixed 22 dots it ran past 100 columns.
	nameW, clearsW, stateW, flagW := len("ACCOUNT"), len("CLEARS"), len("STATE"), 0
	for _, r := range rows {
		nameW, clearsW = max(nameW, visibleLen(r.name)), max(clearsW, visibleLen(r.clears))
		stateW = max(stateW, visibleLen(r.state))
		for _, w := range []*usage.Window{r.five, r.week} {
			if w != nil {
				flagW = max(flagW, visibleLen(windowFlag(*w, cfg, r.reserve, w == r.week)))
			}
		}
	}
	// A window cell is "[bar] 100.0%" and its flag.
	cellW := (width - (6 + nameW + clearsW + min(stateW, 20))) / 2
	barW := min(max(cellW-9-flagW, 6), 22)
	cellW = barW + 9 + flagW
	stateAt := 2 + nameW + 1 + cellW + 1 + cellW + 1 + clearsW + 1

	cell := func(w *usage.Window, weekly bool, reserve float64) string {
		if w == nil {
			return ""
		}
		return windowCell(*w, cfg, reserve, weekly, barW)
	}
	line := func(name, five, week, clears, state string) {
		l := "  " + padRight(name, nameW) + " " + padRight(five, cellW) + " " + padRight(week, cellW) + " " +
			padRight(clears, clearsW) + " "
		parts := Wrap(state, max(width-stateAt, 12))
		fmt.Fprintln(out, strings.TrimRight(l+parts[0], " "))
		for _, p := range parts[1:] {
			fmt.Fprintln(out, strings.Repeat(" ", stateAt)+p)
		}
	}
	line("ACCOUNT", "SESSION", "WEEK", "CLEARS", "STATE")
	notePad := strings.Repeat(" ", 2+nameW+1)
	for _, r := range rows {
		line(r.name, cell(r.five, false, r.reserve), cell(r.week, true, r.reserve), r.clears, r.state)
		for _, n := range r.notes {
			marker := "↳ "
			if strings.HasPrefix(n, "⚠ ") {
				marker, n = "⚠ ", strings.TrimPrefix(n, "⚠ ")
			}
			parts := WrapPhrases(n, max(width-len(notePad)-2, 20))
			fmt.Fprintln(out, notePad+marker+parts[0])
			for _, p := range parts[1:] {
				fmt.Fprintln(out, notePad+"  "+p)
			}
		}
	}

	if line := poolLine(o, true, time.Now()); line != "" {
		fmt.Fprintln(out)
		for _, l := range Wrap(line, width-2) {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, Joined(rotationParts(cfg, nil), "  ", width))
	if o.Budget != nil {
		// The window's real size: api_budget for each profile in use.
		for _, l := range Wrap(fmt.Sprintf("api budget: %s available now (%d per %s, one held back for rotations)",
			Plural(o.Budget.Remaining(), "scheduled call", "scheduled calls"), o.Budget.Allowance(),
			span(o.Budget.Window())), width-2) {
			fmt.Fprintf(out, "  %s\n", l)
		}
	}
	fmt.Fprintf(out, "  %s\n", paint(grey, daemonLine(o)))
	fmt.Fprintln(out)
}

// detailRowOf is one configured account's row in status --detail.
func (o Options) detailRowOf(a config.Account) detailRow {
	cfg, st := o.Cfg, o.St
	r := detailRow{name: a.Name(), reserve: a.Reserve, clears: "-"}
	acct := st.Accounts[a.ID]
	elsewhere := o.inUseBy(a.ID)
	if acct == nil {
		r.state = "not set up: claudeswitch login " + a.ID + " --direct"
		if o.Vaulted[a.ID] {
			r.state = "vaulted, not polled yet"
		}
		if elsewhere != "" {
			r.state = "in use by " + elsewhere
		}
		return r
	}
	avail := acct.Availability(a.Reserve)
	stateStr := string(avail)
	// "available" must not be shown for an account with no headroom. The
	// policy engine already refuses to rotate into one (firstEligible skips
	// anything at or over the trigger), but the status column said
	// "available" beside a 100% bar, which contradicted itself.
	// Judged as the compact row judges it, on the same figure.
	switch {
	case needsLogin(acct.LastErr):
		stateStr = "needs sign-in"
	case avail == state.Burnt:
		stateStr = "refused · " + WindowName(acct.BurntWin)
	case avail == state.Available && acct.Last != nil:
		switch j := judge(cfg, st, a.ID, acct, time.Now()); {
		case j.fig >= cfg.TriggerFor(j.which):
			stateStr = fmt.Sprintf("no headroom (%.0f%%)", j.fig)
			if d, ok := refillsIn(acct, j.which, time.Now()); ok {
				stateStr = fmt.Sprintf("refills in %s (%.0f%%)", shortDur(d), j.fig)
			}
		case a.ID != o.active() && elsewhere == "" && shortOfMargin(acct, cfg):
			stateStr = "short of margin"
		}
	}
	if a.ID == o.active() {
		stateStr = "ACTIVE " + stateStr
	}
	if elsewhere != "" {
		stateStr = "in use by " + elsewhere
	}
	if acct.Last == nil {
		// An account that was never set up is not an error to report at the
		// user; say what to do about it instead of surfacing a raw keychain
		// message.
		detail := "not set up: claudeswitch login " + a.ID + " --direct"
		if o.Vaulted[a.ID] {
			detail = acct.LastErr
			if detail == "" {
				detail = "not polled yet"
			}
		}
		r.state = detail
		if o.Vaulted[a.ID] {
			r.state = "unknown (" + truncate(detail, 40) + ")"
		}
		if elsewhere != "" {
			r.state = stateStr
		}
		return r
	}
	r.five, r.week, r.state = &acct.Last.FiveHour, &acct.Last.SevenDay, stateStr
	// CLEARS as in the compact view: the judged window's reset, or when a
	// refusal lifts.
	r.clears = until(judge(cfg, st, a.ID, acct, time.Now()).resetOf())
	if avail == state.Burnt {
		r.clears = until(&acct.BurntTil)
	}
	if fix := loginFix(acct.LastErr); fix != "" {
		r.notes = append(r.notes, fmt.Sprintf(fix, a.ID))
	}

	// Always state the age. The poll interval is longer than the old stale
	// threshold, so a figure could be minutes out of date with nothing to
	// say so — and under heavy use minutes matter (2026-09-10: 8 points in
	// 3 minutes).
	age := time.Since(acct.LastAt)
	projects := st.Projects(a.ID, time.Now(), cfg.PollActive.Duration)
	if rate := acct.BurnRate(); rate > 0 && projects {
		r.notes = append(r.notes, fmt.Sprintf("read %s ago, burning %.1f%%/min → about %.0f%% now",
			span(age), rate, acct.Projected(time.Now())))
	} else {
		r.notes = append(r.notes, fmt.Sprintf("read %s ago", span(age)))
	}
	if b := acct.Last.Binding(); b != nil {
		flag := ""
		if b.Severity != "normal" && b.Severity != "" {
			// Anthropic's own early warning. Observed at 78% on 2026-09-09.
			flag = "  ⚠ the API itself is flagging this"
		}
		kind := WindowName(b.Kind)
		if m := b.ModelName(); m != "" {
			kind = m + " week"
		}
		r.notes = append(r.notes, fmt.Sprintf("binding: %s (%s) %s, resets %s%s",
			kind, b.Severity, limitPct(*b), until(b.ResetsAt), flag))
	}
	if o.Runway != nil {
		if at, ok := o.Runway.TriggerAt[a.ID]; ok {
			r.notes = append(r.notes, "reaches its trigger "+clockAt(at, time.Now())+" at this pace")
		}
	}
	r.notes = append(r.notes, detailLimits(cfg, acct, projects)...)
	if !acct.RefreshExpiry.IsZero() && time.Until(acct.RefreshExpiry) < 7*24*time.Hour {
		r.notes = append(r.notes, fmt.Sprintf("⚠ its sign-in expires in %s; sign in again before then: "+
			"claudeswitch login %s --direct", shortDur(time.Until(acct.RefreshExpiry)), a.ID))
	}
	return r
}

// Unattributed mirrors poller.Unattributed. Kept here to avoid an import cycle
// between rendering and polling.
const Unattributed = state.Unattributed

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// StaleAge is the fallback when no cadence is configured.
const StaleAge = 3 * time.Minute

// staleAfter is when a reading is old enough to be worth flagging. Derived from
// the configured cadence: at a 60-second poll a 90-second reading is normal, and
// warning about it trains the reader to ignore warnings.
func staleAfter(cfg *config.Config, active bool) time.Duration {
	iv := cfg.PollActive.Duration
	if !active {
		iv = cfg.PollIdle.Duration
	}
	if iv <= 0 {
		return StaleAge
	}
	return 3 * iv
}

func nonEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// truncate cuts s to n columns, on runes: slicing bytes split "→" and "…".
func truncate(s string, n int) string { return Fit(s, n) }

// firstLine keeps a multi-line error to the part worth putting in a warning
// list. The rest is available from `claudeswitch doctor`.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// needsLogin recognises the one failure a user has to act on themselves. The
// usage API answers 401 for a revoked or expired credential, and no amount of
// retrying fixes it — so a row in that state must say so rather than claim it
// is being re-read.
func needsLogin(lastErr string) bool { return state.NeedsLoginErr(lastErr) }

// NeedsLogin is needsLogin for other packages: an error only an
// interactive sign-in cures.
func NeedsLogin(lastErr string) bool { return needsLogin(lastErr) }

// loginFix returns the command that puts an account back in service, or "" when
// nothing a person does would help. Retrying is useless for all of these: the
// credential is gone, revoked, or was never stored, and only an interactive
// sign-in produces a new one.
//
// The account id is filled in by the caller, so the message a user reads is
// something they can paste rather than a description of their situation.
func loginFix(lastErr string) string {
	switch {
	case !state.NeedsLoginErr(lastErr):
		return ""
	case strings.Contains(lastErr, "no stored credential"),
		strings.Contains(lastErr, "not in the vault"):
		// Configured but never captured, or the entry has been removed.
		return "no credential is stored for it; sign in with claudeswitch login %s --direct"
	}
	// The stored credential was revoked or expired beyond refreshing. Signing
	// in with `claude` and then `add` moved the live session onto it; login
	// --direct leaves the live session alone (owner decision, 0.7.3).
	return "its credential was rejected; sign in again with claudeswitch login %s --direct"
}
