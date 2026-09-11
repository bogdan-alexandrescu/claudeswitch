package session

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
)

var base = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// Attribution is the whole point: a message belongs to whichever account was
// live when it was written, not to whichever is live now.
func TestAccountAtFollowsTheSwitches(t *testing.T) {
	events := []audit.Event{
		{Kind: "switch", At: base.Add(1 * time.Hour), From: "a", To: "b"},
		{Kind: "switch", At: base.Add(3 * time.Hour), From: "b", To: "c"},
	}
	tl := buildTimeline(events, base, "c")

	for _, tc := range []struct {
		at   time.Duration
		want string
	}{
		{30 * time.Minute, "a"}, // before the first switch
		{90 * time.Minute, "b"}, // between the two
		{4 * time.Hour, "c"},    // after the last
	} {
		if got := accountAt(tl, base.Add(tc.at)); got != tc.want {
			t.Errorf("at +%s: got %q, want %q", tc.at, got, tc.want)
		}
	}
}

// The window usually opens mid-session, so the account live at its start has to
// be inferred from the switch that later moved away from it.
func TestTimelineInfersTheAccountLiveAtTheWindowStart(t *testing.T) {
	events := []audit.Event{
		{Kind: "switch", At: base.Add(2 * time.Hour), From: "started-here", To: "next"},
	}
	tl := buildTimeline(events, base, "next")
	if got := accountAt(tl, base.Add(time.Hour)); got != "started-here" {
		t.Fatalf("got %q, want started-here", got)
	}
}

// A switch before the window means that target was live when the window opened.
func TestTimelineUsesTheLastSwitchBeforeTheWindow(t *testing.T) {
	events := []audit.Event{
		{Kind: "switch", At: base.Add(-2 * time.Hour), From: "old", To: "current"},
	}
	tl := buildTimeline(events, base, "current")
	if got := accountAt(tl, base.Add(time.Minute)); got != "current" {
		t.Fatalf("got %q, want current", got)
	}
}

// With no audit history at all, everything belongs to the account live now —
// the only account we can honestly name.
func TestTimelineFallsBackToTheActiveAccount(t *testing.T) {
	tl := buildTimeline(nil, base, "only-one")
	if got := accountAt(tl, base.Add(time.Hour)); got != "only-one" {
		t.Fatalf("got %q, want only-one", got)
	}
}

// Switch events are not guaranteed to arrive in order.
func TestTimelineSortsOutOfOrderEvents(t *testing.T) {
	events := []audit.Event{
		{Kind: "switch", At: base.Add(3 * time.Hour), From: "b", To: "c"},
		{Kind: "switch", At: base.Add(1 * time.Hour), From: "a", To: "b"},
	}
	tl := buildTimeline(events, base, "c")
	if got := accountAt(tl, base.Add(90*time.Minute)); got != "b" {
		t.Fatalf("got %q, want b", got)
	}
}

// Non-switch events must not move the timeline.
func TestTimelineIgnoresOtherEventKinds(t *testing.T) {
	events := []audit.Event{
		{Kind: "decision", At: base.Add(time.Hour), To: "not-a-switch"},
		{Kind: "rejection", At: base.Add(2 * time.Hour), From: "a"},
	}
	tl := buildTimeline(events, base, "a")
	if got := accountAt(tl, base.Add(3*time.Hour)); got != "a" {
		t.Fatalf("a decision or rejection must not change attribution: got %q", got)
	}
}

func TestTokensTotalCountsEveryKind(t *testing.T) {
	tok := Tokens{Input: 1, Output: 2, CacheRead: 4, CacheCreation: 8, Thinking: 16}
	// Thinking is a subset of output, so it must not be added again.
	if got := tok.Total(); got != 15 {
		t.Fatalf("got %d, want 15 (thinking is already inside output)", got)
	}
}

func TestTokensAddAccumulates(t *testing.T) {
	var acc Tokens
	acc.add(Tokens{Input: 1, Output: 2, Thinking: 1})
	acc.add(Tokens{Input: 3, Output: 4, Thinking: 2})
	if acc.Input != 4 || acc.Output != 6 || acc.Thinking != 3 {
		t.Fatalf("got %+v", acc)
	}
}
