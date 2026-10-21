package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
)

const orgTOML = `priority = ["work-a"]

[[account]]
id           = "work-a"
account_uuid = "person-1"
org_id       = "org-1"
`

func directURL(t *testing.T, args ...string) (url.Values, map[string]any) {
	t.Helper()
	t.Cleanup(func() { _ = oauth.ClearPending() })
	out := captureStdout(t, func() error {
		return cmdLogin(append(args, "--direct", "--json", "--no-open"))
	})
	m := decodeJSON(t, []byte(out))
	raw, _ := m["url"].(string)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pend, _ := m["pending"].(map[string]any)
	return u.Query(), pend
}

// Signing an account in again asks for the organization the config pins it
// to, so a browser sitting in another organization does not decide.
func TestSigningAnAccountInAgainAsksForItsOrganization(t *testing.T) {
	path := appWorld(t, orgTOML)
	q, pend := directURL(t, "work-a", "--config", path)
	if q.Get("orgUUID") != "org-1" {
		t.Errorf("orgUUID = %q, want the account's org-1", q.Get("orgUUID"))
	}
	if pend["org"] != "org-1" {
		t.Errorf("pending %v, want org org-1", pend)
	}
}

// A new account asks for the organization named with --org: the same
// person in another organization is another quota pool, and the browser's
// current organization is often one already vaulted.
func TestANewAccountAsksForTheOrganizationNamed(t *testing.T) {
	path := appWorld(t, orgTOML)
	q, pend := directURL(t, "work-b", "--config", path, "--org", "org-2")
	if q.Get("orgUUID") != "org-2" || pend["org"] != "org-2" {
		t.Errorf("orgUUID %q, pending %v; want org-2", q.Get("orgUUID"), pend)
	}
	q, _ = directURL(t, "work-c", "--config", path)
	if q.Has("orgUUID") {
		t.Errorf("no --org, but orgUUID %q", q.Get("orgUUID"))
	}
}

// An account the config pins cannot be signed in to another organization:
// that would be another pool under its name.
func TestAnotherOrganizationForAPinnedAccountIsRefused(t *testing.T) {
	path := appWorld(t, orgTOML)
	t.Cleanup(func() { _ = oauth.ClearPending() })
	err := cmdLogin([]string{"work-a", "--config", path, "--org", "org-2", "--direct", "--json", "--no-open"})
	if err == nil || !strings.Contains(err.Error(), "org-1") {
		t.Fatalf("got %v, want a refusal naming the pinned org-1", err)
	}
	if _, _, perr := oauth.LoadPending(); perr == nil {
		t.Error("a refused login left an attempt pending")
	}
}

func TestAMalformedOrganizationIsRefused(t *testing.T) {
	path := appWorld(t, orgTOML)
	t.Cleanup(func() { _ = oauth.ClearPending() })
	err := cmdLogin([]string{"work-b", "--config", path, "--org", "org 2&x=y", "--direct", "--json", "--no-open"})
	if got := errCode(t, err); got != codeUsage {
		t.Errorf("code %q, want usage", got)
	}
}
