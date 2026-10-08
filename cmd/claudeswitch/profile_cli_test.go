package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

const twoProfileTOML = `
[[account]]
id = "personal"
[[account]]
id = "w1"
[[account]]
id = "w2"

[[profile]]
name = "default"

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1", "w2"]
switch_at = 75
`

// setCCDir sets CLAUDE_CONFIG_DIR for a test, "" meaning unset.
func setCCDir(t *testing.T, dir string) {
	t.Helper()
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if dir == "" {
		unsetenvT(t, "CLAUDE_CONFIG_DIR")
		return
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func TestPickProfileWithNoProfileConfigIsTheImplicitDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, "[[account]]\nid = \"a\"\n")
	for _, env := range []string{"", "~/.claude-work", "/anywhere"} {
		setCCDir(t, env)
		in, err := pickProfile(cfg, "")
		if err != nil {
			t.Fatalf("env %q: %v", env, err)
		}
		if in.Name != "default" || !in.FromEnv {
			t.Fatalf("env %q: got %+v, want the implicit default resolving from the environment", env, in)
		}
	}
	if _, err := pickProfile(cfg, "work"); err == nil {
		t.Fatal("--profile naming no profile must be an error")
	}
	if in, err := pickProfile(cfg, "default"); err != nil || in.Name != "default" {
		t.Fatalf("--profile default = %+v, %v", in, err)
	}
}

func TestPickProfileFromTheCallersEnvironment(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	cfg := doctorConfig(t, twoProfileTOML)
	for env, want := range map[string]string{
		"":                                     "default", // D11: unset is the profile with no dir
		"~/.claude-work":                       "work",
		filepath.Join(h, ".claude-work"):       "work",
		filepath.Join(h, ".claude-work") + "/": "work",
	} {
		setCCDir(t, env)
		in, err := pickProfile(cfg, "")
		if err != nil {
			t.Fatalf("env %q: %v", env, err)
		}
		if in.Name != want {
			t.Fatalf("CLAUDE_CONFIG_DIR=%q picked %q, want %q", env, in.Name, want)
		}
	}
}

// A flag beats the environment, so `--profile` works from any shell.
func TestPickProfileFlagWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, twoProfileTOML)
	setCCDir(t, "~/.claude-work")
	in, err := pickProfile(cfg, "default")
	if err != nil || in.Name != "default" {
		t.Fatalf("--profile default from a work shell = %+v, %v", in, err)
	}
	_, err = pickProfile(cfg, "nope")
	if err == nil || !strings.Contains(err.Error(), `"default"`) || !strings.Contains(err.Error(), `"work"`) {
		t.Fatalf("an unknown --profile must list the profiles there are: %v", err)
	}
}

// The caller's environment matches no declared profile: say so, and how to
// pick one, rather than guess.
func TestPickProfileEnvMatchingNothingIsAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, twoProfileTOML)
	setCCDir(t, "/somewhere/else")
	_, err := pickProfile(cfg, "")
	if err == nil {
		t.Fatal("an environment no profile declares must be an error")
	}
	for _, want := range []string{"/somewhere/else", "--profile", `"default"`, `"work"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	// With no profile omitting dir, an unset CLAUDE_CONFIG_DIR matches nothing.
	cfg = doctorConfig(t, `
[[account]]
id = "w1"
[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`)
	setCCDir(t, "")
	_, err = pickProfile(cfg, "")
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR") {
		t.Fatalf("unset env with no dir-less profile: %v", err)
	}
}

// Two profiles on one directory spelled two ways: the spelling the caller
// used decides, since that is what Claude Code hashed. Such a config no
// longer loads (lane 10 security review), so it is built directly here to
// keep pickProfile's own answer to it pinned.
func TestPickProfilePrefersTheExactSpelling(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	if _, err := config.Load(writeConfig(t, fmt.Sprintf(`
[[account]]
id = "a"
[[account]]
id = "b"
[[profile]]
name = "default"
dir = "~/x"
pool = ["a"]
[[profile]]
name = "abs"
dir = %q
pool = ["b"]
`, filepath.Join(h, "x")))); err == nil {
		t.Fatal("two profiles on one folder must not load")
	}
	cfg := &config.Config{
		Accounts: []config.Account{{ID: "a"}, {ID: "b"}},
		Profiles: []config.Profile{
			{Name: "default", Dir: "~/x", Pool: []string{"a"}},
			{Name: "abs", Dir: filepath.Join(h, "x"), Pool: []string{"b"}},
		},
	}
	setCCDir(t, "~/x")
	if in, err := pickProfile(cfg, ""); err != nil || in.Name != "default" {
		t.Fatalf("~/x = %+v, %v", in, err)
	}
	setCCDir(t, filepath.Join(h, "x"))
	if in, err := pickProfile(cfg, ""); err != nil || in.Name != "abs" {
		t.Fatalf("%s = %+v, %v", filepath.Join(h, "x"), in, err)
	}
	setCCDir(t, filepath.Join(h, "x")+"/")
	if _, err := pickProfile(cfg, ""); err == nil {
		t.Fatal("a spelling two profiles share must be an error, not a guess")
	}
}

// D5: a manual swap stays within the target profile's pool, naming the
// profile that owns the account. There is no --force.
func TestUseOutsideThePoolIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, twoProfileTOML)
	def, _ := cfg.ProfileNamed("default")
	work, _ := cfg.ProfileNamed("work")

	if err := refuseOutsidePool(cfg, def, "personal"); err != nil {
		t.Fatalf("personal is default's own: %v", err)
	}
	if err := refuseOutsidePool(cfg, work, "w2"); err != nil {
		t.Fatalf("w2 is work's own: %v", err)
	}
	err := refuseOutsidePool(cfg, def, "w1")
	if err == nil {
		t.Fatal("w1 belongs to work; using it in default must be refused")
	}
	for _, want := range []string{`"w1"`, `"work"`, `"default"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}

	// With no profile config every account is in the one pool.
	single := doctorConfig(t, "[[account]]\nid = \"a\"\n[[account]]\nid = \"b\"\n")
	in, _ := single.ProfileNamed("default")
	if err := refuseOutsidePool(single, in, "b"); err != nil {
		t.Fatalf("no profile config: %v", err)
	}
}

// The refusal happens before anything is read or written: `use` exits with it
// without touching the vault or a live credential.
func TestCmdUseRefusesAcrossPoolsBeforeTouchingAnything(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	setCCDir(t, "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(twoProfileTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdUse([]string{"--config", path, "w1"})
	if err == nil || !strings.Contains(err.Error(), `"work"`) {
		t.Fatalf("cs use w1 from the default profile = %v, want a refusal naming work", err)
	}
	err = cmdUse([]string{"--config", path, "--profile", "work", "personal"})
	if err == nil || !strings.Contains(err.Error(), `"default"`) {
		t.Fatalf("cs use personal --profile work = %v, want a refusal naming default", err)
	}
}

// §3, the same rule the daemon applies before a swap: refuse when the account
// is, or may be, live in another profile.
func TestLiveElsewhereOf(t *testing.T) {
	cfg := doctorConfig(t, twoProfileTOML)
	defItem, workItem := fakeLive{"def-item"}, fakeLive{"work-item"}
	targets := func(workUnresolved error) []liveTarget {
		w := liveTarget{name: "work", live: workItem}
		if workUnresolved != nil {
			w = liveTarget{name: "work", unresolved: workUnresolved}
		}
		return []liveTarget{{name: "default", live: defItem}, w}
	}
	ctx := context.Background()
	fresh := func() *state.State {
		st, _ := state.Load(filepath.Join(t.TempDir(), "s.json"), "default", "work")
		return st
	}

	cases := []struct {
		name      string
		holds     map[string]string
		active    string // work's recorded active account
		unres     error
		wantOther string
	}{
		{name: "nowhere else", wantOther: ""},
		{name: "recorded live in work", active: "personal", wantOther: "work"},
		{name: "in work's item", holds: map[string]string{"work-item/personal": "yes"}, wantOther: "work"},
		{name: "unknown in work (D18)", holds: map[string]string{"work-item/personal": "unknown"}, wantOther: "work"},
		{name: "work not logged in", unres: fmt.Errorf("x: %w", keychain.ErrNotFound), wantOther: ""},
		{name: "work lookup failed", unres: errors.New("keychain timed out"), wantOther: "work"},
		{name: "only self holds it", holds: map[string]string{"def-item/personal": "yes"}, wantOther: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := fresh()
			st.Profile("work").Active = c.active
			v := &fakeVault{holds: c.holds}
			other, why := liveElsewhereOf(ctx, v, cfg, st, "default", targets(c.unres), "personal")
			if other != c.wantOther {
				t.Fatalf("liveElsewhereOf = %q (%s), want %q", other, why, c.wantOther)
			}
			if other != "" && why == "" {
				t.Fatal("a refusal must say why")
			}
		})
	}
}
