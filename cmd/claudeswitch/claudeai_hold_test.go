package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// IMPROVEMENTS P4 (owner decision 2026-10-21): rotating a profile while one
// of its sessions runs a Claude in Chrome task breaks the browser bridge. A
// session that called a Claude in Chrome tool within hold_for_claude_ai (5
// minutes unless set) counts as busy: the rotation waits for the hold to run
// out, still goes at once past the hard floor, and hold_for_claude_ai = 0 is
// the behaviour before. Phase 1 H (owner decision 2026-10-21) renamed it from
// hold_for_browser and widened it to artifact and Docs calls, which act as
// the account live when they are made (GROUND_TRUTH §55); the detector's
// tests cover which calls count.

// browserRig is one profile on a, over its 85% trigger, beside b with room;
// the transcripts are quiet, so without a browser call it would swap now.
func browserRig(t *testing.T, cfg *config.Config) *rig {
	t.Helper()
	r := newRig(t, cfg, true)
	r.d.st.Default().SetActive("a")
	put(r.d.st, "a", 90)
	put(r.d.st, "b", 10)
	return r
}

func TestDaemonHoldsARotationWhileABrowserTaskRuns(t *testing.T) {
	r := browserRig(t, testCfg())
	det, il := r.dets["default"], r.d.profs[0]
	det.browser = time.Now().Add(-2*time.Minute - 10*time.Second)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("swapped two minutes after a browser call: %+v", swaps)
	}
	want := "switch wanted, session busy (claude.ai work 2m ago) — waiting for an idle gap"
	if !strings.Contains(r.logs.String(), want) {
		t.Fatalf("log lacks %q:\n%s", want, r.logs.String())
	}

	// max_switch_wait (90s here) does not cut the hold short: the hold is
	// the wait, and only the hard floor overrides it.
	il.wantSwitchSince = time.Now().Add(-time.Hour)
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("max_switch_wait ended a browser hold: %+v", swaps)
	}

	// Five minutes after the last call the hold is over. (The call moves
	// back in the state too: the daemon only ever records a later one.)
	det.browser = time.Now().Add(-5*time.Minute - time.Second)
	r.d.st.Default().ClaudeAIAt = det.browser
	r.d.evaluate(context.Background(), il, "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("after the hold: swaps = %+v, want one into b", swaps)
	}
}

func TestDaemonABrowserHoldStillGoesAtTheHardFloor(t *testing.T) {
	r := browserRig(t, testCfg())
	put(r.d.st, "a", 97) // testCfg's hard floor is 96
	r.dets["default"].browser = time.Now().Add(-10 * time.Second)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("past the hard floor: swaps = %+v, want one into b at once", swaps)
	}
}

func TestDaemonWithHoldForClaudeAIZeroRotatesAsBefore(t *testing.T) {
	cfg := testCfg()
	cfg.HoldForClaudeAI = &config.Duration{}
	r := browserRig(t, cfg)
	r.dets["default"].browser = time.Now().Add(-10 * time.Second)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("hold_for_claude_ai = 0: swaps = %+v, want one into b at once", swaps)
	}
	if strings.Contains(r.logs.String(), "claude.ai work") {
		t.Fatalf("hold_for_claude_ai = 0 still spoke of claude.ai work:\n%s", r.logs.String())
	}
}

// A refused account serves nothing, the browser task included, so a refusal
// keeps today's timing.
func TestDaemonABrowserHoldDoesNotDelayARefusal(t *testing.T) {
	r := browserRig(t, testCfg())
	put(r.d.st, "a", 50)
	a := r.d.st.Accounts["a"]
	a.BurntTil, a.BurntWin = time.Now().Add(time.Hour), "five_hour"
	r.dets["default"].browser = time.Now().Add(-10 * time.Second)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("refused, browser busy, transcripts quiet: swaps = %+v, want one into b at once", swaps)
	}
}

func TestDaemonHoldsABlindFailoverForABrowserTask(t *testing.T) {
	r := blindRig(t, 3)
	r.dets["default"].browser = time.Now().Add(-3*time.Minute - 5*time.Second)
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if swaps := r.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("failed over during a browser task: %+v", swaps)
	}
	want := "blind failover wanted, session busy (claude.ai work 3m ago) — waiting for an idle gap, never mid-turn"
	if !strings.Contains(r.logs.String(), want) {
		t.Fatalf("log lacks %q:\n%s", want, r.logs.String())
	}
}

// The daemon records the last call in its state: `cs why` reads it there, and
// a restarted daemon, whose detector has not seen the call, keeps holding.
func TestDaemonRecordsTheLastClaudeAICallAndARestartKeepsHolding(t *testing.T) {
	r := browserRig(t, testCfg())
	call := time.Now().Add(-time.Minute).Truncate(time.Second)
	r.dets["default"].browser = call
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if got := r.d.st.Default().ClaudeAIAt; !got.Equal(call) {
		t.Fatalf("state's claude_ai_at = %v, want the detector's %v", got, call)
	}

	again := browserRig(t, testCfg())
	again.d.st.Default().ClaudeAIAt = call // what the previous daemon saved
	again.d.evaluate(context.Background(), again.d.profs[0], "poll")
	if swaps := again.v.ops("swap"); len(swaps) != 0 {
		t.Fatalf("a restarted daemon swapped a minute after a browser call: %+v", swaps)
	}
	if !strings.Contains(again.logs.String(), "claude.ai work 1m ago") {
		t.Fatalf("log lacks the hold:\n%s", again.logs.String())
	}
}

// Stopping saves the last call the detector saw, even one made since the
// last tick.
func TestDaemonRecordsTheLastClaudeAICallWhenItStops(t *testing.T) {
	r := browserRig(t, testCfg())
	call := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	r.dets["default"].browser = call
	stop, sig, done := make(chan struct{}), make(chan os.Signal, 1), make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, make(chan time.Time), make(chan time.Time)) }()
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := r.d.st.Default().ClaudeAIAt; !got.Equal(call) {
		t.Fatalf("claude_ai_at at stop = %v, want %v", got, call)
	}
}

// `cs why` says it in the daemon's words, from state.
func TestWhyExplainsARotationHeldForABrowserTask(t *testing.T) {
	_, cfg, st := pinWorld(t, 90)
	now := time.Now()
	st.Default().ClaudeAIAt = now.Add(-2*time.Minute - 10*time.Second)
	var b bytes.Buffer
	renderWhy(&b, cfg, st, now, "")
	out := b.String()
	for _, want := range []string{
		"waiting to rotate to b",
		"switch wanted, session busy (claude.ai work 2m ago) — waiting for an idle gap",
		"hold_for_claude_ai",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("why lacks %q:\n%s", want, out)
		}
	}

	dec := whyJSON(cfg, st, now, "")["decision"].(map[string]any)
	if dec["kind"] != "switch" || dec["target"] != "b" || dec["held"] != "session busy (claude.ai work 2m ago)" {
		t.Fatalf("why --json decision = %v", dec)
	}
	until, ok := dec["held_until"].(time.Time)
	if !ok || !until.Equal(st.Default().ClaudeAIAt.Add(5*time.Minute)) {
		t.Fatalf("held_until = %v, want five minutes after the call", dec["held_until"])
	}
}

func TestWhyWithHoldForClaudeAIZeroSaysNothingOfIt(t *testing.T) {
	_, cfg, st := pinWorld(t, 90)
	cfg.HoldForClaudeAI = &config.Duration{}
	now := time.Now()
	st.Default().ClaudeAIAt = now.Add(-time.Minute)
	var b bytes.Buffer
	renderWhy(&b, cfg, st, now, "")
	if out := b.String(); strings.Contains(out, "claude.ai work") || strings.Contains(out, "hold_for") || !strings.Contains(out, "rotating to b") {
		t.Fatalf("why with the hold off:\n%s", out)
	}
	if dec := whyJSON(cfg, st, now, "")["decision"].(map[string]any); dec["held"] != nil || dec["held_until"] != nil {
		t.Fatalf("why --json decision with the hold off = %v", dec)
	}
}

// The setting: in the schema with the rotation settings, after
// max_switch_wait; global only, like it; a duration of at least 0.
func TestConfigSchemaHasHoldForClaudeAI(t *testing.T) {
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, "", []string{"schema"}, true, ""); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Settings []struct {
			Key, Type, Default, Description string
			Min                             *float64
			MinExclusive                    bool `json:"min_exclusive"`
			Scopes                          []string
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for i, s := range out.Settings {
		if s.Key != "hold_for_claude_ai" {
			continue
		}
		if s.Type != "duration" || s.Default != "5m0s" || s.Min == nil || *s.Min != 0 || s.MinExclusive ||
			!slices.Equal(s.Scopes, []string{"global"}) || !strings.Contains(s.Description, "Claude in Chrome") ||
			!strings.Contains(s.Description, "artifact") || !strings.Contains(s.Description, "Doc") {
			t.Fatalf("hold_for_claude_ai in the schema: %+v", s)
		}
		if i == 0 || out.Settings[i-1].Key != "max_switch_wait" {
			t.Fatalf("hold_for_claude_ai follows %q, want max_switch_wait", out.Settings[i-1].Key)
		}
		for _, o := range out.Settings {
			if o.Key == "hold_for_browser" {
				t.Fatal("the schema still lists hold_for_browser")
			}
		}
		return
	}
	t.Fatal("no hold_for_claude_ai in the schema")
}

func TestConfigSetHoldForClaudeAI(t *testing.T) {
	path := appWorld(t, appTOML)
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, path, []string{"set", "hold_for_claude_ai", "0s"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["previous"] != "5m0s" || m["value"] != "0s" {
		t.Fatalf("answered %v", m)
	}
	if got := loadOrFail(t, path).ClaudeAIHold(); got != 0 {
		t.Fatalf("read back %v, want 0 (off)", got)
	}
	if got := errCode(t, runConfigCmd(&buf, path, []string{"set", "hold_for_claude_ai", "-1m"}, true, "")); got != codeInvalidValue {
		t.Fatalf("a negative hold: %q", got)
	}
	if got := errCode(t, runConfigCmd(&buf, path, []string{"set", "hold_for_claude_ai", "soon"}, true, "")); got != codeInvalidValue {
		t.Fatalf("not a duration: %q", got)
	}
}

// A running daemon picks an edit up, and its reload says what changed.
func TestAReloadNamesAChangedClaudeAIHold(t *testing.T) {
	old, next := testCfg(), testCfg()
	next.HoldForClaudeAI = &config.Duration{Duration: 2 * time.Minute}
	if got := configChanges(old, next); !slices.Contains(got, "hold_for_claude_ai 5m0s → 2m0s") {
		t.Fatalf("changes = %q", got)
	}
}

// The old name still works on the command line, and means the new one.
func TestConfigTakesTheOldNameHoldForBrowser(t *testing.T) {
	path := appWorld(t, appTOML)
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, path, []string{"set", "hold_for_browser", "2m"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["key"] != "hold_for_claude_ai" || m["value"] != "2m0s" {
		t.Fatalf("answered %v", m)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "hold_for_browser") || !strings.Contains(string(raw), `hold_for_claude_ai = "2m0s"`) {
		t.Fatalf("file:\n%s", raw)
	}
	buf.Reset()
	if err := runConfigCmd(&buf, path, []string{"get", "hold_for_browser"}, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "2m0s" {
		t.Fatalf("get hold_for_browser = %q, want 2m0s", got)
	}
}

// Setting it in a file that still has the old name replaces that line, so
// the file never carries both.
func TestConfigSetReplacesTheOldNameInTheFile(t *testing.T) {
	path := appWorld(t, "hold_for_browser = \"1m\" # mine\n"+appTOML)
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, path, []string{"set", "hold_for_claude_ai", "3m"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["previous"] != "1m0s" || m["value"] != "3m0s" {
		t.Fatalf("answered %v", m)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "hold_for_browser") || strings.Count(string(raw), "hold_for_claude_ai") != 1 {
		t.Fatalf("file:\n%s", raw)
	}
	if got := loadOrFail(t, path).ClaudeAIHold(); got != 3*time.Minute {
		t.Fatalf("read back %v, want 3m", got)
	}
}

// settle_after_swap (phase 1 S) follows hold_for_claude_ai in the schema:
// global, a duration from 0 to 5m.
func TestConfigSchemaHasSettleAfterSwap(t *testing.T) {
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, "", []string{"schema"}, true, ""); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Settings []struct {
			Key, Type, Default, Description string
			Min, Max                        *float64
			Scopes                          []string
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for i, s := range out.Settings {
		if s.Key != "settle_after_swap" {
			continue
		}
		if s.Type != "duration" || s.Default != "45s" || s.Min == nil || *s.Min != 0 || s.Max == nil || *s.Max != 300 ||
			!slices.Equal(s.Scopes, []string{"global"}) || !strings.Contains(s.Description, "switch") {
			t.Fatalf("settle_after_swap in the schema: %+v", s)
		}
		if out.Settings[i-1].Key != "hold_for_claude_ai" {
			t.Fatalf("settle_after_swap follows %q, want hold_for_claude_ai", out.Settings[i-1].Key)
		}
		return
	}
	t.Fatal("no settle_after_swap in the schema")
}

func TestAReloadNamesAChangedSettle(t *testing.T) {
	old, next := testCfg(), testCfg()
	next.SettleAfterSwap = &config.Duration{Duration: 30 * time.Second}
	if got := configChanges(old, next); !slices.Contains(got, "settle_after_swap 45s → 30s") {
		t.Fatalf("changes = %q", got)
	}
}
