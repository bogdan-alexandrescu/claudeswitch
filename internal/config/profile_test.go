package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

const profileAccounts = `
switch_at = 85
switch_at_weekly = 98
hard_floor = 99

[[account]]
id = "personal"

[[account]]
id = "a4"

[[account]]
id = "work-1"

[[account]]
id = "work-2"
`

func loadString(t *testing.T, body string) (*Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func mustLoad(t *testing.T, body string) *Config {
	t.Helper()
	c, err := loadString(t, body)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func names(in []Profile) []string {
	var out []string
	for _, i := range in {
		out = append(out, i.Name)
	}
	return out
}

// No [[profile]] blocks is today's behaviour: one profile, every account, its
// directory taken from the environment as Claude Code would.
func TestNoProfileBlocksIsOneImplicitDefault(t *testing.T) {
	c := mustLoad(t, profileAccounts)
	got := c.EffectiveProfiles()
	if len(got) != 1 {
		t.Fatalf("want one implicit profile, got %+v", got)
	}
	d := got[0]
	if d.Name != "default" || !d.FromEnv || d.Dir != "" {
		t.Fatalf("implicit profile = %+v, want default resolved from the environment", d)
	}
	if want := []string{"personal", "a4", "work-1", "work-2"}; !reflect.DeepEqual(d.Pool, want) {
		t.Fatalf("implicit pool = %v, want every account %v", d.Pool, want)
	}
	if len(c.Profiles) != 0 {
		t.Fatalf("the implicit profile must not become a declared one: %+v", c.Profiles)
	}
}

func TestProfileBlocksParse(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"
pool = ["personal", "a4"]

[[profile]]
name             = "work"
dir              = "~/.claude-work"
pool             = ["work-1", "work-2"]
switch_at        = 75
switch_at_weekly = 90
hard_floor       = 95
`)
	got := c.EffectiveProfiles()
	if !reflect.DeepEqual(names(got), []string{"default", "work"}) {
		t.Fatalf("profiles = %v", names(got))
	}
	if !reflect.DeepEqual(c.ProfileNames(), []string{"default", "work"}) {
		t.Fatalf("ProfileNames = %v", c.ProfileNames())
	}
	d, w := got[0], got[1]
	if d.Dir != "" || d.FromEnv {
		t.Errorf("default omits dir, so it is the CLAUDE_CONFIG_DIR-unset profile, not env-resolved: %+v", d)
	}
	if w.Dir != "~/.claude-work" || !reflect.DeepEqual(w.Pool, []string{"work-1", "work-2"}) {
		t.Errorf("work = %+v", w)
	}
	if w.SwitchAt != 75 || w.SwitchAtWeekly != 90 || w.HardFloor != 95 {
		t.Errorf("overrides lost: %+v", w)
	}
}

// D6: accounts no pool names join the profile called "default".
func TestUnlistedAccountsJoinDefault(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"
pool = ["a4"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["work-1"]
`)
	d, ok := c.ProfileNamed("default")
	if !ok {
		t.Fatal("no default profile")
	}
	if want := []string{"a4", "personal", "work-2"}; !reflect.DeepEqual(d.Pool, want) {
		t.Fatalf("default pool = %v, want %v (listed first, then unlisted in config order)", d.Pool, want)
	}
	if got, _ := c.ProfileOf("work-2"); got != "default" {
		t.Errorf("ProfileOf(work-2) = %q, want default", got)
	}
	if got, _ := c.ProfileOf("work-1"); got != "work" {
		t.Errorf("ProfileOf(work-1) = %q, want work", got)
	}
	if _, ok := c.ProfileOf("nobody"); ok {
		t.Error("an unknown account belongs to no profile")
	}
	// The declared block is not rewritten: the writer must print what the
	// person wrote, not the derived pool.
	if !reflect.DeepEqual(c.Profiles[0].Pool, []string{"a4"}) {
		t.Errorf("declared pool was mutated: %v", c.Profiles[0].Pool)
	}
}

func TestDefaultMayOmitItsPool(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["work-1", "work-2"]
`)
	d, _ := c.ProfileNamed("default")
	if want := []string{"personal", "a4"}; !reflect.DeepEqual(d.Pool, want) {
		t.Fatalf("default pool = %v, want %v", d.Pool, want)
	}
}

func TestUnlistedDisabledAccountNeedsNoDefault(t *testing.T) {
	c := mustLoad(t, `
[[account]]
id = "w"

[[account]]
id = "old"
enabled = false

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w"]
`)
	if _, ok := c.ProfileOf("old"); ok {
		t.Error("a disabled account in no pool belongs to no profile when there is no default")
	}
}

func TestInvalidProfileConfigIsAnError(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string // every one must appear in the error
	}{
		{"empty name", `
[[profile]]
dir = "~/.claude-work"
pool = ["work-1"]
`, []string{"name"}},
		{"duplicate name", `
[[profile]]
name = "work"
dir = "~/.claude-a"
[[profile]]
name = "work"
dir = "~/.claude-b"
`, []string{"work", "twice"}},
		{"two profiles without dir", `
[[profile]]
name = "default"
[[profile]]
name = "other"
`, []string{"default", "other", "dir"}},
		{"account twice in one pool", `
[[profile]]
name = "default"
pool = ["personal", "personal"]
`, []string{"default", "personal", "twice"}},
		{"overlapping pools", `
[[profile]]
name = "default"
pool = ["personal", "a4"]
[[profile]]
name = "work"
dir = "~/.claude-work"
pool = ["a4", "work-1"]
`, []string{"a4", "default", "work"}},
		{"pool names an unknown account", `
[[profile]]
name = "default"
pool = ["personal", "ghost"]
`, []string{"ghost", "default"}},
		{"no default and an unlisted enabled account", `
[[profile]]
name = "home"
pool = ["personal", "a4"]
[[profile]]
name = "work"
dir = "~/.claude-work"
pool = ["work-1"]
`, []string{"work-2", "default"}},
		{"override out of range", `
[[profile]]
name = "default"
switch_at = 120
`, []string{"default", "switch_at"}},
		{"hard_floor under the profile's own switch_at", `
[[profile]]
name = "default"
switch_at = 90
hard_floor = 88
`, []string{"default", "hard_floor"}},
		{"switch_at override above the global hard_floor", `
[[profile]]
name = "default"
switch_at = 99.5
`, []string{"default", "hard_floor"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, profileAccounts+tc.body)
			if err == nil {
				t.Fatal("want a config error, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestThresholdsFallBackToGlobal(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"

[[profile]]
name      = "work"
dir       = "~/.claude-work"
pool      = ["work-1", "work-2"]
switch_at = 75
`)
	if got := c.SwitchAtFor("work"); got != 75 {
		t.Errorf("SwitchAtFor(work) = %v, want the override 75", got)
	}
	if got := c.SwitchAtFor("default"); got != 85 {
		t.Errorf("SwitchAtFor(default) = %v, want the global 85", got)
	}
	if got := c.SwitchAtWeeklyFor("work"); got != 98 {
		t.Errorf("SwitchAtWeeklyFor(work) = %v, want the global 98", got)
	}
	if got := c.HardFloorFor("work"); got != 99 {
		t.Errorf("HardFloorFor(work) = %v, want the global 99", got)
	}
	if got := c.TriggerForProfile("work", usage.FiveHourKey); got != 75 {
		t.Errorf("session trigger for work = %v, want 75", got)
	}
	if got := c.TriggerForProfile("work", usage.SevenDayKey); got != 98 {
		t.Errorf("weekly trigger for work = %v, want 98", got)
	}
	if got := c.SwitchAtFor("nonexistent"); got != 85 {
		t.Errorf("an unknown profile uses the global value, got %v", got)
	}

	// ForProfile is the whole config with this profile's thresholds in
	// place, so code that takes a *Config (policy) needs no change.
	w := c.ForProfile("work")
	if w.SwitchAt != 75 || w.SwitchAtWeekly != 98 || w.HardFloor != 99 {
		t.Errorf("ForProfile(work) thresholds = %v/%v/%v", w.SwitchAt, w.SwitchAtWeekly, w.HardFloor)
	}
	if c.SwitchAt != 85 {
		t.Error("ForProfile must not change the global config")
	}
}

func TestWeeklyAndFloorOverrides(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name             = "default"
switch_at_weekly = 90
hard_floor       = 97
`)
	if got := c.TriggerForProfile("default", usage.SevenDayKey); got != 90 {
		t.Errorf("weekly trigger = %v, want 90", got)
	}
	if got := c.HardFloorFor("default"); got != 97 {
		t.Errorf("hard floor = %v, want 97", got)
	}
}

// What the writer prints must load back to the same profiles: `cs config`
// rewrites the whole file, so a block it forgot would be deleted.
func TestWrittenProfilesRoundTrip(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"
pool = ["personal", "a4"]

[[profile]]
name             = "work"
dir              = "~/.claude-work"
pool             = ["work-1", "work-2"]
switch_at        = 75
switch_at_weekly = 90
hard_floor       = 95
`)
	path := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		b, _ := os.ReadFile(path)
		t.Fatalf("written config does not load: %v\n%s", err, b)
	}
	if !reflect.DeepEqual(back.Profiles, c.Profiles) {
		t.Fatalf("profiles changed in the round trip:\n got %+v\nwant %+v", back.Profiles, c.Profiles)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "CLAUDE_CONFIG_DIR") {
		t.Error("a profile without dir must say in the file what that means")
	}
}

func TestNoProfilesWritesNoProfileBlock(t *testing.T) {
	c := mustLoad(t, profileAccounts)
	path := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "[[profile]]") {
		t.Fatalf("no profiles were declared, none must be written:\n%s", b)
	}
}

func hasWarning(c *Config, want string) bool {
	for _, w := range c.Warnings() {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

// Owner decision 2026-10-07: two profiles on one directory load, with a
// warning rather than an error. They share transcripts, so activity and
// refusals can be attributed to the wrong one.
func TestSharedDirectoryIsAWarning(t *testing.T) {
	c, err := loadString(t, profileAccounts+`
[[profile]]
name = "default"
[[profile]]
name = "alt"
dir = "~/.claude"
`)
	if err != nil {
		t.Fatalf("a shared directory must load: %v", err)
	}
	want := `profiles "default" and "alt" share ~/.claude; activity and refusals may be attributed to the wrong one`
	if !hasWarning(c, want) {
		t.Fatalf("Warnings() = %q, want one containing %q", c.Warnings(), want)
	}
}

func TestSameDirSpelledTwoWaysIsAWarning(t *testing.T) {
	c, err := loadString(t, profileAccounts+`
[[profile]]
name = "default"
[[profile]]
name = "a"
dir = "~/.claude-work"
[[profile]]
name = "b"
dir = "~/.claude-work/"
`)
	if err != nil {
		t.Fatalf("a shared directory must load: %v", err)
	}
	if !hasWarning(c, `profiles "a" and "b" share ~/.claude-work;`) {
		t.Fatalf("Warnings() = %q", c.Warnings())
	}
}

func TestDistinctDirectoriesDoNotWarn(t *testing.T) {
	c := mustLoad(t, profileAccounts+`
[[profile]]
name = "default"
[[profile]]
name = "work"
dir = "~/.claude-work"
`)
	if hasWarning(c, "share") {
		t.Fatalf("no directory is shared: %q", c.Warnings())
	}
}
