package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// RECENT SWITCHES is each profile's last five. Taking the last five of every
// profile's and then keeping one profile's left a profile whose switches
// were older than five of another's with none listed.
func TestRecentSwitchesAreEachProfilesLastFive(t *testing.T) {
	_, cfg, st := multiWorld(t)
	now := time.Now()
	var evs []audit.Event
	// default switched once, long ago; work six times since.
	evs = append(evs, audit.Event{At: now.Add(-3 * time.Hour), Kind: "switch", Profile: "default",
		From: "personal", To: "personal", Reason: "default-switch"})
	for i := 0; i < 6; i++ {
		evs = append(evs, audit.Event{At: now.Add(time.Duration(i-6) * time.Minute), Kind: "switch",
			Profile: "work", From: "w1", To: "w2", Reason: fmt.Sprintf("reason-%d", i)})
	}
	log, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if err := log.Write(e); err != nil {
			t.Fatal(err)
		}
	}

	got := recentSwitches(5)
	var reasons []string
	for _, e := range got {
		reasons = append(reasons, e.Reason)
	}
	if len(got) != 6 || got[0].Reason != "default-switch" {
		t.Errorf("recentSwitches(5) = %v, want default's one and work's last five", reasons)
	}

	// Every block lists at most five, whatever it is handed.
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, evs, now, "")
	bl := blocks(t, b.String())
	if !strings.Contains(bl["default"], "RECENT SWITCHES") {
		t.Errorf("default's one switch must be listed:\n%s", bl["default"])
	}
	if n := strings.Count(bl["work"], "reason-"); n != 5 || strings.Contains(bl["work"], "reason-0") {
		t.Errorf("work must list its last five switches (%d listed):\n%s", n, bl["work"])
	}
}
