package poller

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// 0.6.1: fetchInto computed the burn rate while LastAt still equalled the
// PrevAt it had just set, so the pair spanned no time and last_rate was
// always 0. A rising pair must leave its rate in LastRate; a flat pair keeps
// it; a fall (a window reset) clears it.
func TestLastRateIsTheRisingPairsRate(t *testing.T) {
	p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
	acct := p.st.Get("a")
	poll := func(five float64) {
		api.five["tok-a"] = five
		if r := p.fetchInto(context.Background(), acct, "tok-a", time.Time{}, usage.Interactive); r != usage.ReasonOK {
			t.Fatalf("poll refused: %q", r)
		}
	}

	poll(40)
	if acct.LastRate != 0 {
		t.Fatalf("one reading set LastRate = %v", acct.LastRate)
	}
	*clock = clock.Add(2 * time.Minute)
	poll(46)
	if acct.LastRate != 3 {
		t.Fatalf("40%% then 46%% two minutes apart: LastRate = %v, want 3", acct.LastRate)
	}
	*clock = clock.Add(2 * time.Minute)
	poll(46)
	if acct.LastRate != 3 {
		t.Fatalf("a flat pair changed LastRate to %v, want 3 kept", acct.LastRate)
	}
	*clock = clock.Add(2 * time.Minute)
	poll(1)
	if acct.LastRate != 0 {
		t.Fatalf("a window reset kept LastRate = %v", acct.LastRate)
	}
}
