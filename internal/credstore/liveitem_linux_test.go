//go:build linux

package credstore

import (
	"errors"
	"path/filepath"
	"testing"
)

// On Linux a configured profile's live credential is its own file, not the
// one this process's environment names.
func TestLinuxLiveItemUsesTheProfilesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	file := filepath.Join(home, ".claude-work", ".credentials.json")
	item := LiveItem("Claude Code-credentials-abcd1234", file)

	b := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "work-token"}}
	if err := item.Write(b); err != nil {
		t.Fatal(err)
	}
	got, err := item.Read()
	if err != nil || got.ClaudeAIOAuth.AccessToken != "work-token" {
		t.Fatalf("Read = %+v, %v", got, err)
	}
	if _, err := ReadLive(); err == nil {
		t.Fatal("the environment's credential file was written too")
	}
	if item.Name() != file {
		t.Errorf("Name = %q, want the file", item.Name())
	}
}

// A missing credential file is a definite "holds nothing", told apart from a
// read that failed.
func TestLinuxMissingLiveItemIsNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	item := LiveItem("svc", filepath.Join(t.TempDir(), ".credentials.json"))
	if _, err := item.Read(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read of a missing file = %v, want ErrNotFound", err)
	}
}
