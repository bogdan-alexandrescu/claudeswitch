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

func window(w usage.Window, switchAt, reserve float64) string {
	if !w.Known() {
		return fmt.Sprintf("%-24s %6s", strings.Repeat(" ", 24), "  -  ")
	}
	p := w.Pct()
	flag := ""
	switch {
	case p >= switchAt:
		flag = " SWITCH"
	case reserve > 0 && p > reserve:
		flag = " reserve"
	}
	return fmt.Sprintf("%s %5.1f%%%s", bar(p, 22), p, flag)
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
	if flagged {
		s += "!"
	}
	if projected {
		s = "~" + s
	}
	// Pad before colouring: escape codes have no width, so a coloured cell
	// padded by Printf comes out short.
	s = fmt.Sprintf("%6s", s)
	return paint(levelFor(v, switchAt, severity), s)
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
		if which == "seven_day" && l.Group == "weekly" {
			return l.Severity
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
		if which == "seven_day" && l.Group == "weekly" {
			return true
		}
	}
	return false
}

// padderFor sizes the account column to the widest id. Done as a function
// because coloured cells cannot be padded by Printf: escape codes have no width,
// so %-12s on a coloured string pads to the wrong place.
func padderFor(accounts []config.Account) func(string) string {
	w := len("ACCOUNT")
	for _, a := range accounts {
		if n := len(a.Name()); n > w {
			w = n
		}
	}
	return func(s string) string {
		for len(s) < w {
			s += " "
		}
		return s
	}
}

// holdingOffReason describes a deliberate pause on calling the usage API, or ""
// when there is none. The daemon backs off when the API refuses it, and during
// that window every reading legitimately goes stale.
func holdingOffReason(b *usage.Budget) string {
	if b == nil {
		return ""
	}
	til, locked := b.LockedUntil()
	if !locked {
		return ""
	}
	return fmt.Sprintf("holding off after the API refused us, resuming %s",
		til.Local().Format("15:04"))
}

func statusCompact(out io.Writer, o Options) {
	cfg, st := o.Cfg, o.St
	fmt.Fprintln(out)

	accounts := cfg.Ordered()
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
	lay := layoutFor(terminalWidth(), nameW)

	fmt.Fprintf(out, "  %s\n\n", headline(o))

	// Columns are assembled to fit the terminal. A table that wraps is worse
	// than a narrower one: wrapping destroys the alignment the eye relies on.
	head := []string{"", "ACCOUNT"}
	right := map[int]bool{}
	if lay.plan {
		head = append(head, "PLAN")
	}
	head = append(head, "5H")
	right[len(head)-1] = true
	if lay.bars {
		head = append(head, "")
	}
	head = append(head, "7D")
	right[len(head)-1] = true
	if lay.bars {
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
		if a.ID == st.Active {
			// Which account you are on should be visible without reading words.
			marker = paint(green, "▸")
			nameCell = paint(bold, nameCell)
		}

		if acct == nil || acct.Last == nil {
			why := "not set up — run `cs login " + a.ID + "`"
			if o.Vaulted[a.ID] {
				why = "not polled yet"
				if acct != nil && acct.LastErr != "" {
					why = "unreadable"
					warnings = append(warnings,
						fmt.Sprintf("%s could not be read: %s", a.ID, truncate(acct.LastErr, 60)))
				}
			}
			row := []string{marker, nameCell}
			if lay.plan {
				row = append(row, plan)
			}
			row = append(row, "-")
			if lay.bars {
				row = append(row, "")
			}
			row = append(row, "-")
			if lay.bars {
				row = append(row, "")
			}
			if lay.clears {
				row = append(row, "-")
			}
			t.add(append(row, colorState(why))...)
			continue
		}

		which, worst := acct.Last.Worst()
		proj := acct.Projected(time.Now())
		projected := proj-worst >= 1

		five := pct(acct.Last.FiveHour.Pct(), severityOf(acct.Last, "five_hour"),
			projected && which == "five_hour", cfg.SwitchAt, severityName(acct.Last, "five_hour"))
		seven := pct(acct.Last.SevenDay.Pct(), severityOf(acct.Last, "seven_day"),
			projected && which == "seven_day", cfg.SwitchAt, severityName(acct.Last, "seven_day"))

		// CLEARS is the reset of whichever window is binding — the one that will
		// actually stop you — not always the five-hour.
		clears := "-"
		if b := acct.Last.Binding(); b != nil && b.ResetsAt != nil {
			clears = until(b.ResetsAt)
		} else if which == "seven_day" {
			clears = until(acct.Last.SevenDay.ResetsAt)
		} else {
			clears = until(acct.Last.FiveHour.ResetsAt)
		}

		// The marker says which account is active, so STATE no longer has to.
		state := stateOf(acct, a, cfg, proj, which, lay.burn)
		if bill := acct.Last.Billing(); bill != "" {
			state += " · " + paint(yellow, bill)
		}

		row := []string{marker, nameCell}
		if lay.plan {
			row = append(row, plan)
		}
		row = append(row, five)
		if lay.bars {
			row = append(row, paint(levelFor(acct.Last.FiveHour.Pct(), cfg.SwitchAt,
				severityName(acct.Last, "five_hour")), miniBar(acct.Last.FiveHour.Pct())))
		}
		row = append(row, seven)
		if lay.bars {
			row = append(row, paint(levelFor(acct.Last.SevenDay.Pct(), cfg.SwitchAt,
				severityName(acct.Last, "seven_day")), miniBar(acct.Last.SevenDay.Pct())))
		}
		if lay.clears {
			row = append(row, clears)
		}
		t.add(append(row, colorState(state))...)

		// Anything the reader should act on goes to the warnings block rather
		// than crowding the row it belongs to.
		// "the poller is behind" is only true when the poller is trying and not
		// keeping up. When the last attempt failed, the daemon already knows
		// exactly why, and printing a generic lag message instead throws away
		// the one line that tells the reader what to do about it.
		if age := time.Since(acct.LastAt); age > staleAfter(cfg, a.ID == st.Active) {
			switch {
			case holdingOff != "":
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
			warnings = append(warnings, fmt.Sprintf("%s needs a login within %s",
				a.ID, time.Until(acct.RefreshExpiry).Round(time.Hour)))
		}
	}

	fmt.Fprint(out, t.render("  "))

	if len(o.Switches) > 0 {
		fmt.Fprintf(out, "\n  %s\n", paint(dim, "RECENT SWITCHES"))
		st := &table{head: []string{"", "", "", ""}}
		today := time.Now().Local().Format("2006-01-02")
		// Newest first: the most recent rotation is the one being looked for.
		for i := len(o.Switches) - 1; i >= 0; i-- {
			e := o.Switches[i]
			// Same format for every row: a mix of "Wed 20:04" and "15:04" makes
			// the older entries look like the newer ones.
			when := e.At.Local().Format("Mon 15:04")
			if e.At.Local().Format("2006-01-02") == today {
				when = "today " + e.At.Local().Format("15:04")
			}
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
			mark := ""
			if e.Forced {
				mark = paint(orange, " forced")
			}
			st.add(paint(grey, when), name(e.From)+" → "+paint(bold, name(e.To)),
				paint(grey, truncate(shortReason(e.Reason), lay.reason))+mark)
		}
		// No header row for this one: the columns speak for themselves.
		st.head = nil
		fmt.Fprint(out, st.render("  "))
	}

	if o.Degraded {
		warnings = append(warnings,
			"usage API shape changed — predictive switching DISABLED, reactive only: "+truncate(o.DegradedWhy, 50))
	}
	if o.Budget != nil {
		if til, locked := o.Budget.LockedUntil(); locked {
			warnings = append(warnings, fmt.Sprintf(
				"usage API rate limited; readings resume in %s (figures above are the last good ones)",
				time.Until(til).Round(time.Second)))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(out, "\n  %s\n", paint(dim, "WARNINGS"))
		for _, w := range warnings {
			fmt.Fprintf(out, "  %s %s\n", paint(orange, "⚠"), w)
		}
	}

	fmt.Fprintln(out)
	if o.DaemonOwns {
		fmt.Fprintf(out, "  %s\n", paint(grey, "a daemon is polling; these are its readings"))
	}
	footer(out, o)
}

// headline is the single line worth reading first: where you stand, and what
// happens next.
func headline(o Options) string {
	st, cfg := o.St, o.Cfg
	acct := st.Accounts[st.Active]
	if st.Active == "" || acct == nil || acct.Last == nil {
		return paint(dim, "no reading for the active account yet")
	}
	which, _ := acct.Last.Worst()
	proj := acct.Projected(time.Now())
	window := "5-hour"
	if which == "seven_day" {
		window = "weekly"
	}

	level := levelFor(proj, cfg.SwitchAt, severityName(acct.Last, which))
	head := fmt.Sprintf("%s at %s of its %s", paint(bold, st.Active),
		paint(level, fmt.Sprintf("%.0f%%", proj)), window)

	switch {
	case o.Decision == nil:
		return head
	case o.Decision.Kind == "switch":
		return head + paint(grey, " · ") + paint(green, "rotating to "+o.Decision.Target)
	case o.Decision.Kind == "wait" && !o.Decision.RecoversAt.IsZero():
		return head + paint(grey, " · ") +
			paint(red, "every account is out") +
			paint(grey, fmt.Sprintf(" · %s returns in %s",
				o.Decision.RecoversAccount, shortDur(time.Until(o.Decision.RecoversAt))))
	case o.Decision.Kind == "wait":
		return head + paint(grey, " · ") + paint(red, "every account is out")
	}

	// Staying put. Say how much room is left, which is the reassuring form of
	// the same fact.
	if left := cfg.SwitchAt - proj; left > 0 {
		return head + paint(grey, fmt.Sprintf(" · %.0f points before it rotates", left))
	}
	return head
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

// stateOf is the one-phrase verdict for an account.
func stateOf(acct *state.Account, a config.Account, cfg *config.Config, proj float64, which string, withBurn bool) string {
	window := "5h"
	if which == "seven_day" {
		window = "7d"
	}
	avail := acct.Availability(a.Reserve)
	switch {
	case needsLogin(acct.LastErr):
		// Nothing else in this row can be trusted: the figures are whatever was
		// last readable, and no amount of waiting will refresh them.
		return "needs login"
	case acct.ExpiredAt(time.Now()):
		// The window this reading describes has since refilled, so the figure
		// says nothing about the account now.
		return "window reset · re-reading"
	case avail == state.Burnt:
		return "refused · " + acct.BurntWin
	case avail == state.Reserved:
		return "reserved · " + window
	case proj >= cfg.SwitchAt:
		return "no headroom · " + window
	}
	s := string(avail)
	if withBurn {
		if rate := acct.BurnRate(); rate > 0 {
			s += paint(grey, fmt.Sprintf(" · %.1f%%/min", rate))
		}
	}
	return s
}

func warnings(out io.Writer, o Options) {
	if o.Degraded {
		fmt.Fprintf(out, "  ⚠ usage API shape changed — predictive switching DISABLED, reactive only.\n")
		fmt.Fprintf(out, "    %s\n\n", o.DegradedWhy)
	}
	if o.Budget != nil {
		if til, locked := o.Budget.LockedUntil(); locked {
			fmt.Fprintf(out, "  ⚠ usage API rate limited; readings resume in %s (figures below are the last good ones)\n\n",
				time.Until(til).Round(time.Second))
		}
	}
	if o.DaemonOwns {
		fmt.Fprintf(out, "  (a daemon is running and owns the polling; these are its readings)\n\n")
	}
}

func footer(out io.Writer, o Options) {
	fmt.Fprintf(out, "  switch ≥%.0f%%  ·  hard floor ≥%.0f%%  ·  swap %s, forced after %s",
		o.Cfg.SwitchAt, o.Cfg.HardFloor, o.Cfg.SwitchWhen, o.Cfg.MaxSwitchWait.Duration)
	if o.Budget != nil {
		fmt.Fprintf(out, "  ·  %d api call(s) spare", o.Budget.Remaining())
	}
	fmt.Fprintln(out)
	legend := "  ! flagged by the API  ·  ~ projected from an older reading"
	if useColor {
		// These four are colour swatches, not words to read in sequence. Without
		// a label and separators they run together into "ok climbing close act
		// now", which is what the line looks like the moment colour is stripped
		// — by a pipe, a paste, or a terminal that does not support it.
		legend += "\n  colour:  " + paint(green, "ok") + "  ·  " + paint(yellow, "climbing") +
			"  ·  " + paint(orange, "close") + "  ·  " + paint(red, "act now")
	}
	fmt.Fprintln(out, legend)
	fmt.Fprintln(out)
}

// statusDetailed is the previous view, kept behind --detail for when a number
// needs interrogating rather than glancing at.
func statusDetailed(out io.Writer, o Options) {
	cfg, st := o.Cfg, o.St
	fmt.Fprintln(out)

	if o.Degraded {
		fmt.Fprintf(out, "  ⚠ usage API returned an unexpected shape. Predictive switching is DISABLED;\n")
		fmt.Fprintf(out, "    running reactive-only, so brief pauses at limits are possible.\n")
		fmt.Fprintf(out, "    detail: %s\n", o.DegradedWhy)
		fmt.Fprintf(out, "    run `claudeswitch doctor` for the full picture.\n\n")
	}
	if o.Budget != nil {
		if til, locked := o.Budget.LockedUntil(); locked {
			fmt.Fprintf(out, "  ⚠ usage API rate limited; readings resume in %s (this is expected,\n",
				time.Until(til).Round(time.Second))
			fmt.Fprintf(out, "    not a failure — figures below are the last good ones).\n\n")
		}
	}

	if o.DaemonOwns {
		fmt.Fprintf(out, "  (a daemon is running and owns the polling; these are its readings)\n\n")
	}

	accounts := cfg.Ordered()
	// Ids are the display name now, and they are not all short. Size the column
	// to the widest one rather than truncating or letting it run ragged.
	pad := padderFor(accounts)
	if len(accounts) == 0 {
		fmt.Fprintf(out, "  no accounts configured. See %s\n\n", cfg.Path)
		return
	}

	fmt.Fprintf(out, "  %s %-31s %-31s %-9s %s\n", pad("ACCOUNT"), "5-HOUR", "7-DAY", "RESETS", "STATE")

	// The live credential may not match any configured account. Show it plainly
	// rather than filing it under a guess.
	if un, ok := st.Accounts[Unattributed]; ok && un.Last != nil {
		fmt.Fprintf(out, "  %s %-31s %-31s %-9s %s\n", pad("(live)"),
			window(un.Last.FiveHour, cfg.SwitchAt, 0),
			window(un.Last.SevenDay, cfg.SwitchAt, 0),
			until(un.Last.FiveHour.ResetsAt), "ACTIVE, unattributed")
		fmt.Fprintf(out, "  %s   ↳ org %s is not in your config. Add org_id = %q to the\n",
			pad(""), shortID(un.OrgID), un.OrgID)
		fmt.Fprintf(out, "  %s     account it belongs to, so its usage is tracked by name.\n", pad(""))
	}
	for _, a := range accounts {
		acct := st.Accounts[a.ID]
		if acct == nil {
			why := "not set up — run `claudeswitch login " + a.ID + "`"
			if o.Vaulted[a.ID] {
				why = "vaulted, not polled yet"
			}
			fmt.Fprintf(out, "  %s %-31s %-31s %-9s %s\n", pad(a.Name()), "", "", "-", why)
			continue
		}
		avail := acct.Availability(a.Reserve)
		stateStr := string(avail)
		// "available" must not be shown for an account with no headroom. The
		// policy engine already refuses to rotate into one (firstEligible skips
		// anything at or over the trigger), but the status column said
		// "available" beside a 100% bar, which contradicted itself.
		if avail == state.Available && acct.Last != nil {
			if _, worst := acct.Last.Worst(); worst >= cfg.SwitchAt {
				stateStr = fmt.Sprintf("no headroom (%.0f%%)", worst)
			}
		}
		if a.ID == st.Active {
			stateStr = "ACTIVE " + stateStr
		}
		if avail == state.Burnt {
			stateStr = fmt.Sprintf("burnt %s, clears %s", acct.BurntWin, until(&acct.BurntTil))
		}
		if acct.Last == nil {
			// An account that was never set up is not an error to report at the
			// user; say what to do about it instead of surfacing a raw keychain
			// message.
			detail := "not set up — run `claudeswitch login " + a.ID + "`"
			if o.Vaulted[a.ID] {
				detail = acct.LastErr
				if detail == "" {
					detail = "not polled yet"
				}
			}
			label := detail
			if o.Vaulted[a.ID] {
				label = "unknown (" + truncate(detail, 40) + ")"
			}
			fmt.Fprintf(out, "  %s %-31s %-31s %-9s %s\n", pad(a.Name()), "", "", "-", label)
			continue
		}
		five := window(acct.Last.FiveHour, cfg.SwitchAt, a.Reserve)
		seven := window(acct.Last.SevenDay, cfg.SwitchAt, a.Reserve)
		fmt.Fprintf(out, "  %s %-31s %-31s %-9s %s\n",
			pad(a.Name()), five, seven, until(acct.Last.FiveHour.ResetsAt), stateStr)

		// Always state the age. The poll interval is longer than the old stale
		// threshold, so a figure could be minutes out of date with nothing to
		// say so — and under heavy use minutes matter (2026-09-10: 8 points in
		// 3 minutes).
		age := time.Since(acct.LastAt)
		rate := acct.BurnRate()
		switch {
		case rate > 0:
			proj := acct.Projected(time.Now())
			fmt.Fprintf(out, "  %s   ↳ read %s ago, burning %.1f%%/min → about %.0f%% now\n",
				pad(""), age.Round(time.Second), rate, proj)
		case age > StaleAge:
			fmt.Fprintf(out, "  %s   ↳ read %s ago\n", pad(""), age.Round(time.Second))
		default:
			fmt.Fprintf(out, "  %s   ↳ read %s ago\n", pad(""), age.Round(time.Second))
		}
		if b := acct.Last.Binding(); b != nil {
			flag := ""
			if b.Severity != "normal" && b.Severity != "" {
				// Anthropic's own early warning. Observed at 78% on 2026-09-09.
				flag = "  ⚠ the API itself is flagging this"
			}
			fmt.Fprintf(out, "  %s   ↳ binding: %s (%s) %.0f%%, resets %s%s\n",
				pad(""), b.Kind, b.Severity, b.Percent, until(b.ResetsAt), flag)
		}
		if !acct.RefreshExpiry.IsZero() && time.Until(acct.RefreshExpiry) < 7*24*time.Hour {
			fmt.Fprintf(out, "  %s   ⚠ refresh token expires in %s — this account needs a login before then\n",
				pad(""), time.Until(acct.RefreshExpiry).Round(time.Hour))
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "  thresholds  switch ≥%.0f%%   hard floor ≥%.0f%%   swap %s, forced after %s\n",
		cfg.SwitchAt, cfg.HardFloor, cfg.SwitchWhen, cfg.MaxSwitchWait.Duration)
	if o.Budget != nil {
		fmt.Fprintf(out, "  api budget  %d scheduled call(s) available now (%d per %s, one held for swaps)\n",
			o.Budget.Remaining(), usage.DefaultAllowance, usage.DefaultWindow)
	}
	fmt.Fprintln(out)
}

// Unattributed mirrors poller.Unattributed. Kept here to avoid an import cycle
// between rendering and polling.
const Unattributed = "active"

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

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
func needsLogin(lastErr string) bool {
	if lastErr == "" {
		return false
	}
	return strings.Contains(lastErr, "401") ||
		strings.Contains(lastErr, "re-login") ||
		strings.Contains(lastErr, "needs an interactive login")
}
