package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A floor beneath the weekly trigger is met by every weekly rotation, which
// turns off the cooldown and the idle-gap preference without saying so. It is
// a legal configuration, so it warns rather than refusing to load.
func TestAFloorBelowTheWeeklyTriggerWarnsButLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(
		"switch_at = 85\nswitch_at_weekly = 98\nhard_floor = 96\n"+
			"priority = [\"work\"]\n\n[[account]]\nid = \"work\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("this is a legal configuration, not an error: %v", err)
	}
	w := c.Warnings()
	if len(w) != 1 {
		t.Fatalf("want one warning, got %v", w)
	}
	for _, want := range []string{"hard_floor (96)", "switch_at_weekly (98)", "10m0s"} {
		if !strings.Contains(w[0], want) {
			t.Errorf("warning must quote %s: %s", want, w[0])
		}
	}
}

func TestAFloorAboveBothTriggersIsQuiet(t *testing.T) {
	c := &Config{SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 99,
		Cooldown: Duration{10 * time.Minute}, RefreshWindow: Duration{time.Hour}}
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("nothing to warn about here: %v", w)
	}
}

// The shipped defaults must not warn about themselves. Raising the weekly
// trigger without raising the floor would have every fresh install complaining
// on first run.
func TestTheDefaultsAreSelfConsistent(t *testing.T) {
	// Loaded from a file that sets none of them, so this is the default set as
	// it actually ships rather than one reassembled by hand here — the two have
	// drifted apart before.
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("priority = [\"work\"]\n\n[[account]]\nid = \"work\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
	if c.SwitchAtWeekly != DefaultSwitchAtWeekly || c.HardFloor != DefaultHardFloor {
		t.Fatalf("not the defaults: %v / %v", c.SwitchAtWeekly, c.HardFloor)
	}
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("the defaults warn about themselves: %v", w)
	}
}

// The weekly trigger was absent from the generated config, so `cs setup` on an
// existing install silently reverted a tuned value to the default.
func TestTheWrittenConfigCarriesTheWeeklyTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{SwitchAt: 85, SwitchAtWeekly: 98, HardFloor: 99, SwitchWhen: "idle",
		Cooldown: Duration{10 * time.Minute}, MaxSwitchWait: Duration{30 * time.Second},
		RefreshWindow: Duration{time.Hour}, RefreshProbe: Duration{24 * time.Hour},
		Priority: []string{"work"}, Accounts: []Account{{ID: "work"}}}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("what we wrote does not load: %v", err)
	}
	if out.SwitchAtWeekly != 98 {
		t.Errorf("weekly trigger lost in the round trip: got %v, want 98", out.SwitchAtWeekly)
	}
}

// A Config assembled in code leaves its thresholds zero, and zero written out
// literally produces a file that will not load.
func TestAnUnsetThresholdIsWrittenAtItsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{SwitchWhen: "idle", Cooldown: Duration{10 * time.Minute},
		MaxSwitchWait: Duration{30 * time.Second}, RefreshWindow: Duration{time.Hour},
		RefreshProbe: Duration{24 * time.Hour},
		Priority:     []string{"work"}, Accounts: []Account{{ID: "work"}}}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("a config written from zero values must still load: %v", err)
	}
	if out.SwitchAtWeekly != DefaultSwitchAtWeekly || out.SwitchAt != DefaultSwitchAt {
		t.Errorf("want the defaults, got %v / %v", out.SwitchAt, out.SwitchAtWeekly)
	}
}
