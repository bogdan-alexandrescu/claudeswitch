package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

func liveIs(email, org, orgID string) func() (string, string, string) {
	return func() (string, string, string) { return email, org, orgID }
}

func noPrompt(t *testing.T) func(string, string) string {
	return func(q, def string) string {
		t.Fatalf("must not prompt: %q", q)
		return ""
	}
}

// claude-swap's flow is /login, then add — no name to invent first. With
// nobody at the terminal there is nobody to confirm a guess, so it asks for a
// name instead of filing the credential under one it made up.
func TestAddWithoutANameRefusesWhenNobodyCanConfirm(t *testing.T) {
	_, err := chooseAddName(nil, false, liveIs("a@example.com", "Acme", "org-1"), nil, noPrompt(t))
	if err == nil {
		t.Fatal("a non-interactive add with no name must be refused")
	}
	if !strings.Contains(err.Error(), "cs add <name>") {
		t.Errorf("the refusal must say how to give a name: %v", err)
	}
}

func TestAddWithoutANameSuggestsOneFromTheLiveAccount(t *testing.T) {
	var asked, offered string
	prompt := func(q, def string) string { asked, offered = q, def; return def }
	got, err := chooseAddName(nil, true, liveIs("jane@acme.io", "Acme Corp", "org-1"), []string{"personal"}, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if offered != "acme-corp" || got != "acme-corp" {
		t.Errorf("suggestion = %q, chosen = %q; want acme-corp from the organization name", offered, got)
	}
	if !strings.Contains(asked, "jane@acme.io") {
		t.Errorf("the prompt must say whose credential it is naming: %q", asked)
	}
}

func TestAddWithoutANameTakesWhatThePersonTypes(t *testing.T) {
	got, err := chooseAddName(nil, true, liveIs("jane@acme.io", "Acme", "org-1"), nil,
		func(string, string) string { return "  my-name  " })
	if err != nil || got != "my-name" {
		t.Errorf("got %q, %v; want my-name", got, err)
	}
}

func TestAddSuggestionAvoidsANameAlreadyTaken(t *testing.T) {
	var offered string
	_, _ = chooseAddName(nil, true, liveIs("jane@acme.io", "Acme", "org-1"), []string{"acme"},
		func(_, def string) string { offered = def; return def })
	if offered == "acme" {
		t.Error("the suggestion must not collide with an existing name")
	}
}

func TestAddWithoutANameNeedsToKnowWhoIsLive(t *testing.T) {
	_, err := chooseAddName(nil, true, liveIs("", "", ""), nil, noPrompt(t))
	if err == nil || !strings.Contains(err.Error(), "cs add <name>") {
		t.Errorf("an unknown live account must be refused with how to name it: %v", err)
	}
}

func TestAddWithANameUsesIt(t *testing.T) {
	got, err := chooseAddName([]string{"work-b"}, false, liveIs("", "", ""), nil, noPrompt(t))
	if err != nil || got != "work-b" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := chooseAddName([]string{"a", "b"}, true, liveIs("", "", ""), nil, noPrompt(t)); err == nil {
		t.Error("two names is a usage error")
	}
}

// Re-adding a name refreshes it, but only with the same seat: the vault entry
// itself says who it is, so an unpinned config is no excuse to file a different
// person under that name.
func TestReAddExpectsTheSeatAlreadyVaultedUnderThatName(t *testing.T) {
	if got := addExpectSeat("", "p@o"); got != "p@o" {
		t.Errorf("an unpinned config falls back to the vaulted seat, got %q", got)
	}
	if got := addExpectSeat("cfg@o", "p@o"); got != "cfg@o" {
		t.Errorf("the config pin wins, got %q", got)
	}
	if got := addExpectSeat("", ""); got != "" {
		t.Errorf("a first add expects nothing, got %q", got)
	}
}

func TestReAddSaysItRefreshedInPlace(t *testing.T) {
	if got := addHeadline("work-b", true); !strings.Contains(got, "refreshed work-b in place") {
		t.Errorf("got %q", got)
	}
	if got := addHeadline("work-b", false); strings.Contains(got, "refreshed") {
		t.Errorf("a first add is not a refresh: %q", got)
	}
}

func TestReAddOfADifferentSeatExplainsTheNameIsTaken(t *testing.T) {
	err := addError("work-b", &vault.WrongOrgError{AccountID: "work-b", WantOrg: "p1@o1",
		GotOrg: "p2@o2", GotEmail: "other@example.com in Other"})
	for _, want := range []string{`"work-b"`, "other@example.com", "Nothing was stored"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q:\n%v", want, err)
		}
	}
	var w *vault.WrongOrgError
	if !errors.As(err, &w) {
		t.Error("the typed error must stay reachable for callers")
	}
}
