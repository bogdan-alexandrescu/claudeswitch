package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/cclogin"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// claudeJSON is a profile's .claude.json recording a's login.
func claudeJSON(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".claude.json")
	body := `{
  "numStartups": 3,
  "oauthAccount": {
    "accountUuid": "ua",
    "emailAddress": "a@x",
    "organizationUuid": "oa",
    "organizationRole": "admin",
    "profileFetchedAt": 1
  }
}
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// After a swap Claude Code is "logged in as" the account it was swapped to:
// the profile's recorded login is rewritten from one profile read with the
// new token, so Claude in Chrome's bridge, which compares the two, connects
// (IMPROVEMENTS P1).
func TestASwapRecordsTheNewLoginForClaudeCode(t *testing.T) {
	v, _, _, it := swapWorld(t, "a-old", map[string]string{"b-token": "ub@org-b"})
	o := outgoingA()
	o.Identity = claudeJSON(t)
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatal(err)
	}
	l, err := cclogin.Read(o.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if l.AccountUUID != "ub" || l.OrgUUID != "org-b" || l.Partial() {
		t.Errorf("recorded login %+v, want ub in org-b, from a profile read", l)
	}
	b, _ := cclogin.Block(o.Identity)
	if _, ok := b["organizationRole"]; ok {
		t.Errorf("a's role carried over to b: %v", b)
	}
}

// A profile that cannot be read leaves the identity the vault recorded, so
// the bridge still matches, marked partial for the daemon to complete.
func TestASwapWhoseProfileCannotBeReadRecordsTheIdentity(t *testing.T) {
	v, _, entries, it := swapWorld(t, "a-old", nil)
	entries["b"].Meta = &keychain.Meta{AccountID: "b", AccountUUID: "ub", Email: "b@x", OrgID: "org-b", OrgName: "B Org"}
	o := outgoingA()
	o.Identity = claudeJSON(t)
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatal(err)
	}
	l, _ := cclogin.Read(o.Identity)
	if l.AccountUUID != "ub" || l.Email != "b@x" || l.OrgName != "B Org" || !l.Partial() {
		t.Errorf("recorded login %+v, want b's identity, partial", l)
	}
}

// A profile that never ran Claude Code has no .claude.json; the swap stands
// and none is made.
func TestASwapWithoutAClaudeJSONMakesNone(t *testing.T) {
	v, _, _, it := swapWorld(t, "a-old", map[string]string{"b-token": "ub@org-b"})
	o := outgoingA()
	o.Identity = filepath.Join(t.TempDir(), ".claude.json")
	if _, err := v.SwapToWith(context.Background(), it, "b", "org-b", o); err != nil {
		t.Fatalf("the swap failed over a missing .claude.json: %v", err)
	}
	if _, err := os.Stat(o.Identity); !os.IsNotExist(err) {
		t.Errorf("a .claude.json was made: %v", err)
	}
}

// SyncLogin completes a partial block later (the daemon's next poll, or
// `whoami --sync`): one profile read, the whole block.
func TestSyncLoginCompletesAPartialBlock(t *testing.T) {
	v, _, _, _ := swapWorld(t, "a-old", map[string]string{"b-token": "ub@org-b"})
	p := claudeJSON(t)
	if err := cclogin.Write(p, cclogin.IdentityOnly(cclogin.Login{AccountUUID: "ub", Email: "b@x"}, nil)); err != nil {
		t.Fatal(err)
	}
	full, err := v.SyncLogin(context.Background(), p, "b", "b-token", 0)
	if err != nil || !full {
		t.Fatalf("SyncLogin = %v, %v; want a full block", full, err)
	}
	if l, _ := cclogin.Read(p); l.Partial() || l.AccountUUID != "ub" || l.OrgUUID != "org-b" {
		t.Errorf("recorded login %+v, want complete", l)
	}
}
