package state

import "testing"

// `add` will vault an account the config does not list. Those entries were
// invisible to the duplicate-credential checks, which enumerated the config —
// so a second name could be given to a pool that already had one, and the check
// meant to catch exactly that saw nothing. Both entries then report the same
// utilization, so rotating between them does nothing, and refreshing one
// revokes the other.
func TestKnownAccountsIncludesWhatWasVaultedOutsideTheConfig(t *testing.T) {
	s := &State{Vaulted: []string{"configured", "vaulted-only"}}
	got := s.KnownAccounts([]string{"configured", "other-configured"})

	want := map[string]bool{"configured": true, "other-configured": true, "vaulted-only": true}
	if len(got) != len(want) {
		t.Fatalf("want %d ids, got %v", len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected id %q in %v", id, got)
		}
	}
	// Configured accounts first, so messages name them in the order a person
	// sees them in their own file.
	if got[0] != "configured" || got[1] != "other-configured" {
		t.Errorf("configured ids must come first, got %v", got)
	}
}

func TestVaultedIsRecordedOnceAndForgottenOnRemoval(t *testing.T) {
	s := &State{}
	s.AddVaulted("work-a")
	s.AddVaulted("work-a")
	if len(s.Vaulted) != 1 {
		t.Errorf("adding twice must record once: %v", s.Vaulted)
	}
	s.AddVaulted("work-b")
	s.DropVaulted("work-a")
	if len(s.Vaulted) != 1 || s.Vaulted[0] != "work-b" {
		t.Errorf("want just work-b, got %v", s.Vaulted)
	}
}

// Reconcile prunes observations for accounts the config no longer lists. It
// must not prune this: "not in the config" is the case it exists to remember.
func TestReconcileDoesNotPruneTheVaultedList(t *testing.T) {
	s := &State{
		Accounts: map[string]*Account{"gone": {ID: "gone"}},
		Vaulted:  []string{"gone", "vaulted-only"},
	}
	s.Reconcile(map[string]string{})

	if len(s.Vaulted) != 2 {
		t.Errorf("the vaulted record must survive reconciliation: %v", s.Vaulted)
	}
	if _, ok := s.Accounts["gone"]; ok {
		t.Error("the observation itself should still have been dropped")
	}
}
