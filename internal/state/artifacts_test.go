package state

import (
	"fmt"
	"testing"
	"time"
)

// Phase 1 G (owner decision 2026-10-21): claude.ai keeps an artifact for the
// account that published it, and only that account can read, change or
// delete it (GROUND_TRUTH §55). The plugin records each publish here, so a
// later call on it while another account is live can be refused with
// instructions instead of failing as "There is no Artifact at …".

func TestArtifactIDFromItsURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://claude.ai/artifact/abc123":         "abc123",
		"https://claude.ai/code/artifact/0f6e-11aa": "0f6e-11aa",
		"claude.ai/code/artifact/0f6e-11aa?tab=1":   "0f6e-11aa",
		"https://claude.ai/artifact/abc123/":        "abc123",
		"https://claude.ai/artifact/abc123#c":       "abc123",
		"https://claude.ai/chat/abc123":             "",
		"":                                          "",
	} {
		if got := ArtifactID(url); got != want {
			t.Errorf("ArtifactID(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestARecordedArtifactIsFoundByIDAndByItsSessionsPath(t *testing.T) {
	isolate(t)
	at := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	a := Artifact{ID: "abc", URL: "https://claude.ai/code/artifact/abc", Path: "/w/page.html",
		Session: "s1", Account: "AI-Eng-3", Seat: "u3@o1", At: at}
	if err := RecordArtifact(a); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ArtifactOwner("abc")
	if err != nil || !ok || got != a {
		t.Fatalf("by id: %+v %v %v, want %+v", got, ok, err, a)
	}
	if got, ok, _ := ArtifactAt("s1", "/w/page.html"); !ok || got.ID != "abc" {
		t.Fatalf("by session and path: %+v %v", got, ok)
	}
	// A path is a conversation's handle on its artifact: another session
	// publishing the same file makes a new one.
	if _, ok, _ := ArtifactAt("s2", "/w/page.html"); ok {
		t.Fatal("found by path from another session")
	}
	// Publishing again replaces the record.
	a.Account, a.At = "personal", at.Add(time.Minute)
	if err := RecordArtifact(a); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := ArtifactOwner("abc"); got.Account != "personal" {
		t.Fatalf("after a second publish: %+v", got)
	}
}

func TestTheArtifactBookKeepsTheNewest(t *testing.T) {
	isolate(t)
	base := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	for i := 0; i < ArtifactKeep+5; i++ {
		if err := RecordArtifact(Artifact{ID: fmt.Sprint("a", i), Account: "x", At: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := ArtifactOwner("a0"); ok {
		t.Error("the oldest is still kept")
	}
	if _, ok, _ := ArtifactOwner(fmt.Sprint("a", ArtifactKeep+4)); !ok {
		t.Error("the newest is gone")
	}
}

func TestNoArtifactBookIsNoOwner(t *testing.T) {
	isolate(t)
	if _, ok, err := ArtifactOwner("abc"); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}
