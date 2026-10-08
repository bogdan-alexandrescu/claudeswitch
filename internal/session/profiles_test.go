package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
)

// Owner, 2026-10-08: cs session covers every profile. Each profile's
// messages are attributed by that profile's own switches; the switch list
// merges them all, each labelled by its profile.

// transcript writes one transcript under root with a message per entry:
// the time after base and its input tokens.
func transcript(t *testing.T, root, name string, msgs map[time.Duration]int64) {
	t.Helper()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var body string
	for at, in := range msgs {
		body += fmt.Sprintf(`{"timestamp": %q, "requestId": "%s-%d", "message": {"model": "m", "usage": {"input_tokens": %d}}}`+"\n",
			base.Add(at).Format(time.RFC3339), name, at, in)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func twoProfiles(t *testing.T) ([]Source, []audit.Event) {
	d, w := t.TempDir(), t.TempDir()
	transcript(t, d, "d", map[time.Duration]int64{30 * time.Minute: 10, 3 * time.Hour: 100})
	transcript(t, w, "w", map[time.Duration]int64{30 * time.Minute: 1000, 3 * time.Hour: 10000})
	events := []audit.Event{
		{Kind: "switch", Profile: "default", At: base.Add(time.Hour), From: "a", To: "b", Reason: "r1"},
		{Kind: "switch", Profile: "work", At: base.Add(2 * time.Hour), From: "c", To: "e", Reason: "r2"},
	}
	return []Source{
		{Profile: "default", Root: d, Active: "b", Legacy: true},
		{Profile: "work", Root: w, Active: "e"},
	}, events
}

func shares(s *Session) map[string]int64 {
	out := map[string]int64{}
	for _, sh := range s.Shares {
		out[sh.Account] = sh.Tokens.Input
	}
	return out
}

func TestEveryProfileIsCounted(t *testing.T) {
	src, events := twoProfiles(t)
	s, err := BuildFrom(events, src, base, base.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Each message goes to the account live in its own profile when it was
	// written: work's 3h message is e's, not b's (default's switch).
	want := map[string]int64{"a": 10, "b": 100, "c": 1000, "e": 10000}
	got := shares(s)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %d tokens, want %d (all %v)", k, got[k], v, got)
		}
	}
	if s.Total.Input != 11110 || s.Messages != 4 {
		t.Errorf("total %d over %d messages", s.Total.Input, s.Messages)
	}
	if len(s.Switches) != 2 || s.Switches[0].Profile != "default" || s.Switches[1].Profile != "work" {
		t.Fatalf("switches = %+v, want default's then work's", s.Switches)
	}
}

func TestOneProfileOnly(t *testing.T) {
	src, events := twoProfiles(t)
	s, err := BuildFrom(events, src[1:], base, base.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got := shares(s)
	if len(got) != 2 || got["c"] != 1000 || got["e"] != 10000 {
		t.Errorf("shares = %v, want only work's c and e", got)
	}
	if len(s.Switches) != 1 || s.Switches[0].Profile != "work" {
		t.Errorf("switches = %+v, want work's only", s.Switches)
	}
}

// A switch from before profiles has no profile: it is the default
// profile's (the only one there was), labelled so.
func TestALegacySwitchIsTheDefaultProfiles(t *testing.T) {
	src, events := twoProfiles(t)
	events[0].Profile = ""
	s, err := BuildFrom(events, src, base, base.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := shares(s); got["a"] != 10 || got["b"] != 100 || got["e"] != 10000 {
		t.Errorf("shares = %v", got)
	}
	if s.Switches[0].Profile != "default" {
		t.Errorf("legacy switch labelled %q, want default", s.Switches[0].Profile)
	}
}
