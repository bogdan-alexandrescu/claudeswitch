package render

import (
	"fmt"
	"io"
	"strings"
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
	// Profile is the profile decided about ("" is default). An account
	// live in another profile is never shown as a candidate here (D34).
	Profile string
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
		v = InUseVerdict(o.Cfg, o.St, o.Profile, v)
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

// InUseVerdict is v as the profile self shows it: an account state records
// live in another profile is not a candidate, "in use by" that profile
// (D34), whatever else the verdict says.
func InUseVerdict(cfg *config.Config, st *state.State, self string, v policy.Verdict) policy.Verdict {
	if v.Active {
		return v
	}
	if other := InUseBy(cfg, st, self, v.ID); other != "" {
		v.Eligible = false
		if !strings.Contains(v.Why, "in use by") {
			v.Why, v.ClearsAt = "in use by "+other, time.Time{}
		}
	}
	return v
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
