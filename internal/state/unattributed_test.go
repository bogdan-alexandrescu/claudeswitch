package state

import "testing"

// The unattributed record holds a reading for a credential that is signed in
// but matches no configured account — that is what it is for, so "not in the
// config" is its normal condition and not a reason to delete it. Reconcile used
// to drop it on every CLI invocation, which defeated the report that tells you
// to pin the credential, and announced "discarded stale observation for active
// (not in config)" about something nobody had configured.
func TestReconcileKeepsTheUnattributedRecord(t *testing.T) {
	s := &State{Accounts: map[string]*Account{
		Unattributed: {ID: Unattributed, OrgID: "org-live"},
		"work-a":     {ID: "work-a", Seat: "seat-a@org-a"},
	}}
	dropped := s.Reconcile(map[string]string{"work-a": "seat-a@org-a"})

	if len(dropped) != 0 {
		t.Errorf("nothing should have been dropped, got %v", dropped)
	}
	if _, ok := s.Accounts[Unattributed]; !ok {
		t.Error("the unattributed record must survive: it is the report, not a stale account")
	}
}

// Exempting it must not exempt anything else: a record for an account that has
// genuinely left the config is still stale and still goes.
func TestReconcileStillDropsAGenuinelyUnknownAccount(t *testing.T) {
	s := &State{Accounts: map[string]*Account{
		Unattributed: {ID: Unattributed},
		"removed":    {ID: "removed", Seat: "seat-x@org-x"},
	}}
	dropped := s.Reconcile(map[string]string{})

	if len(dropped) != 1 {
		t.Fatalf("want exactly the removed account dropped, got %v", dropped)
	}
	if _, ok := s.Accounts["removed"]; ok {
		t.Error("an account no longer in the config must be dropped")
	}
	if _, ok := s.Accounts[Unattributed]; !ok {
		t.Error("the unattributed record must still survive")
	}
}

// A seat that disagrees with the config is the case this whole mechanism exists
// for: one credential filed under another account's name.
func TestReconcileStillDropsAMisfiledSeat(t *testing.T) {
	s := &State{Accounts: map[string]*Account{
		"work-a": {ID: "work-a", Seat: "someone-else@org-a"},
	}}
	if dropped := s.Reconcile(map[string]string{"work-a": "seat-a@org-a"}); len(dropped) != 1 {
		t.Errorf("a mis-filed seat must be dropped, got %v", dropped)
	}
}
