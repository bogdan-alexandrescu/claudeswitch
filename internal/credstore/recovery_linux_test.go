//go:build linux

package credstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// B1 end to end on Linux, with no seam replaced: writing a recovery copy
// leaves the live credential file alone, and reading an empty slot is "not
// found", not the live credential.
func TestLinuxRecoveryNeverTouchesTheLiveFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetenv(t, "CLAUDE_CONFIG_DIR")
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	live := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "live-default"}}
	if err := Write(liveServiceBase, live); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(home, ".claude", ".credentials.json")

	if _, err := Read(RecoveryService("work", 1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an empty recovery slot read as %v, want ErrNotFound", err)
	}
	if err := Write(RecoveryService("work", 1), &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "saved"}}); err != nil {
		t.Fatal(err)
	}
	got, err := readAt("live", livePath)
	if err != nil || got.ClaudeAIOAuth.AccessToken != "live-default" {
		t.Fatalf("live file now %+v, %v; a recovery write reached it", got, err)
	}
	rec := filepath.Join(home, ".claude", "claudeswitch", "recovery", "work-1.json")
	fi, err := os.Stat(rec)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("recovery file mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(rec))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Errorf("recovery dir mode %v (%v), want 0700", di.Mode().Perm(), err)
	}
	back, err := Read(RecoveryService("work", 1))
	if err != nil || back.ClaudeAIOAuth.AccessToken != "saved" {
		t.Fatalf("recovery read back %+v, %v", back, err)
	}
}
