package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Owner report 2026-10-23: the default profile was pinned to personal,
// and a `cs use AI-Eng-2` from another session switched it anyway, which
// moved the deck session's Claude in Chrome to another account's browser in
// the middle of its work. A pin is a promise that nothing moves the profile:
// `cs use` to another account refuses, says why and how to move or lift the
// pin, and touches nothing.
func TestUseRefusesToMoveAPinnedProfile(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("w1")
	st.Get("w2")
	st.Profile("work").SetActive("w1")
	st.Profile("work").Pinned = "w1"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	err = cmdUse([]string{"--config", path, "--profile", "work", "w2", "--json"})
	if got := errCode(t, err); got != codePinned {
		t.Fatalf("code %q (%v), want %q", got, err, codePinned)
	}
	for _, want := range []string{"pinned to w1", "cs account unpin", "cs use w2"} {
		if !strings.Contains(err.Error()+" "+appErrorHint(err), want) {
			t.Errorf("refusal lacks %q: %v / %s", want, err, appErrorHint(err))
		}
	}
	back, _ := state.Load("", "default", "work")
	if back.Profile("work").Active != "w1" || back.Profile("work").Pinned != "w1" {
		t.Fatalf("the refusal changed state: active %q pinned %q", back.Profile("work").Active, back.Profile("work").Pinned)
	}
}

func appErrorHint(err error) string {
	var ae *appError
	if errors.As(err, &ae) {
		return ae.Hint
	}
	return ""
}

// usePinWorld is the work profile live on w1, w1 and w2 both recorded.
func usePinWorld(t *testing.T, body string) (string, *state.State) {
	t.Helper()
	path := appWorld(t, body)
	st, err := state.Load("", "default", "work")
	if err != nil {
		t.Fatal(err)
	}
	st.Get("w1")
	st.Get("w2")
	st.Profile("work").SetActive("w1")
	return path, st
}

// Owner decision 2026-10-23, after a test `cs use` moved a deck session's
// browser mid-task: a manual switch refuses while a session in the profile
// did claude.ai work (Claude in Chrome, an artifact, a Doc) within
// hold_for_claude_ai, saying what it would move; --force switches anyway.
// The automatic hold already protects that work from rotation.
func TestUseRefusesWhileClaudeAIWorkIsInProgress(t *testing.T) {
	path, st := usePinWorld(t, twoProfileTOML)
	st.Profile("work").ClaudeAIAt = time.Now().Add(-time.Minute)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	err := cmdUse([]string{"--config", path, "--profile", "work", "w2", "--json"})
	if got := errCode(t, err); got != codeClaudeAIBusy {
		t.Fatalf("code %q (%v), want %q", got, err, codeClaudeAIBusy)
	}
	for _, want := range []string{"claude.ai work", "1m ago", "--force"} {
		if !strings.Contains(err.Error()+" "+appErrorHint(err), want) {
			t.Errorf("refusal lacks %q: %v / %s", want, err, appErrorHint(err))
		}
	}
}

// Older work, or the hold turned off, refuses nothing. (Tested on the guard
// itself: past it, `use` reaches the vault, which tests must not.)
func TestTheBusyGuardIgnoresOldOrDisabledClaudeAIWork(t *testing.T) {
	path, st := usePinWorld(t, twoProfileTOML)
	cfg := loadOrFail(t, path)
	now := time.Now()
	st.Profile("work").ClaudeAIAt = now.Add(-time.Minute)
	if claudeAIBusyRefusal(cfg, st, "work", "w2", now) == nil {
		t.Fatal("a minute-old call refused nothing")
	}
	st.Profile("work").ClaudeAIAt = now.Add(-6 * time.Minute)
	if err := claudeAIBusyRefusal(cfg, st, "work", "w2", now); err != nil {
		t.Fatalf("refused for claude.ai work six minutes old: %v", err)
	}
	st.Profile("work").ClaudeAIAt = now.Add(-time.Minute)
	off := loadOrFail(t, appWorld(t, "hold_for_claude_ai = \"0s\"\n"+twoProfileTOML))
	if err := claudeAIBusyRefusal(off, st, "work", "w2", now); err != nil {
		t.Fatalf("refused with the hold turned off: %v", err)
	}
}

// `cs use` of the account already live is no switch (seen 2026-10-23: one
// reset the profile's switch time, so every session's next claude.ai call
// waited out settle_after_swap for nothing). It says so and changes nothing;
// --force reinstalls it.
func TestUseOfTheLiveAccountChangesNothing(t *testing.T) {
	path, st := usePinWorld(t, twoProfileTOML)
	before := time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC)
	st.Profile("work").LastSwitch = before
	st.Profile("work").ClaudeAIAt = time.Now().Add(-time.Minute) // not a switch, so no busy refusal either
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error { return cmdUse([]string{"--config", path, "--profile", "work", "w1", "--json"}) })
	if !strings.Contains(out, `"already": true`) {
		t.Fatalf("answer = %s", out)
	}
	back, _ := state.Load("", "default", "work")
	if !back.Profile("work").LastSwitch.Equal(before) {
		t.Fatalf("last switch moved to %v", back.Profile("work").LastSwitch)
	}
}
