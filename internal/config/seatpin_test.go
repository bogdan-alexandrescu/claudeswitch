package config

import "testing"

// Every integrity check in State.Reconcile is gated on a non-empty seat, and a
// seat needs BOTH fields. So an account configured with the organization alone
// is not merely pinned loosely — it is not checked at all, and a credential
// filed under the wrong name goes undetected. `add` used to print exactly that
// block as its suggested configuration.
func TestAnOrganizationAloneDoesNotIdentifyAnAccount(t *testing.T) {
	orgOnly := Account{ID: "work-a", OrgID: "org-1"}
	if orgOnly.Seat() != "" {
		t.Fatalf("precondition: an org-only account has no seat, got %q", orgOnly.Seat())
	}

	// Two colleagues in one organization: the case the seat exists to separate.
	a := Account{ID: "work-a", AccountUUID: "person-1", OrgID: "org-1"}
	b := Account{ID: "work-b", AccountUUID: "person-2", OrgID: "org-1"}
	if a.Seat() == b.Seat() {
		t.Errorf("two seats in one organization must differ: %q == %q", a.Seat(), b.Seat())
	}
	if a.Seat() == "" || b.Seat() == "" {
		t.Errorf("a fully pinned account must have a seat: %q / %q", a.Seat(), b.Seat())
	}
}

// One person can hold seats in several organizations, each its own quota pool.
func TestOnePersonInTwoOrganizationsIsTwoSeats(t *testing.T) {
	here := Account{ID: "work", AccountUUID: "person-1", OrgID: "org-1"}
	there := Account{ID: "own", AccountUUID: "person-1", OrgID: "org-2"}
	if here.Seat() == there.Seat() {
		t.Errorf("the same person in two organizations is two pools: %q", here.Seat())
	}
}
