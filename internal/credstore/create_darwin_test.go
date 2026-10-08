//go:build darwin

package credstore

import (
	"errors"
	"strings"
	"testing"
)

// I7 seeds a new profile's live item so Claude Code starts signed in. The
// item is created, never updated: an item that already exists belongs to a
// profile someone has signed in, and overwriting it is a swap, which is
// `use`'s job and carries its own checks.

func stubLookup(t *testing.T, found bool, err error) *[]string {
	t.Helper()
	old := liveLookup
	t.Cleanup(func() { liveLookup = old })
	var asked []string
	liveLookup = func(svc string) (bool, error) {
		asked = append(asked, svc)
		return found, err
	}
	return &asked
}

func TestCreateLiveItemAddsWithoutUpdateOrAccessList(t *testing.T) {
	f := &fakeKeychain{}
	f.install(t)
	stubLookup(t, false, nil)
	svc := LiveServiceName("/home/someone/.claude-work")
	if err := CreateLiveItem(svc, "", vaultBlob()); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 1 {
		t.Fatalf("commands: %q", f.commands)
	}
	head := strings.Split(f.commands[0], " -w ")[0]
	if !strings.HasPrefix(head, "add-generic-password ") {
		t.Errorf("not an add: %s", head)
	}
	if strings.Contains(head, " -U") {
		t.Errorf("create-only, so no -U (which would update an existing item): %s", head)
	}
	if strings.Contains(head, " -T ") {
		t.Errorf("the live item's access list is Claude Code's, never broadened by us (§41): %s", head)
	}
	if !strings.Contains(head, `-s "`+svc+`"`) {
		t.Errorf("wrong item: %s", head)
	}
}

func TestCreateLiveItemRefusesAnExistingItem(t *testing.T) {
	f := &fakeKeychain{}
	f.install(t)
	stubLookup(t, true, nil)
	err := CreateLiveItem(LiveServiceName("/home/someone/.claude-work"), "", vaultBlob())
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if len(f.commands) != 0 {
		t.Fatalf("wrote anyway: %q", f.commands)
	}
}

// A lookup that did not answer is not "absent": creating then would be a
// blind write over whatever is there.
func TestCreateLiveItemRefusesWhenTheLookupFails(t *testing.T) {
	f := &fakeKeychain{}
	f.install(t)
	stubLookup(t, false, errors.New("keychain lookup timed out"))
	if err := CreateLiveItem(LiveServiceName("/home/someone/.claude-work"), "", vaultBlob()); err == nil {
		t.Fatal("a failed lookup must refuse")
	}
	if len(f.commands) != 0 {
		t.Fatalf("wrote anyway: %q", f.commands)
	}
}

func TestCreateLiveItemRefusesVaultAndPlainNames(t *testing.T) {
	f := &fakeKeychain{}
	f.install(t)
	stubLookup(t, false, nil)
	for _, svc := range []string{VaultService("someone"), liveServiceBase, "anything-else"} {
		if err := CreateLiveItem(svc, "", vaultBlob()); err == nil {
			t.Errorf("%q is not a profile's suffixed live item, and must be refused", svc)
		}
	}
	if len(f.commands) != 0 {
		t.Fatalf("wrote: %q", f.commands)
	}
}

func TestCreateLiveItemKeepsToTheLineLimit(t *testing.T) {
	f := &fakeKeychain{}
	f.install(t)
	stubLookup(t, false, nil)
	big := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: strings.Repeat("x", SecurityLineMax)}}
	err := CreateLiveItem(LiveServiceName("/home/someone/.claude-work"), "", big)
	if err == nil || !strings.Contains(err.Error(), "security -i") {
		t.Fatalf("want the line-limit refusal, got %v", err)
	}
	if len(f.commands) != 0 {
		t.Fatal("an oversized line must not be sent")
	}
}

func TestLiveExistsForReadsMetadataOnly(t *testing.T) {
	resetLiveCache()
	t.Cleanup(resetLiveCache)
	asked := stubLookup(t, false, nil)
	ok, err := LiveExistsFor("/home/someone/.claude-work")
	if err != nil || ok {
		t.Fatalf("absent: %v %v", ok, err)
	}
	if len(*asked) == 0 {
		t.Fatal("no lookup made")
	}
	stubLookup(t, false, errors.New("no answer"))
	if _, err := LiveExistsFor("/home/someone/.claude-work"); err == nil {
		t.Fatal("a failed lookup is not an answer")
	}
	stubLookup(t, true, nil)
	if ok, err := LiveExistsFor("/home/someone/.claude-work"); err != nil || !ok {
		t.Fatalf("present: %v %v", ok, err)
	}
}
