package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hold_for_claude_ai (IMPROVEMENTS P4, and phase 1 H, owner decisions
// 2026-10-21): how long after a session's last claude.ai call (Claude in
// Chrome, an artifact, a Doc) a rotation waits. On by default at five
// minutes; 0 turns it off. 0.7.8 and 0.7.9 called it hold_for_browser, and a
// file that still does is read as before.

func TestHoldForClaudeAIDefaultsToFiveMinutes(t *testing.T) {
	if got := Defaults().ClaudeAIHold(); got != 5*time.Minute {
		t.Errorf("Defaults().ClaudeAIHold() = %v, want 5m", got)
	}
	c, err := loadText(t, oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ClaudeAIHold(); got != 5*time.Minute {
		t.Errorf("a config without hold_for_claude_ai holds for %v, want 5m", got)
	}
	// One built in code (setup's) gets the default too, not an accidental off.
	if got := (&Config{}).ClaudeAIHold(); got != 5*time.Minute {
		t.Errorf("a Config literal holds for %v, want 5m", got)
	}
}

func TestHoldForClaudeAILoadsAndZeroTurnsItOff(t *testing.T) {
	for body, want := range map[string]time.Duration{
		`hold_for_claude_ai = "2m"`:  2 * time.Minute,
		`hold_for_claude_ai = "0s"`:  0,
		`hold_for_claude_ai = "90s"`: 90 * time.Second,
	} {
		c, err := loadText(t, body+"\n"+oneAccount)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := c.ClaudeAIHold(); got != want {
			t.Errorf("%s: ClaudeAIHold() = %v, want %v", body, got, want)
		}
	}
}

func TestHoldForClaudeAICannotBeNegative(t *testing.T) {
	_, err := loadText(t, `hold_for_claude_ai = "-1m"`+"\n"+oneAccount)
	if err == nil || !strings.Contains(err.Error(), "hold_for_claude_ai") {
		t.Fatalf("a negative hold loaded: %v", err)
	}
}

// A whole-file write keeps a chosen 0 (off) as 0, and leaves an unchosen
// default to the default rather than pinning it.
func TestHoldForClaudeAISurvivesAWholeFileWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path) // missing file: the defaults
	c.Priority, c.Accounts = []string{"a"}, []Account{{ID: "a"}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "\nhold_for_claude_ai") {
		t.Errorf("an unchosen default was written as a setting:\n%s", raw)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.ClaudeAIHold(); got != 5*time.Minute {
		t.Errorf("read back %v, want the 5m default", got)
	}

	back.HoldForClaudeAI = &Duration{}
	back.MarkSet("hold_for_claude_ai")
	if err := back.Write(path); err != nil {
		t.Fatal(err)
	}
	off, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := off.ClaudeAIHold(); got != 0 {
		raw, _ := os.ReadFile(path)
		t.Errorf("a chosen 0 read back as %v:\n%s", got, raw)
	}
}

// A file written by 0.7.8 or 0.7.9 says hold_for_browser. It is read as
// hold_for_claude_ai, and the next whole-file write carries the new name only.
func TestTheOldNameHoldForBrowserIsStillRead(t *testing.T) {
	for body, want := range map[string]time.Duration{
		`hold_for_browser = "2m"`: 2 * time.Minute,
		`hold_for_browser = "0s"`: 0,
	} {
		c, err := loadText(t, body+"\n"+oneAccount)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := c.ClaudeAIHold(); got != want {
			t.Errorf("%s: ClaudeAIHold() = %v, want %v", body, got, want)
		}
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`hold_for_browser = "2m"`+"\n"+oneAccount), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "hold_for_browser") || !strings.Contains(string(raw), "\nhold_for_claude_ai = \"2m0s\"") {
		t.Fatalf("written back:\n%s", raw)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.ClaudeAIHold(); got != 2*time.Minute {
		t.Errorf("read back %v, want 2m", got)
	}
}

// A file with both names means the new one.
func TestHoldForClaudeAIWinsOverTheOldName(t *testing.T) {
	c, err := loadText(t, `hold_for_browser = "2m"`+"\n"+`hold_for_claude_ai = "30s"`+"\n"+oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ClaudeAIHold(); got != 30*time.Second {
		t.Fatalf("ClaudeAIHold() = %v, want hold_for_claude_ai's 30s", got)
	}
}

// settle_after_swap (phase 1 S, owner decision 2026-10-21): after a switch,
// Claude Code's claude.ai tools act as the previous account for up to about
// half a minute (GROUND_TRUTH §55), so the plugin holds such a call until
// this long after the profile's last switch. 45s unless set, 0 off, and at
// most MaxSettleAfterSwap, which the plugin hook's timeout must exceed.
func TestSettleAfterSwapDefaultsTo45SecondsAndZeroTurnsItOff(t *testing.T) {
	if got := Defaults().Settle(); got != 45*time.Second {
		t.Errorf("Defaults().Settle() = %v, want 45s", got)
	}
	if got := (&Config{}).Settle(); got != 45*time.Second {
		t.Errorf("a Config literal settles for %v, want 45s", got)
	}
	for body, want := range map[string]time.Duration{
		`settle_after_swap = "0s"`:  0,
		`settle_after_swap = "30s"`: 30 * time.Second,
		`settle_after_swap = "5m"`:  5 * time.Minute,
	} {
		c, err := loadText(t, body+"\n"+oneAccount)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := c.Settle(); got != want {
			t.Errorf("%s: Settle() = %v, want %v", body, got, want)
		}
	}
}

func TestSettleAfterSwapIsBounded(t *testing.T) {
	if MaxSettleAfterSwap != 5*time.Minute {
		t.Fatalf("MaxSettleAfterSwap = %v", MaxSettleAfterSwap)
	}
	for _, body := range []string{`settle_after_swap = "-1s"`, `settle_after_swap = "5m1s"`} {
		if _, err := loadText(t, body+"\n"+oneAccount); err == nil || !strings.Contains(err.Error(), "settle_after_swap") {
			t.Errorf("%s loaded: %v", body, err)
		}
	}
}

func TestSettleAfterSwapSurvivesAWholeFileWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path)
	c.Priority, c.Accounts = []string{"a"}, []Account{{ID: "a"}}
	c.SettleAfterSwap = &Duration{}
	c.MarkSet("settle_after_swap")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Settle(); got != 0 {
		raw, _ := os.ReadFile(path)
		t.Errorf("a chosen 0 read back as %v:\n%s", got, raw)
	}
}

// release_idle_after (owner decision 2026-10-23): an account live in a
// profile whose sessions have been idle this long is given up when another
// profile wants it. 30m unless set; 0 never.
func TestReleaseIdleAfterDefaultsTo30MinutesAndZeroTurnsItOff(t *testing.T) {
	if got := Defaults().ReleaseIdle(); got != 30*time.Minute {
		t.Errorf("Defaults().ReleaseIdle() = %v, want 30m", got)
	}
	if got := (&Config{}).ReleaseIdle(); got != 30*time.Minute {
		t.Errorf("a Config literal releases after %v, want 30m", got)
	}
	for body, want := range map[string]time.Duration{
		`release_idle_after = "0s"`: 0,
		`release_idle_after = "2h"`: 2 * time.Hour,
	} {
		c, err := loadText(t, body+"\n"+oneAccount)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := c.ReleaseIdle(); got != want {
			t.Errorf("%s: ReleaseIdle() = %v, want %v", body, got, want)
		}
	}
	if _, err := loadText(t, `release_idle_after = "-1m"`+"\n"+oneAccount); err == nil ||
		!strings.Contains(err.Error(), "release_idle_after") {
		t.Errorf("a negative value loaded: %v", err)
	}
}
