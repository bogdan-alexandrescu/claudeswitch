package usage

import "testing"

// A seat is person@organization. Truncating it to eight characters yields the
// person alone, so any message contrasting two seats named the half that
// matched and discarded the half that differed. Fixing that locally left three
// more copies of the bug in other packages, which is why the helper lives here
// now — in the package that defines what a seat is.
func TestShortSeatKeepsBothHalves(t *testing.T) {
	const person = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	here := ShortSeat(person + "@11111111-1111-1111-1111-111111111111")
	there := ShortSeat(person + "@22222222-2222-2222-2222-222222222222")

	if here != "aaaaaaaa@11111111" {
		t.Errorf("want aaaaaaaa@11111111, got %q", here)
	}
	// The property the whole thing turns on: one person in two organizations is
	// two quota pools, and they must not render identically.
	if here == there {
		t.Errorf("seats differing only by organization must differ: %q", here)
	}
}

// Two people in one organization are also two pools.
func TestShortSeatSeparatesPeopleWithinOneOrganization(t *testing.T) {
	const org = "@11111111-1111-1111-1111-111111111111"
	if ShortSeat("aaaaaaaa-aaaa"+org) == ShortSeat("bbbbbbbb-bbbb"+org) {
		t.Error("seats differing only by person must differ")
	}
}

// Not every value handed to this is a seat: a record predating seats carries a
// bare organization uuid, and it must still abbreviate rather than be mangled.
func TestShortSeatToleratesAPlainID(t *testing.T) {
	if got := ShortSeat("11111111-1111-1111-1111-111111111111"); got != "11111111" {
		t.Errorf("want 11111111, got %q", got)
	}
	if got := ShortSeat(""); got != "" {
		t.Errorf("empty in, empty out; got %q", got)
	}
}

// Profile.Seat() is one of the three producers of this format, so the pair must
// agree end to end.
func TestShortSeatMatchesWhatProfileProduces(t *testing.T) {
	var p Profile
	p.Account.UUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	p.Organization.UUID = "11111111-1111-1111-1111-111111111111"
	if got := ShortSeat(p.Seat()); got != "aaaaaaaa@11111111" {
		t.Errorf("want aaaaaaaa@11111111, got %q", got)
	}
}
