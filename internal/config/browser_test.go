package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hold_for_browser (IMPROVEMENTS P4, owner decision 2026-10-21): how long
// after a session's last Claude in Chrome call a rotation waits. On by
// default at five minutes; 0 turns it off.

func TestHoldForBrowserDefaultsToFiveMinutes(t *testing.T) {
	if got := Defaults().BrowserHold(); got != 5*time.Minute {
		t.Errorf("Defaults().BrowserHold() = %v, want 5m", got)
	}
	c, err := loadText(t, oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.BrowserHold(); got != 5*time.Minute {
		t.Errorf("a config without hold_for_browser holds for %v, want 5m", got)
	}
	// One built in code (setup's) gets the default too, not an accidental off.
	if got := (&Config{}).BrowserHold(); got != 5*time.Minute {
		t.Errorf("a Config literal holds for %v, want 5m", got)
	}
}

func TestHoldForBrowserLoadsAndZeroTurnsItOff(t *testing.T) {
	for body, want := range map[string]time.Duration{
		`hold_for_browser = "2m"`:  2 * time.Minute,
		`hold_for_browser = "0s"`:  0,
		`hold_for_browser = "90s"`: 90 * time.Second,
	} {
		c, err := loadText(t, body+"\n"+oneAccount)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := c.BrowserHold(); got != want {
			t.Errorf("%s: BrowserHold() = %v, want %v", body, got, want)
		}
	}
}

func TestHoldForBrowserCannotBeNegative(t *testing.T) {
	_, err := loadText(t, `hold_for_browser = "-1m"`+"\n"+oneAccount)
	if err == nil || !strings.Contains(err.Error(), "hold_for_browser") {
		t.Fatalf("a negative hold loaded: %v", err)
	}
}

// A whole-file write keeps a chosen 0 (off) as 0, and leaves an unchosen
// default to the default rather than pinning it.
func TestHoldForBrowserSurvivesAWholeFileWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path) // missing file: the defaults
	c.Priority, c.Accounts = []string{"a"}, []Account{{ID: "a"}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "\nhold_for_browser") {
		t.Errorf("an unchosen default was written as a setting:\n%s", raw)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.BrowserHold(); got != 5*time.Minute {
		t.Errorf("read back %v, want the 5m default", got)
	}

	back.HoldForBrowser = &Duration{}
	back.MarkSet("hold_for_browser")
	if err := back.Write(path); err != nil {
		t.Fatal(err)
	}
	off, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := off.BrowserHold(); got != 0 {
		raw, _ := os.ReadFile(path)
		t.Errorf("a chosen 0 read back as %v:\n%s", got, raw)
	}
}
