package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Per-model weekly limits (IMPROVEMENTS I6) and the weekly pace view (I8).
// The pace view is display only and never appears in the compact status or
// the status line.

// limitPct is a limit's figure, or "unknown" when the API gave none: a blank
// or a zero would read as room.
func limitPct(l usage.Limit) string {
	if !l.Known() {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%%", l.Pct())
}

// modelLimitsText is one account's per-model weekly limits on one line:
// "Modelname 12%  ·  Othermodel unknown".
func modelLimitsText(u *usage.Usage) string {
	var parts []string
	for _, l := range u.ModelWeekly() {
		parts = append(parts, l.ModelName()+" "+limitPct(l))
	}
	return strings.Join(parts, "  ·  ")
}

// PaceText is the pace half of the view: "20% used · 57% of the week gone ·
// 37 points behind".
func PaceText(p state.Pace) string {
	s := fmt.Sprintf("%.0f%% used · %.0f%% of the week gone", p.Actual, p.Expected)
	switch d := p.Actual - p.Expected; {
	case d <= -1:
		s += fmt.Sprintf(" · %.0f points behind", -d)
	case d >= 1:
		s += fmt.Sprintf(" · %.0f points ahead", d)
	default:
		s += " · on pace"
	}
	return s
}

// ExpiryText is the estimate: "~65% would expire unused at reset", or "" when
// it is too early in the week to make one.
func ExpiryText(p state.Pace) string {
	switch {
	case !p.Estimated:
		return ""
	case p.Unused >= 0.5:
		return fmt.Sprintf("~%.0f%% would expire unused at reset", p.Unused)
	}
	return "on pace to use it all"
}

// paceLine joins the two, for the views that show both.
func paceLine(p state.Pace) string {
	s := PaceText(p)
	if e := ExpiryText(p); e != "" {
		s += " · " + e
	}
	return s
}

// modelBlock lists per-model weekly limits under the compact table, only when
// some account shown has one, so a view without them is unchanged.
func modelBlock(out io.Writer, o Options, accounts []config.Account) {
	t := &table{}
	for _, a := range accounts {
		acct := o.St.Accounts[a.ID]
		if acct == nil || acct.Last == nil || len(acct.Last.ModelWeekly()) == 0 {
			continue
		}
		t.add(" ", a.Name(), modelLimitsText(acct.Last))
	}
	if len(t.rows) == 0 {
		return
	}
	fmt.Fprintf(out, "\n  %s\n", paint(dim, "WEEKLY BY MODEL"))
	fmt.Fprint(out, t.render("  "))
}

// unknownLimitWarnings names every limit an account reports that this code
// does not understand. They are never counted; saying nothing would let one
// that matters pass unseen.
func unknownLimitWarnings(id string, u *usage.Usage) []string {
	var out []string
	for _, l := range u.UnknownLimits() {
		out = append(out, fmt.Sprintf("%s reports a limit claudeswitch does not understand: %s at %s — unknown, not counted",
			id, l.Describe(), limitPct(l)))
	}
	return out
}

// detailLimits writes the per-model, unknown-limit and pace lines of one
// account in status --detail.
func detailLimits(out io.Writer, pad func(string) string, cfg *config.Config, acct *state.Account) {
	for _, l := range acct.Last.ModelWeekly() {
		s := fmt.Sprintf("%s weekly %s", l.ModelName(), limitPct(l))
		if !l.Known() {
			s += " (the API gave no figure)"
		}
		if l.ResetsAt != nil {
			s += ", resets " + until(l.ResetsAt)
		}
		if l.Severity != "" && l.Severity != "normal" {
			s += " (" + l.Severity + ")"
		}
		if cfg.CountsModel(l.ModelName()) {
			s += " · counted like the weekly window"
		}
		fmt.Fprintf(out, "  %s   ↳ %s\n", pad(""), s)
	}
	for _, l := range acct.Last.UnknownLimits() {
		fmt.Fprintf(out, "  %s   ↳ %s %s: not understood — unknown, not counted\n",
			pad(""), l.Describe(), limitPct(l))
	}
	if p, ok := acct.WeeklyPace(time.Now()); ok {
		fmt.Fprintf(out, "  %s   ↳ pace: %s\n", pad(""), paceLine(p))
	}
}
