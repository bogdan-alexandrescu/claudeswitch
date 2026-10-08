package credstore

import (
	"strings"
	"testing"
)

// The ground truth this lane turns on: the profile a person runs without
// CLAUDE_CONFIG_DIR reads the bare item, even though its directory is
// ~/.claude. An omitted dir says exactly that, and needs no lookup.
func TestLiveServiceForOmittedDirIsBare(t *testing.T) {
	f := &fakeLookup{}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-other") // this process's env must not leak in
	got, err := LiveServiceFor("")
	if err != nil {
		t.Fatal(err)
	}
	if got != liveServiceBase {
		t.Fatalf("LiveServiceFor(\"\") = %s, want %s", got, liveServiceBase)
	}
	if len(f.asked) != 0 {
		t.Fatalf("the bare item needs no lookup, asked %v", f.asked)
	}
}

// The same directory set explicitly is a different keychain item.
func TestLiveServiceForExplicitDotClaudeIsSuffixed(t *testing.T) {
	want := liveServiceFor("~/.claude")
	f := &fakeLookup{present: map[string]bool{liveServiceBase: true, want: true}}
	f.install(t)
	liveEnv(t, "/h", "")
	got, err := LiveServiceFor("~/.claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != want || got == liveServiceBase {
		t.Fatalf("LiveServiceFor(~/.claude) = %s, want the suffixed %s", got, want)
	}
}

func TestLiveServiceForFindsTheExpandedSpelling(t *testing.T) {
	want := liveServiceFor("/h/.claude-work")
	f := &fakeLookup{present: map[string]bool{want: true}}
	f.install(t)
	liveEnv(t, "/h", "")
	got, err := LiveServiceFor("~/.claude-work")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// D9 holds for a configured dir too: no suffixed item is an error, with the
// not-logged-in hint when the plain item exists, never the plain item itself.
func TestLiveServiceForNotLoggedInHint(t *testing.T) {
	f := &fakeLookup{present: map[string]bool{liveServiceBase: true}}
	f.install(t)
	liveEnv(t, "/h", "")
	got, err := LiveServiceFor("~/.claude-work")
	if err == nil {
		t.Fatalf("want an error, got %s", got)
	}
	for _, w := range []string{"~/.claude-work", "logged in", "/login"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
}

// The env-based path is LiveServiceFor of the env's hashed dir and keeps its
// behaviour; an explicit lookup for one dir must not be answered from the
// cache entry of another.
func TestLiveServiceForAndEnvShareResolution(t *testing.T) {
	work, other := liveServiceFor("/h/.claude-work"), liveServiceFor("/h/.claude-other")
	f := &fakeLookup{present: map[string]bool{work: true, other: true}}
	f.install(t)
	liveEnv(t, "/h", "/h/.claude-work")
	if got := resolve(t); got != work {
		t.Fatalf("LiveService() = %s, want %s", got, work)
	}
	got, err := LiveServiceFor("/h/.claude-other")
	if err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Fatalf("LiveServiceFor(other) = %s, want %s", got, other)
	}
}
