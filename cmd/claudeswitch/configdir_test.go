package main

import (
	"os"
	"path/filepath"
	"testing"
)

// settings.json and the plugin record resolve through the same helper as
// everything else, so a literal ~ (from a plist or a quoted assignment) means
// the same directory for all of them.
func TestSettingsPathExpandsATildeConfigDir(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/.claude-work")
	got, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(h, ".claude-work", "settings.json"); got != want {
		t.Fatalf("settingsPath() = %s, want %s", got, want)
	}
}

func TestPluginInstalledReadsTheConfigDir(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/.claude-work")
	dir := filepath.Join(h, ".claude-work", "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"plugins":{"cs@claudeswitch":[]}}`
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pluginInstalled() {
		t.Fatal("the plugin record in the config dir was not found")
	}
}
