package vault

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/cclock"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// ErrLockBusy means Claude Code held its credential lock past the wait:
// nothing was written, and the swap can simply be tried again.
var ErrLockBusy = cclock.ErrBusy

// MaxRecoveryCopies bounds the recovery items kept per profile.
const MaxRecoveryCopies = 5

// Seams for the recovery items, like readEntry and writeEntry.
var (
	readRecovery  = keychain.Read
	writeRecovery = keychain.Write
	// acquireLock takes Claude Code's credential locks for a dir.
	acquireLock = cclock.Acquire
)

// SwapOptions tells a swap about the profile it writes.
type SwapOptions struct {
	// Profile names the profile, for the recovery item and the logs.
	Profile string
	// Outgoing is the account recorded live in the profile, "" if none, and
	// OutgoingSeat its configured seat. The live credential is compared with
	// Outgoing's vault entry before it is overwritten.
	Outgoing     string
	OutgoingSeat string
	// SeatOwner attributes a live credential to a configured account by its
	// seat, "" when no account owns it. Nil attributes to Outgoing alone.
	SeatOwner func(seat string) string
	// TargetSeat is the incoming account's configured seat: a live credential
	// with it is the target's already, under a newer token.
	TargetSeat string
	// LockWait bounds the wait for Claude Code's lock; 0 is cclock.DefaultWait.
	LockWait time.Duration
}

// SwapToIn is SwapTo against one profile's live credential, knowing nothing
// about what it overwrites: an unattributable live credential is kept as a
// recovery copy. Callers that know the profile use SwapToWith.
func (v *Vault) SwapToIn(ctx context.Context, item keychain.Live, accountID, expectOrg string) (*SwapResult, error) {
	return v.SwapToWith(ctx, item, accountID, expectOrg, SwapOptions{})
}

// swapAttempts bounds how often a swap starts over because the live
// credential changed while it was being attributed.
const swapAttempts = 3

// SwapToWith installs accountID in item.
//
// Sequence (lane 6):
//
//  1. outside any lock, read the live credential and find whose it is: a token
//     equal to the outgoing account's vaulted one is a free match; otherwise
//     the seat behind it is asked for (one identity lookup);
//  2. take Claude Code's credential locks for the profile (I2), re-read, and
//     start over if the credential changed meanwhile;
//  3. under the locks: save what is about to be overwritten — into its
//     account's vault entry unless that entry is better, or into a recovery
//     item when it cannot be attributed (I1) — then merge and write;
//  4. release, verify by org id over the network, and on failure take the
//     locks again to roll back.
//
// A credential that cannot be saved is never overwritten.
func (v *Vault) SwapToWith(ctx context.Context, item keychain.Live, accountID, expectOrg string, o SwapOptions) (*SwapResult, error) {
	incoming, err := v.Load(accountID)
	if err != nil {
		return nil, err
	}
	if incoming.RefreshDead() {
		return nil, fmt.Errorf("account %q has an expired refresh token and needs an interactive login", accountID)
	}

	var snapshot keychain.Blob
	var merged *keychain.Blob
	written := false
	for attempt := 0; attempt < swapAttempts && !written; attempt++ {
		pre, err := item.Read()
		if err != nil {
			return nil, err
		}
		plan := v.planCapture(ctx, pre, accountID, incoming.AccessToken, o)

		held, err := v.lock(ctx, item, o.LockWait)
		if err != nil {
			return nil, err
		}
		live, err := item.Read()
		if err != nil {
			_ = held.Release()
			return nil, err
		}
		if live.ClaudeAIOAuth.AccessToken != pre.ClaudeAIOAuth.AccessToken {
			_ = held.Release()
			v.log.Info("the live credential changed while the swap was attributing it; starting over",
				"item", item.Name())
			continue
		}
		snapshot = *live // value copy; MCPOAuth is a slice we do not mutate
		if plan.target {
			// Already the target's credential: keep it, newer vault copy
			// and all. Writing the vaulted one would install an older token.
			cerr := v.capture(plan, live, o)
			_ = held.Release()
			if cerr != nil {
				return nil, fmt.Errorf("account %q is already live, but its newer credential could not "+
					"be saved to the vault: %w", accountID, cerr)
			}
			v.log.Info("swap target is already the live credential; nothing written",
				"account", accountID, "item", item.Name(), "because", plan.why)
			return &SwapResult{AccountID: accountID}, nil
		}
		merged, err = keychain.MergeForSwap(live, incoming)
		if err != nil {
			_ = held.Release()
			return nil, err
		}
		// Both writes this swap may make must be possible before the first:
		// a rollback refused by the store would leave the profile on a
		// credential nobody chose.
		if wc, ok := item.(keychain.WriteChecker); ok {
			if err := wc.CheckWrite(merged); err != nil {
				_ = held.Release()
				return nil, fmt.Errorf("swap not made: %w", err)
			}
			if err := wc.CheckWrite(&snapshot); err != nil {
				_ = held.Release()
				return nil, fmt.Errorf("swap not made: the current credential could not be written back "+
					"if the swap failed: %w", err)
			}
		}
		if err := v.capture(plan, live, o); err != nil {
			_ = held.Release()
			return nil, fmt.Errorf("swap aborted before it took effect: the credential it would "+
				"overwrite could not be saved first: %w", err)
		}
		werr := item.Write(merged)
		_ = held.Release()
		if werr != nil {
			return nil, fmt.Errorf("swap aborted before it took effect: %w", werr)
		}
		written = true
	}
	if !written {
		return nil, fmt.Errorf("swap not made: the live credential in %s kept changing underneath it; "+
			"Claude Code is probably refreshing it, so try again in a moment", item.Name())
	}

	u, verr := v.fetch(ctx, incoming.AccessToken, usage.Swap)
	switch {
	case verr != nil:
		if _, ok := usage.IsRateLimited(verr); ok {
			// The credential is installed and may well be fine; we simply cannot
			// prove it right now. Say so rather than roll back a good swap.
			v.log.Warn("swap installed but could not be verified: usage API rate limited",
				"account", accountID)
			return &SwapResult{AccountID: accountID}, nil
		}
		return v.rollback(ctx, item, &snapshot, merged, accountID, o.LockWait,
			fmt.Errorf("the new credential could not read usage: %w", verr))
	case expectOrg != "" && u.OrgID != expectOrg:
		return v.rollback(ctx, item, &snapshot, merged, accountID, o.LockWait,
			fmt.Errorf("swap installed the wrong account: expected org %s, got %s", expectOrg, u.OrgID))
	}

	v.log.Info("swapped account", "account", accountID, "org", u.OrgID,
		"five_hour", u.FiveHour.Pct(), "seven_day", u.SevenDay.Pct(),
		"mcp_preserved", len(merged.MCPOAuth) > 0)
	return &SwapResult{AccountID: accountID, OrgID: u.OrgID, Usage: u}, nil
}

// lock takes Claude Code's credential locks for item's secure-storage dir.
// An item that cannot name one is written without.
func (v *Vault) lock(ctx context.Context, item keychain.Live, wait time.Duration) (*cclock.Held, error) {
	ld, ok := item.(keychain.LockDirer)
	if !ok {
		return &cclock.Held{}, nil
	}
	h, err := acquireLock(ctx, ld.LockDir(), wait)
	if err != nil {
		if errors.Is(err, cclock.ErrBusy) {
			v.log.Info("Claude Code holds its credential lock; not writing now", "item", item.Name())
		}
		return nil, err
	}
	return h, nil
}

// rollback restores a snapshot under the locks and verifies the restore. A
// failure here is the worst outcome the program can produce, so it is
// reported in full. If the item no longer holds what the swap wrote, someone
// else has written it since, and that is left alone.
func (v *Vault) rollback(ctx context.Context, item keychain.Live, snapshot, wrote *keychain.Blob,
	accountID string, wait time.Duration, cause error) (*SwapResult, error) {
	held, lerr := v.lock(ctx, item, 2*max(wait, cclock.DefaultWait))
	if lerr != nil {
		return &SwapResult{AccountID: accountID}, fmt.Errorf(
			"the swap failed (%v) and could not be rolled back, because Claude Code's credential lock "+
				"could not be taken (%v). Run `claudeswitch use` with the account you want", cause, lerr)
	}
	defer held.Release()
	if cur, err := item.Read(); err == nil && cur.ClaudeAIOAuth.AccessToken != wrote.ClaudeAIOAuth.AccessToken {
		return &SwapResult{AccountID: accountID}, fmt.Errorf(
			"the swap failed (%v), and the live credential has been changed since by something else, "+
				"so it was not rolled back over that", cause)
	}
	if err := item.Write(snapshot); err != nil {
		return &SwapResult{AccountID: accountID, RolledBack: false}, fmt.Errorf(
			"CREDENTIAL LEFT IN A BAD STATE. The swap failed (%v) and the rollback also failed (%v). "+
				"Run `claude /login` to restore your session", cause, err)
	}
	back, err := item.Read()
	if err != nil || back.ClaudeAIOAuth.AccessToken != snapshot.ClaudeAIOAuth.AccessToken {
		return &SwapResult{AccountID: accountID, RolledBack: false}, fmt.Errorf(
			"rollback wrote but did not verify after: %v. Run `claude /login` if Claude Code misbehaves", cause)
	}
	v.log.Warn("swap rolled back", "account", accountID, "cause", cause.Error())
	return &SwapResult{AccountID: accountID, RolledBack: true}, cause
}

// capturePlan is what to do with the credential a swap overwrites.
type capturePlan struct {
	token   string // the live access token the plan was made for
	account string // file it under this account; "" with recover false: nothing to do
	seat    string // the seat behind it, when one was asked for
	recover bool   // keep a recovery copy
	target  bool   // it is the swap target's credential already
	why     string
}

// planCapture decides, outside the lock, where the live credential goes.
// Network: at most one identity lookup, and none when the outgoing account's
// vault entry already holds the live token.
func (v *Vault) planCapture(ctx context.Context, live *keychain.Blob, target, targetTok string, o SwapOptions) capturePlan {
	tok := live.ClaudeAIOAuth.AccessToken
	p := capturePlan{token: tok}
	if tok == targetTok {
		p.target, p.why = true, "the vault's token for it is the live one"
		return p
	}
	if o.Outgoing != "" {
		if e, err := readEntry(o.Outgoing); err == nil && e.ClaudeAIOAuth != nil &&
			e.ClaudeAIOAuth.AccessToken == tok {
			p.why = "the vault already holds it"
			return p
		}
	}
	seat := v.seatForCapture(ctx, tok)
	p.seat = seat
	owner := ""
	if seat != "" && o.SeatOwner != nil {
		owner = o.SeatOwner(seat)
	}
	switch {
	case seat == "":
		p.recover, p.why = true, "whose it is could not be established"
	case (o.TargetSeat != "" && seat == o.TargetSeat) || owner == target:
		p.account, p.target, p.why = target, true, "its seat is the target's"
	case o.Outgoing != "" && seat == o.OutgoingSeat:
		p.account, p.why = o.Outgoing, "the outgoing account's seat"
	case owner != "":
		p.account, p.why = owner, "its seat belongs to this account"
	default:
		p.recover, p.why = true, "its seat belongs to no configured account"
	}
	return p
}

// seatForCapture is the seat behind a live token, "" when unknown. The probe
// cache answers first; otherwise one identity lookup at swap priority, since an
// overwritten refresh token cannot be got back.
func (v *Vault) seatForCapture(ctx context.Context, token string) string {
	v.probeMu.Lock()
	if p, ok := v.probes[token]; ok && p.seat != "" && time.Since(p.at) < p.ttl() {
		v.probeMu.Unlock()
		return p.seat
	}
	v.probeMu.Unlock()
	pr, err := v.identify(ctx, token, usage.Swap)
	if err != nil {
		v.log.Warn("could not tell whose live credential a swap will overwrite", "err", err)
		return ""
	}
	seat := pr.Seat()
	if seat != "" {
		v.probeMu.Lock()
		if v.probes == nil {
			v.probes = map[string]seatProbe{}
		}
		v.probes[token] = seatProbe{seat: seat, at: time.Now()}
		v.probeMu.Unlock()
	}
	return seat
}

// capture carries out a plan, under the lock, against the credential read
// there. Only claudeAiOauth is saved: mcpOAuth belongs to the machine.
func (v *Vault) capture(p capturePlan, live *keychain.Blob, o SwapOptions) error {
	cred := *live.ClaudeAIOAuth
	switch {
	case p.account != "":
		e, err := readEntry(p.account)
		if err != nil || e.ClaudeAIOAuth == nil {
			// No entry to compare with or to file it under: keep it instead.
			return v.keepRecovery(&cred, p, o, "account "+p.account+" has no vault entry")
		}
		if e.Meta != nil && e.Meta.Seat() != "" && p.seat != "" && e.Meta.Seat() != p.seat {
			return v.keepRecovery(&cred, p, o, "the vault entry for "+p.account+" records another seat")
		}
		if why := liveWorse(e.ClaudeAIOAuth, &cred); why != "" {
			v.log.Info("not capturing the outgoing credential: the vaulted one is better",
				"account", p.account, "why", why)
			return nil
		}
		meta := &keychain.Meta{AccountID: p.account}
		if e.Meta != nil {
			cp := *e.Meta
			meta = &cp
		}
		meta.AccountID = p.account
		meta.VaultedAt = time.Now().Format(time.RFC3339)
		if err := writeEntry(p.account, &keychain.Blob{ClaudeAIOAuth: &cred, Meta: meta}); err != nil {
			return err
		}
		v.log.Info("captured the live credential into the vault before overwriting it",
			"account", p.account, "profile", o.Profile, "because", p.why)
		return nil
	case p.recover:
		return v.keepRecovery(&cred, p, o, p.why)
	}
	return nil
}

// RecoveryService is the name of a profile's recovery item in a slot; see
// credstore.RecoveryService. It never names a vault entry or a live item.
func RecoveryService(profile string, slot int) string {
	return keychain.RecoveryService(profile, slot)
}

// keepRecovery stores a credential that could not be filed under an account,
// in the profile's first free recovery slot or else its oldest.
func (v *Vault) keepRecovery(cred *keychain.OAuth, p capturePlan, o SwapOptions, why string) error {
	slot, oldest := 0, time.Time{}
	for i := 1; i <= MaxRecoveryCopies; i++ {
		b, err := readRecovery(RecoveryService(o.Profile, i))
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				slot = i
				break
			}
			continue // unreadable: leave it be
		}
		at := time.Time{}
		if b.Meta != nil {
			at, _ = time.Parse(time.RFC3339Nano, b.Meta.VaultedAt)
		}
		if slot == 0 || at.Before(oldest) {
			slot, oldest = i, at
		}
	}
	if slot == 0 {
		// Not one slot could be read (the store is not answering): any of
		// them may hold the only copy of something, so none is overwritten,
		// and the swap does not overwrite this credential either.
		return fmt.Errorf("no recovery slot for profile %q could be read, so the credential "+
			"could not be kept", o.Profile)
	}
	meta := &keychain.Meta{AccountID: "recovery", VaultedAt: time.Now().Format(time.RFC3339Nano)}
	if acct, org, ok := cutSeat(p.seat); ok {
		meta.AccountUUID, meta.OrgID = acct, org
	}
	name := RecoveryService(o.Profile, slot)
	if err := writeRecovery(name, &keychain.Blob{ClaudeAIOAuth: cred, Meta: meta}); err != nil {
		return err
	}
	v.log.Warn("kept a recovery copy of a live credential a swap overwrote",
		"item", name, "profile", o.Profile, "why", why, "seat", p.seat,
		"token", keychain.Redact(cred.AccessToken), "at", meta.VaultedAt)
	return nil
}

func cutSeat(seat string) (acct, org string, ok bool) {
	for i := 0; i < len(seat); i++ {
		if seat[i] == '@' {
			return seat[:i], seat[i+1:], i > 0 && i < len(seat)-1
		}
	}
	return "", "", false
}

// liveWorse says why the live credential should not replace the vaulted one,
// or "" when it is at least as good: the re-add rule. Worse means it cannot be
// renewed and the vaulted one can; or its refresh token runs out sooner; or,
// with neither refresh expiry known, its access token runs out sooner.
func liveWorse(vaulted, live *keychain.OAuth) string {
	if vaulted == nil || live == nil || vaulted.AccessToken == live.AccessToken {
		return ""
	}
	if vaulted.RefreshToken != "" && live.RefreshToken == "" {
		return "the live credential has no refresh token, and the vaulted one does"
	}
	vr, lr := vaulted.RefreshExpiry(), live.RefreshExpiry()
	if !vr.IsZero() && !lr.IsZero() {
		if vr.After(lr) {
			return "the vaulted refresh token outlives the live one"
		}
		return ""
	}
	if va, la := vaulted.Expiry(), live.Expiry(); !va.IsZero() && va.After(la) {
		return "the vaulted access token outlives the live one"
	}
	return ""
}
