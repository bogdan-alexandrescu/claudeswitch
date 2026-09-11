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
	Dir      string
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
		fmt.Fprintf(out, "  %s %s\n", paint(green, verb), paint(bold, o.Decision.Target))
	case policy.Wait:
		fmt.Fprintf(out, "  %s\n", paint(red, "nowhere to rotate to"))
	default:
		fmt.Fprintf(out, "  %s\n", paint(green, "staying put"))
	}
	fmt.Fprintf(out, "  %s\n", paint(grey, o.Decision.Reason))

	if !o.Decision.RecoversAt.IsZero() {
		fmt.Fprintf(out, "  %s\n", paint(grey, fmt.Sprintf("%s frees up in %s, at %s",
			o.Decision.RecoversAccount, shortDur(time.Until(o.Decision.RecoversAt)),
			o.Decision.RecoversAt.Local().Format("15:04"))))
	}

	if o.Dir != "" {
		if pr, ok := o.Cfg.ProjectFor(o.Dir); ok && (len(pr.Eligible) > 0 || len(pr.Prefer) > 0) {
			fmt.Fprintf(out, "\n  %s %s\n", paint(dim, "here"), o.Dir)
			if len(pr.Eligible) > 0 {
				fmt.Fprintf(out, "  %s only %v may serve this directory\n", paint(dim, "rule"), pr.Eligible)
			}
			if len(pr.Prefer) > 0 {
				fmt.Fprintf(out, "  %s %v preferred here\n", paint(dim, "rule"), pr.Prefer)
			}
		}
	}

	fmt.Fprintf(out, "\n  %s\n", paint(dim, "CONSIDERED, in order"))
	t := &table{}
	for _, v := range o.Verdicts {
		mark, name := " ", v.ID
		switch {
		case v.Active:
			mark, name = paint(green, "▸"), paint(bold, v.ID)
		case v.Eligible:
			mark = paint(green, "✓")
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
	fmt.Fprintln(out)
}
