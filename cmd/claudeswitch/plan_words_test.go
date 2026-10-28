package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// plan said "recovers work-1 in 1h23m0s": a span a person reads is said as
// the rest of the CLI says one.
func TestPlanSaysWhenAnAccountRecoversThePersonWay(t *testing.T) {
	var b bytes.Buffer
	printPlan(&b, &config.Config{HardFloor: 96}, policy.Decision{Kind: policy.Wait, Reason: "every account is out",
		RecoversAccount: "work-1", RecoversAt: time.Now().Add(83*time.Minute + 30*time.Second)})
	if out := b.String(); !strings.Contains(out, "work-1 in 1h 23m") || strings.Contains(out, "0s") {
		t.Errorf("want \"work-1 in 1h 23m\":\n%s", out)
	}
}
