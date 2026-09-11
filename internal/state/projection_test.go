package state

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func reading(pct float64) *usage.Usage {
	zero := 0.0
	return &usage.Usage{
		FiveHour: usage.Window{Utilization: &pct},
		SevenDay: usage.Window{Utilization: &zero},
	}
}

// The failure this guards against: while the poller is stuck, both readings are
// the same one, so the burn rate computed from them is zero and Projected()
// hands back the frozen figure as though it were current. The daemon then grows
// more confident the longer it is blind. It sat on 60% for two hours while the
// account reached 100%.
func TestProjectionSurvivesAStuckPoller(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      reading(60),
		LastAt:    now.Add(-20 * time.Minute),
		PrevWorst: 60, // identical to Last: the poller has not moved
		PrevAt:    now.Add(-20 * time.Minute),
		LastRate:  2, // but we saw it burning 2 points/min before we went blind
	}
	got := a.Projected(now)
	if got <= 60 {
		t.Fatalf("a stuck poller must not freeze the projection: got %.0f, want well above 60", got)
	}
	if got != 100 {
		t.Errorf("60%% plus 20min at 2pts/min should cap at 100, got %.0f", got)
	}
}

// A window reset is the one case where a remembered rate must be discarded:
// utilization fell, so the rate describes a window that no longer exists.
func TestAWindowResetClearsTheRememberedRate(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      reading(5),
		LastAt:    now.Add(-time.Minute),
		PrevWorst: 90, // it was at 90, now 5: the window rolled over
		PrevAt:    now.Add(-2 * time.Minute),
		LastRate:  3,
	}
	if r := a.BurnRate(); r != 0 {
		t.Errorf("a reset must report no burn, got %.1f", r)
	}
	if p := a.Projected(now); p != 5 {
		t.Errorf("a reset must project the new figure unchanged, got %.0f", p)
	}
}

// An estimate this old says more about how long we have been blind than about
// the account, so it stops rather than running away to 100%.
func TestARememberedRateExpires(t *testing.T) {
	now := time.Now()
	a := &Account{
		Last:      reading(60),
		LastAt:    now.Add(-MaxProjection - time.Minute),
		PrevWorst: 60,
		PrevAt:    now.Add(-MaxProjection - time.Minute),
		LastRate:  2,
	}
	if r := a.BurnRate(); r != 0 {
		t.Errorf("a rate older than MaxProjection must not be used, got %.1f", r)
	}
}
