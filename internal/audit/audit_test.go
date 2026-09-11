package audit

import (
	"testing"
	"time"
)

func TestFilterByKindAndAge(t *testing.T) {
	now := time.Now()
	events := []Event{
		{At: now.Add(-48 * time.Hour), Kind: "decision"},
		{At: now.Add(-2 * time.Hour), Kind: "rejection"},
		{At: now.Add(-1 * time.Hour), Kind: "decision"},
		{At: now.Add(-30 * time.Minute), Kind: "switch"},
	}

	if got := Filter(events, "", 0); len(got) != 4 {
		t.Fatalf("no filter should pass everything, got %d", len(got))
	}
	if got := Filter(events, "decision", 0); len(got) != 2 {
		t.Fatalf("kind filter: got %d, want 2", len(got))
	}
	if got := Filter(events, "", 24*time.Hour); len(got) != 3 {
		t.Fatalf("age filter: got %d, want 3", len(got))
	}
	got := Filter(events, "decision", 24*time.Hour)
	if len(got) != 1 || got[0].Kind != "decision" {
		t.Fatalf("combined filter: got %v", got)
	}
}

func TestFilterDoesNotAliasTheInput(t *testing.T) {
	events := []Event{{Kind: "a"}, {Kind: "b"}}
	got := Filter(events, "b", 0)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	got[0].Kind = "mutated"
	if events[0].Kind != "a" {
		t.Fatal("Filter wrote through into the caller's slice")
	}
}
