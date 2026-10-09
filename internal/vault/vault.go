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
	"strings"
	"sync"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// readEntry reads an account's vault entry. A seam, so tests of the swap never
// reach the keychain.
var readEntry = func(accountID string) (*keychain.Blob, error) {
	return keychain.Read(keychain.VaultService(accountID))
}

// writeEntry stores an account's vault entry. A seam, like readEntry.
var writeEntry = func(accountID string, b *keychain.Blob) error {
	return keychain.Write(keychain.VaultService(accountID), b)
}

type Vault struct {
	log    *slog.Logger
	client *usage.Client
	oauth  *oauth.Client
	budget *usage.Budget

	// probes remembers the seat behind live tokens; see seatBehind.
	probeMu sync.Mutex
	probes  map[string]seatProbe
	// orgs maps a live token's key (usage.CredKey) to the organization the
	// daemon read behind it; see KnowOrg.
	orgs map[string]string
}

// KnowOrg records the organization behind the live token with this key
// (usage.CredKey), as the daemon read it. A token's organization never
// changes, so it settles HoldsAccount for any account of another
// organization without a call.
func (v *Vault) KnowOrg(key, org string) {
	if key == "" || org == "" {
		return
	}
	v.probeMu.Lock()
	defer v.probeMu.Unlock()
	if v.orgs == nil {
		v.orgs = map[string]string{}
	}
	v.orgs[key] = org
}

func (v *Vault) knownOrg(token string) string {
	v.probeMu.Lock()
	defer v.probeMu.Unlock()
	return v.orgs[usage.CredKey(token)]
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
func (v *Vault) fetch(ctx context.Context, token string, p usage.Priority) (*usage.Usage, error) {
	v.budget.Pace(ctx)
	if ok, reason := v.budget.Allow(token, p); !ok {
		if til, locked := v.budget.LockedUntil(token); locked {
			return nil, &usage.RateLimitedError{RetryAfter: time.Until(til), Local: true}
		}
		return nil, fmt.Errorf("usage API call budget exhausted (%s); try again shortly", reason)
	}
	u, err := v.client.Fetch(ctx, token)
	if rl, ok := usage.IsRateLimited(err); ok {
		// The budget applies the live cap itself when token was marked live
		// (MarkLive) by whoever read it from a profile's item.
		v.budget.Penalize(token, rl.RetryAfter)
	} else if err == nil {
		v.budget.Succeeded(token)
	}
	return u, err
}

// Entry is what the vault knows about a stored account.
type Entry struct {
	AccountID string
	// AccountUUID is the seat: the thing that actually owns a quota pool. It
	// was always read during Store and written into the keychain annotation,
	// but never handed back — so `add`, which had it in memory, told people to
	// pin their config on the organization alone.
	AccountUUID string
	// Email is the account's email as the profile endpoint reported it when
	// the entry was stored, so callers can record it in state.json and name
	// the account later without reading the keychain. "" when unknown.
	Email string
	// Plan is the subscription the profile endpoint named ("Max 20x"), so
	// callers can record it in state.json; "" when unknown.
	Plan          string
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
// identify reads the profile endpoint, which is not the usage endpoint and has
// its own limits — but it is paced and counted with everything else, since one
// budget for one program is the only kind that holds across processes.
func (v *Vault) identify(ctx context.Context, token string, p usage.Priority) (*usage.Profile, error) {
	v.budget.Pace(ctx)
	if ok, reason := v.budget.Allow(token, p); !ok {
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
	return v.StoreFrom(ctx, keychain.EnvLive(), accountID, expectSeat, conflictCheck)
}

// StoreFrom is Store reading one profile's live credential.
func (v *Vault) StoreFrom(ctx context.Context, item keychain.Live, accountID, expectSeat string, conflictCheck []string) (*Entry, error) {
	return v.StoreGuardedFrom(ctx, item, accountID, expectSeat, conflictCheck, nil)
}

// StoreGuarded is Store with a last check: once the live credential is known to
// be the right seat and no duplicate, guard sees it and the entry it would
// replace (nil when there is none), and an error from it stores nothing. It is
// how `add` refuses to overwrite a vaulted credential with a staler one.
func (v *Vault) StoreGuarded(ctx context.Context, accountID, expectSeat string, conflictCheck []string,
	guard func(live, vaulted *keychain.OAuth) error) (*Entry, error) {
	return v.StoreGuardedFrom(ctx, keychain.EnvLive(), accountID, expectSeat, conflictCheck, guard)
}

// StoreGuardedFrom is StoreGuarded reading one profile's live credential.
func (v *Vault) StoreGuardedFrom(ctx context.Context, item keychain.Live, accountID, expectSeat string, conflictCheck []string,
	guard func(live, vaulted *keychain.OAuth) error) (*Entry, error) {
	live, err := item.Read()
	if err != nil {
		return nil, err
	}
	// The usage read is a liveness check, not the thing being stored: the only
	// value taken from it is the organization id, which the profile carries
	// too. So it disqualifies a credential only when it says the credential is
	// bad. Being rate limited says close to the opposite — the token reached
	// the API and was recognised — and refusing on it threw away the
	// interactive login that produced the credential, at the exact moment more
	// capacity was being added because the existing accounts had none.
	u, uerr := v.fetch(ctx, live.ClaudeAIOAuth.AccessToken, usage.Interactive)
	if uerr != nil {
		if _, rateLimited := usage.IsRateLimited(uerr); !rateLimited {
			return nil, fmt.Errorf("the live credential could not read its own usage, so it is not worth vaulting: %w", uerr)
		}
		v.log.Warn("vaulting without a usage reading: the API is rate limiting these calls, "+
			"which says nothing about this credential",
			"account", accountID, "detail", uerr)
	}

	pr, perr := v.identify(ctx, live.ClaudeAIOAuth.AccessToken, usage.Interactive)
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
			return nil, &DuplicateSeatError{Other: other, Who: pr.Describe()}
		}
	}

	// Both sources agree where they overlap, and elsewhere in this file the
	// profile is already the one used. Preferring the usage header keeps the
	// stored value identical to what the poller will later write.
	orgID := pr.Organization.UUID
	if u != nil && u.OrgID != "" {
		orgID = u.OrgID
	}

	if guard != nil {
		var vaulted *keychain.OAuth
		if b, err := keychain.Read(keychain.VaultService(accountID)); err == nil {
			vaulted = b.ClaudeAIOAuth
		}
		if err := guard(live.ClaudeAIOAuth, vaulted); err != nil {
			return nil, err
		}
	}

	// Vault the account credential alone. mcpOAuth belongs to the machine, not
	// to any one account, and is never copied into a vault entry.
	entry := &keychain.Blob{
		ClaudeAIOAuth: live.ClaudeAIOAuth,
		Meta: &keychain.Meta{
			OrgID:       orgID,
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
		AccountUUID:    pr.Account.UUID,
		Email:          pr.Account.Email,
		Plan:           pr.Plan(),
		OrgID:          orgID,
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
	pr, err := v.identify(ctx, b.ClaudeAIOAuth.AccessToken, usage.Swap)
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
//
// Interactive, like FetchLive, because the only callers are `whoami`, `doctor`
// and `setup` — each one a command a person types once and waits on, and each
// one something they reach for precisely when the accounts are in trouble. A
// lockout armed by that same trouble used to refuse all three, which left the
// recovery tools broken exactly when they were needed. Neither is a loop, and
// `status` reaches for neither: it reads the state file.
func (v *Vault) Identify(ctx context.Context, token string) (*usage.Profile, error) {
	return v.identify(ctx, token, usage.Interactive)
}

// FetchLive reads usage for an arbitrary token through the shared budget. It is
// how callers ask "whose credential is this?" without going around the limit.
func (v *Vault) FetchLive(ctx context.Context, token string) (*usage.Usage, error) {
	return v.fetch(ctx, token, usage.Interactive)
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
	u, err := v.fetch(ctx, cred.AccessToken, usage.Interactive)
	if err != nil {
		return nil, fmt.Errorf("the new credential could not read its own usage, so it is not worth vaulting: %w", err)
	}
	pr, perr := v.identify(ctx, cred.AccessToken, usage.Interactive)
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
			return nil, &DuplicateSeatError{Other: other, Who: pr.Describe()}
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
		AccountUUID:    pr.Account.UUID,
		Email:          pr.Account.Email,
		Plan:           pr.Plan(),
		OrgID:          u.OrgID,
		Expiry:         cred.Expiry(),
		RefreshExpiry:  cred.RefreshExpiry(),
		NoRefreshToken: cred.RefreshToken == "",
	}, nil
}

// Load reads a vaulted account.
func (v *Vault) Load(accountID string) (*keychain.OAuth, error) {
	b, err := readEntry(accountID)
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
	return v.SwapToIn(ctx, keychain.EnvLive(), accountID, expectOrg)
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
// isActive tells us whether accountID is the credential Claude Code is using
// in this process's environment; RefreshIn names the profile instead.
func (v *Vault) Refresh(ctx context.Context, accountID, wantSeat string, isActive, allowActive bool) (*Entry, error) {
	var holder keychain.Live
	if isActive {
		holder = keychain.EnvLive()
	}
	return v.RefreshIn(ctx, accountID, wantSeat, holder, allowActive)
}

// RefreshIn is Refresh where holder is the live credential of the profile
// that has accountID live, or nil when no profile does. Step 3 writes the
// renewed token back into that item and no other: the profile whose session
// is using the old token is the one that must not be left holding it.
func (v *Vault) RefreshIn(ctx context.Context, accountID, wantSeat string, holder keychain.Live, allowActive bool) (*Entry, error) {
	isActive := holder != nil
	if isActive && !allowActive {
		return nil, ErrActiveAccount
	}
	blob, err := readEntry(accountID)
	if err != nil {
		return nil, fmt.Errorf("account %q is not in the vault (run `claudeswitch add %s`): %w",
			accountID, accountID, err)
	}
	cur := blob.ClaudeAIOAuth

	// Never refresh a credential that is not this account's. Refreshing revokes
	// the token it was given, so doing it through a mis-filed entry kills the
	// account that entry actually belongs to — and if two entries hold the same
	// credential, refreshing either one destroys both. That cascade took three
	// accounts out here before this check existed.
	if wantSeat != "" && blob.Meta != nil && blob.Meta.Seat() != "" && blob.Meta.Seat() != wantSeat {
		return nil, fmt.Errorf(
			"refusing to refresh %q: its stored credential belongs to seat %s, not the %s this "+
				"account is pinned to. Refreshing would revoke another account's token. "+
				"Re-add it with `claudeswitch add %s` while the right account is signed in",
			accountID, usage.ShortSeat(blob.Meta.Seat()), usage.ShortSeat(wantSeat), accountID)
	}
	if cur.RefreshDead() {
		return nil, &oauth.NeedsLoginError{Detail: "the stored refresh token expired on " +
			cur.RefreshExpiry().Format("2006-01-02")}
	}

	// A live account is refreshed as Claude Code refreshes it: holding its
	// credential locks from the token exchange to the write of the live item,
	// so Claude Code cannot exchange the same refresh token meanwhile
	// (GROUND_TRUTH §43). The verifying usage call comes after the release.
	release := func() {}
	if isActive {
		held, lerr := v.lock(ctx, holder, 0)
		if lerr != nil {
			return nil, fmt.Errorf("not refreshing %q now: %w", accountID, lerr)
		}
		release = func() { _ = held.Release() } // idempotent
		defer release()
		live, rerr := holder.Read()
		if rerr != nil {
			// Unknown is not "still the vaulted token" (D18): a refresh
			// revokes whatever is live there.
			return nil, fmt.Errorf("not refreshing %q: its live item could not be read to confirm it "+
				"still holds the vaulted token: %w", accountID, rerr)
		}
		if live.ClaudeAIOAuth.AccessToken != cur.AccessToken {
			return nil, fmt.Errorf("not refreshing %q: the live credential is no longer the vaulted one "+
				"(Claude Code has probably refreshed it already), and refreshing the vaulted copy would "+
				"revoke nothing useful while risking the live one", accountID)
		}
		// The exchange revokes the token the session holds, so step 3 must
		// succeed. If the store would refuse the write-back (the security -i
		// line limit), refuse now, while the live token still works.
		if err := checkRefreshWrite(holder, live, cur); err != nil {
			return nil, fmt.Errorf("not refreshing %q: the renewed credential could not be written back "+
				"into %s, and the refresh would revoke the token it holds: %w", accountID, holder.Name(), err)
		}
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
	if b, err := readEntry(accountID); err == nil && b.Meta != nil {
		cp := *b.Meta
		meta = &cp
	}
	meta.AccountID = accountID
	meta.VaultedAt = time.Now().Format(time.RFC3339)
	entry := &keychain.Blob{ClaudeAIOAuth: &next, Meta: meta}
	if err := writeEntry(accountID, entry); err != nil {
		return nil, fmt.Errorf(
			"CREDENTIAL AT RISK: refreshed %q but could not store the new token (%w). "+
				"The old token is now revoked; run `claude` and /login for this account", accountID, err)
	}

	// Step 3. Keep the live item in step, preserving mcpOAuth.
	if isActive {
		live, lerr := holder.Read()
		if lerr != nil {
			return nil, fmt.Errorf("refreshed and vaulted %q, but could not read the live item to update it: %w",
				accountID, lerr)
		}
		merged, merr := keychain.MergeForSwap(live, &next)
		if merr != nil {
			return nil, merr
		}
		if werr := holder.Write(merged); werr != nil {
			return nil, fmt.Errorf("refreshed and vaulted %q, but the live item still holds the revoked token (%w). "+
				"Run `claudeswitch use %s`", accountID, werr, accountID)
		}
	}

	// Step 4. Prove it works — over the network, so outside the lock.
	release()
	u, verr := v.fetch(ctx, next.AccessToken, usage.Swap)
	if verr != nil {
		if _, rl := usage.IsRateLimited(verr); rl {
			v.log.Warn("refreshed but could not verify: usage API rate limited", "account", accountID)
			return &Entry{AccountID: accountID, OrgID: meta.OrgID, Plan: meta.Plan, Expiry: next.Expiry(),
				RefreshExpiry: next.RefreshExpiry()}, nil
		}
		return nil, fmt.Errorf("refreshed %q and stored the new token, but it does not work: %w", accountID, verr)
	}
	v.log.Info("refreshed credential", "account", accountID, "org", u.OrgID,
		"expires", next.Expiry().Format(time.RFC3339))
	return &Entry{AccountID: accountID, OrgID: u.OrgID, Plan: meta.Plan, Expiry: next.Expiry(),
		RefreshExpiry: next.RefreshExpiry(), Tier: next.RateLimitTier,
		Subscription: next.SubscriptionType}, nil
}

// refreshGrowth is how much longer each renewed token may be than the one it
// replaces, for checking the write-back before the exchange: the new tokens
// are not known until then, and are the same format as the old.
const refreshGrowth = 32

// checkRefreshWrite asks holder's store whether the live write a refresh will
// make fits: the live item merged with the current credential, its tokens
// lengthened by refreshGrowth. A store that cannot check is not asked.
func checkRefreshWrite(holder keychain.Live, live *keychain.Blob, cur *keychain.OAuth) error {
	wc, ok := holder.(keychain.WriteChecker)
	if !ok {
		return nil
	}
	proj := *cur
	pad := strings.Repeat("x", refreshGrowth)
	proj.AccessToken += pad
	if proj.RefreshToken != "" {
		proj.RefreshToken += pad
	}
	merged, err := keychain.MergeForSwap(live, &proj)
	if err != nil {
		return err
	}
	return wc.CheckWrite(merged)
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
func (v *Vault) SyncActive(ctx context.Context, accountID, wantSeat string) (bool, error) {
	return v.SyncActiveIn(ctx, keychain.EnvLive(), accountID, wantSeat)
}

// SyncActiveIn is SyncActive against one profile's live credential: the
// profile whose live item holds accountID, so a token Claude Code refreshed
// there is the one re-captured.
func (v *Vault) SyncActiveIn(ctx context.Context, item keychain.Live, accountID, wantSeat string) (bool, error) {
	// Before touching the store at all: without a pinned seat there is no way to
	// tell this account's credential from a colleague's in the same
	// organization, and guessing is what corrupted two entries here.
	if wantSeat == "" {
		return false, fmt.Errorf(
			"refusing to re-capture %q: it has no `account_uuid` in the config, so there is "+
				"no way to tell whether the live credential is this account or a colleague's "+
				"in the same organization. Run `claudeswitch identify` and add it", accountID)
	}

	live, err := item.Read()
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

	// Verify the SEAT, not just the organization. An organization holds many
	// people and each has their own quota, so an org match alone says only
	// "someone at this company" — and on that evidence this function will
	// happily overwrite one colleague's vault entry with another's. It did
	// exactly that: two accounts in one organization ended up holding the same
	// credential and reporting identical utilization, which quietly turned a
	// rotation between them into a no-op.
	pr, err := v.identify(ctx, live.ClaudeAIOAuth.AccessToken, usage.Swap)
	if err != nil {
		return false, fmt.Errorf("cannot verify which account the live credential belongs to: %w", err)
	}
	if got := pr.Seat(); got != wantSeat {
		return false, &ForeignCredentialError{
			AccountID: accountID, WantOrg: wantSeat, GotOrg: got,
			GotEmail: pr.Describe(),
		}
	}

	entry := &keychain.Blob{
		ClaudeAIOAuth: live.ClaudeAIOAuth,
		Meta: &keychain.Meta{
			OrgID:       pr.Organization.UUID,
			AccountUUID: pr.Account.UUID,
			Email:       pr.Account.Email,
			Plan:        pr.Plan(),
			OrgName:     pr.Organization.Name,
			AccountID:   accountID,
			VaultedAt:   time.Now().Format(time.RFC3339),
		},
	}
	if err := writeEntry(accountID, entry); err != nil {
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
			"not overwriting it", who, e.AccountID, usage.ShortSeat(e.WantOrg))
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
	u, err := v.fetch(ctx, o.AccessToken, usage.Swap)
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
func (v *Vault) IsLive(accountID string) bool { return v.IsLiveIn(keychain.EnvLive(), accountID) }

// IsLiveIn reports whether a vault entry holds the credential one profile's
// live item holds, with IsLive's answer when the item cannot be read.
func (v *Vault) IsLiveIn(item keychain.Live, accountID string) bool {
	holds, known := v.LiveHolds(item, accountID)
	// Unable to tell — assume it IS live, because that is the answer that
	// makes the caller ask for confirmation rather than act.
	return holds || !known
}

// LiveHolds reports whether an item holds accountID's vaulted credential, and
// whether that is known. A missing item (ErrNotFound: the profile is not
// logged in) is known to hold nothing; an item that could not be read is
// unknown. Telling the two apart is what stops one logged-out profile making
// every account look live everywhere. Local only: no network.
func (v *Vault) LiveHolds(item keychain.Live, accountID string) (holds, known bool) {
	live, err := item.Read()
	if errors.Is(err, keychain.ErrNotFound) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	cur, err := v.Load(accountID)
	if err != nil {
		return false, true
	}
	return cur.AccessToken == live.ClaudeAIOAuth.AccessToken, true
}

// HoldsAccount is the check before installing accountID into another
// profile: does item hold that ACCOUNT, by any token? A person signing in by
// hand gets a token claudeswitch never vaulted, so the token comparison is
// only the cheap first step; after it the seat behind the live token is read
// from the profile endpoint and compared with wantSeat (or, when the config
// pins none, the seat the vault entry records).
//
// known is false whenever the answer could not be settled — an unreadable
// item, a failed identity lookup, no seat to compare against — and callers about
// to write must treat that as "may hold it".
func (v *Vault) HoldsAccount(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool) {
	holds, known, _ = v.HoldsAccountWhy(ctx, item, accountID, wantSeat)
	return holds, known
}

// HoldsAccountWhy is HoldsAccount, and when the answer is unknown because a
// rate-limit lock on the live token kept the identity lookup from being made
// (or a 429 just armed one), retryAt is when that lock clears (R1). It is zero
// for every other answer, an unknown of any other cause included.
func (v *Vault) HoldsAccountWhy(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool, retryAt time.Time) {
	live, err := item.Read()
	if errors.Is(err, keychain.ErrNotFound) {
		return false, true, time.Time{}
	}
	if err != nil || live.ClaudeAIOAuth == nil {
		return false, false, time.Time{}
	}
	entry, eerr := readEntry(accountID)
	if eerr == nil && entry.ClaudeAIOAuth != nil &&
		entry.ClaudeAIOAuth.AccessToken == live.ClaudeAIOAuth.AccessToken {
		return true, true, time.Time{}
	}
	if wantSeat == "" && eerr == nil && entry.Meta != nil {
		wantSeat = entry.Meta.Seat()
	}
	if wantSeat == "" {
		return false, false, time.Time{}
	}
	// A seat is person@organization: a token known to belong to another
	// organization cannot hold this account, and nothing needs asking. This
	// keeps a rate-limit lock on one profile's token from blocking every
	// swap in the others.
	if org := v.knownOrg(live.ClaudeAIOAuth.AccessToken); org != "" {
		if i := strings.LastIndex(wantSeat, "@"); i >= 0 && wantSeat[i+1:] != org {
			return false, true, time.Time{}
		}
	}
	p := v.probeSeat(ctx, live.ClaudeAIOAuth.AccessToken)
	if p.seat == "" {
		return false, false, p.retryAt
	}
	return p.seat == wantSeat, true, time.Time{}
}

// holdsProbeTTL is how long the seat behind a live token is remembered. A
// refusal that persists re-checks on every twenty-second tick, and asking the
// profile endpoint each time spent the whole budget in four minutes. The cache
// is keyed by the token itself, so a new login is a new question at once.
var holdsProbeTTL = 5 * time.Minute

// holdsUnknownTTL is how long a probe that could not say is remembered: one
// refused by the budget, cancelled, or failed. That doubt makes the daemon
// refuse a swap (D18), so it must clear soon; a minute still keeps a lasting
// refusal from spending a call every tick.
var holdsUnknownTTL = 60 * time.Second

// ttl is how long this probe's answer holds.
func (p seatProbe) ttl() time.Duration {
	if p.seat == "" {
		return min(holdsUnknownTTL, holdsProbeTTL)
	}
	return holdsProbeTTL
}

type seatProbe struct {
	seat string // "" when the probe could not say
	at   time.Time
	// retryAt, for a probe that could not say because a rate-limit lock on
	// the token refused it (or a 429 armed one), is when that lock clears.
	retryAt time.Time
}

// stale reports whether a cached probe no longer answers: its period is over,
// or the lock that kept it from asking has cleared.
func (p seatProbe) stale(now time.Time) bool {
	if now.Sub(p.at) >= p.ttl() {
		return true
	}
	return p.seat == "" && !p.retryAt.IsZero() && !now.Before(p.retryAt)
}

// seatBehind is the seat a live token belongs to, "" when unknown.
func (v *Vault) seatBehind(ctx context.Context, token string) string {
	return v.probeSeat(ctx, token).seat
}

// probeSeat asks the seat behind a live token, seat "" when unknown. It is
// the safety check a swap waits on, so it asks at Hot priority: it may spend
// the hot reserve, but never the calls reserved for the swap itself. At
// Scheduled priority it needed the whole hot reserve left as well, so while
// the daemon polled an account closely the check was refused, the answer
// stayed unknown, and every swap into another profile's account was refused
// with it (observed 2026-10-08). A refused or failed probe is remembered for
// a shorter period (holdsUnknownTTL) rather than retried every tick — or
// until the rate-limit lock that refused it clears, if that is sooner.
func (v *Vault) probeSeat(ctx context.Context, token string) seatProbe {
	v.probeMu.Lock()
	if p, ok := v.probes[token]; ok && !p.stale(time.Now()) {
		v.probeMu.Unlock()
		return p
	}
	v.probeMu.Unlock()

	var p seatProbe
	// Pacing blocks the caller for at most the burst spacing, and the cache
	// above bounds how often that can happen.
	v.budget.Pace(ctx)
	if ok, reason := v.budget.Allow(token, usage.Hot); ok {
		if pr, err := v.client.FetchProfile(ctx, token); err == nil {
			p.seat = pr.Seat()
			v.budget.Succeeded(token)
		} else if rl, isRL := usage.IsRateLimited(err); isRL {
			// Only ever asked about live tokens: the live cap applies.
			v.budget.PenalizeLive(token, rl.RetryAfter)
			p.retryAt, _ = v.budget.LockedUntil(token)
		}
	} else if reason == usage.ReasonLockout {
		p.retryAt, _ = v.budget.LockedUntil(token)
	}
	p.at = time.Now()

	v.probeMu.Lock()
	defer v.probeMu.Unlock()
	if v.probes == nil {
		v.probes = map[string]seatProbe{}
	}
	for k, old := range v.probes { // keep it small: old tokens are dead anyway
		if old.stale(p.at) {
			delete(v.probes, k)
		}
	}
	v.probes[token] = p
	return p
}

// DuplicateSeatError means the credential belongs to a seat already vaulted
// under another name. It is a type so the caller can say what to do next: only
// it knows whether the credential was whatever happened to be live (`add`) or
// one that was just signed in to (`login`).
type DuplicateSeatError struct {
	Other string // the name it is already vaulted as
	Who   string // the person and organization, as Describe gives them
}

func (e *DuplicateSeatError) Error() string {
	return fmt.Sprintf(
		"this is the same account AND organization already vaulted as %q — %s.\n"+
			"  Nothing was stored. A quota pool is one person in one organization, so to\n"+
			"  add a different pool you need a different person, a different organization,\n"+
			"  or both.",
		e.Other, e.Who)
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
			"  Nothing was stored. The same person in a different organization is a\n"+
			"  different quota pool, so this is not the one you asked for.\n"+
			"  The login lands on whichever organization the browser is in and cannot be\n"+
			"  asked for one: switch claude.ai to it, or use --sso if it is SSO-backed.",
		who, e.AccountID, usage.ShortSeat(e.WantOrg))
}
