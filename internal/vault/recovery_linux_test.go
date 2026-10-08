//go:build linux

package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// B1, through the swap, with the recovery seams left as production has them:
// work's unattributable credential is kept, and the default profile's live
// file — what every non-vault name used to mean on Linux — is untouched.
func TestLinuxSwapRecoveryLeavesAnotherProfilesLiveFileAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	v, _, _, it := swapWorld(t, "stranger", nil)
	readRecovery, writeRecovery = keychain.Read, keychain.Write // production

	defaultLive := &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "default-live"}}
	if err := keychain.WriteLive(defaultLive); err != nil {
		t.Fatal(err)
	}
	o := outgoingA()
	o.Profile = "work"
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatal(err)
	}
	got, err := keychain.ReadLive()
	if err != nil || got.ClaudeAIOAuth.AccessToken != "default-live" {
		t.Fatalf("default profile's live credential is now %+v, %v", got, err)
	}
	rec, err := keychain.Read(RecoveryService("work", 1))
	if err != nil || rec.ClaudeAIOAuth.AccessToken != "stranger" {
		t.Fatalf("recovery copy %+v, %v", rec, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "claudeswitch", "recovery", "work-1.json")); err != nil {
		t.Fatal(err)
	}
}
