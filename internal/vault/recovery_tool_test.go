package vault

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// recoveryWorld is a vault with recovery items and a delete seam over the
// fake store.
func recoveryWorld(t *testing.T, entries map[string]*keychain.Blob, seatOf map[string]string) (*Vault, *fakeEndpoints) {
	t.Helper()
	v, ep := testVaultWith(t, entries, nil, seatOf)
	old := deleteRecovery
	t.Cleanup(func() { deleteRecovery = old })
	deleteRecovery = func(name string) error {
		if _, ok := ep.recovered[name]; !ok {
			return keychain.ErrNotFound
		}
		delete(ep.recovered, name)
		return nil
	}
	return v, ep
}

func kept(token string, at time.Time, seat string) *keychain.Blob {
	m := &keychain.Meta{AccountID: "recovery", VaultedAt: at.Format(time.RFC3339Nano)}
	if a, o, ok := cutSeat(seat); ok {
		m.AccountUUID, m.OrgID = a, o
	}
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: token, RefreshToken: "r-" + token,
		ExpiresAt: at.Add(8 * time.Hour).UnixMilli(), RefreshTokenExpiresAt: at.Add(30 * 24 * time.Hour).UnixMilli()},
		Meta: m}
}

// I1a: `cs recovery` lists every kept credential by slot and profile, with
// only what the item itself records — when, the seat if it was identified,
// expiries, whether it can be renewed. No network, no secrets.
func TestListRecoveryReadsOnlyWhatTheItemsRecord(t *testing.T) {
	_, ep := recoveryWorld(t, nil, nil)
	t1 := time.Now().Add(-3 * time.Hour)
	ep.recovered[RecoveryService("work", 2)] = kept("tok-work", t1, "u1@o1")
	ep.recovered[RecoveryService("default", 1)] = kept("tok-def", t1.Add(time.Hour), "")
	ep.recovered[RecoveryService("", 4)] = kept("tok-unnamed", t1, "")
	ep.recovered[RecoveryService("gone", 1)] = kept("tok-gone", t1, "") // profile no longer configured

	items := ListRecovery([]string{"default", "work"})
	got := map[string]RecoveryItem{}
	for _, it := range items {
		got[it.Slot] = it
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want work-2, default-1 and the unnamed 4", items)
	}
	w := got["work-2"]
	if w.Profile != "work" || w.N != 2 || w.Seat != "u1@o1" || w.KeptAt.Sub(t1).Abs() > time.Second {
		t.Errorf("work-2 = %+v", w)
	}
	if !w.HasRefresh || w.Expiry.IsZero() || w.RefreshExpiry.IsZero() {
		t.Errorf("work-2 expiries/refresh: %+v", w)
	}
	if u, ok := got["4"]; !ok || u.Profile != "" {
		t.Errorf("unnamed slot: %+v", got["4"])
	}
	if ep.profiles != 0 {
		t.Errorf("listing made %d identity lookups", ep.profiles)
	}
	// Profile names beyond the configured ones are listed when asked for.
	if n := len(ListRecovery([]string{"default", "work", "gone"})); n != 4 {
		t.Errorf("with gone named: %d items, want 4", n)
	}
}

// An item that cannot be read is listed as unreadable, not skipped: it may be
// the only copy of something.
func TestListRecoveryShowsUnreadableSlots(t *testing.T) {
	recoveryWorld(t, nil, nil)
	readRecovery = func(name string) (*keychain.Blob, error) {
		if name == RecoveryService("work", 3) {
			return nil, errors.New("keychain timed out")
		}
		return nil, keychain.ErrNotFound
	}
	items := ListRecovery([]string{"work"})
	if len(items) != 1 || items[0].Slot != "work-3" || items[0].Err == nil {
		t.Fatalf("items = %+v", items)
	}
}

// Restore files a kept credential under an account after checking it is
// that account's seat, then clears the slot.
func TestRestoreRecoveryVaultsTheRightSeatAndClearsTheSlot(t *testing.T) {
	entries := map[string]*keychain.Blob{}
	v, ep := recoveryWorld(t, entries, map[string]string{"tok-w1": "u1@o1"})
	name := RecoveryService("work", 1)
	ep.recovered[name] = kept("tok-w1", time.Now().Add(-time.Hour), "")

	e, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "u1@o1", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.AccountUUID != "u1" || e.OrgID != "o1" {
		t.Errorf("entry = %+v", e)
	}
	got := entries["w1"]
	if got == nil || got.ClaudeAIOAuth.AccessToken != "tok-w1" || got.Meta.Seat() != "u1@o1" ||
		got.Meta.AccountID != "w1" {
		t.Fatalf("vaulted = %+v", got)
	}
	if _, still := ep.recovered[name]; still {
		t.Error("the slot was not cleared")
	}
}

// A credential whose seat is not the account's is never filed under it.
func TestRestoreRecoveryRefusesAnotherSeat(t *testing.T) {
	entries := map[string]*keychain.Blob{}
	v, ep := recoveryWorld(t, entries, map[string]string{"tok-x": "stranger@o9"})
	name := RecoveryService("work", 1)
	ep.recovered[name] = kept("tok-x", time.Now(), "")

	_, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "u1@o1", false)
	if err == nil || !strings.Contains(err.Error(), "seat") {
		t.Fatalf("restored a stranger's credential: %v", err)
	}
	if entries["w1"] != nil {
		t.Error("vaulted anyway")
	}
	if _, still := ep.recovered[name]; !still {
		t.Error("the slot was cleared after a refusal")
	}
	if _, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "u1@o1", true); err == nil {
		t.Error("--force must not skip the seat check")
	}
}

// With no pin and no vaulted entry there is nothing to verify against.
func TestRestoreRecoveryRefusesAnAccountItCannotVerify(t *testing.T) {
	v, ep := recoveryWorld(t, map[string]*keychain.Blob{}, map[string]string{"tok-w1": "u1@o1"})
	ep.recovered[RecoveryService("work", 1)] = kept("tok-w1", time.Now(), "")
	if _, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "", false); err == nil {
		t.Fatal("restored under an account with no seat to compare against")
	}
}

// The re-add rule: a kept credential worse than the vaulted one does not
// replace it unless forced.
func TestRestoreRecoveryKeepsABetterVaultedCredentialUnlessForced(t *testing.T) {
	now := time.Now()
	entries := map[string]*keychain.Blob{
		"w1": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-vaulted", RefreshToken: "r-v",
			RefreshTokenExpiresAt: now.Add(60 * 24 * time.Hour).UnixMilli()},
			Meta: &keychain.Meta{AccountID: "w1", AccountUUID: "u1", OrgID: "o1", Email: "w1@example.com"}},
	}
	v, ep := recoveryWorld(t, entries, map[string]string{"tok-old": "u1@o1"})
	name := RecoveryService("work", 1)
	ep.recovered[name] = kept("tok-old", now.Add(-5*24*time.Hour), "u1@o1")

	if _, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "u1@o1", false); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("a worse credential replaced the vaulted one: %v", err)
	}
	if entries["w1"].ClaudeAIOAuth.AccessToken != "tok-vaulted" {
		t.Fatal("replaced")
	}
	if _, err := v.RestoreRecovery(context.Background(), "work-1", "w1", "u1@o1", true); err != nil {
		t.Fatalf("forced: %v", err)
	}
	if got := entries["w1"]; got.ClaudeAIOAuth.AccessToken != "tok-old" || got.Meta.Email != "w1@example.com" {
		t.Errorf("forced restore: %+v / %+v", got.ClaudeAIOAuth, got.Meta)
	}
	if _, still := ep.recovered[name]; still {
		t.Error("slot not cleared")
	}
}

// A malformed slot id is refused before anything is read.
func TestRestoreRecoveryRefusesABadSlot(t *testing.T) {
	v, _ := recoveryWorld(t, nil, nil)
	for _, bad := range []string{"", "work-", "work-x", "../x-1"} {
		if _, err := v.RestoreRecovery(context.Background(), bad, "w1", "u1@o1", false); err == nil {
			t.Errorf("slot %q accepted", bad)
		}
	}
}

func TestClearRecoveryDeletesOneSlot(t *testing.T) {
	_, ep := recoveryWorld(t, nil, nil)
	ep.recovered[RecoveryService("work", 1)] = kept("a", time.Now(), "")
	ep.recovered[RecoveryService("work", 2)] = kept("b", time.Now(), "")
	if err := ClearRecovery("work-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ep.recovered[RecoveryService("work", 1)]; ok {
		t.Error("work-1 still there")
	}
	if _, ok := ep.recovered[RecoveryService("work", 2)]; !ok {
		t.Error("work-2 went too")
	}
	if err := ClearRecovery("work-9"); err == nil {
		t.Error("clearing an empty slot reported success")
	}
}
