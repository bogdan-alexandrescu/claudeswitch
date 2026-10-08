package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// New rows say "profile" (D19); rows a dev build wrote say "instance" and
// still read into Profile.
func TestOldRowsWithInstanceStillRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	old := `{"at":"2026-10-07T09:00:00Z","kind":"switch","instance":"work","from":"a","to":"b"}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Write(Event{Kind: "switch", Profile: "home", From: "b", To: "c"}); err != nil {
		t.Fatal(err)
	}
	evs, err := Tail(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Profile != "work" || evs[1].Profile != "home" {
		t.Fatalf("events = %+v, want profiles work then home", evs)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if !strings.Contains(lines[1], `"profile":"home"`) || strings.Contains(lines[1], `"instance"`) {
		t.Fatalf("new row written as %s, want a profile field and no instance", lines[1])
	}
}
