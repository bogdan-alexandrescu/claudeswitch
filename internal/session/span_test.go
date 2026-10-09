package session

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
)

// The default span starts at today's first switch in any reported profile,
// or 8 hours ago when that is earlier (owner decision, 2026-10-08).
func TestDefaultFrom(t *testing.T) {
	loc := time.Local
	to := time.Date(2026, 10, 8, 18, 0, 0, 0, loc)
	sw := func(h, m int, profile string) audit.Event {
		return audit.Event{At: time.Date(2026, 10, 8, h, m, 0, 0, loc), Kind: "switch", Profile: profile}
	}
	srcs := []Source{{Profile: "default", Legacy: true}, {Profile: "work"}}
	eight := to.Add(-8 * time.Hour)

	cases := []struct {
		name   string
		events []audit.Event
		srcs   []Source
		want   time.Time
	}{
		{"no switches: the last 8 hours", nil, srcs, eight},
		{"a switch inside the 8 hours changes nothing", []audit.Event{sw(15, 1, "default")}, srcs, eight},
		{"today's first switch, earlier than 8 hours", []audit.Event{sw(15, 1, "default"), sw(9, 12, "work")}, srcs, time.Date(2026, 10, 8, 9, 12, 0, 0, loc)},
		{"yesterday's switch is ignored", []audit.Event{{At: to.Add(-20 * time.Hour), Kind: "switch", Profile: "work"}}, srcs, eight},
		{"a profile not reported is ignored", []audit.Event{sw(7, 0, "review")}, srcs, eight},
		{"a pre-profile switch counts as default's", []audit.Event{sw(6, 30, "")}, srcs, time.Date(2026, 10, 8, 6, 30, 0, 0, loc)},
		{"only switches count, not decisions", []audit.Event{{At: time.Date(2026, 10, 8, 5, 0, 0, 0, loc), Kind: "decision", Profile: "work"}}, srcs, eight},
		{"--profile work leaves out default's switch", []audit.Event{sw(6, 0, "default")}, srcs[1:], eight},
	}
	for _, c := range cases {
		if got := DefaultFrom(c.events, c.srcs, to); !got.Equal(c.want) {
			t.Errorf("%s: from = %v, want %v", c.name, got.Format("15:04"), c.want.Format("15:04"))
		}
	}
}
