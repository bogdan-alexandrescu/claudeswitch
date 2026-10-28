package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// A flag took a column of its own from the figure's left, so "97%!" sat one
// column left of every unflagged figure: the flag has a slot after it.
func TestAFlaggedFigureLinesUpWithTheRest(t *testing.T) {
	cfg, st := judgedWorld()
	idle := st.Accounts["idle"]
	idle.Last.FiveHour.Utilization, idle.Last.SevenDay.Utilization = ptr(20), ptr(97)
	idle.Last.Limits = []usage.Limit{{Kind: "weekly_all", Group: "weekly", Percent: ptr(97), Severity: "critical"}}
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true})
	var at []int
	for _, line := range strings.Split(b.String(), "\n") {
		if f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "▸")); len(f) > 3 && f[1] != "at" && (f[0] == "live" || f[0] == "idle") {
			// The week's figure: the second percentage on the row, in
			// columns, not bytes ("▸" is three).
			first := strings.Index(line, "%")
			at = append(at, utf8.RuneCountInString(line[:first+1+strings.Index(line[first+1:], "%")]))
		}
	}
	if len(at) != 2 || at[0] != at[1] {
		t.Errorf("the week's figures end at columns %v; a flag must not move one:\n%s", at, b.String())
	}
	// Red beside "available" needs saying: the API grades the week
	// critical, and rotation goes by the trigger.
	if out := b.String(); !strings.Contains(out, "critical") {
		t.Errorf("a figure the API grades critical under the trigger must be explained:\n%s", out)
	}
}

// needs sign-in and unreadable went uncoloured, though the first is the one
// state a person must act on.
func TestStatesThatNeedAPersonAreColoured(t *testing.T) {
	SetDepth(Depth256)
	t.Cleanup(func() { SetColor(false) })
	if got := colorState("needs sign-in"); !strings.Contains(got, pal.Red) {
		t.Errorf("needs sign-in is not red: %q", got)
	}
	if got := colorState("unreadable"); !strings.Contains(got, pal.Orange) {
		t.Errorf("unreadable is not orange: %q", got)
	}
	if got := colorState("short of margin"); got == "short of margin" {
		t.Errorf("short of margin is uncoloured")
	}
	// The trigger dot is its own glyph in colour too: bold ● beside lit ●
	// told them apart only by weight.
	if bar := dotBar(20, 85, 12, glacier, false); !strings.Contains(bar, dotThreshold) {
		t.Errorf("the trigger dot must be %s in colour: %q", dotThreshold, bar)
	}
}

// The legend named ! and ~ and never the marks the table draws.
func TestTheLegendExplainsTheMarks(t *testing.T) {
	cfg, st := judgedWorld()
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true})
	out := b.String()
	i := strings.Index(out, "flagged by the API")
	if i < 0 {
		t.Fatalf("no legend:\n%s", out)
	}
	legend := out[strings.LastIndex(out[:i], "\n")+1:]
	for _, mark := range []string{"▸", "●", "◉", "!", "~"} {
		if !strings.Contains(legend, mark) {
			t.Errorf("the legend does not explain %s:\n%s", mark, legend)
		}
	}
}

// why showed an account in use by another profile as a red ✗, the mark of
// one that cannot be used, where status greys it: it is another profile's
// for now, nothing to act on.
func TestWhyGreysAnAccountInUseElsewhere(t *testing.T) {
	cfg, st := judgedWorld()
	SetDepth(Depth256)
	t.Cleanup(func() { SetColor(false) })
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: policy.Decision{Kind: policy.Stay},
		Verdicts: []policy.Verdict{{ID: "idle", Why: "in use by work"}}})
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, "in use by work") && strings.Contains(line, pal.Red) {
			t.Errorf("in use elsewhere is not a fault:\n%q", line)
		}
	}
}

// RECENT SWITCHES said "Mon 15:04" for a switch two weeks old.
func TestRecentSwitchesCarryTheirDate(t *testing.T) {
	cfg, st := judgedWorld()
	old := time.Now().AddDate(0, 0, -12)
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true,
		Switches: []audit.Event{{At: old, Kind: "switch", From: "idle", To: "live", Reason: "manual"}}})
	if !strings.Contains(b.String(), old.Format("2 Jan 15:04")) {
		t.Errorf("a switch twelve days old must carry its date:\n%s", b.String())
	}
}

// why's "held until 15:04" and "frees up … at 15:04" named a time and no
// day.
func TestWhyTimesCarryTheirDay(t *testing.T) {
	cfg, st := judgedWorld()
	recovers := time.Now().Add(30 * time.Hour)
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: policy.Decision{Kind: policy.Wait,
		RecoversAccount: "idle", RecoversAt: recovers}})
	if !strings.Contains(b.String(), When(recovers, time.Now())) {
		t.Errorf("when an account frees up must say the day:\n%s", b.String())
	}
}
