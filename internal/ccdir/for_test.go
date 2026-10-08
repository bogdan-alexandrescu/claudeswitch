package ccdir

import (
	"os"
	"path/filepath"
	"testing"
)

func mustFor(t *testing.T, dir string) Paths {
	t.Helper()
	p, err := For(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A profile whose config omits dir is the one Claude Code runs with
// CLAUDE_CONFIG_DIR unset, whatever this process's own environment says.
func TestForEmptyIsTheUnsetProfile(t *testing.T) {
	h := fakeHome(t)
	t.Setenv(EnvConfigDir, "/somewhere/else")
	p := mustFor(t, "")
	want := Paths{
		Dir:           filepath.Join(h, ".claude"),
		Projects:      filepath.Join(h, ".claude", "projects"),
		GlobalConfig:  filepath.Join(h, ".claude.json"),
		SecureStorage: filepath.Join(h, ".claude"),
	}
	if p != want {
		t.Fatalf("For(\"\") = %+v, want %+v", p, want)
	}
}

func TestForExpandsHomeAndKeepsIdentityInTheDir(t *testing.T) {
	h := fakeHome(t)
	unsetenv(t, EnvConfigDir)
	p := mustFor(t, "~/.claude-work")
	work := filepath.Join(h, ".claude-work")
	want := Paths{
		Dir:           work,
		Projects:      filepath.Join(work, "projects"),
		GlobalConfig:  filepath.Join(work, ".claude.json"),
		SecureStorage: work,
	}
	if p != want {
		t.Fatalf("For(~/.claude-work) = %+v, want %+v", p, want)
	}
}

// ~/.claude given explicitly is CLAUDE_CONFIG_DIR=~/.claude, whose identity
// file Claude Code keeps inside the dir rather than in the home directory.
func TestForExplicitDotClaudeIsNotTheUnsetProfile(t *testing.T) {
	h := fakeHome(t)
	p := mustFor(t, "~/.claude")
	if want := filepath.Join(h, ".claude", ".claude.json"); p.GlobalConfig != want {
		t.Fatalf("GlobalConfig = %s, want %s", p.GlobalConfig, want)
	}
}

func TestForPrefersTheLegacyConfigFile(t *testing.T) {
	h := fakeHome(t)
	work := filepath.Join(h, ".claude-work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(work, ".config.json")
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustFor(t, work).GlobalConfig; got != legacy {
		t.Fatalf("GlobalConfig = %s, want %s", got, legacy)
	}
}

// FromEnv is the environment-resolved profile, and must agree with the
// single-path functions it sits beside.
func TestFromEnvMatchesTheEnvFunctions(t *testing.T) {
	for _, cfg := range []string{"", "~/.claude-work"} {
		fakeHome(t)
		unsetenv(t, EnvSecureStorageDir)
		if cfg == "" {
			unsetenv(t, EnvConfigDir)
		} else {
			t.Setenv(EnvConfigDir, cfg)
		}
		p, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		want := Paths{
			Dir:           mustDir(t, Dir),
			Projects:      mustDir(t, Projects),
			GlobalConfig:  mustDir(t, GlobalConfig),
			SecureStorage: mustDir(t, SecureStorageDir),
		}
		if p != want {
			t.Errorf("CLAUDE_CONFIG_DIR=%q: FromEnv() = %+v, want %+v", cfg, p, want)
		}
	}
}
