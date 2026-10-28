package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// One word per idea in text for people, the app's words: the two windows
// are the session and the week (never 5h/5-hour/five_hour, 7d/7-day/weekly/
// seven_day), a refusal is "refused" (never "burnt"), the daemon rotates and
// a person switches (no "swap"), and a dead credential needs sign-in, fixed
// with `claudeswitch login <id> --direct`.

// otherTerms are the words the views used for the same ideas.
var otherTerms = regexp.MustCompile(`\b(5h|5H|5-hour|5-HOUR|five_hour|7d|7D|7-day|7-DAY|weekly|WEEKLY|seven_day|burnt|swap|swaps|RESETS|needs login|needs a login|needs a sign-in)\b`)

// termsWorld: live at 40% of its session; refused refused on its session;
// full over its week; dead with a rejected credential.
func termsWorld() (*config.Config, *state.State) {
	cfg, st := judgedWorld()
	cfg.Accounts = append(cfg.Accounts, config.Account{ID: "refused"}, config.Account{ID: "full"}, config.Account{ID: "dead"})
	cfg.Priority = append(cfg.Priority, "refused", "full", "dead")
	cfg.SwitchWhen, cfg.MaxSwitchWait = "idle", config.Duration{Duration: 30 * time.Second}
	five, week := time.Now().Add(3*time.Hour), time.Now().Add(4*24*time.Hour+time.Hour+30*time.Second)
	reading := func(f, s float64) *usage.Usage {
		return &usage.Usage{FiveHour: usage.Window{Utilization: ptr(f), ResetsAt: &five},
			SevenDay: usage.Window{Utilization: ptr(s), ResetsAt: &week}}
	}
	st.Accounts["live"].Last = reading(40, 20)
	st.Accounts["idle"].Last = reading(10, 20)
	st.Accounts["refused"] = &state.Account{Last: reading(100, 40), LastAt: time.Now(),
		BurntTil: five, BurntWin: usage.FiveHourKey}
	st.Accounts["full"] = &state.Account{Last: reading(10, 99), LastAt: time.Now()}
	st.Accounts["dead"] = &state.Account{Last: reading(10, 10), LastAt: time.Now(),
		LastErr: "usage API rejected the token (401): the account may need a re-login"}
	return cfg, st
}

func TestStatusUsesOneWordPerIdea(t *testing.T) {
	cfg, st := termsWorld()
	for _, detail := range []bool{false, true} {
		var b bytes.Buffer
		Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: detail,
			Decision: &policy.Decision{Kind: policy.Stay}})
		out := b.String()
		if m := otherTerms.FindAllString(out, -1); len(m) > 0 {
			t.Errorf("detail %v: %v in text for people:\n%s", detail, m, out)
		}
		for _, want := range []string{"SESSION", "WEEK", "CLEARS", "refused", "needs sign-in",
			"claudeswitch login dead --direct"} {
			if !strings.Contains(out, want) {
				t.Errorf("detail %v: %q missing:\n%s", detail, want, out)
			}
		}
		if strings.Contains(out, "claudeswitch add") || strings.Contains(out, "`cs ") {
			t.Errorf("detail %v: the fix for a dead credential is login --direct, said with claudeswitch:\n%s", detail, out)
		}
	}
}

// The detail view's RESETS was the session's reset whatever the account was
// judged on, where the compact CLEARS was the judged window's: both are
// CLEARS, and the same reset.
func TestDetailClearsIsTheJudgedWindowsReset(t *testing.T) {
	cfg, st := termsWorld()
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: true})
	for _, line := range strings.Split(b.String(), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "full" && !strings.Contains(line, "4d 1h") {
			t.Errorf("full is judged on its week, which clears in 4d 1h:\n%s", b.String())
		}
	}
}

// why's verdicts and stay reason name the window they judge.
func TestWhyNamesTheWindow(t *testing.T) {
	cfg, st := termsWorld()
	dec, vs := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: vs})
	out := b.String()
	for _, want := range []string{"of its session, under the 85% trigger", "at 10% of its session, ready",
		"at 99% of its week", "refused on its session window"} {
		if !strings.Contains(out, want) {
			t.Errorf("why lacks %q:\n%s", want, out)
		}
	}
	if m := otherTerms.FindAllString(out, -1); len(m) > 0 {
		t.Errorf("%v in why:\n%s", m, out)
	}
}

// The shared footer said "swap idle, forced after 30s".
func TestSharedFooterSaysWhatItMeans(t *testing.T) {
	cfg, st := termsWorld()
	var b bytes.Buffer
	SharedFooter(&b, Options{Cfg: cfg, St: st, DaemonOwns: true})
	if out := b.String(); otherTerms.MatchString(out) || !strings.Contains(out, "idle gap") {
		t.Errorf("the shared footer:\n%s", out)
	}
}
