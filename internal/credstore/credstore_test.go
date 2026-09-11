package credstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// MergeForSwap protects the user's MCP logins. If it ever drops mcpOAuth, every
// rotation signs them out of Notion and Slack.
func TestMergeForSwapPreservesMCPTokens(t *testing.T) {
	mcp := json.RawMessage(`{"notion|abc":{"accessToken":"n-tok"},"plugin:slack:slack|def":{"accessToken":"s-tok"}}`)
	live := &Blob{MCPOAuth: mcp, ClaudeAIOAuth: &OAuth{AccessToken: "old", RefreshToken: "old-r"}}
	incoming := &OAuth{AccessToken: "new", RefreshToken: "new-r", RateLimitTier: "tier"}

	got, err := MergeForSwap(live, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.MCPOAuth) != string(mcp) {
		t.Fatalf("mcpOAuth must survive byte-for-byte\n got: %s\nwant: %s", got.MCPOAuth, mcp)
	}
	if got.ClaudeAIOAuth.AccessToken != "new" || got.ClaudeAIOAuth.RefreshToken != "new-r" {
		t.Fatalf("the incoming credential must replace the old one: %+v", got.ClaudeAIOAuth)
	}
}

func TestMergeForSwapDoesNotAliasTheIncomingCredential(t *testing.T) {
	live := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "old"}}
	incoming := &OAuth{AccessToken: "new"}
	got, _ := MergeForSwap(live, incoming)
	incoming.AccessToken = "mutated"
	if got.ClaudeAIOAuth.AccessToken != "new" {
		t.Fatal("the merged blob must hold a copy, not a pointer into the vault's record")
	}
}

// Meta annotates a vault entry and must not reach the item Claude Code reads.
func TestMergeForSwapDropsOurAnnotation(t *testing.T) {
	live := &Blob{ClaudeAIOAuth: &OAuth{AccessToken: "old"}, Meta: &Meta{Email: "a@b"}}
	got, _ := MergeForSwap(live, &OAuth{AccessToken: "new"})
	if got.Meta != nil {
		t.Fatal("claudeswitchMeta must not be written into the live credential")
	}
}

func TestMergeForSwapRefusesMissingSides(t *testing.T) {
	if _, err := MergeForSwap(nil, &OAuth{}); err == nil {
		t.Error("merging into no live credential must fail")
	}
	if _, err := MergeForSwap(&Blob{}, nil); err == nil {
		t.Error("merging no incoming credential must fail")
	}
}

func TestAbsentMCPSectionIsOmittedNotNull(t *testing.T) {
	got, _ := MergeForSwap(&Blob{ClaudeAIOAuth: &OAuth{AccessToken: "old"}}, &OAuth{AccessToken: "new"})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"mcpOAuth":null`) {
		t.Fatalf("absent mcpOAuth must be omitted, not written as null: %s", b)
	}
}

// Identity is the pair. Neither half alone distinguishes a quota pool.
func TestSeatNeedsBothHalves(t *testing.T) {
	if got := (&Meta{AccountUUID: "a"}).Seat(); got != "" {
		t.Errorf("a seat without an organization is unknown, got %q", got)
	}
	if got := (&Meta{OrgID: "o"}).Seat(); got != "" {
		t.Errorf("an organization without a person is unknown, got %q", got)
	}
	if got := (&Meta{AccountUUID: "a", OrgID: "o"}).Seat(); got != "a@o" {
		t.Errorf("got %q", got)
	}
	var nilMeta *Meta
	if got := nilMeta.Seat(); got != "" {
		t.Errorf("a nil annotation is unknown, got %q", got)
	}
}

func TestRedactKeepsOnlyTheTail(t *testing.T) {
	if got := Redact("abcdefghijklmnop"); got != "…mnop" {
		t.Fatalf("got %q", got)
	}
	if got := Redact("ab"); got != "…" {
		t.Fatalf("short tokens must not leak: got %q", got)
	}
}

func TestParseRejectsACredentialWithNoClaudeSection(t *testing.T) {
	if _, err := parse("x", []byte(`{"mcpOAuth":{}}`)); err == nil {
		t.Fatal("a blob with no claudeAiOauth is not a usable credential")
	}
}

func TestIsVaultServiceDistinguishesOurEntries(t *testing.T) {
	if !IsVaultService(VaultService("work")) {
		t.Error("our own entries must be recognised")
	}
	if IsVaultService(LiveService) {
		t.Error("the live credential is not one of our vault entries")
	}
}
