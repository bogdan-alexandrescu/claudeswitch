package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// A reason is read by people in why, plan and the audit log: "only 2m13s
// since the last switch (cooldown 10m0s)" is Go's way of saying a span, not
// a person's.
func TestTheCooldownReasonSaysSpansThePersonWay(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, LastSwitch: now.Add(-3*time.Minute - 13*time.Second),
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(88, 30, now.Add(time.Hour)),
			"work-b": reading(10, 10, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if !strings.Contains(d.Reason, "only 3m since the last switch (cooldown 10m)") {
		t.Errorf("reason %q, want \"only 3m since the last switch (cooldown 10m)\"", d.Reason)
	}
}
