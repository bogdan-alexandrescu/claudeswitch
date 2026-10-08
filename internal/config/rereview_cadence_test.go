package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Setting a value clears the raise recorded on load: a file pinned at
// "1m0s" must be fixable with `cs config poll_active 3m`.
func TestSettingAPollClearsItsRaise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("poll_active = \"1m0s\"\n"+oneAccount), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.PollActive = Duration{Duration: 3 * time.Minute}
	c.MarkSet("poll_active")
	if err := c.CheckSetting("poll_active"); err != nil {
		t.Fatalf("3m refused: %v", err)
	}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.PollActive.Duration != 3*time.Minute || len(back.Warnings()) != 0 {
		t.Errorf("poll_active %v, warnings %v", back.PollActive.Duration, back.Warnings())
	}
}

// A poll_idle under the floor is not the default, so it is not dropped: the
// person's own value is written back, and keeps warning, rather than the
// raised 2m being pinned in its place.
func TestAPinnedFastIdleIsWrittenBackAsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("poll_idle = \"90s\"\n"+oneAccount), 0o600); err != nil {
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
	if !strings.Contains(string(raw), "\npoll_idle       = \"1m30s\"") {
		t.Errorf("poll_idle not written back as the person's value:\n%s", raw)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(back.Warnings(), "\n"); !strings.Contains(w, "poll_idle") {
		t.Errorf("no warning after the write: %q", w)
	}
}

// unseen_calls_per_hour comes out of the live account's refill, so a high
// one means the account in use cannot be read at poll_active for long: the
// arithmetic names the interval it can sustain, and says when that is past
// the point a read is overdue (1.25 × poll_active).
func TestUnseenSpendSetsTheSustainableActiveInterval(t *testing.T) {
	c := defaultsWithOneAccount(t)
	r := c.AccountCadence()
	if got := r.ActiveEvery.Round(time.Second); got != 3*time.Minute {
		t.Errorf("default sustainable interval %v, want poll_active, 3m (28 calls an hour sustain 2m9s)", got)
	}
	if r.ActiveStarved() {
		t.Error("the defaults are starved")
	}
	twelve := 12.0
	c.UnseenCallsPerHour = &twelve
	r = c.AccountCadence()
	if r.ActiveEvery != 3*time.Minute+20*time.Second || !r.ActiveStarved() {
		t.Errorf("unseen 12/h: every %v starved=%v, want 3m20s and starved", r.ActiveEvery, r.ActiveStarved())
	}
}

// Only the exact value 0.3.x–0.5.0 `cs config` auto-pinned ("1m0s") is
// dropped on a write; any other poll_active under 2m is the person's own,
// written back as written, and keeps loading at 2m with a warning.
func TestOnlyTheOldAutoPinnedActiveCadenceIsDropped(t *testing.T) {
	for _, c := range []struct {
		written, wantLine string // wantLine "" means: no uncommented poll_active
		wantWarn          bool
	}{
		{"1m0s", "", false},
		{"60s", "", false},
		{"1m30s", "\npoll_active     = \"1m30s\"", true},
		{"45s", "\npoll_active     = \"45s\"", true},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("poll_active = \""+c.written+"\"\n"+oneAccount), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PollActive.Duration != MinPoll {
			t.Errorf("%s: loads at %v, want %v", c.written, cfg.PollActive.Duration, MinPoll)
		}
		if err := cfg.Write(path); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(path)
		switch {
		case c.wantLine == "" && strings.Contains(string(raw), "\npoll_active "):
			t.Errorf("%s: still pinned after a write:\n%s", c.written, raw)
		case c.wantLine != "" && !strings.Contains(string(raw), c.wantLine):
			t.Errorf("%s: not written back as written (want %q):\n%s", c.written, c.wantLine, raw)
		}
		back, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		warns := strings.Contains(strings.Join(back.Warnings(), "\n"), "poll_active")
		if warns != c.wantWarn {
			t.Errorf("%s: after the write, warning=%v, want %v (poll_active %v)", c.written, warns, c.wantWarn, back.PollActive.Duration)
		}
	}
}

// The model's advanced numbers (owner decision 2026-10-07): defaults,
// ranges, and what the cadence arithmetic shows.
func TestTheAdvancedModelSettings(t *testing.T) {
	c := defaultsWithOneAccount(t)
	if c.HotReserveCalls() != 10 || c.UnseenPerHour() != 2 {
		t.Errorf("defaults hot_reserve %d unseen %v, want 10 and 2", c.HotReserveCalls(), c.UnseenPerHour())
	}
	for _, bad := range []string{"hot_reserve = 16\n", "hot_reserve = -1\n",
		"unseen_calls_per_hour = 21\n", "unseen_calls_per_hour = -1\n"} {
		if _, err := loadText(t, bad+oneAccount); err == nil {
			t.Errorf("%q loaded", strings.TrimSpace(bad))
		}
	}
	c, err := loadText(t, "hot_reserve = 6\nunseen_calls_per_hour = 4.5\n"+oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if c.HotReserveCalls() != 6 || c.UnseenPerHour() != 4.5 {
		t.Errorf("got %d and %v", c.HotReserveCalls(), c.UnseenPerHour())
	}
	if r := c.AccountCadence(); r.Spare != 6 || r.Unseen != 4.5 {
		t.Errorf("cadence shows hot reserve %d and unseen %v", r.Spare, r.Unseen)
	}
}
