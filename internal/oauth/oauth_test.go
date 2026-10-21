package oauth

import (
	"net/url"
	"strings"
	"testing"
)

// Claude Code asks for an organization with orgUUID, right after state: it
// is what its forceLoginOrgUUID setting sends (2.1.304's authorize-URL
// builder). GROUND_TRUTH §31 found organization_uuid ignored; that was
// never this parameter.
func TestAnAuthorizeURLAsksForTheOrganizationAsClaudeCodeDoes(t *testing.T) {
	f, err := BeginAuthWith("", Extra{Org: "org-x"})
	if err != nil {
		t.Fatal(err)
	}
	q := f.URL[strings.Index(f.URL, "?")+1:]
	parts := strings.Split(q, "&")
	if len(parts) != 9 || !strings.HasPrefix(parts[7], "state=") || parts[8] != "orgUUID=org-x" {
		t.Fatalf("query %q: want Claude Code's eight parameters, then orgUUID", q)
	}
	if strings.Contains(q, "organization_uuid") {
		t.Errorf("query %q sends the parameter §31 found ignored", q)
	}
}

func TestNoOrganizationAskedForSendsNone(t *testing.T) {
	f, err := BeginAuthWith("", Extra{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Has("orgUUID") {
		t.Errorf("%s asks for an organization nobody chose", f.URL)
	}
}

// The organization asked for is remembered with the attempt, so completing
// it can check what came back against it.
func TestThePendingLoginRemembersTheOrganizationAskedFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f, err := BeginAuthWith("", Extra{Org: "org-x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Save(Pending{AccountID: "n", Org: "org-x"}); err != nil {
		t.Fatal(err)
	}
	_, p, err := LoadPending()
	if err != nil {
		t.Fatal(err)
	}
	if p.Org != "org-x" {
		t.Errorf("pending org %q, want org-x", p.Org)
	}
}
