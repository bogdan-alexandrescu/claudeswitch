package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// goDuration matches a duration as Go prints it for a program, "4m12s",
// "100h0m0s", "5m0s": text for people says "4m", "4d 4h", "5m".
var goDuration = regexp.MustCompile(`\b\d+h\d+m\d+s\b|\b\d+m\d+s\b|\b\d+h0m\b`)

// status --detail printed "read 4m12s ago", "within 100h0m0s", "forced
// after 2m0s" and "(12 per 5m0s": every span a person reads is said the way
// the rest of the view says one.
func TestDetailSaysSpansThePersonWay(t *testing.T) {
	cfg, st := judgedWorld()
	cfg.MaxSwitchWait = config.Duration{Duration: 2 * time.Minute}
	idle := st.Accounts["idle"]
	idle.LastAt = time.Now().Add(-4*time.Minute - 12*time.Second)
	idle.RefreshExpiry = time.Now().Add(100*time.Hour + 30*time.Second)
	b := usage.NewBudget()
	b.SetAllowance(24)
	for _, detail := range []bool{true, false} {
		var buf bytes.Buffer
		Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true, Budget: b, Detail: detail})
		out := buf.String()
		if m := goDuration.FindString(out); m != "" {
			t.Errorf("detail %v: %q is a span as Go prints it:\n%s", detail, m, out)
		}
		if !strings.Contains(out, "mid-turn after 2m") {
			t.Errorf("detail %v: the idle-gap wait reads 2m:\n%s", detail, out)
		}
		if !strings.Contains(out, "4d 4h") {
			t.Errorf("detail %v: the refresh expiry reads 4d 4h:\n%s", detail, out)
		}
		if detail && !strings.Contains(out, "read 4m ago") {
			t.Errorf("the reading's age reads 4m:\n%s", out)
		}
	}
}

// The api budget line printed the built-in 12 whatever api_budget and the
// profiles in use made the window: it says what the budget allows.
func TestDetailBudgetLineSaysTheRealAllowance(t *testing.T) {
	cfg, st := judgedWorld()
	b := usage.NewBudget()
	b.SetAllowance(24)
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true, Budget: b, Detail: true})
	if out := buf.String(); !strings.Contains(out, "24 per 5m") {
		t.Errorf("the budget line must give the window's real allowance, 24 per 5m:\n%s", out)
	}
}
