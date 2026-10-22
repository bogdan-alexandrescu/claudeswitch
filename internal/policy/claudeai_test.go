package policy

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// IMPROVEMENTS P4 (owner decision 2026-10-21): a session that called a Claude
// in Chrome tool within hold_for_claude_ai (5 minutes unless set) counts as
// busy, so a rotation waits for its claude.ai work. It still goes at the hard
// floor, and hold_for_claude_ai = 0 is the behaviour before.

func overTrigger() *state.State {
	return st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(10, 10, now.Add(time.Hour)),
	})
}

func TestTheBrowserHoldIsFiveMinutesUnlessSet(t *testing.T) {
	if got := cfg().ClaudeAIHold(); got != 5*time.Minute {
		t.Fatalf("ClaudeAIHold() unset = %v, want 5m", got)
	}
	c := cfg()
	c.HoldForClaudeAI = &config.Duration{}
	if got := c.ClaudeAIHold(); got != 0 {
		t.Fatalf("an explicit 0 read back as %v; zero must turn the hold off", got)
	}
}

func TestABrowserCallTwoMinutesAgoHoldsARotation(t *testing.T) {
	call := now.Add(-2*time.Minute - 10*time.Second)
	d := Decide(Input{Cfg: cfg(), Now: now, St: overTrigger(), ClaudeAIAt: call})
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want the switch to work-b it would make anyway", d)
	}
	if d.Held != "session busy (claude.ai work 2m ago)" {
		t.Fatalf("Held = %q, want %q", d.Held, "session busy (claude.ai work 2m ago)")
	}
	if want := call.Add(5 * time.Minute); !d.HeldUntil.Equal(want) {
		t.Errorf("HeldUntil = %v, want five minutes after the call, %v", d.HeldUntil, want)
	}
}

func TestABrowserCallUnderAMinuteAgoReadsInSeconds(t *testing.T) {
	d := Decide(Input{Cfg: cfg(), Now: now, St: overTrigger(), ClaudeAIAt: now.Add(-40 * time.Second)})
	if d.Held != "session busy (claude.ai work 40s ago)" {
		t.Fatalf("Held = %q", d.Held)
	}
}

func TestABrowserCallOlderThanTheHoldHoldsNothing(t *testing.T) {
	d := Decide(Input{Cfg: cfg(), Now: now, St: overTrigger(), ClaudeAIAt: now.Add(-5 * time.Minute)})
	if d.Kind != Switch || d.Held != "" {
		t.Fatalf("got %v held %q, want an unheld switch once the hold has run out", d, d.Held)
	}
}

func TestHoldForClaudeAIZeroHoldsNothing(t *testing.T) {
	c := cfg()
	c.HoldForClaudeAI = &config.Duration{}
	d := Decide(Input{Cfg: c, Now: now, St: overTrigger(), ClaudeAIAt: now.Add(-10 * time.Second)})
	if d.Kind != Switch || d.Held != "" {
		t.Fatalf("got %v held %q, want today's unheld switch", d, d.Held)
	}
}

// Past the hard floor the switch is forced, as it is mid-turn: the wall is
// worse than a broken browser bridge.
func TestABrowserCallDoesNotHoldAForcedSwitch(t *testing.T) {
	s := overTrigger()
	s.Accounts["work-a"] = reading(97, 30, now.Add(time.Hour))
	d := Decide(Input{Cfg: cfg(), Now: now, St: s, ClaudeAIAt: now.Add(-time.Minute)})
	if d.Kind != Switch || !d.Forced || d.Held != "" {
		t.Fatalf("got %v held %q, want a forced, unheld switch", d, d.Held)
	}
}

// A refused account serves nothing, the claude.ai work included: the switch
// keeps today's timing for a refusal.
func TestABrowserCallDoesNotHoldASwitchOffARefusedAccount(t *testing.T) {
	s := overTrigger()
	s.Accounts["work-a"] = reading(50, 30, now.Add(time.Hour))
	s.Accounts["work-a"].BurntTil, s.Accounts["work-a"].BurntWin = now.Add(time.Hour), "five_hour"
	d := Decide(Input{Cfg: cfg(), Now: now, St: s, ClaudeAIAt: now.Add(-time.Minute)})
	if d.Kind != Switch || d.Held != "" {
		t.Fatalf("got %v held %q, want an unheld switch off the refused account", d, d.Held)
	}
}

// A blind failover is a rotation like any other for timing: busy holds it.
func TestABrowserCallHoldsABlindFailover(t *testing.T) {
	s := st("work-a", map[string]*state.Account{
		"work-a": blindActive(3, 40),
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})
	d := Decide(Input{Cfg: blindCfg(), Now: now, St: s, ClaudeAIAt: now.Add(-3 * time.Minute)})
	if d.Kind != Switch || !d.Failover || d.Held != "session busy (claude.ai work 3m ago)" {
		t.Fatalf("got %v failover %v held %q", d, d.Failover, d.Held)
	}
}

// A decision that is not a switch has nothing to hold.
func TestABrowserCallHoldsNothingWhenStaying(t *testing.T) {
	s := overTrigger()
	s.Accounts["work-a"] = reading(40, 30, now.Add(time.Hour))
	d := Decide(Input{Cfg: cfg(), Now: now, St: s, ClaudeAIAt: now.Add(-time.Minute)})
	if d.Kind != Stay || d.Held != "" {
		t.Fatalf("got %v held %q", d, d.Held)
	}
}

// The daemon's log and `cs why` say a held switch in the same words.
func TestAHeldSwitchReadsAsWaitingForAnIdleGap(t *testing.T) {
	got := WaitingFor("session busy (claude.ai work 2m ago)")
	if want := "switch wanted, session busy (claude.ai work 2m ago) — waiting for an idle gap"; got != want {
		t.Fatalf("WaitingFor = %q, want %q", got, want)
	}
	if got, want := WaitingFor("session busy"), "switch wanted, session busy — waiting for an idle gap"; got != want {
		t.Fatalf("WaitingFor = %q, want %q (the mid-turn line, unchanged)", got, want)
	}
}
