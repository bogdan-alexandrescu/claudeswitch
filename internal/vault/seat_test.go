package vault

import (
	"strings"
	"testing"
)

// The message exists to contrast two seats. Put through short(), a seat comes
// out as its account uuid alone — so whenever the difference was the
// organization, which is the ordinary case, it read "signed in as X but pinned
// to seat <the same person>" and named the one thing that matched.
func TestAWrongOrgErrorNamesTheOrganizationNotJustThePerson(t *testing.T) {
	const person = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	e := &WrongOrgError{
		AccountID: "work-team",
		WantOrg:   person + "@11111111-1111-1111-1111-111111111111",
		GotOrg:    person + "@22222222-2222-2222-2222-222222222222",
		GotEmail:  "someone@example.com in their own Organization",
	}
	msg := e.Error()
	if !strings.Contains(msg, "11111111") {
		t.Errorf("the organization it wants must appear:\n%s", msg)
	}
	if !strings.Contains(msg, "aaaaaaaa@11111111") {
		t.Errorf("both halves of the seat must appear:\n%s", msg)
	}
	// The remedy, not just the refusal: the organization cannot be requested,
	// so knowing to switch it in the browser is the whole fix.
	if !strings.Contains(msg, "--sso") || !strings.Contains(msg, "claude.ai") {
		t.Errorf("the message must say how to land on the right organization:\n%s", msg)
	}
}

func TestShortSeatKeepsBothHalves(t *testing.T) {
	got := shortSeat("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	if got != "aaaaaaaa@bbbbbbbb" {
		t.Errorf("want aaaaaaaa@bbbbbbbb, got %q", got)
	}
	// Two seats differing only in organization must render differently, which
	// is the property that was broken.
	a := shortSeat("same-person-uuid@org-one-uuid")
	b := shortSeat("same-person-uuid@org-two-uuid")
	if a == b {
		t.Errorf("seats differing by organization must render differently: %q", a)
	}
}

// Not every value is a seat; a bare uuid must still abbreviate sensibly.
func TestShortSeatToleratesAPlainID(t *testing.T) {
	if got := shortSeat("11111111-1111-1111-1111-111111111111"); got != "11111111" {
		t.Errorf("want 11111111, got %q", got)
	}
}
