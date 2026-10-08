package config

import (
	"strings"
	"testing"
	"time"
)

// An explicit fast poll_hot (written by `cs config` or setup before the
// default became 60s) is the person's setting and is kept — but `status`
// shows Warnings, so it says what it costs and how to change it.
func TestWarningsFlagAFastHotCadence(t *testing.T) {
	c := &Config{PollHot: Duration{20 * time.Second}}
	got := strings.Join(c.Warnings(), "\n")
	for _, want := range []string{"poll_hot", "20s", "~8 min", "cs config poll_hot 60s"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, d := range []time.Duration{time.Minute, 2 * time.Minute, 0} {
		c := &Config{PollHot: Duration{d}}
		if w := c.Warnings(); len(w) != 0 {
			t.Errorf("poll_hot %v: no warning expected, got %v", d, w)
		}
	}
}
