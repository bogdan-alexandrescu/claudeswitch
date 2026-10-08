package credstore

import (
	"path/filepath"
	"strings"
	"testing"
)

// B1: a recovery copy must never land on a live credential. On Linux every
// name that is not a vault entry used to mean the live file, so a recovery
// write replaced the default profile's credential. Recovery names are their
// own kind now, with their own storage, on every platform.
func TestRecoveryNamesAreTheirOwnKind(t *testing.T) {
	name := RecoveryService("work", 3)
	if !IsRecoveryService(name) {
		t.Fatalf("%q is not recognised as a recovery name", name)
	}
	if IsVaultService(name) {
		t.Errorf("%q reads as a vault entry", name)
	}
	for _, live := range []string{liveServiceBase, liveServiceFor("/x/.claude-work"), VaultService("a")} {
		if IsRecoveryService(live) {
			t.Errorf("%q reads as a recovery name", live)
		}
	}
	// Lane 7: a swap that names no profile must not file its copy among a
	// declared "default" profile's, which may be a different Claude Code.
	if RecoveryService("", 1) == RecoveryService("default", 1) {
		t.Error("the unnamed recovery slots collide with the default profile's")
	}
}

// Every recovery name parses back to the profile and slot it was made from,
// and nothing else parses as one.
func TestRecoveryNamesParseBack(t *testing.T) {
	for _, c := range []struct {
		prof string
		slot int
	}{{"work", 3}, {"default", 1}, {"", 2}, {"work-1", 5}, {"3", 4}} {
		prof, slot, ok := ParseRecoveryService(RecoveryService(c.prof, c.slot))
		if !ok || prof != c.prof || slot != c.slot {
			t.Errorf("%q/%d parsed back as %q/%d ok=%v", c.prof, c.slot, prof, slot, ok)
		}
		id := RecoverySlotID(c.prof, c.slot)
		prof, slot, ok = ParseRecoverySlotID(id)
		if !ok || prof != c.prof || slot != c.slot {
			t.Errorf("slot id %q parsed back as %q/%d ok=%v", id, prof, slot, ok)
		}
	}
	for _, bad := range []string{VaultService("a"), "claudeswitch-recovery-", "claudeswitch-recovery-work-x",
		"claudeswitch-recovery-work-0", "claudeswitch-recovery-work-"} {
		if _, _, ok := ParseRecoveryService(bad); ok {
			t.Errorf("%q parsed as a recovery name", bad)
		}
	}
}

func TestRecoveryFilesLiveInADirOfTheirOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err := recoveryPath(RecoveryService("work", 2))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".claude", "claudeswitch", "recovery", "work-2.json")
	if p != want {
		t.Fatalf("recovery path %s, want %s", p, want)
	}
	if strings.HasSuffix(p, ".credentials.json") {
		t.Fatal("a recovery copy would be written over a live credential file")
	}
	for _, bad := range []string{"../x", "a/b", "", ".."} {
		if _, err := recoveryPath(RecoveryService(bad, 1)); bad != "" && err == nil {
			t.Errorf("profile name %q accepted into a path", bad)
		}
	}
	if _, err := recoveryPath(VaultService("a")); err == nil {
		t.Error("a vault name accepted as a recovery name")
	}
}
