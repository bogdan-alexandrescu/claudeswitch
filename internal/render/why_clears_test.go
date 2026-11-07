package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// why said when an account clears only when it was not a candidate, and
// only for its judged window. Every account read now says when each of its
// windows clears, as a time of day as well.
func TestWhySaysWhenEachWindowClears(t *testing.T) {
	t.Setenv("COLUMNS", "240")
	cfg, st := termsWorld()
	dec, vs := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now()})
	var b bytes.Buffer
	Why(&b, WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: vs})
	out := b.String()
	five := *st.Accounts["live"].Last.FiveHour.ResetsAt
	want := "session clears in 2h 59m (" + When(five, time.Now()) + "), week in 4d 1h ("
	for _, id := range []string{"live", "idle", "refused", "full", "dead"} {
		var row string
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(strings.NewReplacer("▸", " ", "✓", " ", "✗", " ").Replace(line)); len(f) > 0 && f[0] == id && row == "" {
				row = line
			}
		}
		if !strings.Contains(row, want) {
			t.Errorf("%s's line lacks %q:\n%s", id, want, out)
		}
	}
	if m := otherTerms.FindAllString(out, -1); len(m) > 0 {
		t.Errorf("%v in why:\n%s", m, out)
	}
}
