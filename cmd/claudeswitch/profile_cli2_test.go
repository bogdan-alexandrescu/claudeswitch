package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// With no profile config nothing changes: `use` never consulted the config,
// so an account vaulted but not configured is still usable.
func TestNoProfileConfigRefusesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	single := doctorConfig(t, "[[account]]\nid = \"a\"\n")
	in, _ := single.ProfileNamed("default")
	if err := refuseOutsidePool(single, in, "vaulted-not-configured"); err != nil {
		t.Fatalf("no profile config must refuse nothing: %v", err)
	}
}

// add and login put a credential into a profile's live item, so they pick
// the profile as use does and keep to its pool. An id the config does not
// list is add's business (it says so and offers a block), not a pool's.
func TestProfileForAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, twoProfileTOML)
	setCCDir(t, "")
	if in, err := profileForAccount(cfg, "", "personal", false); err != nil || in.Name != "default" {
		t.Fatalf("personal from a default shell = %+v, %v", in, err)
	}
	if _, err := profileForAccount(cfg, "", "w1", false); err == nil || !strings.Contains(err.Error(), `"work"`) {
		t.Fatalf("w1 from a default shell = %v, want a refusal naming work", err)
	}
	if in, err := profileForAccount(cfg, "work", "w1", false); err != nil || in.Name != "work" {
		t.Fatalf("w1 --profile work = %+v, %v", in, err)
	}
	if _, err := profileForAccount(cfg, "", "new-one", true); err != nil {
		t.Fatalf("an unconfigured id may be added: %v", err)
	}
	if _, err := profileForAccount(cfg, "", "new-one", false); err == nil {
		t.Fatal("an unconfigured id is in no pool, so use must refuse it")
	}
}

// Commands that run Claude Code for a profile (login, its identity) run it
// with that profile's CLAUDE_CONFIG_DIR, whatever this shell has.
func TestEnvForProfile(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/shell/dir")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/shell/secure")
	get := func(env []string, k string) (string, bool) {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v, true
			}
		}
		return "", false
	}
	if env := envForProfile(config.Profile{Name: "default", FromEnv: true}); env != nil {
		t.Fatalf("the implicit profile inherits the environment, got %v", env)
	}
	env := envForProfile(config.Profile{Name: "work", Dir: "~/.claude-work"})
	if v, _ := get(env, "CLAUDE_CONFIG_DIR"); v != "~/.claude-work" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the profile's dir as written", v)
	}
	if _, ok := get(env, "CLAUDE_SECURESTORAGE_CONFIG_DIR"); ok {
		t.Fatal("a declared profile's storage is its dir; the shell's override must not leak in")
	}
	env = envForProfile(config.Profile{Name: "default"})
	if _, ok := get(env, "CLAUDE_CONFIG_DIR"); ok {
		t.Fatal("the profile with no dir runs with CLAUDE_CONFIG_DIR unset")
	}
	if _, ok := get(env, "HOME"); !ok && os.Getenv("HOME") != "" {
		t.Fatal("the rest of the environment must be kept")
	}
}

// refresh must never renew a credential live in ANY profile without
// --allow-active, and must write the renewed token back to the one holding it.
func TestLiveHolder(t *testing.T) {
	def, work := fakeLive{"def-item"}, fakeLive{"work-item"}
	targets := []liveTarget{{name: "default", live: def}, {name: "work", live: work}}
	cases := []struct {
		name     string
		holds    map[string]string
		targets  []liveTarget
		wantName string
		wantItem keychain.Live
		wantLive bool
	}{
		{name: "nowhere", targets: targets},
		{name: "in work", targets: targets, holds: map[string]string{"work-item/a": "yes"},
			wantName: "work", wantItem: work, wantLive: true},
		{name: "unreadable in default", targets: targets, holds: map[string]string{"def-item/a": "unknown"},
			wantName: "default", wantItem: def, wantLive: true},
		{name: "work not logged in", targets: []liveTarget{{name: "default", live: def},
			{name: "work", unresolved: fmt.Errorf("x: %w", keychain.ErrNotFound)}}},
		{name: "work lookup failed", targets: []liveTarget{{name: "default", live: def},
			{name: "work", unresolved: errors.New("timeout")}}, wantName: "work", wantLive: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, item, live := liveHolder(c.targets, &fakeVault{holds: c.holds}, "a")
			if name != c.wantName || live != c.wantLive || item != c.wantItem {
				t.Fatalf("liveHolder = (%q, %v, %v), want (%q, %v, %v)",
					name, item, live, c.wantName, c.wantItem, c.wantLive)
			}
		})
	}
}
