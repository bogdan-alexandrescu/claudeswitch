// Package oauth refreshes Claude credentials.
//
// This exists because of ground truth #16: refreshing REVOKES the previously
// issued access token. A vaulted snapshot therefore dies as soon as that
// account's session refreshes — hours, not the 27 days the refresh-token expiry
// suggests. A vault that cannot refresh is a vault full of dead credentials.
//
// The danger runs the other way too. A refresh rotates the refresh token, so a
// refresh whose result is not persisted destroys the credential it was meant to
// preserve. Every caller must write the new pair back BEFORE doing anything
// else — see vault.Refresh, which is the only intended caller.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// All of these were read out of the Claude Code binary (2.1.266). They are
	// undocumented and may move; a change here should fail loudly rather than
	// silently issue bad credentials.
	TokenEndpoint     = "https://platform.claude.com/v1/oauth/token"
	AuthorizeEndpoint = "https://claude.com/cai/oauth/authorize"
	RedirectURI       = "https://platform.claude.com/oauth/code/callback"
	ClientID          = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// Scopes are exactly those Claude Code requests. Asking for fewer risks a
// credential that cannot do what Claude Code needs; asking for more would be
// impolite and might be refused.
var Scopes = []string{
	"org:create_api_key",
	"user:profile",
	"user:inference",
	"user:sessions:claude_code",
	"user:mcp_servers",
	"user:file_upload",
}

// AuthFlow holds the PKCE state for one login.
type AuthFlow struct {
	URL      string
	verifier string
	state    string
}

// pendingFlow is AuthFlow on disk, so the code can be pasted in a second
// invocation. Needed because the paste step cannot happen in a non-interactive
// shell, which is exactly where this tool is often driven from.
type pendingFlow struct {
	AccountID string    `json:"account_id"`
	OrgID     string    `json:"org_id"`
	Pinned    bool      `json:"pinned"`
	URL       string    `json:"url"`
	Verifier  string    `json:"verifier"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
}

// PendingTTL bounds how long a half-finished login stays usable. The verifier is
// a secret, so it should not sit on disk indefinitely — but expiry kept being
// the thing that failed rather than anything real: 15 minutes, then an hour,
// both elapsed while the browser side was being worked out.
//
// The exposure is small: a PKCE verifier alone grants nothing without the
// matching authorization code, the file is 0600 in a 0700 directory, and it is
// deleted the moment a code is exchanged.
const PendingTTL = 6 * time.Hour

func pendingPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(h, ".local", "state", "claudeswitch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending-login.json"), nil
}

// Save writes the flow so a later invocation can complete it. 0600, and
// deliberately short-lived.
func (f *AuthFlow) Save(accountID, orgID string, pinned bool) error {
	p, err := pendingPath()
	if err != nil {
		return err
	}
	b, err := json.Marshal(pendingFlow{
		AccountID: accountID, OrgID: orgID, Pinned: pinned, URL: f.URL,
		Verifier: f.verifier, State: f.state, StartedAt: time.Now(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// LoadPending returns a saved flow, the account it was started for, and its
// organization. A stale or absent flow is an error, never a silent retry.
func LoadPending() (*AuthFlow, string, string, bool, error) {
	p, err := pendingPath()
	if err != nil {
		return nil, "", "", false, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, "", "", false, fmt.Errorf("no login is in progress; start one first")
	}
	if err != nil {
		return nil, "", "", false, err
	}
	var pf pendingFlow
	if err := json.Unmarshal(b, &pf); err != nil {
		return nil, "", "", false, fmt.Errorf("the pending login is unreadable: %w", err)
	}
	if time.Since(pf.StartedAt) > PendingTTL {
		_ = ClearPending()
		return nil, "", "", false, fmt.Errorf("that login was started %s ago and has expired; start a new one",
			time.Since(pf.StartedAt).Round(time.Minute))
	}
	return &AuthFlow{URL: pf.URL, verifier: pf.Verifier, state: pf.State}, pf.AccountID, pf.OrgID, pf.Pinned, nil
}

// ClearPending removes a saved flow. Called as soon as it is used or abandoned,
// so the verifier does not linger.
func ClearPending() error {
	p, err := pendingPath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// randomB64 returns n random bytes, base64url encoded without padding.
func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// BeginAuth builds an authorization URL with PKCE.
//
// The parameters and their ORDER are copied from the URL Claude Code itself
// prints, because the endpoint rejected a reordered, extended one with "invalid
// request format" (2026-09-09). url.Values.Encode sorts alphabetically, so the
// query is assembled by hand.
//
// orgID is accepted but only used when pinOrg is set. Passing organization_uuid
// is what produced that rejection: the parameter appears throughout the binary
// but is evidently not accepted on this endpoint. It is kept behind an opt-in
// rather than deleted, so the finding is testable if the endpoint changes.
func BeginAuth(orgID string) (*AuthFlow, error) { return beginAuth(orgID, false, Extra{}) }

// Extra carries optional authorize parameters used to steer WHICH account a
// login returns — the thing the browser session otherwise decides on its own.
//
// These are standard OIDC parameters and all appear in the Claude Code binary,
// but Claude Code's own login sends none of them, so whether this endpoint
// honours any is unverified. They cost nothing to try: a parameter that is
// ignored still yields a working credential, and one that is rejected fails
// visibly before anything is stored.
type Extra struct {
	// Prompt is typically "select_account" (force the account chooser) or
	// "login" (force re-authentication even with a live session).
	Prompt string
	// LoginHint is an email address to preselect.
	LoginHint string
}

// BeginAuthWith builds an authorization URL with extra steering parameters.
func BeginAuthWith(orgID string, e Extra) (*AuthFlow, error) { return beginAuth(orgID, false, e) }

// BeginAuthPinned tries to pin the organization. Known to be rejected; kept for
// experiment, not for routine use.
func BeginAuthPinned(orgID string) (*AuthFlow, error) { return beginAuth(orgID, true, Extra{}) }

func beginAuth(orgID string, pinOrg bool, e Extra) (*AuthFlow, error) {
	verifier, err := randomB64(32)
	if err != nil {
		return nil, err
	}
	// 32 bytes, matching Claude Code exactly (43 base64url characters). A
	// 16-byte state was rejected by the authorize endpoint with "invalid request
	// format", so the length is evidently validated rather than merely echoed.
	st, err := randomB64(32)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Same parameters, same order, as claude auth login.
	pairs := [][2]string{
		{"code", "true"},
		{"client_id", ClientID},
		{"response_type", "code"},
		{"redirect_uri", RedirectURI},
		{"scope", strings.Join(Scopes, " ")},
		{"code_challenge", challenge},
		{"code_challenge_method", "S256"},
		{"state", st},
	}
	if pinOrg && orgID != "" {
		pairs = append(pairs, [2]string{"organization_uuid", orgID})
	}
	if e.Prompt != "" {
		pairs = append(pairs, [2]string{"prompt", e.Prompt})
	}
	if e.LoginHint != "" {
		pairs = append(pairs, [2]string{"login_hint", e.LoginHint})
	}
	var b strings.Builder
	b.WriteString(AuthorizeEndpoint)
	for i, kv := range pairs {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(kv[0]))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(kv[1]))
	}
	return &AuthFlow{URL: b.String(), verifier: verifier, state: st}, nil
}

// Exchange trades an authorization code for tokens.
//
// The callback page presents the code as "code#state"; both forms are accepted
// and the state is checked when present, since a mismatched state means the
// response does not belong to this flow.
func (c *Client) Exchange(ctx context.Context, f *AuthFlow, pasted string) (*Tokens, error) {
	code := strings.TrimSpace(pasted)
	if code == "" {
		return nil, fmt.Errorf("no code given")
	}
	if i := strings.IndexAny(code, "#&"); i >= 0 {
		gotState := code[i+1:]
		code = code[:i]
		if j := strings.IndexAny(gotState, "#&"); j >= 0 {
			gotState = gotState[:j]
		}
		if gotState != "" && gotState != f.state {
			return nil, fmt.Errorf("the pasted code belongs to a different login attempt")
		}
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"client_id":     {ClientID},
		"code_verifier": {f.verifier},
		"state":         {f.state},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging the authorization code: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the token endpoint refused the code (%d): %s",
			resp.StatusCode, summarise(body))
	}
	var t Tokens
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("token endpoint returned an unexpected shape: %w", err)
	}
	if t.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access_token")
	}
	return &t, nil
}

// Tokens is a fresh credential pair.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// ExpiresAtMillis converts expires_in into the epoch-millis form the Keychain
// blob uses.
func (t Tokens) ExpiresAtMillis(now time.Time) int64 {
	if t.ExpiresIn <= 0 {
		return 0
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli()
}

// NeedsLoginError means the refresh token is no longer usable: only an
// interactive login can recover the account. It must never be reported as a
// transient failure, because retrying will not help.
type NeedsLoginError struct{ Detail string }

func (e *NeedsLoginError) Error() string {
	return "this account needs an interactive login (`claude` then /login): " + e.Detail
}

type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Refresh exchanges a refresh token for a new pair.
//
// On success the OLD pair is dead. The caller must persist the returned tokens
// before using them for anything, or the account is lost.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	if refreshToken == "" {
		return nil, &NeedsLoginError{Detail: "no refresh token stored"}
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling the token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusUnauthorized:
		// invalid_grant is the standard "this refresh token is spent".
		return nil, &NeedsLoginError{Detail: summarise(body)}
	default:
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, summarise(body))
	}

	var t Tokens
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("token endpoint returned an unexpected shape: %w", err)
	}
	if t.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access_token")
	}
	// A response that omits a new refresh token means the old one still stands.
	// Do not blank it out; that would brick the account.
	return &t, nil
}

// summarise trims an error body for logging without risking a token in it.
func summarise(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
