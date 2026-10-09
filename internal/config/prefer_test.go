package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F1: prefer = "room" | "expiring", global and per profile.

const preferAccounts = `
[[account]]
id = "work-1"

[[account]]
id = "work-team"

[[account]]
id = "personal"
`

func TestPreferDefaultsToRoom(t *testing.T) {
	c, err := loadString(t, preferAccounts)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Preference(); got != PreferRoom {
		t.Fatalf("Preference() = %q, want room", got)
	}
	if got := Defaults().Preference(); got != PreferRoom {
		t.Fatalf("Defaults().Preference() = %q, want room", got)
	}
}

func TestPreferGlobalAndPerProfile(t *testing.T) {
	c, err := loadString(t, `prefer = "expiring"`+"\n"+preferAccounts+`
[[profile]]
name = "default"
pool = ["personal"]
prefer = "room"

[[profile]]
name = "work"
dir = "~/.claude-work"
pool = ["work-1", "work-team"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Preference() != PreferExpiring {
		t.Fatalf("global = %q", c.Preference())
	}
	if got := c.PreferFor("default"); got != PreferRoom {
		t.Fatalf("default's override = %q, want room", got)
	}
	if got := c.PreferFor("work"); got != PreferExpiring {
		t.Fatalf("work inherits = %q, want expiring", got)
	}
	if got := c.ForProfile("default").Preference(); got != PreferRoom {
		t.Fatalf("ForProfile(default).Preference() = %q", got)
	}
}

func TestPreferRefusesAnythingElse(t *testing.T) {
	if _, err := loadString(t, `prefer = "soonest"`+"\n"+preferAccounts); err == nil ||
		!strings.Contains(err.Error(), "prefer") {
		t.Fatalf("err = %v, want prefer refused", err)
	}
	if _, err := loadString(t, preferAccounts+`
[[profile]]
name = "default"
prefer = "most"
`); err == nil || !strings.Contains(err.Error(), "prefer") {
		t.Fatalf("err = %v, want the profile's prefer refused", err)
	}
}

// Write keeps an untouched config as it was (no new line), and writes the
// setting once chosen; a profile's override is written in its block.
func TestPreferIsWrittenOnlyOnceChosen(t *testing.T) {
	c, err := loadString(t, preferAccounts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "prefer") {
		t.Fatalf("an untouched config gained a prefer line:\n%s", b)
	}
	c.Prefer = PreferExpiring
	c.Profiles = []Profile{{Name: "default", Prefer: PreferRoom}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Preference() != PreferExpiring || back.PreferFor("default") != PreferRoom {
		t.Fatalf("read back %q / %q", back.Preference(), back.PreferFor("default"))
	}
}
