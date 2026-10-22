package cclogin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A .claude.json as Claude Code writes it: the recorded login among many
// other keys, two-space indented.
const sample = `{
  "numStartups": 292,
  "projects": {
    "/src/x": {
      "allowedTools": []
    }
  },
  "oauthAccount": {
    "accountUuid": "acct-old",
    "emailAddress": "old@example.com",
    "organizationUuid": "org-old",
    "organizationName": "Old Org",
    "organizationType": "claude_team",
    "organizationRole": "admin",
    "workspaceRole": null,
    "ccOnboardingFlags": {
      "x": true
    },
    "displayName": "Old",
    "profileFetchedAt": 1792616165073
  },
  "userID": "u-1"
}
`

func writeSample(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(p, []byte(sample), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReadNamesTheRecordedLogin(t *testing.T) {
	p := writeSample(t, 0o600)
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Login{AccountUUID: "acct-old", Email: "old@example.com", OrgUUID: "org-old", OrgName: "Old Org",
		FetchedAt: time.UnixMilli(1792616165073)}
	if got != want {
		t.Errorf("Read = %+v, want %+v", got, want)
	}
	if _, err := Read(filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, want ErrNotExist", err)
	}
}

// Claude Code owns the file and writes it all day: a write changes the one
// block and nothing else, replaces the file whole (never a half-written
// file for Claude Code to read), keeps its mode, and leaves no temp file.
func TestWriteReplacesOnlyTheBlock(t *testing.T) {
	p := writeSample(t, 0o600)
	before := readJSON(t, p)
	block := map[string]any{"accountUuid": "acct-new", "emailAddress": "new@example.com"}
	if err := Write(p, block); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, p)
	if !reflect.DeepEqual(after["oauthAccount"], block) {
		t.Errorf("oauthAccount = %v, want %v", after["oauthAccount"], block)
	}
	delete(before, "oauthAccount")
	delete(after, "oauthAccount")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("other keys changed:\nbefore %v\nafter  %v", before, after)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("left %d files beside it", len(entries)-1)
	}
	if err := Write(filepath.Join(t.TempDir(), "absent.json"), block); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("writing a file Claude Code never made: %v, want ErrNotExist (it is not created)", err)
	}
}

// A profile response as claude.ai's /api/oauth/profile answers, mapped to
// the names Claude Code keeps it under.
const profile = `{
  "account": {"uuid": "acct-new", "email": "new@example.com", "display_name": "New", "full_name": "New Person",
              "created_at": "2025-01-02T03:04:05Z"},
  "organization": {"uuid": "org-new", "name": "New Org", "organization_type": "claude_max",
                   "rate_limit_tier": "default_claude_max_20x", "seat_tier": "max", "billing_type": "stripe_subscription",
                   "has_extra_usage_enabled": true, "subscription_created_at": "2025-02-03T04:05:06Z",
                   "cc_onboarding_flags": {"y": true}, "claude_code_trial_ends_at": null,
                   "claude_code_trial_duration_days": null},
  "enabled_plugins": []
}`

func TestFromProfileWritesWhatClaudeCodeKeeps(t *testing.T) {
	now := time.UnixMilli(1800000000000)
	prev := map[string]any{"accountUuid": "acct-old", "organizationRole": "admin", "workspaceRole": nil,
		"ccOnboardingFlags": map[string]any{"x": true}, "organizationType": "claude_team"}
	got, err := FromProfile([]byte(profile), prev, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"accountUuid": "acct-new", "emailAddress": "new@example.com", "displayName": "New",
		"fullName": "New Person", "accountCreatedAt": "2025-01-02T03:04:05Z",
		"organizationUuid": "org-new", "organizationName": "New Org", "organizationType": "claude_max",
		"userRateLimitTier": "default_claude_max_20x", "seatTier": "max",
		"billingType": "stripe_subscription", "hasExtraUsageEnabled": true,
		"subscriptionCreatedAt": "2025-02-03T04:05:06Z",
		"ccOnboardingFlags":     map[string]any{"y": true},
		"claudeCodeTrialEndsAt": nil, "claudeCodeTrialDurationDays": nil,
		"profileFetchedAt": float64(1800000000000),
	}
	if !reflect.DeepEqual(roundTrip(t, got), want) {
		t.Errorf("block\n got %v\nwant %v\n(another account's roles and flags must not carry over)", roundTrip(t, got), want)
	}
}

// The same account signed in again keeps what the profile does not carry
// (its roles, its onboarding flags): they are still its own.
func TestFromProfileKeepsTheSameAccountsOwnFields(t *testing.T) {
	prev := map[string]any{"accountUuid": "acct-new", "organizationRole": "admin",
		"ccOnboardingFlags": map[string]any{"x": true}}
	got, err := FromProfile([]byte(profile), prev, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got["organizationRole"] != "admin" || got["ccOnboardingFlags"] == nil {
		t.Errorf("the same account lost its own fields: %v", got)
	}
}

func TestFromProfileRefusesAResponseWithNoAccount(t *testing.T) {
	if _, err := FromProfile([]byte(`{"organization": {"uuid": "o"}}`), nil, time.Now()); err == nil {
		t.Error("a profile with no account uuid was turned into a login")
	}
}

// When the profile cannot be read, what the vault knows is written: the
// identity, which is what the browser bridge compares. Another account's
// plan and roles are dropped rather than left describing it, and the block is
// marked partial (no fetch time) so a later sync completes it.
func TestIdentityOnlyIsPartial(t *testing.T) {
	prev := map[string]any{"accountUuid": "acct-old", "organizationType": "claude_team", "organizationRole": "admin",
		"profileFetchedAt": float64(1)}
	got := IdentityOnly(Login{AccountUUID: "acct-new", Email: "new@example.com", OrgUUID: "org-new", OrgName: "New Org"}, prev)
	want := map[string]any{"accountUuid": "acct-new", "emailAddress": "new@example.com",
		"organizationUuid": "org-new", "organizationName": "New Org"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("block %v, want %v", got, want)
	}
	if l := loginOf(got); !l.Partial() {
		t.Errorf("%+v is not partial", l)
	}
}

func roundTrip(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every byte outside the block is the file's own: key order, spacing and
// all, as Claude Code wrote it.
func TestWriteKeepsTheRestByteForByte(t *testing.T) {
	p := writeSample(t, 0o644)
	if err := Write(p, map[string]any{"accountUuid": "a"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	head, tail := `{
  "numStartups": 292,
  "projects": {
    "/src/x": {
      "allowedTools": []
    }
  },
  "oauthAccount": `, `,
  "userID": "u-1"
}
`
	want := head + "{\n    \"accountUuid\": \"a\"\n  }" + tail
	if string(b) != want {
		t.Errorf("file\n%s\nwant\n%s", b, want)
	}
}

// A file whose Claude Code has not signed in yet gains the block.
func TestWriteAddsAMissingBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(p, []byte("{\n  \"numStartups\": 1\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, map[string]any{"accountUuid": "a"}); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, p); got["numStartups"] != float64(1) || got["oauthAccount"] == nil {
		t.Errorf("file = %v", got)
	}
	if l, _ := Read(p); l.AccountUUID != "a" {
		t.Errorf("Read = %+v", l)
	}
}

// The real answer carries sections that are not objects (an array was seen
// on 2026-10-21): only account and organization are read, and nothing else
// in it stops them being read.
func TestFromProfileReadsPastOtherSections(t *testing.T) {
	raw := `{"account": {"uuid": "acct-new", "email": "new@example.com"}, "memberships": [{"x": 1}], ` +
		`"application": "Claude Code", "organization": {"uuid": "org-new", "name": "New Org"}}`
	got, err := FromProfile([]byte(raw), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got["accountUuid"] != "acct-new" || got["organizationName"] != "New Org" {
		t.Errorf("block %v", got)
	}
}

// Read against a real answer (2026-10-21): organization.rate_limit_tier is
// the user's tier, which Claude Code keeps as userRateLimitTier; its
// organizationRateLimitTier is not in the answer and belongs to the
// organization, so a seat in the same organization keeps it.
func TestTheOrganizationsOwnTierStaysWithinTheOrganization(t *testing.T) {
	raw := []byte(`{"account": {"uuid": "acct-2"}, "organization": {"uuid": "org-1", "rate_limit_tier": "default_claude_max_5x"}}`)
	prev := map[string]any{"accountUuid": "acct-1", "organizationUuid": "org-1",
		"organizationRateLimitTier": "default_raven", "organizationRole": "user"}
	got, err := FromProfile(raw, prev, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got["organizationRateLimitTier"] != "default_raven" || got["userRateLimitTier"] != "default_claude_max_5x" {
		t.Errorf("tiers: organization %v, user %v", got["organizationRateLimitTier"], got["userRateLimitTier"])
	}
	if _, ok := got["organizationRole"]; ok {
		t.Error("another person's role carried over within the organization")
	}
	prev["organizationUuid"] = "org-0"
	if got, _ := FromProfile(raw, prev, time.Now()); got["organizationRateLimitTier"] != nil {
		t.Error("another organization's tier carried over")
	}
}

// Claude Code leaves a null name out of the block, where it keeps null trial
// fields (the block it wrote, 2026-10-21): so does this.
func TestANullNameIsLeftOut(t *testing.T) {
	raw := []byte(`{"account": {"uuid": "a", "display_name": null, "full_name": null}, ` +
		`"organization": {"uuid": "o", "claude_code_trial_ends_at": null}}`)
	got, err := FromProfile(raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["displayName"]; ok {
		t.Errorf("displayName written as null: %v", got)
	}
	if _, ok := got["fullName"]; ok {
		t.Errorf("fullName written as null: %v", got)
	}
	if v, ok := got["claudeCodeTrialEndsAt"]; !ok || v != nil {
		t.Errorf("the trial field is Claude Code's null: %v", got)
	}
}
