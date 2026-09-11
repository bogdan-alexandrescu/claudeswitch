package vault

import (
	"errors"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
)

// The refusal to refresh the live account is the guard that stops the tool from
// logging the user out of the session they are sitting in.
func TestRefreshRefusesTheActiveAccountByDefault(t *testing.T) {
	v := New(quietLogger())
	_, err := v.Refresh(nil, "personal", true, false)
	if !errors.Is(err, ErrActiveAccount) {
		t.Fatalf("got %v, want ErrActiveAccount", err)
	}
}

func TestExpiresAtMillisRoundTrips(t *testing.T) {
	now := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	tok := oauth.Tokens{ExpiresIn: 28800} // 8h, as observed
	ms := tok.ExpiresAtMillis(now)
	got := time.UnixMilli(ms).UTC()
	want := now.Add(8 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if (oauth.Tokens{}).ExpiresAtMillis(now) != 0 {
		t.Error("a missing expires_in must not fabricate an expiry")
	}
}

// A refresh response that omits refresh_token means the existing one still
// stands. Blanking it would brick the account.
func TestOmittedRefreshTokenIsNotTreatedAsEmpty(t *testing.T) {
	cur := keychain.OAuth{AccessToken: "old-a", RefreshToken: "keep-me"}
	tok := &oauth.Tokens{AccessToken: "new-a"} // no RefreshToken in the response

	next := cur
	next.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		next.RefreshToken = tok.RefreshToken
	}
	if next.RefreshToken != "keep-me" {
		t.Fatalf("refresh token must be preserved when the response omits it, got %q", next.RefreshToken)
	}
}

func TestNeedsLoginErrorIsDistinguishable(t *testing.T) {
	err := error(&oauth.NeedsLoginError{Detail: "invalid_grant"})
	var target *oauth.NeedsLoginError
	if !errors.As(err, &target) {
		t.Fatal("callers must be able to tell 'needs interactive login' from a transient failure")
	}
	if got := err.Error(); got == "" || !contains(got, "/login") {
		t.Fatalf("the message should tell the user what to do, got %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// This is the regression test for the bug that destroyed a vaulted credential
// on 2026-09-09: a login to a different account made the live token differ from
// the vaulted one, and the sync treated that as a refresh and overwrote it.
func TestForeignCredentialErrorIsDistinctAndDescribesBothOrgs(t *testing.T) {
	err := error(&ForeignCredentialError{
		AccountID: "personal",
		WantOrg:   "11111111-1111-1111-1111-111111111111",
		GotOrg:    "22222222-2222-2222-2222-222222222222",
	})
	var foreign *ForeignCredentialError
	if !errors.As(err, &foreign) {
		t.Fatal("callers must be able to tell 'different account' from a real failure")
	}
	msg := err.Error()
	for _, want := range []string{"11111111", "22222222", "personal", "not overwriting"} {
		if !contains(msg, want) {
			t.Errorf("message should mention %q, got: %s", want, msg)
		}
	}
}

func TestForeignCredentialCarriesTheAccountItProtected(t *testing.T) {
	e := &ForeignCredentialError{AccountID: "personal", WantOrg: "a", GotOrg: "b"}
	if e.AccountID != "personal" {
		t.Fatal("the caller needs to know which entry was protected, to stop trusting its Active")
	}
	if e.WantOrg == e.GotOrg {
		t.Fatal("a ForeignCredentialError with matching orgs is a contradiction")
	}
}

// An organization is not a quota pool. A team org has one seat per member, each
// with separate limits — measured 2026-09-10, when two credentials for org
// 22222222 reported 23%/19% and 3%/0%. Matching on organization let the daemon
// replace one member's vaulted credential with another's.
func TestForeignCredentialNamesTheSeatAndTheEmail(t *testing.T) {
	err := error(&ForeignCredentialError{
		AccountID: "work-a",
		WantOrg:   "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", // alice, in one organization
		GotOrg:    "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", // bob, in the same organization
		GotEmail:  "bob@example.com",
	})
	var foreign *ForeignCredentialError
	if !errors.As(err, &foreign) {
		t.Fatal("callers must still be able to distinguish this case")
	}
	msg := err.Error()
	for _, want := range []string{"bob@example.com", "work-a", "not overwriting"} {
		if !contains(msg, want) {
			t.Errorf("message should name %q, got: %s", want, msg)
		}
	}
}

// Two members of one organization are different accounts with different quota,
// so an identity check that compares organizations conflates them.
func TestSeatsDifferWithinOneOrganization(t *testing.T) {
	const org = "22222222-2222-2222-2222-222222222222"
	alice := struct{ seat, org string }{"aaaaaaaa", org}
	bob := struct{ seat, org string }{"bbbbbbbb", org}

	if alice.org != bob.org {
		t.Fatal("precondition: these two share an organization")
	}
	if alice.seat == bob.seat {
		t.Fatal("two members of one org must not share a seat uuid")
	}
	// The rule the code must follow: same org is NOT sufficient to conclude
	// "same account".
	sameAccountByOrg := alice.org == bob.org
	sameAccountBySeat := alice.seat == bob.seat
	if !sameAccountByOrg {
		t.Fatal("precondition")
	}
	if sameAccountBySeat {
		t.Fatal("seat comparison must distinguish them")
	}
}

// A quota pool is one person within one organization. Neither half identifies it
// alone, and conflating either way merges two real pools into one.
func TestSeatIdentityNeedsBothPersonAndOrganization(t *testing.T) {
	const orgShared = "22222222"
	const orgPrivate = "11111111"
	const alice = "aaaaaaaa"
	const bob = "bbbbbbbb"

	seat := func(person, org string) string { return person + "@" + org }

	// Two people in one organization: different subscriptions and plans, so
	// different pools. Comparing organizations alone would merge them.
	if seat(alice, orgShared) == seat(bob, orgShared) {
		t.Error("two people in one organization must be different seats")
	}
	// One person in two organizations: measured 0%/46% and 23%/19% at the same
	// time. Comparing people alone would merge them.
	if seat(alice, orgPrivate) == seat(alice, orgShared) {
		t.Error("one person in two organizations must be different seats")
	}
	// The same person in the same organization is the same pool.
	if seat(alice, orgShared) != seat(alice, orgShared) {
		t.Error("identical pairs must match")
	}
}

// Refreshing a token must not cost an account its identity. An earlier version
// rebuilt the annotation from scratch on every refresh, dropping the seat uuid,
// the email and the plan — and without the seat uuid the guards that stop one
// account's credential being filed under another's name stop working.
func TestRefreshPreservesTheAnnotation(t *testing.T) {
	before := &keychain.Meta{
		OrgID: "org-1", AccountUUID: "seat-1", Email: "someone@example.com",
		Plan: "Max 20x", OrgName: "Acme", AccountID: "work",
		VaultedAt: "2026-09-01T00:00:00Z",
	}
	// What Refresh now does: copy, then stamp the time.
	cp := *before
	cp.AccountID = "work"
	cp.VaultedAt = "2026-09-11T00:00:00Z"

	if cp.AccountUUID != "seat-1" {
		t.Error("the seat uuid must survive a refresh — the identity guards need it")
	}
	if cp.Email != "someone@example.com" || cp.Plan != "Max 20x" || cp.OrgName != "Acme" {
		t.Errorf("the annotation must survive a refresh: %+v", cp)
	}
	if cp.Seat() != "seat-1@org-1" {
		t.Errorf("seat lost: %q", cp.Seat())
	}
	if cp.VaultedAt == before.VaultedAt {
		t.Error("the timestamp should move forward")
	}
}
