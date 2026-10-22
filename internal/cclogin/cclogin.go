// Package cclogin reads and writes the login Claude Code records for a
// profile: the oauthAccount block of its .claude.json (IMPROVEMENTS P1).
//
// A swap replaces the credential in the keychain but not this block, so
// after one Claude Code is "logged in as" an account it no longer holds a
// token for. Claude in Chrome's bridge compares the two when it connects,
// and refuses ("the OAuth token Claude Code is using belongs to a different
// claude.ai account than the one Claude Code is logged in as"). Keeping the
// block in step with each swap is what lets it connect.
//
// A running Claude Code keeps what is on disk in this block: a key added to
// it survived every write Claude Code made for eight minutes (tested
// 2026-10-21, IMPROVEMENTS).
package cclogin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const blockKey = "oauthAccount"

// Login is who a .claude.json says Claude Code is logged in as.
type Login struct {
	AccountUUID string
	Email       string
	OrgUUID     string
	OrgName     string
	// FetchedAt is when the block was last filled from a profile read; zero
	// when it was not (an identity-only write, or a block from elsewhere).
	FetchedAt time.Time
}

// Partial says the block holds the identity alone, written when the profile
// could not be read: a later sync should complete it.
func (l Login) Partial() bool { return l.AccountUUID != "" && l.FetchedAt.IsZero() }

// Read returns the login recorded in the .claude.json at path. A file with
// no block is the zero Login; a missing file is an error wrapping
// os.ErrNotExist.
func Read(path string) (Login, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Login{}, err
	}
	var doc struct {
		Block map[string]any `json:"oauthAccount"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return Login{}, fmt.Errorf("%s: %w", path, err)
	}
	return loginOf(doc.Block), nil
}

func loginOf(m map[string]any) Login {
	s := func(k string) string { v, _ := m[k].(string); return v }
	l := Login{AccountUUID: s("accountUuid"), Email: s("emailAddress"), OrgUUID: s("organizationUuid"),
		OrgName: s("organizationName")}
	if ms, ok := m["profileFetchedAt"].(float64); ok && ms > 0 {
		l.FetchedAt = time.UnixMilli(int64(ms))
	}
	return l
}

// Block returns the oauthAccount block recorded at path, nil when there is
// none.
func Block(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Block map[string]any `json:"oauthAccount"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc.Block, nil
}

// Write replaces the oauthAccount block in the .claude.json at path with
// block, leaving every other byte of the file as it was: Claude Code owns
// the file, and re-encoding it whole would reorder its keys. The file is
// read just before it is replaced, and replaced whole by a rename, so Claude
// Code never reads half of it. A file that does not exist is not created:
// that profile has never run Claude Code.
func Write(path string, block map[string]any) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	value, err := json.MarshalIndent(block, "  ", "  ")
	if err != nil {
		return err
	}
	out, err := replaceTopLevel(b, blockKey, value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.claudeswitch-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// replaceTopLevel returns doc with the value of its top-level key replaced
// by value, or the key added at the end when it is missing.
func replaceTopLevel(doc []byte, key string, value []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		afterKey := dec.InputOffset()
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if k != key {
			continue
		}
		end := int(dec.InputOffset())
		start := int(afterKey)
		// Past the colon and the whitespace around it, to the value itself.
		for start < end && (doc[start] == ':' || doc[start] == ' ' || doc[start] == '\t' ||
			doc[start] == '\n' || doc[start] == '\r') {
			start++
		}
		out := append([]byte{}, doc[:start]...)
		out = append(out, value...)
		return append(out, doc[end:]...), nil
	}
	// No block yet: add it before the closing brace.
	end := bytes.LastIndexByte(doc, '}')
	if end < 0 {
		return nil, errors.New("not a JSON object")
	}
	head := bytes.TrimRight(doc[:end], " \t\r\n")
	sep := ",\n  "
	if bytes.HasSuffix(head, []byte("{")) {
		sep = "\n  "
	}
	out := append([]byte{}, head...)
	out = append(out, sep...)
	out = append(out, fmt.Sprintf("%q: ", key)...)
	out = append(out, value...)
	out = append(out, '\n')
	return append(out, doc[end:]...), nil
}

// The profile's fields, by where claude.ai's /api/oauth/profile answer
// carries them, under the names Claude Code keeps them by in oauthAccount.
// Checked against a real answer and the block Claude Code wrote for another
// seat of the same organization (2026-10-21, GROUND_TRUTH §53): the
// organization's rate_limit_tier is the user's tier.
var fields = []struct{ key, section, name string }{
	{"accountUuid", "account", "uuid"},
	{"emailAddress", "account", "email"},
	{"displayName", "account", "display_name"},
	{"fullName", "account", "full_name"},
	{"accountCreatedAt", "account", "created_at"},
	{"organizationUuid", "organization", "uuid"},
	{"organizationName", "organization", "name"},
	{"organizationType", "organization", "organization_type"},
	{"userRateLimitTier", "organization", "rate_limit_tier"},
	{"seatTier", "organization", "seat_tier"},
	{"billingType", "organization", "billing_type"},
	{"hasExtraUsageEnabled", "organization", "has_extra_usage_enabled"},
	{"subscriptionCreatedAt", "organization", "subscription_created_at"},
	{"ccOnboardingFlags", "organization", "cc_onboarding_flags"},
	{"claudeCodeTrialEndsAt", "organization", "claude_code_trial_ends_at"},
	{"claudeCodeTrialDurationDays", "organization", "claude_code_trial_duration_days"},
}

// orgOwn are the fields Claude Code keeps that the answer does not carry and
// that belong to the organization, not the person: another seat in the same
// organization keeps them. The rest it does not carry (organizationRole,
// workspaceRole) are the person's, kept only for the same account.
var orgOwn = []string{"organizationRateLimitTier"}

// FromProfile builds the block for the account a profile answer (raw, as
// /api/oauth/profile sent it) describes. prev is the block it replaces.
// For the same account, what the answer does not carry is kept from prev;
// for another account it is left out, rather than left describing the one
// before.
func FromProfile(raw []byte, prev map[string]any, now time.Time) (map[string]any, error) {
	// Only account and organization are read: the answer carries other
	// sections, not all of them objects.
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, fmt.Errorf("profile answer: %w", err)
	}
	p := map[string]map[string]any{}
	for _, name := range []string{"account", "organization"} {
		var m map[string]any
		if err := json.Unmarshal(sections[name], &m); err == nil {
			p[name] = m
		}
	}
	uuid, _ := p["account"]["uuid"].(string)
	if uuid == "" {
		return nil, errors.New("profile answer carries no account uuid")
	}
	block := map[string]any{}
	org, _ := p["organization"]["uuid"].(string)
	switch {
	case prev["accountUuid"] == uuid:
		for k, v := range prev {
			block[k] = v
		}
	case org != "" && prev["organizationUuid"] == org:
		for _, k := range orgOwn {
			if v, ok := prev[k]; ok {
				block[k] = v
			}
		}
	}
	// What the answer carries is written as it carries it, null included:
	// Claude Code keeps the trial fields as null when there is no trial. A
	// null name it leaves out.
	for _, f := range fields {
		v, ok := p[f.section][f.name]
		switch {
		case !ok:
		case v == nil && (f.key == "displayName" || f.key == "fullName"):
			delete(block, f.key)
		default:
			block[f.key] = v
		}
	}
	block["profileFetchedAt"] = now.UnixMilli()
	return block, nil
}

// IdentityOnly builds the block from what the vault knows when the profile
// cannot be read. For another account than prev's, only the identity goes in
// and the block is partial (no fetch time), for a later sync to complete;
// for the same account, prev is kept.
func IdentityOnly(l Login, prev map[string]any) map[string]any {
	if same, _ := prev["accountUuid"].(string); same != "" && same == l.AccountUUID {
		return prev
	}
	block := map[string]any{"accountUuid": l.AccountUUID, "emailAddress": l.Email}
	if l.OrgUUID != "" {
		block["organizationUuid"] = l.OrgUUID
	}
	if l.OrgName != "" {
		block["organizationName"] = l.OrgName
	}
	return block
}
