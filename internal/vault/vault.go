// Package vault stores each account's credential and performs the swap.
//
// Two invariants, both from measured ground truth:
//
//  1. Only the claudeAiOauth subtree is ever vaulted or installed. The live
//     Keychain item also holds mcpOAuth — the user's Notion and Slack tokens —
//     and those must survive every rotation untouched.
//  2. A swap is never assumed to have worked. It is verified against the usage
//     API's anthropic-organization-id header, and any failure rolls the previous
//     credential back and confirms the rollback.
package vault

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

type Vault struct {
	log    *slog.Logger
	client *usage.Client
	oauth  *oauth.Client
	budget *usage.Budget
}

func New(log *slog.Logger) *Vault {
	return &Vault{log: log, client: usage.NewClient(), oauth: oauth.NewClient(),
		budget: usage.Shared()}
}

// SetBudgetAllowance applies a configured call allowance to the shared budget.
func (v *Vault) SetBudgetAllowance(n int) { v.budget.SetAllowance(n) }

// fetch is the only way this package talks to the usage API. Everything here is
// either user-initiated or swap-critical, so it spends the reserved slot —
// but it does spend from the shared budget, and it honours a 429.
func (v *Vault) fetch(ctx context.Context, token string) (*usage.Usage, error) {
	v.budget.Pace(ctx)
	if ok, reason := v.budget.Allow(true); !ok {
		if til, locked := v.budget.LockedUntil(); locked {
			return nil, &usage.RateLimitedError{RetryAfter: time.Until(til)}
		}
		return nil, fmt.Errorf("usage API call budget exhausted (%s); try again shortly", reason)
	}
	u, err := v.client.Fetch(ctx, token)
	if rl, ok := usage.IsRateLimited(err); ok {
		v.budget.Penalize(rl.RetryAfter)
	}
	return u, err
}

// Entry is what the vault knows about a stored account.
type Entry struct {
	AccountID     string
	OrgID         string
	Expiry        time.Time
	RefreshExpiry time.Time
	Tier          string
	Subscription  string

	// NoRefreshToken means this credential cannot be renewed. Observed risk for
	// SSO accounts, which may issue a short-lived access token alone. Such an
	// entry works until its access token expires and then needs an interactive
	// login — the vault cannot keep it alive, and the caller must say so plainly
	// rather than presenting it as a healthy rotation target.
	NoRefreshToken bool
}

// IdentityOf returns the seat a vault entry belongs to. Empty means the entry
// predates seat-aware identity and cannot be verified; callers must treat that
// as "unknown", never as "matches".
func (v *Vault) IdentityOf(accountID string) (seat, orgID string) {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil {
		return "", ""
	}
	return b.Meta.Seat(), b.Meta.OrgID
}

// DescribeOf renders a vault entry's owner for a human.
func (v *Vault) DescribeOf(accountID string) string {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil || b.Meta.Email == "" {
		return ""
	}
	return b.Meta.Email
}

// PlanOf is the recorded subscription for an entry, e.g. "Max 20x" or
// "Max 5x team". Empty when the entry predates plan recording.
func (v *Vault) PlanOf(accountID string) string {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil {
		return ""
	}
	return b.Meta.Plan
}

// identify reads the seat behind a token, through the shared call budget.
func (v *Vault) identify(ctx context.Context, token string) (*usage.Profile, error) {
	v.budget.Pace(ctx)
	if ok, reason := v.budget.Allow(true); !ok {
		return nil, fmt.Errorf("cannot identify this credential: %s", reason)
	}
	return v.client.FetchProfile(ctx, token)
}

// OrgOf returns the organization a vault entry belongs to, from its annotation.
// Empty means the entry predates the annotation or was written by hand.
func (v *Vault) OrgOf(accountID string) string {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil {
		return ""
	}
	return b.Meta.OrgID
}

// Store captures the credential that is live right now and files it under an
// account id. This is how an account is onboarded: the user logs in normally
// with `claude`, then names what they just logged into.
//
// It verifies the credential works before storing it, so a broken entry never
// enters the vault.
func (v *Vault) Store(ctx context.Context, accountID, expectSeat string, conflictCheck []string) (*Entry, error) {
	live, err := keychain.ReadLive()
	if err != nil {
		return nil, err
	}
	u, err := v.fetch(ctx, live.ClaudeAIOAuth.AccessToken)
	if err != nil {
		if _, ok := usage.IsRateLimited(err); ok {
			return nil, fmt.Errorf("%w — wait it out and try again; the credential was NOT vaulted", err)
		}
		return nil, fmt.Errorf("the live credential could not read its own usage, so it is not worth vaulting: %w", err)
	}

	pr, perr := v.identify(ctx, live.ClaudeAIOAuth.AccessToken)
	if perr != nil {
		return nil, fmt.Errorf("cannot identify whose credential this is: %w", perr)
	}
	// If the config pins this entry to a seat, the credential had better belong
	// to it. Without this, logging in as the wrong person and running `add`
	// files a stranger's credential under a familiar name.
	if expectSeat != "" && pr.Seat() != expectSeat {
		return nil, &WrongOrgError{AccountID: accountID, WantOrg: expectSeat,
			GotOrg: pr.Seat(), GotEmail: pr.Describe()}
	}

	// Refuse to file the same SEAT under two names. This used to compare
	// organizations, which wrongly refused two members of one team org — they
	// have entirely separate quota pools and are legitimately different
	// accounts (2026-09-10).
	for _, other := range conflictCheck {
		if other == accountID {
			continue
		}
		if seat, _ := v.IdentityOf(other); seat != "" && seat == pr.Seat() {
			return nil, fmt.Errorf(
				"this is the same account AND organization already vaulted as %q — %s.\n"+
					"  Nothing was stored. A quota pool is one person in one organization, so to\n"+
					"  add a different pool you need a different person, a different organization,\n"+
					"  or both.",
				other, pr.Describe())
		}
	}

	// Vault the account credential alone. mcpOAuth belongs to the machine, not
	// to any one account, and is never copied into a vault entry.
	entry := &keychain.Blob{
		ClaudeAIOAuth: live.ClaudeAIOAuth,
		Meta: &keychain.Meta{
			OrgID:       u.OrgID,
			AccountUUID: pr.Account.UUID,
			Email:       pr.Account.Email,
			Plan:        pr.Plan(),
			OrgName:     pr.Organization.Name,
			AccountID:   accountID,
			VaultedAt:   time.Now().Format(time.RFC3339),
		},
	}
	if err := keychain.Write(keychain.VaultService(accountID), entry); err != nil {
		return nil, err
	}
	o := live.ClaudeAIOAuth
	if o.RefreshToken == "" {
		v.log.Warn("vaulted a credential with no refresh token: it cannot be renewed",
			"account", accountID, "expires", o.Expiry().Format(time.RFC3339))
	}
	return &Entry{
		AccountID:      accountID,
		OrgID:          u.OrgID,
		Expiry:         o.Expiry(),
		RefreshExpiry:  o.RefreshExpiry(),
		Tier:           o.RateLimitTier,
		Subscription:   o.SubscriptionType,
		NoRefreshToken: o.RefreshToken == "",
	}, nil
}

// RecordIdentity asks a vaulted credential who it belongs to and writes the seat
// into its annotation, leaving the credential itself untouched.
func (v *Vault) RecordIdentity(ctx context.Context, accountID string) (*usage.Profile, error) {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil {
		return nil, err
	}
	pr, err := v.identify(ctx, b.ClaudeAIOAuth.AccessToken)
	if err != nil {
		return nil, err
	}
	meta := b.Meta
	if meta == nil {
		meta = &keychain.Meta{AccountID: accountID}
	}
	meta.AccountUUID = pr.Account.UUID
	meta.Email = pr.Account.Email
	meta.OrgID = pr.Organization.UUID
	meta.Plan = pr.Plan()
	meta.OrgName = pr.Organization.Name
	if err := keychain.Write(keychain.VaultService(accountID),
		&keychain.Blob{ClaudeAIOAuth: b.ClaudeAIOAuth, Meta: meta}); err != nil {
		return nil, err
	}
	v.log.Info("recorded seat identity", "account", accountID,
		"email", pr.Account.Email, "seat", pr.Account.UUID)
	return pr, nil
}

// Identify reads the seat behind a token: the account uuid and email, which is
// what actually owns a quota pool.
func (v *Vault) Identify(ctx context.Context, token string) (*usage.Profile, error) {
	return v.identify(ctx, token)
}

// FetchLive reads usage for an arbitrary token through the shared budget. It is
// how callers ask "whose credential is this?" without going around the limit.
func (v *Vault) FetchLive(ctx context.Context, token string) (*usage.Usage, error) {
	return v.fetch(ctx, token)
}

// StoreTokens vaults a credential obtained directly, without it ever having
// been installed as the live one.
//
// This is the better shape for onboarding an account: `claude auth login`
// replaces the live credential as a side effect, so vaulting a second account
// meant logging out of the first and swapping back. Obtaining the credential
// ourselves leaves the running session completely untouched.
//
// expectOrg is checked before anything is written. A credential for the wrong
// organization is discarded, not stored under the wrong name.
func (v *Vault) StoreTokens(ctx context.Context, accountID, expectSeat string, tok *oauth.Tokens,
	conflictCheck []string) (*Entry, error) {

	cred := &keychain.OAuth{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAtMillis(time.Now()),
		Scopes:       oauth.Scopes,
	}
	u, err := v.fetch(ctx, cred.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("the new credential could not read its own usage, so it is not worth vaulting: %w", err)
	}
	pr, perr := v.identify(ctx, cred.AccessToken)
	if perr != nil {
		return nil, fmt.Errorf("cannot identify whose credential this is: %w", perr)
	}
	if expectSeat != "" && pr.Seat() != expectSeat {
		return nil, &WrongOrgError{AccountID: accountID, WantOrg: expectSeat,
			GotOrg: pr.Seat(), GotEmail: pr.Describe()}
	}
	// The duplicate check stops the same seat being filed under two names, which
	// is the realistic mistake.
	for _, other := range conflictCheck {
		if other == accountID {
			continue
		}
		if seat, _ := v.IdentityOf(other); seat != "" && seat == pr.Seat() {
			return nil, fmt.Errorf(
				"that is the same account AND organization already vaulted as %q — %s.\n"+
					"  Nothing was stored. A quota pool is one person in one organization, so to add\n"+
					"  a different pool you need a different person, a different organization, or both.",
				other, pr.Describe())
		}
	}

	entry := &keychain.Blob{
		ClaudeAIOAuth: cred,
		Meta: &keychain.Meta{OrgID: u.OrgID, AccountUUID: pr.Account.UUID,
			Email: pr.Account.Email, Plan: pr.Plan(), OrgName: pr.Organization.Name,
			AccountID: accountID, VaultedAt: time.Now().Format(time.RFC3339)},
	}
	if err := keychain.Write(keychain.VaultService(accountID), entry); err != nil {
		return nil, err
	}
	v.log.Info("vaulted a credential obtained directly", "account", accountID,
		"email", pr.Account.Email, "seat", pr.Account.UUID, "org", u.OrgID)
	return &Entry{
		AccountID:      accountID,
		OrgID:          u.OrgID,
		Expiry:         cred.Expiry(),
		RefreshExpiry:  cred.RefreshExpiry(),
		NoRefreshToken: cred.RefreshToken == "",
	}, nil
}

// Load reads a vaulted account.
func (v *Vault) Load(accountID string) (*keychain.OAuth, error) {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil {
		return nil, fmt.Errorf("account %q is not in the vault (run `claudeswitch add %s`): %w",
			accountID, accountID, err)
	}
	return b.ClaudeAIOAuth, nil
}

// Has reports whether an account has a vaulted credential.
func (v *Vault) Has(accountID string) bool {
	_, err := keychain.Read(keychain.VaultService(accountID))
	return err == nil
}

// SwapResult describes what a swap actually did.
type SwapResult struct {
	AccountID  string
	OrgID      string
	Usage      *usage.Usage
	RolledBack bool
}

// SwapTo installs an account's credential as the live one.
//
// Sequence: snapshot, merge (keeping mcpOAuth), write, verify by org id, and on
// any failure restore the snapshot and confirm the restore. expectOrg may be
// empty on the first swap to an account, in which case whatever org answers is
// recorded rather than checked.
func (v *Vault) SwapTo(ctx context.Context, accountID, expectOrg string) (*SwapResult, error) {
	live, err := keychain.ReadLive()
	if err != nil {
		return nil, err
	}
	snapshot := *live // value copy; MCPOAuth is a slice we do not mutate

	incoming, err := v.Load(accountID)
	if err != nil {
		return nil, err
	}
	if incoming.RefreshDead() {
		return nil, fmt.Errorf("account %q has an expired refresh token and needs an interactive login", accountID)
	}

	merged, err := keychain.MergeForSwap(live, incoming)
	if err != nil {
		return nil, err
	}
	if err := keychain.Write(keychain.LiveService, merged); err != nil {
		return nil, fmt.Errorf("swap aborted before it took effect: %w", err)
	}

	u, verr := v.fetch(ctx, incoming.AccessToken)
	switch {
	case verr != nil:
		if _, ok := usage.IsRateLimited(verr); ok {
			// The credential is installed and may well be fine; we simply cannot
			// prove it right now. Say so rather than roll back a good swap.
			v.log.Warn("swap installed but could not be verified: usage API rate limited",
				"account", accountID)
			return &SwapResult{AccountID: accountID}, nil
		}
		return v.rollback(&snapshot, accountID, fmt.Errorf("the new credential could not read usage: %w", verr))
	case expectOrg != "" && u.OrgID != expectOrg:
		return v.rollback(&snapshot, accountID,
			fmt.Errorf("swap installed the wrong account: expected org %s, got %s", expectOrg, u.OrgID))
	}

	v.log.Info("swapped account", "account", accountID, "org", u.OrgID,
		"five_hour", u.FiveHour.Pct(), "seven_day", u.SevenDay.Pct(),
		"mcp_preserved", len(merged.MCPOAuth) > 0)
	return &SwapResult{AccountID: accountID, OrgID: u.OrgID, Usage: u}, nil
}

// rollback restores a snapshot and verifies the restore. A failure here is the
// worst outcome the program can produce, so it is reported in full.
func (v *Vault) rollback(snapshot *keychain.Blob, accountID string, cause error) (*SwapResult, error) {
	if err := keychain.Write(keychain.LiveService, snapshot); err != nil {
		return &SwapResult{AccountID: accountID, RolledBack: false}, fmt.Errorf(
			"CREDENTIAL LEFT IN A BAD STATE. The swap failed (%v) and the rollback also failed (%v). "+
				"Run `claude /login` to restore your session", cause, err)
	}
	back, err := keychain.ReadLive()
	if err != nil || back.ClaudeAIOAuth.AccessToken != snapshot.ClaudeAIOAuth.AccessToken {
		return &SwapResult{AccountID: accountID, RolledBack: false}, fmt.Errorf(
			"rollback wrote but did not verify after: %v. Run `claude /login` if Claude Code misbehaves", cause)
	}
	v.log.Warn("swap rolled back", "account", accountID, "cause", cause.Error())
	return &SwapResult{AccountID: accountID, RolledBack: true}, cause
}

// RefreshWindow is how close to expiry a token gets before we refresh it.
// Access tokens last about 8 hours; an hour of margin is plenty and keeps the
// number of refreshes (and therefore rotations) low.
const RefreshWindow = time.Hour

// ErrActiveAccount is returned when a refresh is attempted on the account that
// is currently live without explicit consent. Refreshing revokes the current
// access token, so getting this wrong logs the user out of their own session.
var ErrActiveAccount = errors.New(
	"refusing to refresh the account that is currently live: a refresh revokes the " +
		"token the running session is using. Pass allowActive only if you are certain")

// Refresh renews a vaulted account's credential and persists it.
//
// Ordering is the whole point, and it is not negotiable:
//
//  1. exchange the refresh token
//  2. write the new pair to the vault IMMEDIATELY — the old pair is already dead
//     by this point, so a crash here without a write loses the account
//  3. only then, if this account is also the live one, update the live item
//  4. verify
//
// isActive tells us whether accountID is the credential Claude Code is using.
func (v *Vault) Refresh(ctx context.Context, accountID string, isActive, allowActive bool) (*Entry, error) {
	if isActive && !allowActive {
		return nil, ErrActiveAccount
	}
	cur, err := v.Load(accountID)
	if err != nil {
		return nil, err
	}
	if cur.RefreshDead() {
		return nil, &oauth.NeedsLoginError{Detail: "the stored refresh token expired on " +
			cur.RefreshExpiry().Format("2006-01-02")}
	}

	tok, err := v.oauth.Refresh(ctx, cur.RefreshToken)
	if err != nil {
		return nil, err
	}

	// Step 2. Persist before anything can go wrong. The previous pair is
	// already revoked; if this write is skipped, the account is unrecoverable
	// without an interactive login.
	next := *cur
	next.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		next.RefreshToken = tok.RefreshToken
	}
	if ms := tok.ExpiresAtMillis(time.Now()); ms != 0 {
		next.ExpiresAt = ms
	}
	// Carry the existing annotation forward rather than rebuilding it. Writing a
	// fresh Meta here dropped the seat uuid, the email and the plan on every
	// automatic refresh — and losing the seat uuid disables the identity guards
	// that stop one account's credential being filed under another's name.
	meta := &keychain.Meta{AccountID: accountID}
	if b, err := keychain.Read(keychain.VaultService(accountID)); err == nil && b.Meta != nil {
		cp := *b.Meta
		meta = &cp
	}
	meta.AccountID = accountID
	meta.VaultedAt = time.Now().Format(time.RFC3339)
	entry := &keychain.Blob{ClaudeAIOAuth: &next, Meta: meta}
	if err := keychain.Write(keychain.VaultService(accountID), entry); err != nil {
		return nil, fmt.Errorf(
			"CREDENTIAL AT RISK: refreshed %q but could not store the new token (%w). "+
				"The old token is now revoked; run `claude` and /login for this account", accountID, err)
	}

	// Step 3. Keep the live item in step, preserving mcpOAuth.
	if isActive {
		live, lerr := keychain.ReadLive()
		if lerr != nil {
			return nil, fmt.Errorf("refreshed and vaulted %q, but could not read the live item to update it: %w",
				accountID, lerr)
		}
		merged, merr := keychain.MergeForSwap(live, &next)
		if merr != nil {
			return nil, merr
		}
		if werr := keychain.Write(keychain.LiveService, merged); werr != nil {
			return nil, fmt.Errorf("refreshed and vaulted %q, but the live item still holds the revoked token (%w). "+
				"Run `claudeswitch use %s`", accountID, werr, accountID)
		}
	}

	// Step 4. Prove it works.
	u, verr := v.fetch(ctx, next.AccessToken)
	if verr != nil {
		if _, rl := usage.IsRateLimited(verr); rl {
			v.log.Warn("refreshed but could not verify: usage API rate limited", "account", accountID)
			return &Entry{AccountID: accountID, OrgID: meta.OrgID, Expiry: next.Expiry(),
				RefreshExpiry: next.RefreshExpiry()}, nil
		}
		return nil, fmt.Errorf("refreshed %q and stored the new token, but it does not work: %w", accountID, verr)
	}
	v.log.Info("refreshed credential", "account", accountID, "org", u.OrgID,
		"expires", next.Expiry().Format(time.RFC3339))
	return &Entry{AccountID: accountID, OrgID: u.OrgID, Expiry: next.Expiry(),
		RefreshExpiry: next.RefreshExpiry(), Tier: next.RateLimitTier,
		Subscription: next.SubscriptionType}, nil
}

// NeedsRefresh reports whether a vaulted account is close enough to expiry to
// be worth renewing.
func (v *Vault) NeedsRefresh(accountID string, window time.Duration) bool {
	if window <= 0 {
		window = RefreshWindow
	}
	o, err := v.Load(accountID)
	if err != nil {
		return false
	}
	e := o.Expiry()
	return e.IsZero() || time.Until(e) < window
}

// VaultedAt is when an entry was last written, which is also when it was last
// refreshed. Zero when unknown.
func (v *Vault) VaultedAt(accountID string) time.Time {
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil || b.Meta.VaultedAt == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, b.Meta.VaultedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// SyncActive copies the live credential into the vault when the two have
// drifted apart — but only after proving they are the same Claude account.
//
// The naive version of this function destroyed a vaulted credential on
// 2026-09-09. It compared tokens, saw a difference, and assumed "Claude Code
// refreshed this account". In fact the user had just logged in to a DIFFERENT
// account, so it overwrote the vaulted personal credential with a work one.
// Two tokens differing means one of two very different things:
//
//   - the same account was refreshed  -> re-capture is correct and necessary
//   - a different account is now live -> re-capturing destroys the entry
//
// Nothing in the credential itself distinguishes them, so the organization is
// checked against the entry's recorded org before anything is written. That
// costs one API call, and only on the rare occasions the tokens differ.
func (v *Vault) SyncActive(ctx context.Context, accountID string) (bool, error) {
	live, err := keychain.ReadLive()
	if err != nil {
		return false, err
	}
	cur, err := v.Load(accountID)
	if err != nil {
		return false, err
	}
	if cur.AccessToken == live.ClaudeAIOAuth.AccessToken {
		return false, nil
	}

	wantOrg := v.OrgOf(accountID)
	if wantOrg == "" {
		// An entry with no recorded org cannot be verified, so it is not
		// re-captured. Better a stale entry than the wrong account's credential.
		return false, fmt.Errorf(
			"vault entry %q has no recorded organization, so a re-capture cannot be verified; "+
				"re-add it with `claudeswitch add %s` while that account is signed in", accountID, accountID)
	}

	u, err := v.fetch(ctx, live.ClaudeAIOAuth.AccessToken)
	if err != nil {
		return false, fmt.Errorf("cannot verify which account the live credential belongs to: %w", err)
	}
	if u.OrgID != wantOrg {
		return false, &ForeignCredentialError{
			AccountID: accountID, WantOrg: wantOrg, GotOrg: u.OrgID,
		}
	}

	entry := &keychain.Blob{
		ClaudeAIOAuth: live.ClaudeAIOAuth,
		Meta: &keychain.Meta{OrgID: wantOrg, AccountID: accountID,
			VaultedAt: time.Now().Format(time.RFC3339)},
	}
	if err := keychain.Write(keychain.VaultService(accountID), entry); err != nil {
		return false, err
	}
	v.log.Info("vault entry re-captured from the live credential (same account, token had been refreshed)",
		"account", accountID, "org", wantOrg)
	return true, nil
}

// ForeignCredentialError means the live credential belongs to a different
// account than the one being synced. It is not a failure — it is the normal
// consequence of signing in to another account — but it must never lead to a
// write.
type ForeignCredentialError struct {
	AccountID string
	// WantOrg and GotOrg carry seat uuids now, not organization uuids: the seat
	// is the quota pool, and two seats can share an organization.
	WantOrg   string
	GotOrg    string
	WantEmail string
	GotEmail  string
}

func (e *ForeignCredentialError) Error() string {
	who := e.GotOrg
	if e.GotEmail != "" {
		who = e.GotEmail + " (" + short(e.GotOrg) + ")"
	}
	return fmt.Sprintf(
		"the live credential belongs to %s, but vault entry %q is a different account (%s); "+
			"not overwriting it", who, e.AccountID, short(e.WantOrg))
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Verify reports whether a vault entry's stored credential actually belongs to
// the organization its metadata claims. An entry can be left inconsistent by an
// interrupted write or by an older, buggier version of this program, and such an
// entry must not be treated as a usable rotation target.
func (v *Vault) Verify(ctx context.Context, accountID string) error {
	o, err := v.Load(accountID)
	if err != nil {
		return err
	}
	claimed := v.OrgOf(accountID)
	u, err := v.fetch(ctx, o.AccessToken)
	if err != nil {
		return err
	}
	if claimed != "" && u.OrgID != claimed {
		return fmt.Errorf(
			"vault entry %q is inconsistent: its metadata says organization %s but its "+
				"credential belongs to %s. Re-add it with `claudeswitch add %s`",
			accountID, claimed, u.OrgID, accountID)
	}
	return nil
}

// IsLive reports whether a vault entry holds the credential Claude Code is
// currently using.
//
// This must never be answered from stored state. State can be empty, stale, or
// simply wrong, and the consequence of getting it wrong is refreshing the token
// the running session depends on. The live Keychain item is the only authority.
func (v *Vault) IsLive(accountID string) bool {
	live, err := keychain.ReadLive()
	if err != nil {
		// Unable to tell — assume it IS live, because that is the answer that
		// makes the caller ask for confirmation rather than act.
		return true
	}
	cur, err := v.Load(accountID)
	if err != nil {
		return false
	}
	return cur.AccessToken == live.ClaudeAIOAuth.AccessToken
}

// WrongOrgError means the live credential does not belong to the organization
// the config pins this account to: the wrong account is signed in.
type WrongOrgError struct {
	AccountID       string
	WantOrg, GotOrg string // seat uuids
	GotEmail        string
}

func (e *WrongOrgError) Error() string {
	who := short(e.GotOrg)
	if e.GotEmail != "" {
		who = e.GotEmail
	}
	return fmt.Sprintf(
		"the account currently signed in is %s, but %q is pinned to seat %s in your config.\n"+
			"  Nothing was stored — this is a different account than you asked to vault.\n"+
			"  Check with `claudeswitch whoami`, then try again.",
		who, e.AccountID, short(e.WantOrg))
}
