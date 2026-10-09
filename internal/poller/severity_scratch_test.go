package poller

import (
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Each profile reads its live credential into a scratch record named
// "active". Severity was tracked by that name, so two profiles at different
// severities overwrote each other, and every alternate poll logged a
// "change" (default at 92% critical, work at 25% normal, every 16 minutes;
// observed 2026-10-08). The scratch record is tracked per profile.
func TestScratchSeverityIsTrackedPerProfile(t *testing.T) {
	p, _ := testPoller()
	var changes int
	p.OnSeverityChange = func(string, string, string, string, float64) { changes++ }
	critical := &usage.Usage{Limits: []usage.Limit{{Kind: "weekly_all", Severity: "critical"}}}
	normal := &usage.Usage{Limits: []usage.Limit{{Kind: "weekly_all", Severity: "normal"}}}
	for i := 0; i < 3; i++ {
		p.noteSeverity(scratchFor("default").SeverityKey, critical)
		p.noteSeverity(scratchFor("work").SeverityKey, normal)
	}
	if changes != 0 {
		t.Fatalf("%d severity changes from two steady profiles, want 0", changes)
	}
	if got := scratchFor("work").ID; got != "active" {
		t.Errorf("scratch ID = %q; the rest of the poller relies on \"active\"", got)
	}
}
