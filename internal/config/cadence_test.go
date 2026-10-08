package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func defaultsWithOneAccount(t *testing.T) *Config {
	t.Helper()
	c, _ := Load(filepath.Join(t.TempDir(), "missing.toml")) // the defaults
	c.Accounts = []Account{{ID: "a"}}
	c.Priority = []string{"a"}
	return c
}

// Every `cs config` on 0.3.x–0.5.0 wrote poll_active = "1m0s" into the file,
// because Write persisted the defaults it was filled with. Refusing that
// config would stop the daemon and `cs config` itself (owner decision
// 2026-10-07): it loads, runs at the per-account floor, and says so.
func TestAFastActiveCadenceLoadsAtTheFloorWithAWarning(t *testing.T) {
	c, err := loadText(t, "poll_active = \"1m0s\"\n"+oneAccount)
	if err != nil {
		t.Fatalf("a 1m poll_active must load: %v", err)
	}
	if c.PollActive.Duration != 2*time.Minute {
		t.Errorf("effective poll_active = %v, want 2m", c.PollActive.Duration)
	}
	got := strings.Join(c.Warnings(), "\n")
	for _, want := range []string{"poll_active", "1m0s", "2m0s", "per account", "cs config poll_active 3m"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "cs config set") {
		t.Errorf("the hint names a command that does not exist: %q", got)
	}
}

func TestAFastIdleCadenceLoadsAtTheFloorWithAWarning(t *testing.T) {
	c, err := loadText(t, "poll_idle = \"90s\"\n"+oneAccount)
	if err != nil {
		t.Fatalf("a 90s poll_idle must load: %v", err)
	}
	if c.PollIdle.Duration != 2*time.Minute {
		t.Errorf("effective poll_idle = %v, want 2m", c.PollIdle.Duration)
	}
	if got := strings.Join(c.Warnings(), "\n"); !strings.Contains(got, "cs config poll_idle 10m") {
		t.Errorf("warning lacks the fix: %q", got)
	}
}

// Setting one now is refused: the value would be ignored.
func TestCheckSettingRefusesAFastPollCadence(t *testing.T) {
	c := defaultsWithOneAccount(t)
	c.PollActive = Duration{Duration: time.Minute}
	if err := c.CheckSetting("poll_active"); err == nil || !strings.Contains(err.Error(), "2m") {
		t.Fatalf("got %v, want a refusal naming the 2m floor", err)
	}
	c.PollActive = Duration{Duration: 2 * time.Minute}
	if err := c.CheckSetting("poll_active"); err != nil {
		t.Errorf("2m refused: %v", err)
	}
}

// The default reads the account in use every three minutes: twenty calls an
// hour against a live refill of ~28, so the hot reserve rebuilds (owner
// decision 2026-10-07).
func TestTheDefaultActiveCadenceIsThreeMinutes(t *testing.T) {
	c := defaultsWithOneAccount(t)
	if c.PollActive.Duration != 3*time.Minute {
		t.Errorf("default poll_active = %v, want 3m", c.PollActive.Duration)
	}
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("the defaults warn: %v", w)
	}
}

// The per-account arithmetic doctor shows.
func TestAccountCadenceArithmetic(t *testing.T) {
	c := defaultsWithOneAccount(t)
	r := c.AccountCadence()
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(r.ActivePerHour, 20) || !near(r.HotPerHour, 60) || !near(r.IdlePerHour, 6) {
		t.Errorf("rates active %v hot %v idle %v, want 20 / 60 / 6", r.ActivePerHour, r.HotPerHour, r.IdlePerHour)
	}
	if !near(r.RefillPerHour, 30) {
		t.Errorf("refill %v, want 30 an hour", r.RefillPerHour)
	}
	// A 15-minute hot spell at 60s: 15 calls, 7.5 refilled.
	if !near(r.HotSpellNet, 7.5) || r.Spare != 10 {
		t.Errorf("hot spell net %v of %d spare, want 7.5 of 10", r.HotSpellNet, r.Spare)
	}
	if r.Drains() {
		t.Error("the defaults drain nothing")
	}
	// A code-built config below the floor is counted at the floor: that is
	// what runs.
	c.PollActive = Duration{Duration: time.Minute}
	if r := c.AccountCadence(); !near(r.ActivePerHour, 30) {
		t.Errorf("1m counted as %v/h, want the floor's 30", r.ActivePerHour)
	}
}

// Write keeps what the file or the person set, and leaves every default as a
// comment, so a default that changes later reaches the file.
func TestWriteDoesNotPinDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("poll_hot = \"90s\"\n"+oneAccount), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.SwitchAt = 80
	c.MarkSet("switch_at")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	for _, want := range []string{"\nswitch_at ", "\npoll_hot "} {
		if !strings.Contains(text, want) {
			t.Errorf("an explicit setting was dropped (%q):\n%s", want, text)
		}
	}
	for _, key := range []string{"poll_active", "poll_idle", "api_budget", "cooldown", "hot_threshold",
		"switch_at_weekly", "hard_floor", "refresh_window", "landing_margin", "blind_failover_polls"} {
		if strings.Contains(text, "\n"+key+" ") {
			t.Errorf("the default %s was pinned:\n%s", key, text)
		}
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("what was written does not load: %v\n%s", err, text)
	}
	if back.SwitchAt != 80 || back.PollHot.Duration != 90*time.Second || back.PollActive.Duration != 3*time.Minute {
		t.Errorf("round trip: switch_at %v poll_hot %v poll_active %v", back.SwitchAt, back.PollHot.Duration, back.PollActive.Duration)
	}
}

// A file carrying the old pinned "1m0s" is healed by the next write: the
// floor is the default, so nothing is pinned and the warning goes.
func TestWriteHealsAPinnedFastActiveCadence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("poll_active = \"1m0s\"\npoll_idle = \"5m\"\n"+oneAccount), 0o600); err != nil {
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
	if strings.Contains(string(raw), "\npoll_active ") {
		t.Errorf("poll_active is still pinned:\n%s", raw)
	}
	if !strings.Contains(string(raw), "\npoll_idle       = \"5m0s\"") {
		t.Errorf("an explicit poll_idle was lost:\n%s", raw)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w := back.Warnings(); len(w) != 0 {
		t.Errorf("still warning after the heal: %v", w)
	}
}
