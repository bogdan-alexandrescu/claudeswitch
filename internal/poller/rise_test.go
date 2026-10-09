package poller

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// F2: a forecast carries a burn rate over a flat pair of readings only when
// the rise behind it is recent, so the poller records when a reading last
// rose (RiseAt) and the rate it rose at (RiseRate). A flat pair keeps
// both; a fall clears them.
func TestRiseIsTheLastRisingPair(t *testing.T) {
	p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
	acct := p.st.Get("a")
	poll := func(five float64) {
		api.five["tok-a"] = five
		if r := p.fetchInto(context.Background(), acct, "tok-a", usage.Interactive); r != usage.ReasonOK {
			t.Fatalf("poll refused: %q", r)
		}
	}

	poll(40)
	if !acct.RiseAt.IsZero() {
		t.Fatalf("one reading set RiseAt = %v", acct.RiseAt)
	}
	*clock = clock.Add(3 * time.Minute)
	poll(43)
	rose := *clock
	if !acct.RiseAt.Equal(rose) || acct.RiseRate != 1 {
		t.Fatalf("after a rise: RiseAt %v, rate %v; want %v, 1", acct.RiseAt, acct.RiseRate, rose)
	}
	*clock = clock.Add(3 * time.Minute)
	poll(43)
	if !acct.RiseAt.Equal(rose) {
		t.Fatalf("a flat pair moved RiseAt to %v", acct.RiseAt)
	}
	*clock = clock.Add(3 * time.Minute)
	poll(2)
	if !acct.RiseAt.IsZero() || acct.RiseRate != 0 {
		t.Fatalf("a window reset kept RiseAt %v, rate %v", acct.RiseAt, acct.RiseRate)
	}
}
