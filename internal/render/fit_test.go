package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// overWidth reports the lines of out wider than width, or that end in
// padding.
func overWidth(t *testing.T, name, out string, width int) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if n := visibleLen(line); n > width {
			t.Errorf("%s: a %d-column line at %d columns:\n%s", name, n, width, line)
		}
		if strings.HasSuffix(line, " ") {
			t.Errorf("%s: a line padded at its end: %q", name, line)
		}
	}
}

// Every view fits an 80-column terminal: the detail view ran to 112, why's
// pace rows to 106, warnings past the edge; and no table pads its last
// column, which left every row of a wide one trailing spaces.
func TestEveryViewFitsEightyColumns(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	cfg, st := termsWorld()
	st.Accounts["dead"].RefreshExpiry = time.Now().Add(50 * time.Hour)
	reason := "active account at 86% of its session, over the 85% trigger; full resets in 9h with 40% unused, so it goes first"
	switches := []audit.Event{{At: time.Now(), Kind: "switch", From: "idle", To: "live", Reason: reason, Forced: true}}
	dec := policy.Decide(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	for _, detail := range []bool{false, true} {
		var b bytes.Buffer
		Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: detail, Decision: &dec, Switches: switches})
		overWidth(t, map[bool]string{false: "status", true: "status --detail"}[detail], b.String(), 80)
	}
	dec, vs := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: vs})
	overWidth(t, "why", b.String(), 80)
	if !strings.Contains(b.String(), "would expire unused") {
		t.Errorf("fitting must keep the pace's words, on a line of their own if need be:\n%s", b.String())
	}
}

// RECENT SWITCHES cut a reason at a width worked out from the names, not
// the terminal: a wide one showed half of it.
func TestRecentSwitchesUseTheWidthThereIs(t *testing.T) {
	t.Setenv("COLUMNS", "200")
	cfg, st := termsWorld()
	reason := "active account at 86% of its session, over the 85% trigger; full resets in 9h with 40% unused, so it goes first"
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true,
		Switches: []audit.Event{{At: time.Now(), Kind: "switch", From: "idle", To: "live", Reason: reason}}})
	if !strings.Contains(b.String(), "at 86% of its session, over the 85% trigger; full resets in 9h with 40% unused, so it goes first") {
		t.Errorf("at 200 columns the whole reason fits:\n%s", b.String())
	}
}

// truncate sliced bytes, so a cut could split "→" or "…" into invalid UTF-8.
func TestTruncateCutsRunesNotBytes(t *testing.T) {
	for _, s := range []string{"w1 → w2 → w3 → w4", "ééééééééééé", "a…b…c…d…e…f"} {
		for n := 2; n < 12; n++ {
			got := truncate(s, n)
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > n {
				t.Errorf("truncate(%q, %d) = %q", s, n, got)
			}
		}
	}
}
