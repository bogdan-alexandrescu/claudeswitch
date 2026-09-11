// Package credstore holds Claude credentials, wherever the platform keeps them.
//
// macOS uses a Keychain generic-password item; Linux uses a plain JSON file at
// ~/.claude/.credentials.json. The Linux side is the simpler of the two — no
// approval prompts, no separate tool to shell out to — but both must uphold the
// same invariants:
//
//   - the live item holds mcpOAuth (the user's MCP server logins) alongside the
//     Claude credential, and only claudeAiOauth is ever swapped
//   - every write is verified by reading it back
//   - secrets never reach argv, where any process could read them
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OAuth is the claudeAiOauth subtree: the part that identifies an account.
type OAuth struct {
	AccessToken           string   `json:"accessToken"`
	RefreshToken          string   `json:"refreshToken"`
	ExpiresAt             int64    `json:"expiresAt"`             // ms since epoch
	RefreshTokenExpiresAt int64    `json:"refreshTokenExpiresAt"` // ms since epoch
	Scopes                []string `json:"scopes,omitempty"`
	SubscriptionType      string   `json:"subscriptionType,omitempty"`
	RateLimitTier         string   `json:"rateLimitTier,omitempty"`
}

func msTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (o OAuth) Expiry() time.Time        { return msTime(o.ExpiresAt) }
func (o OAuth) RefreshExpiry() time.Time { return msTime(o.RefreshTokenExpiresAt) }

func (o OAuth) AccessExpired() bool {
	e := o.Expiry()
	return !e.IsZero() && time.Now().After(e)
}

// RefreshDead reports whether the refresh token has expired, meaning the account
// cannot be revived without an interactive login.
func (o OAuth) RefreshDead() bool {
	e := o.RefreshExpiry()
	return !e.IsZero() && time.Now().After(e)
}

// Meta is claudeswitch's own annotation on a vault entry. It never appears in
// the live item.
type Meta struct {
	OrgID string `json:"orgId,omitempty"`
	// AccountUUID is the person. It is NOT sufficient on its own: the same
	// person in two organizations has two separate quota pools. Identity is the
	// PAIR — use Seat().
	AccountUUID string `json:"accountUuid,omitempty"`
	Email       string `json:"email,omitempty"`
	Plan        string `json:"plan,omitempty"`
	OrgName     string `json:"orgName,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	VaultedAt   string `json:"vaultedAt,omitempty"`
}

// Seat is the identity of a quota pool: one person within one organization.
func (m *Meta) Seat() string {
	if m == nil || m.AccountUUID == "" || m.OrgID == "" {
		return ""
	}
	return m.AccountUUID + "@" + m.OrgID
}

// Blob is the full contents of a credential item. mcpOAuth is kept as raw JSON
// so it survives a round trip byte-for-byte.
type Blob struct {
	MCPOAuth      json.RawMessage `json:"mcpOAuth,omitempty"`
	ClaudeAIOAuth *OAuth          `json:"claudeAiOauth,omitempty"`
	Meta          *Meta           `json:"claudeswitchMeta,omitempty"`
}

// LiveService names the item Claude Code itself reads. On Linux this is not a
// service name but is kept as the sentinel for "the live credential".
const LiveService = "Claude Code-credentials"

// VaultService is the per-account item name in the vault.
func VaultService(accountID string) string { return "claudeswitch:" + accountID }

// IsVaultService reports whether a name belongs to our vault rather than to the
// live credential.
func IsVaultService(name string) bool { return strings.HasPrefix(name, "claudeswitch:") }

// Redact renders a token for logs: last 4 characters only.
func Redact(tok string) string {
	if len(tok) <= 4 {
		return "…"
	}
	return "…" + tok[len(tok)-4:]
}

// MergeForSwap builds the blob to install when switching to an account.
//
// This is the single most important function in the program. The live item holds
// the MCP servers' OAuth tokens alongside the Claude credential, so replacing it
// wholesale would sign the user out of every MCP server on every rotation. Only
// claudeAiOauth moves; mcpOAuth is carried over from whatever is live.
func MergeForSwap(live *Blob, incoming *OAuth) (*Blob, error) {
	if live == nil {
		return nil, fmt.Errorf("no live credential to merge into")
	}
	if incoming == nil {
		return nil, fmt.Errorf("no incoming credential")
	}
	cp := *incoming
	// Meta annotates a vault entry and has no business in the item Claude Code
	// reads.
	return &Blob{MCPOAuth: live.MCPOAuth, ClaudeAIOAuth: &cp}, nil
}

// parse turns stored bytes into a Blob, with the checks both platforms need.
func parse(name string, raw []byte) (*Blob, error) {
	var b Blob
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &b); err != nil {
		return nil, fmt.Errorf("credential %q is not the JSON shape we expect: %w", name, err)
	}
	if b.ClaudeAIOAuth == nil {
		return nil, fmt.Errorf("credential %q has no claudeAiOauth section", name)
	}
	return &b, nil
}

// verifyWrite re-reads a credential just written and checks it matches. A write
// that reports success but stored something else is the worst outcome this
// package can produce, so it is never assumed.
func verifyWrite(service string, want *Blob) error {
	got, err := Read(service)
	if err != nil {
		return fmt.Errorf("wrote credential %q but could not read it back: %w", service, err)
	}
	if got.ClaudeAIOAuth.AccessToken != want.ClaudeAIOAuth.AccessToken {
		return fmt.Errorf("credential %q read back different from what was written; not trusting it", service)
	}
	return nil
}

// ErrUnavailable marks a store that did not answer at all — a keychain read or
// write that ran out of time rather than returning something. It is worth its
// own type because the two cases call for opposite responses: a store that
// answered wrongly is a bug to fix, while one that is not answering is a
// machine to wait for, and only the caller knows which it can afford.
var ErrUnavailable = errors.New("the credential store is not answering")
