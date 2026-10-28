package main

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// `account list`'s binding window is the one nearest its own trigger, as the
// daemon judges it: at 80% of the session and 90% of the week, against 85
// and 98, the session.
func TestTheBindingWindowIsTheOneNearestItsTrigger(t *testing.T) {
	f, s := 80.0, 90.0
	acct := &state.Account{Last: &usage.Usage{
		FiveHour: usage.Window{Utilization: &f},
		SevenDay: usage.Window{Utilization: &s},
	}}
	cfg := &config.Config{SwitchAt: 85, SwitchAtWeekly: 98}
	m, _ := readingJSON(cfg, acct, time.Now()).(map[string]any)
	if m["binding"] != "five_hour" {
		t.Fatalf("binding = %v, want five_hour", m["binding"])
	}
}
