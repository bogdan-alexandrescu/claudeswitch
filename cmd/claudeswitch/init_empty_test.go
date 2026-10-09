package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.6.1 (decided 2026-10-08): `cs init --empty --json` writes the empty,
// comments-only config the app's first-run window needs before `add --json`
// can append to it, so the app never writes claudeswitch's files itself.
func TestInitEmptyWritesACommentsOnlyConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "claudeswitch")
	path := filepath.Join(dir, "config.toml")

	out := captureStdout(t, func() error {
		return cmdInit([]string{"--empty", "--json", "--config", path})
	})
	m := decodeJSON(t, []byte(out))
	if m["path"] != path {
		t.Fatalf("init --empty --json answered %v, want path %q", m, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 0600", info.Mode().Perm())
	}
	if d, err := os.Stat(dir); err != nil || d.Mode().Perm() != 0o700 {
		t.Errorf("config folder mode %v (%v), want 0700", d.Mode().Perm(), err)
	}
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) == 0 {
		t.Fatal("the empty config has no lines at all")
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "#") {
			t.Errorf("the empty config holds a setting %q: it must be comments only", l)
		}
	}
}

func TestInitEmptyRefusesAnExistingConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("keep = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdInit([]string{"--empty", "--json", "--config", path})
	if code := errCode(t, err); code != codeExists {
		t.Errorf("code %q, want %q", code, codeExists)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep = 1\n" {
		t.Errorf("an existing config was changed: %q", b)
	}
}

// Without --config, the default path, which is under HOME.
func TestInitEmptyDefaultsToTheDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetenvT(t, "XDG_CONFIG_HOME")
	out := captureStdout(t, func() error { return cmdInit([]string{"--empty", "--json"}) })
	got, _ := decodeJSON(t, []byte(out))["path"].(string)
	if !strings.HasPrefix(got, home) || filepath.Base(got) != "config.toml" {
		t.Fatalf("path %q, want config.toml under HOME %q", got, home)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("not written: %v", err)
	}
}
