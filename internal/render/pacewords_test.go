package render

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Ahead of pace, "on pace to use it all" undersold it: at the average so far
// the week runs out before it resets, and when is what matters.
func TestAheadOfPaceSaysWhenTheWeekRunsOut(t *testing.T) {
	resets := time.Now().Add(84 * time.Hour)
	p := state.Pace{Expected: 50, Actual: 84, Estimated: true, AtReset: 100, Unused: 0, ResetsAt: resets}
	got := ExpiryText(p)
	if strings.Contains(got, "use it all") || !strings.Contains(got, "run out") || !strings.Contains(got, "before it resets") {
		t.Errorf("ExpiryText = %q, want when it runs out, before it resets", got)
	}
	// 16 points left at 84 points a half-week: about 16 hours from now.
	if want := When(time.Now().Add(16*time.Hour), time.Now()); !strings.Contains(got, want) {
		t.Errorf("ExpiryText = %q, want the time it runs out, %s", got, want)
	}
	// Exactly on pace it uses it all, and says so.
	p.Actual = 50
	if got := ExpiryText(p); got != "on pace to use it all" {
		t.Errorf("on pace: %q", got)
	}
}
