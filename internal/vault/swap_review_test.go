package vault

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// Findings from the lane 6 security review.

// The live credential is already the target's: nothing is written. Writing
// the vaulted copy over it would install an older token than the one live.
func TestSwapToTheAccountAlreadyLiveWritesNothing(t *testing.T) {
	v, ep, entries, it := swapWorld(t, "b-token", nil)
	before := entries["b"]
	res, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if err != nil {
		t.Fatal(err)
	}
	if res.AccountID != "b" || res.RolledBack {
		t.Errorf("result %+v", res)
	}
	if len(it.writes) != 0 {
		t.Fatalf("wrote %v over the target's own credential", it.writes)
	}
	if ep.profiles != 0 || entries["b"] != before || len(ep.recovered) != 0 {
		t.Errorf("profiles %d, b rewritten %v, recovered %v; want nothing done",
			ep.profiles, entries["b"] != before, ep.recovered)
	}
}

// The target is live under a newer token than its vault entry (Claude Code
// refreshed it there): the newer one is captured into the target's entry, and
// the live item is left as it is.
func TestSwapToTheTargetLiveUnderANewerTokenCapturesItAndWritesNothing(t *testing.T) {
	v, _, entries, it := swapWorld(t, "b-newer", map[string]string{"b-newer": "ub@ob"})
	entries["b"].Meta = &keychain.Meta{AccountID: "b", AccountUUID: "ub", OrgID: "ob"}
	o := outgoingA()
	o.TargetSeat = "ub@ob"
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatal(err)
	}
	if len(it.writes) != 0 {
		t.Fatalf("wrote %v over the target's newer credential", it.writes)
	}
	if got := entries["b"].ClaudeAIOAuth.AccessToken; got != "b-newer" {
		t.Errorf("b's entry holds %q, want the newer live token captured", got)
	}
	if entries["a"].ClaudeAIOAuth.AccessToken != "a-old" {
		t.Error("the target's credential was filed under the outgoing account")
	}
}

// checkedItem refuses, as a keychain write would, any blob holding a token in
// tooBig.
type checkedItem struct {
	*recordingItem
	tooBig map[string]bool
}

func (c *checkedItem) CheckWrite(b *keychain.Blob) error {
	if c.tooBig[b.ClaudeAIOAuth.AccessToken] {
		return errors.New("over the security -i line limit")
	}
	return nil
}

// A swap whose rollback could not be written is not started: otherwise a
// failed verification would leave the profile on a credential nobody chose.
func TestASwapThatCouldNotBeRolledBackIsNotStarted(t *testing.T) {
	v, _, _, rec := swapWorld(t, "a-old", nil)
	it := &checkedItem{recordingItem: rec, tooBig: map[string]bool{"a-old": true}}
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err == nil {
		t.Fatal("swapped although the snapshot could not be restored")
	}
	if len(rec.writes) != 0 {
		t.Fatalf("wrote %v", rec.writes)
	}

	// And the forward write too, before the capture is spent on it.
	v, _, _, rec = swapWorld(t, "a-old", nil)
	it = &checkedItem{recordingItem: rec, tooBig: map[string]bool{"b-token": true}}
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err == nil {
		t.Fatal("swapped although the new credential could not be written")
	}
	if len(rec.writes) != 0 {
		t.Fatalf("wrote %v", rec.writes)
	}
}

// Every recovery slot unreadable (the keychain not answering): abort rather
// than overwrite a slot that may hold the only copy of something.
func TestRecoveryWithEverySlotUnreadableAborts(t *testing.T) {
	v, ep, _, it := swapWorld(t, "stranger", nil)
	readRecovery = func(string) (*keychain.Blob, error) {
		return nil, errors.New("keychain timed out")
	}
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA()); err == nil {
		t.Fatal("swapped without keeping the credential it overwrote")
	}
	if len(it.writes) != 0 || len(ep.recovered) != 0 {
		t.Fatalf("writes %v, recovered %v", it.writes, ep.recovered)
	}
}

// A live refresh whose holder cannot be read is refused (D18): unknown is
// not "still the vaulted token".
func TestRefreshInRefusesWhenTheLiveItemCannotBeRead(t *testing.T) {
	entries := map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "w1-token", RefreshToken: "r-w1",
			ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}},
	}
	v := testVault(t, entries, map[string]string{"renewed": "org-w"})
	work := item("work", "w1-token")
	work.readErr = errors.New("keychain timed out")
	if _, err := v.RefreshIn(context.Background(), "w1", "", work, true); err == nil {
		t.Fatal("refreshed a live account whose item could not be read")
	}
	if entries["w1"].ClaudeAIOAuth.AccessToken != "w1-token" {
		t.Fatal("the token was exchanged anyway")
	}
}
