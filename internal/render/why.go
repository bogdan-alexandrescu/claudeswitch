package render

import (
	"fmt"
	"io"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

type WhyOptions struct {
	Cfg      *config.Config
	St       *state.State
	Decision policy.Decision
	Verdicts []policy.Verdict
}

// Why renders the reasoning behind the current decision.
//
// The order is deliberate: the answer first, then the evidence. Someone running
// this wants to know what is happening; the account-by-account detail is there
// to make the answer believable, not to be waded through.
func Why(out io.Writer, o WhyOptions) {
	fmt.Fprintln(out)

	switch o.Decision.Kind {
	case policy.Switch:
		verb := "rotating to"
		if o.Decision.Forced {
			verb = "switching immediately to"
		}
		fmt.Fprintf(out, "  %s %s\n", paint(glacier, verb), paint(bold, o.Decision.Target))
	case policy.Wait:
		fmt.Fprintf(out, "  %s\n", paint(red, "nowhere to rotate to"))
	default:
		fmt.Fprintf(out, "  %s\n", paint(meltwater, "staying put"))
	}
	fmt.Fprintf(out, "  %s\n", paint(grey, o.Decision.Reason))

	if !o.Decision.RecoversAt.IsZero() {
		fmt.Fprintf(out, "  %s\n", paint(grey, fmt.Sprintf("%s frees up in %s, at %s",
			o.Decision.RecoversAccount, shortDur(time.Until(o.Decision.RecoversAt)),
			o.Decision.RecoversAt.Local().Format("15:04"))))
	}

	fmt.Fprintf(out, "\n  %s\n", paint(dim, "CONSIDERED, in order"))
	t := &table{}
	for _, v := range o.Verdicts {
		mark, name := " ", v.ID
		switch {
		case v.Active:
			mark, name = paint(glacier, "▸"), paint(bold, v.ID)
		case v.Eligible:
			mark = paint(meltwater, "✓")
		default:
			mark = paint(red, "✗")
		}
		why := v.Why
		if !v.Eligible && !v.ClearsAt.IsZero() && time.Until(v.ClearsAt) > 0 {
			why += paint(grey, fmt.Sprintf(" · clears in %s", shortDur(time.Until(v.ClearsAt))))
		}
		t.add(mark, name, why)
	}
	fmt.Fprint(out, t.render("  "))
	weeklySection(out, o)
	fmt.Fprintln(out)
}

// weeklySection is each account's weekly pace and per-model weekly limits
// (IMPROVEMENTS I6, I8), in the order considered. Display only: nothing here
// changes the decision above, except a model the config counts, which the
// verdicts already reflect.
func weeklySection(out io.Writer, o WhyOptions) {
	t := &table{}
	now := time.Now()
	for _, v := range o.Verdicts {
		acct := o.St.Accounts[v.ID]
		if !acct.HasReading() {
			continue
		}
		p, ok := acct.WeeklyPace(now)
		models := modelLimitsText(acct.Last)
		if !ok && models == "" {
			continue
		}
		first := models
		if ok {
			first = paceLine(p)
		}
		t.add(" ", v.ID, first)
		if ok && models != "" {
			t.add("", "", models)
		}
	}
	if len(t.rows) == 0 {
		return
	}
	fmt.Fprintf(out, "\n  %s\n", paint(dim, "WEEKLY, at the pace so far"))
	fmt.Fprint(out, t.render("  "))
}
