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

// Validation uses that arithmetic: a cadence that fits one profile can be
// too much for two.
func TestValidateRejectsACadenceTwoProfilesCannotAfford(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "missing.toml")) // the defaults
	c.APIBudget = 4                                          // 3 available after the swap reserve
	c.PollActive = Duration{Duration: 2 * time.Minute}
	c.PollIdle = Duration{Duration: 10 * time.Minute}
	c.Accounts = []Account{{ID: "a"}, {ID: "w1"}}
	c.Priority = []string{"a", "w1"}
	if err := c.Validate(); err != nil {
		t.Fatalf("one profile (3.0 calls) should fit: %v", err)
	}
	c.Profiles = []Profile{
		{Name: "default", Pool: []string{"a"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("two active accounts (5.0 calls) against 3 available must not validate")
	}
}
