package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
)

func doctorConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDoctorChecksEachProfile(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	present := filepath.Join(h, ".claude")
	if err := os.MkdirAll(present, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(present, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(h, ".claude-work")

	cfg := doctorConfig(t, `
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
`)
	resolve := func(in config.Profile) (profile.Resolved, error) {
		switch in.Name {
		case "default":
			return profile.Resolved{Name: "default", Dir: present,
				CredentialFile: filepath.Join(present, ".credentials.json"),
				Service:        "Claude Code-credentials", Pool: in.Pool}, nil
		default:
			return profile.Resolved{Name: in.Name, Dir: missing,
					CredentialFile: filepath.Join(missing, ".credentials.json"), Pool: in.Pool},
				errors.New(`no Claude Code credential found for config dir "~/.claude-work"; ` +
					`this profile may not be logged in yet`)
		}
	}
	var b strings.Builder
	failed := doctorProfiles(&b, cfg, resolve)
	out := b.String()
	if !failed {
		t.Errorf("a missing dir and an unresolved item are failures:\n%s", out)
	}
	for _, want := range []string{
		"default", "work",
		present, missing,
		"does not exist",
		liveItemWord(),
		"may not be logged in",
		"personal", "w1, w2",
		"CLAUDE_CONFIG_DIR unset",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorFlagsAnEmptyPool(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	cfg := doctorConfig(t, `
[[account]]
id = "personal"

[[profile]]
name = "default"

[[profile]]
name = "spare"
dir  = "~/.claude-spare"
`)
	resolve := func(in config.Profile) (profile.Resolved, error) {
		return profile.Resolved{Name: in.Name, Dir: h, CredentialFile: filepath.Join(h, "x"),
			Service: "svc", Pool: in.Pool}, nil
	}
	var b strings.Builder
	doctorProfiles(&b, cfg, resolve)
	if !strings.Contains(b.String(), "empty") {
		t.Errorf("a profile with no accounts cannot rotate, doctor must say so:\n%s", b.String())
	}
}

// An empty pool is a warning: it must not make doctor fail.
func TestDoctorEmptyPoolIsNotAFailure(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	if err := os.WriteFile(filepath.Join(h, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := doctorConfig(t, `
[[account]]
id = "personal"

[[profile]]
name = "default"

[[profile]]
name = "spare"
dir  = "~/.claude-spare"
`)
	resolve := func(in config.Profile) (profile.Resolved, error) {
		return profile.Resolved{Name: in.Name, Dir: h, CredentialFile: filepath.Join(h, ".credentials.json"),
			Service: "svc", Pool: in.Pool}, nil
	}
	var b strings.Builder
	if doctorProfiles(&b, cfg, resolve) {
		t.Fatalf("an empty pool is a warning, not a failure:\n%s", b.String())
	}
}

// doctor's exit status reflects failed checks.
func TestDoctorExit(t *testing.T) {
	if err := doctorExit(0); err != nil {
		t.Fatalf("no failures, want nil, got %v", err)
	}
	if err := doctorExit(2); err == nil || !strings.Contains(err.Error(), "2") {
		t.Fatalf("two failures, want an error naming them, got %v", err)
	}
}

// doctor shows the config's warnings, including a shared profile directory.
func TestDoctorShowsConfigWarnings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := doctorConfig(t, `
[[account]]
id = "personal"

[[profile]]
name = "default"

[[profile]]
name = "alt"
dir  = "~/.claude"
`)
	var b strings.Builder
	doctorWarnings(&b, cfg)
	if !strings.Contains(b.String(), `profiles "default" and "alt" share ~/.claude`) {
		t.Fatalf("doctor output lacks the shared-directory warning:\n%s", b.String())
	}
}

// liveItemWord is how doctor names a profile's live credential on this
// platform: a keychain item on macOS, a credentials file elsewhere.
func liveItemWord() string {
	if runtime.GOOS == "darwin" {
		return "Claude Code-credentials"
	}
	return ".credentials.json"
}
