package main

import (
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

const (
	whoOrg   = "22222222-2222-2222-2222-222222222222"
	whoAlice = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	whoBob   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// Two people in one team organization are two quota pools. Matching the live
// credential on the organization alone named a colleague's account as the one
// signed in (2026-10-07: a new seat in a team org reported as "work-team").
func TestWhoamiDoesNotNameAnOrgMateAsTheLiveAccount(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{
		{ID: "work-team", AccountUUID: whoAlice, OrgID: whoOrg},
	}}
	got := whoamiVerdict(cfg, whoBob+"@"+whoOrg, whoOrg)
	if strings.Contains(got, `known to claudeswitch as "work-team"`) {
		t.Fatalf("named an org-mate as the live account:\n%s", got)
	}
	if !strings.Contains(got, "not in your config") {
		t.Errorf("should say this seat is not configured:\n%s", got)
	}
}

func TestWhoamiNamesTheAccountPinnedToTheSeat(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{
		{ID: "work-team", AccountUUID: whoAlice, OrgID: whoOrg},
		{ID: "work-two", AccountUUID: whoBob, OrgID: whoOrg},
	}}
	got := whoamiVerdict(cfg, whoBob+"@"+whoOrg, whoOrg)
	if !strings.Contains(got, `known to claudeswitch as "work-two"`) {
		t.Errorf("want work-two:\n%s", got)
	}
}

// An entry with only an organization cannot be confirmed or ruled out. Say so,
// and say how to settle it, rather than guessing either way.
func TestWhoamiCallsAnOrgOnlyMatchUnconfirmed(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{{ID: "old", OrgID: whoOrg}}}
	got := whoamiVerdict(cfg, whoBob+"@"+whoOrg, whoOrg)
	if strings.Contains(got, "known to claudeswitch as") {
		t.Errorf("an org-only entry must not be reported as a match:\n%s", got)
	}
	for _, want := range []string{`"old"`, "account_uuid", whoBob} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// When the identity lookup failed there is no seat, only the organization.
// Then the organization is all there is to go on, and it has to say so.
func TestWhoamiWithoutASeatSaysItCannotTellWhichPerson(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{
		{ID: "work-team", AccountUUID: whoAlice, OrgID: whoOrg},
	}}
	got := whoamiVerdict(cfg, "", whoOrg)
	if strings.Contains(got, "known to claudeswitch as") {
		t.Errorf("must not claim a match without a seat:\n%s", got)
	}
	if !strings.Contains(got, `"work-team"`) {
		t.Errorf("should name the candidate:\n%s", got)
	}
}

func TestWhoamiNewOrganization(t *testing.T) {
	got := whoamiVerdict(&config.Config{}, whoBob+"@"+whoOrg, whoOrg)
	if !strings.Contains(got, "claudeswitch add <name>") {
		t.Errorf("a new account should point to add:\n%s", got)
	}
}
