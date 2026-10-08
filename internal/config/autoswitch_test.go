package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// landing_margin (A1) and blind_failover_polls (A2), docs/IMPROVEMENTS.md.

func loadText(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const oneAccount = "priority = [\"work\"]\n\n[[account]]\nid = \"work\"\n"

func TestAutoSwitchDefaults(t *testing.T) {
	c, err := loadText(t, oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Margin(); got != 10 {
		t.Errorf("landing margin default = %v, want 10", got)
	}
	if got := c.BlindPolls(); got != 3 {
		t.Errorf("blind_failover_polls default = %d, want 3", got)
	}
}

func TestAutoSwitchSettingsLoad(t *testing.T) {
	c, err := loadText(t, "landing_margin = 0\nblind_failover_polls = 0\n"+oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if c.Margin() != 0 || c.BlindPolls() != 0 {
		t.Errorf("explicit zeros read back as margin %v, polls %d; zero means off", c.Margin(), c.BlindPolls())
	}
	c, err = loadText(t, "landing_margin = 25\nblind_failover_polls = 5\n"+oneAccount)
	if err != nil {
		t.Fatal(err)
	}
	if c.Margin() != 25 || c.BlindPolls() != 5 {
		t.Errorf("got margin %v, polls %d; want 25, 5", c.Margin(), c.BlindPolls())
	}
}

func TestAutoSwitchSettingsAreValidated(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"landing_margin = 51\n", "landing_margin"},
		{"landing_margin = -1\n", "landing_margin"},
		{"blind_failover_polls = -1\n", "blind_failover_polls"},
		{oneAccount + "\n[[profile]]\nname = \"default\"\npool = [\"work\"]\nlanding_margin = 60\n", "landing_margin"},
	} {
		body := tc.body
		if !strings.Contains(body, "[[account]]") {
			body += oneAccount
		}
		if _, err := loadText(t, body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want one naming %s", tc.body, err, tc.want)
		}
	}
}

func TestLandingMarginPerProfile(t *testing.T) {
	c, err := loadText(t, "landing_margin = 15\n"+
		"priority = [\"a\", \"b\"]\n\n[[account]]\nid = \"a\"\n\n[[account]]\nid = \"b\"\n"+
		"\n[[profile]]\nname = \"default\"\npool = [\"a\"]\n"+
		"\n[[profile]]\nname = \"work\"\ndir = \"~/.claude-work\"\npool = [\"b\"]\nlanding_margin = 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LandingMarginFor("default"); got != 15 {
		t.Errorf("default = %v, want the global 15", got)
	}
	if got := c.LandingMarginFor("work"); got != 0 {
		t.Errorf("work = %v, want its own 0 (an explicit zero is an override)", got)
	}
	if got := c.ForProfile("work").Margin(); got != 0 {
		t.Errorf("ForProfile(work).Margin() = %v, want 0", got)
	}
}

// Everything `cs config` can change must survive Write, or changing one
// setting silently resets another.
func TestAutoSwitchSettingsRoundTrip(t *testing.T) {
	m, n, im := 0.0, 4, 20.0
	in := &Config{
		SwitchAt: 85, HardFloor: 96, SwitchWhen: "idle",
		Cooldown: Duration{10 * time.Minute}, RefreshWindow: Duration{time.Hour},
		LandingMargin: &m, BlindFailoverPolls: &n,
		Priority: []string{"a", "b"},
		Accounts: []Account{{ID: "a"}, {ID: "b"}},
		Profiles: []Profile{
			{Name: "default", Pool: []string{"a"}},
			{Name: "work", Dir: "~/.claude-work", Pool: []string{"b"}, LandingMargin: &im},
		},
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	if out.Margin() != 0 || out.BlindPolls() != 4 || out.LandingMarginFor("work") != 20 {
		t.Errorf("round trip: margin %v, polls %d, work margin %v; want 0, 4, 20",
			out.Margin(), out.BlindPolls(), out.LandingMarginFor("work"))
	}
}
