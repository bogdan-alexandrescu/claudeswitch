//go:build darwin

package credstore

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeKeychain replaces every seam that would reach security(1).
type fakeKeychain struct {
	exists   bool
	commands []string
	timeout  bool
	stored   bool // whether the content lands even when the command times out
	written  *Blob
}

func (f *fakeKeychain) install(t *testing.T) {
	t.Helper()
	oe, or, ob := itemExists, runSecurity, readBack
	t.Cleanup(func() { itemExists, runSecurity, readBack = oe, or, ob })
	itemExists = func(string) bool { return f.exists }
	runSecurity = func(stdin string) (bool, string, error) {
		f.commands = append(f.commands, stdin)
		if f.timeout {
			return true, "", errors.New("signal: killed")
		}
		return false, "", nil
	}
	readBack = func(service string, want *Blob) error {
		if f.timeout && !f.stored {
			return errors.New("not there")
		}
		return nil
	}
}

func vaultBlob() *Blob { return &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "new-token"}} }

// Changing an access list always asks for approval, so an update must not
// carry -T: after a rebuild every token refresh prompted, and an unanswered
// prompt wedged securityd for an hour (2026-09-16).
func TestUpdatingAVaultItemLeavesItsAccessListAlone(t *testing.T) {
	f := &fakeKeychain{exists: true}
	f.install(t)
	if err := Write(VaultService("work"), vaultBlob()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.commands[0], " -T ") {
		t.Errorf("update changes the access list: %s", strings.Split(f.commands[0], " -w ")[0])
	}
}

func TestCreatingAVaultItemTrustsThisBinary(t *testing.T) {
	f := &fakeKeychain{exists: false}
	f.install(t)
	if err := Write(VaultService("work"), vaultBlob()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.commands[0], " -T ") {
		t.Errorf("a new vault item should name this binary: %s", strings.Split(f.commands[0], " -w ")[0])
	}
}

func TestTheLiveItemNeverGetsOurAccessList(t *testing.T) {
	f := &fakeKeychain{exists: false}
	f.install(t)
	if err := Write(liveServiceBase, vaultBlob()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.commands[0], " -T ") {
		t.Error("the live item's access control is Claude Code's, not ours")
	}
}

// A write that timed out after the content landed is not a lost credential.
// Reporting it as one said "log in again" about two accounts that were fine.
func TestATimedOutWriteThatLandedIsNotAnError(t *testing.T) {
	f := &fakeKeychain{exists: true, timeout: true, stored: true}
	f.install(t)
	if err := Write(VaultService("work"), vaultBlob()); err != nil {
		t.Errorf("the token is stored, but Write reported: %v", err)
	}
}

func TestATimedOutWriteThatDidNotLandIsReported(t *testing.T) {
	f := &fakeKeychain{exists: true, timeout: true, stored: false}
	f.install(t)
	err := Write(VaultService("work"), vaultBlob())
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("want ErrUnavailable, got %v", err)
	}
}

func TestAWriteErrorNeverCarriesTheToken(t *testing.T) {
	f := &fakeKeychain{exists: true}
	f.install(t)
	runSecurity = func(string) (bool, string, error) { return false, "boom", errors.New("exit status 1") }
	if err := Write(VaultService("work"), vaultBlob()); err == nil || strings.Contains(err.Error(), "new-token") {
		t.Errorf("error = %v", err)
	}
}

// The metadata lookup must tell "not there" (exit 44) from "did not answer":
// treating a hung keychain as a missing item would report a live credential
// as absent.
func TestLookupClassifiesSecurityExitCodes(t *testing.T) {
	found, err := classifyLookup(nil, false)
	if !found || err != nil {
		t.Fatalf("exit 0 is found: %v %v", found, err)
	}
	notFound := exec.Command("sh", "-c", "exit 44").Run()
	if found, err := classifyLookup(notFound, false); found || err != nil {
		t.Fatalf("exit 44 is not found: %v %v", found, err)
	}
	other := exec.Command("sh", "-c", "exit 51").Run()
	if _, err := classifyLookup(other, false); err == nil {
		t.Fatal("any other failure is an error, not an answer")
	}
	if _, err := classifyLookup(errors.New("killed"), true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a timeout is ErrUnavailable: %v", err)
	}
}

// The lookup must never ask for the secret: -w is what prompts.
func TestLookupReadsMetadataOnly(t *testing.T) {
	for _, a := range lookupArgs("Claude Code-credentials-147868d1") {
		if a == "-w" || a == "-g" {
			t.Fatalf("lookup asks for the secret: %v", lookupArgs("x"))
		}
	}
}
