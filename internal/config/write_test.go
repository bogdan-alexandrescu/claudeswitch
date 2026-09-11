package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setup generates the config rather than templating it, because the seat pins
// cannot be written in advance — a seat uuid is only knowable after signing in.
// So what it writes must load back exactly.
func TestWrittenConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	in := &Config{
		SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		Cooldown:      Duration{10 * time.Minute},
		MaxSwitchWait: Duration{90 * time.Second},
		RefreshWindow: Duration{time.Hour},
		RefreshProbe:  Duration{24 * time.Hour},
		Priority:      []string{"work", "personal"},
		Accounts: []Account{
			{ID: "work", Scope: "work", AccountUUID: "seat-1", OrgID: "org-1",
				Comment: "someone@example.com · Max 20x"},
			{ID: "personal", Scope: "personal", Reserve: 70,
				AccountUUID: "seat-2", OrgID: "org-2"},
		},
	}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("what we wrote does not load: %v", err)
	}
	if out.SwitchAt != 85 || out.HardFloor != 96 {
		t.Errorf("thresholds lost: %v / %v", out.SwitchAt, out.HardFloor)
	}
	if out.Cooldown.Duration != 10*time.Minute || out.MaxSwitchWait.Duration != 90*time.Second {
		t.Errorf("durations lost: %v / %v", out.Cooldown, out.MaxSwitchWait)
	}
	if out.RefreshWindow.Duration != time.Hour || out.RefreshProbe.Duration != 24*time.Hour {
		t.Errorf("refresh policy lost: %v / %v", out.RefreshWindow, out.RefreshProbe)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("accounts lost: %d", len(out.Accounts))
	}
	// The pins are the point: without them the wrong-account guard cannot work.
	if out.Accounts[0].Seat() != "seat-1@org-1" {
		t.Errorf("seat pin lost: %q", out.Accounts[0].Seat())
	}
	if out.Accounts[1].Reserve != 70 {
		t.Errorf("reserve lost: %v", out.Accounts[1].Reserve)
	}
	if len(out.Priority) != 2 || out.Priority[0] != "work" {
		t.Errorf("priority lost: %v", out.Priority)
	}
}

func TestWrittenConfigIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		RefreshWindow: Duration{time.Hour}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config should be 0600, got %o", fi.Mode().Perm())
	}
}
