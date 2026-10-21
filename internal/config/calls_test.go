package config

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

// Each profile has an active account polled at poll_active, so the budget
// arithmetic counts one per profile that can have one.
func TestCallsPerWindowCountsAnActiveAccountPerProfile(t *testing.T) {
	base := func() *Config {
		return &Config{
			PollActive: Duration{Duration: 2 * time.Minute},
			PollIdle:   Duration{Duration: 10 * time.Minute},
			Accounts:   []Account{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "w1"}, {ID: "w2"}},
		}
	}
	// 5 min / 2 min = 2.5 per active; 5 min / 10 min = 0.5 per idle.
	one := base()
	if got, want := one.CallsPerWindow(), 2.5+4*0.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("no profiles: %v calls, want %v (unchanged)", got, want)
	}

	two := base()
	two.Profiles = []Profile{
		{Name: "default", Pool: []string{"a", "b", "c"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1", "w2"}},
	}
	if got, want := two.CallsPerWindow(), 2*2.5+3*0.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("two profiles: %v calls, want %v", got, want)
	}

	// An empty pool (D13) has nothing active to poll.
	empty := base()
	empty.Profiles = []Profile{
		{Name: "default", Pool: []string{"a", "b", "c", "w1", "w2"}},
		{Name: "spare", Dir: "~/.claude-spare"},
	}
	if got, want := empty.CallsPerWindow(), 2.5+4*0.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("an empty pool: %v calls, want %v", got, want)
	}
}

// D36: the machine-wide window is api_budget for each profile with an
// account to use. Two busy profiles under one profile's window held routine
// reads 14 minutes stale (GROUND_TRUTH §47).
func TestTheWindowGrowsWithTheProfilesInUse(t *testing.T) {
	c := &Config{APIBudget: 12, Accounts: []Account{{ID: "a"}, {ID: "w1"}, {ID: "off", Enabled: new(bool)}}}
	if got := c.WindowAllowance(); got != 12 {
		t.Errorf("no profiles: %d, want 12", got)
	}
	c.Profiles = []Profile{
		{Name: "default", Pool: []string{"a"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1"}},
		{Name: "spare", Dir: "~/.claude-spare"},
		{Name: "idle", Dir: "~/.claude-idle", Pool: []string{"off"}},
	}
	if got := c.WindowAllowance(); got != 24 {
		t.Errorf("two profiles with an enabled account: %d, want 24", got)
	}
}

// Validation checks a cadence against that window: what one profile's window
// could not afford, two profiles' can, and a budget too small for both is
// still refused.
func TestValidateChecksTheCadenceAgainstTheGrownWindow(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "missing.toml")) // the defaults
	c.APIBudget = 4
	c.PollActive = Duration{Duration: 2 * time.Minute}
	c.PollIdle = Duration{Duration: 10 * time.Minute}
	c.Accounts = []Account{{ID: "a"}, {ID: "w1"}}
	c.Priority = []string{"a", "w1"}
	c.Profiles = []Profile{
		{Name: "default", Pool: []string{"a"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("two active accounts (5.0 calls) against 7 available should fit: %v", err)
	}
	c.APIBudget = 2
	if err := c.Validate(); err == nil {
		t.Fatal("5.0 calls against 3 available must not validate")
	}
}
