package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// CLEARS was the judged window's reset alone: an account judged on its
// session said nothing of when its week clears. Where the terminal has
// room, each window has its own CLEARS, after its figure.
func TestStatusClearsEachWindowWhenWide(t *testing.T) {
	t.Setenv("COLUMNS", "160")
	cfg, st := judgedWorld()
	row, out := judgedRow(t, cfg, st, "idle", nil)
	if !strings.Contains(row, "3h 10m") || !strings.Contains(row, "3d 23h") {
		t.Errorf("idle's row must say when its session (3h 10m) and its week (3d 23h) clear:\n%s", out)
	}
	var head string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "SESSION") {
			head = l
		}
	}
	if strings.Count(head, "CLEARS") != 2 {
		t.Errorf("want a CLEARS after each window's figure:\n%s", out)
	}
	if i, j := strings.Index(row, "3h 10m"), strings.Index(row, "3d 23h"); i < 0 || j < i {
		t.Errorf("the session's CLEARS comes before the week's:\n%s", out)
	}
}

// A refused account's CLEARS for the refused window is when the refusal
// lifts.
func TestStatusRefusedClearsWhenWide(t *testing.T) {
	t.Setenv("COLUMNS", "160")
	cfg, st := termsWorld()
	st.Accounts["refused"].BurntTil = time.Now().Add(45*time.Minute + 30*time.Second)
	var b bytes.Buffer
	Status(&b, Options{Cfg: cfg, St: st, DaemonOwns: true, Decision: &policy.Decision{Kind: policy.Stay}})
	out := b.String()
	if m := otherTerms.FindAllString(out, -1); len(m) > 0 {
		t.Errorf("%v in text for people:\n%s", m, out)
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "refused" {
			if i, j := strings.Index(line, "45m"), strings.Index(line, "4d 1h"); i < 0 || j < i {
				t.Errorf("refused on its session: its session clears as the refusal lifts, in 45m:\n%s", out)
			}
		}
	}
}

func TestLayoutClearsBothOnlyWhenWide(t *testing.T) {
	if l := layoutFor(160, 9, 17); !l.clears || !l.clearsBoth {
		t.Errorf("160 columns has room for a CLEARS per window: %+v", l)
	}
	if l := layoutFor(100, 9, 17); l.clearsBoth {
		t.Errorf("100 columns keeps one CLEARS, the bars and the plan: %+v", l)
	}
}
