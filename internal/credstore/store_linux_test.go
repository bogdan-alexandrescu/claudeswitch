//go:build linux

package credstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// On Linux the store is plain files, so the properties that macOS gets from the
// Keychain have to be enforced here instead.
func TestLinuxWriteReadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	in := &Blob{
		MCPOAuth:      json.RawMessage(`{"notion|x":{"accessToken":"n"}}`),
		ClaudeAIOAuth: &OAuth{AccessToken: "acc", RefreshToken: "ref", ExpiresAt: 1700000000000},
		Meta:          &Meta{AccountUUID: "seat", OrgID: "org", Email: "a@b"},
	}
	if err := Write(VaultService("work"), in); err != nil {
		t.Fatal(err)
	}
	got, err := Read(VaultService("work"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaudeAIOAuth.AccessToken != "acc" || got.ClaudeAIOAuth.RefreshToken != "ref" {
		t.Fatalf("credential did not survive: %+v", got.ClaudeAIOAuth)
	}
	if string(got.MCPOAuth) != string(in.MCPOAuth) {
		t.Fatalf("mcpOAuth did not survive: %s", got.MCPOAuth)
	}
	if got.Meta.Seat() != "seat@org" {
		t.Fatalf("annotation did not survive: %+v", got.Meta)
	}
}

// A credential must never exist world-readable, not even briefly.
func TestLinuxCredentialsArePrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Write(VaultService("work"), &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "x"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := pathFor(VaultService("work"))
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credential file is %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("credential directory is %o, want 700", perm)
	}
}

// The live credential is the file Claude Code reads; vault entries must never
// land on top of it.
func TestLinuxLiveAndVaultPathsAreDistinct(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unsetenv(t, "CLAUDE_CONFIG_DIR")
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	live, err := pathFor(liveServiceBase)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(live) != ".credentials.json" {
		t.Fatalf("live credential should be ~/.claude/.credentials.json, got %s", live)
	}
	vaulted, err := pathFor(VaultService("work"))
	if err != nil {
		t.Fatal(err)
	}
	if vaulted == live {
		t.Fatal("a vault entry must never be written over the live credential")
	}
	if filepath.Dir(vaulted) == filepath.Dir(live) {
		t.Fatal("vault entries should live in their own directory, not loose in ~/.claude")
	}
}

// An account id is used to build a path, so it must not be able to escape.
func TestLinuxRejectsPathTraversalInAccountIds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, bad := range []string{"../escape", "a/b", "..", "/etc/passwd"} {
		if _, err := pathFor(VaultService(bad)); err == nil {
			t.Errorf("id %q should be refused, not turned into a path", bad)
		}
	}
}

func TestLinuxReadMissingCredentialSaysWhere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := Read(VaultService("absent"))
	if err == nil {
		t.Fatal("reading a credential that is not there must fail")
	}
}

func TestLinuxDeleteIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Delete(VaultService("never-existed")); err != nil {
		t.Fatalf("deleting something absent should be fine: %v", err)
	}
}

// A half-written credential is worse than none: the swap path reads it back and
// must see either the old value or the new one.
func TestLinuxWriteLeavesNoTempFileBehind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Write(VaultService("work"), &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "x"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := pathFor(VaultService("work"))
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("a .tmp file was left beside the credential")
	}
}

// Claude Code keeps the plaintext credential in its secure-storage dir, which
// is CLAUDE_CONFIG_DIR unless CLAUDE_SECURESTORAGE_CONFIG_DIR says otherwise.
func TestLinuxLiveCredentialFollowsTheConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	d := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", d)
	live, err := pathFor(liveServiceFor(d))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d, ".credentials.json"); live != want {
		t.Fatalf("live credential = %s, want %s", live, want)
	}
}

func TestLinuxLiveCredentialFollowsTheSecureStorageDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	d := t.TempDir()
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", d)
	live, err := pathFor(liveServiceFor(d))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d, ".credentials.json"); live != want {
		t.Fatalf("live credential = %s, want %s", live, want)
	}
}

// The vault is shared by every profile, so it stays put whatever
// CLAUDE_CONFIG_DIR says: claudeswitch run from an acme session must see the
// same accounts as the daemon.
func TestLinuxVaultDoesNotFollowTheConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	p, err := pathFor(VaultService("work"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude", "claudeswitch", "work.json"); p != want {
		t.Fatalf("vault entry = %s, want %s", p, want)
	}
}

// On Linux any name resolves to the one file, so resolution needs no lookup
// and cannot fail for want of an item.
func TestLinuxLiveServiceNeedsNoItemToExist(t *testing.T) {
	resetLiveCache()
	t.Cleanup(resetLiveCache)
	t.Setenv("HOME", t.TempDir())
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	t.Setenv("CLAUDE_CONFIG_DIR", "/nowhere/.claude-work")
	if _, err := LiveService(); err != nil {
		t.Fatal(err)
	}
}
