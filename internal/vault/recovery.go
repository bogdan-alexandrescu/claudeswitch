package vault

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// The recovery tooling (IMPROVEMENTS I1a): what a swap kept because it could
// not file the credential it overwrote under an account (keepRecovery), and
// the way back from there into the vault.

// deleteRecovery removes a recovery item. A seam, like readRecovery.
var deleteRecovery = keychain.Delete

// RecoveryItem is one kept credential, as `cs recovery` lists it. Only what
// the item itself records: never a token.
type RecoveryItem struct {
	Name    string // the store's name for it
	Slot    string // how a person names it: "<profile>-<n>", or "<n>" for the unnamed slots
	Profile string // "" for a swap that named no profile
	N       int
	// KeptAt is when the swap kept it; zero when the item does not say.
	KeptAt time.Time
	// Seat is the seat recorded when it was kept, "" when it could not be
	// identified then (the usual reason it is here at all).
	Seat          string
	Expiry        time.Time // access token
	RefreshExpiry time.Time
	HasRefresh    bool
	// Err is set when the item exists or may exist but could not be read; it
	// is listed rather than skipped, since it may be the only copy.
	Err error
}

// recoveryProfiles is every profile whose slots are looked at: the ones
// named, then "default" (the slots an older build used for unnamed swaps),
// then the unnamed slots.
func recoveryProfiles(profiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string{}, profiles...), config.DefaultProfile, "") {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// ListRecovery reads every recovery slot of the named profiles (and the
// "default" and unnamed slots), and returns the ones that hold something or
// could not be read. It makes no network call.
func ListRecovery(profiles []string) []RecoveryItem {
	var out []RecoveryItem
	for _, prof := range recoveryProfiles(profiles) {
		for n := 1; n <= MaxRecoveryCopies; n++ {
			name := RecoveryService(prof, n)
			it := RecoveryItem{Name: name, Slot: keychain.RecoverySlotID(prof, n), Profile: prof, N: n}
			b, err := readRecovery(name)
			switch {
			case errors.Is(err, keychain.ErrNotFound):
				continue
			case err != nil:
				it.Err = err
			case b == nil || b.ClaudeAIOAuth == nil:
				it.Err = errors.New("the item holds no credential")
			default:
				describeRecovery(&it, b)
			}
			out = append(out, it)
		}
	}
	return out
}

func describeRecovery(it *RecoveryItem, b *keychain.Blob) {
	o := b.ClaudeAIOAuth
	it.Expiry, it.RefreshExpiry, it.HasRefresh = o.Expiry(), o.RefreshExpiry(), o.RefreshToken != ""
	if b.Meta != nil {
		it.Seat = b.Meta.Seat()
		if t, err := time.Parse(time.RFC3339Nano, b.Meta.VaultedAt); err == nil {
			it.KeptAt = t
		}
	}
}

// readSlot resolves a slot id to its item and reads it.
func readSlot(slot string) (string, *keychain.Blob, error) {
	prof, n, ok := keychain.ParseRecoverySlotID(slot)
	if !ok || n > MaxRecoveryCopies {
		return "", nil, fmt.Errorf("%q is not a recovery slot; `cs recovery` lists them "+
			"(e.g. work-1, or 2 for a slot with no profile)", slot)
	}
	if prof != "" {
		if err := config.ValidName("profile name", prof); err != nil {
			return "", nil, fmt.Errorf("%q is not a recovery slot: %w", slot, err)
		}
	}
	name := RecoveryService(prof, n)
	b, err := readRecovery(name)
	if errors.Is(err, keychain.ErrNotFound) {
		return name, nil, fmt.Errorf("recovery slot %s is empty", slot)
	}
	if err != nil {
		return name, nil, fmt.Errorf("recovery slot %s could not be read (%v): %w", slot, err, errUnreadableSlot)
	}
	if b == nil || b.ClaudeAIOAuth == nil {
		return name, nil, fmt.Errorf("recovery slot %s holds no credential: %w", slot, errUnreadableSlot)
	}
	return name, b, nil
}

// errUnreadableSlot marks a slot that holds something that could not be read.
var errUnreadableSlot = errors.New("unreadable recovery slot")

// IdentifyRecovery asks the profile endpoint whose a kept credential is: one
// call, through the shared budget, at interactive priority (a person asked).
func (v *Vault) IdentifyRecovery(ctx context.Context, it RecoveryItem) (*usage.Profile, error) {
	_, b, err := readSlot(it.Slot)
	if err != nil {
		return nil, err
	}
	return v.identify(ctx, b.ClaudeAIOAuth.AccessToken, usage.Interactive)
}

// RestoreRecovery files a kept credential under accountID and clears its
// slot.
//
//   - The credential's seat must be the account's: wantSeat (the config's
//     pin), or else the seat of the entry already vaulted under it. With
//     neither there is nothing to check against, and it is refused. The seat
//     is asked for (one identity lookup); when that call fails, the seat
//     recorded with the item when it was kept stands in, and with none it is
//     refused. force never skips this.
//   - A credential worse than the one already vaulted (the re-add rule:
//     renewable beats not, the later refresh expiry wins) does not replace it
//     unless force.
//   - The vault entry keeps the existing entry's annotation, with the seat
//     and the profile's details written over it.
func (v *Vault) RestoreRecovery(ctx context.Context, slot, accountID, wantSeat string, force bool) (*Entry, error) {
	if err := config.ValidName("account id", accountID); err != nil {
		return nil, err
	}
	name, b, err := readSlot(slot)
	if err != nil {
		return nil, err
	}
	cred := *b.ClaudeAIOAuth

	existing, _ := readEntry(accountID)
	expect := wantSeat
	if expect == "" && existing != nil && existing.Meta != nil {
		expect = existing.Meta.Seat()
	}
	if expect == "" {
		return nil, fmt.Errorf("account %q has no pinned seat and no vaulted entry to compare against, "+
			"so whether slot %s is its credential cannot be checked; sign in to it first "+
			"(`cs login %s`), which pins it", accountID, slot, accountID)
	}

	got := ""
	pr, perr := v.identify(ctx, cred.AccessToken, usage.Interactive)
	switch {
	case perr == nil:
		got = pr.Seat()
	case b.Meta != nil && b.Meta.Seat() != "":
		got = b.Meta.Seat()
		v.log.Info("could not ask whose the kept credential is; using the seat recorded when it was kept",
			"slot", slot, "err", perr)
	default:
		return nil, fmt.Errorf("cannot tell whose the credential in slot %s is (%v), and none was recorded "+
			"when it was kept; nothing was restored", slot, perr)
	}
	if got != expect {
		return nil, fmt.Errorf("slot %s holds seat %s, but %s is seat %s; nothing was restored",
			slot, usage.ShortSeat(got), accountID, usage.ShortSeat(expect))
	}

	if existing != nil && existing.ClaudeAIOAuth != nil && !force {
		if why := liveWorse(existing.ClaudeAIOAuth, &cred); why != "" {
			why = strings.ReplaceAll(why, "live", "kept")
			return nil, fmt.Errorf("not replacing %s's vault entry with slot %s: %s. "+
				"To replace it anyway: cs recovery restore %s %s --force", accountID, slot, why, slot, accountID)
		}
	}

	meta := &keychain.Meta{}
	if existing != nil && existing.Meta != nil {
		cp := *existing.Meta
		meta = &cp
	}
	meta.AccountID = accountID
	meta.AccountUUID, meta.OrgID, _ = cutSeat(got)
	if pr != nil {
		if pr.Account.Email != "" {
			meta.Email = pr.Account.Email
		}
		if p := pr.Plan(); p != "" {
			meta.Plan = p
		}
		if pr.Organization.Name != "" {
			meta.OrgName = pr.Organization.Name
		}
	}
	meta.VaultedAt = time.Now().Format(time.RFC3339)
	if err := writeEntry(accountID, &keychain.Blob{ClaudeAIOAuth: &cred, Meta: meta}); err != nil {
		return nil, fmt.Errorf("could not vault slot %s as %s (the slot is untouched): %w", slot, accountID, err)
	}
	v.log.Info("restored a recovery copy into the vault", "slot", slot, "account", accountID,
		"token", keychain.Redact(cred.AccessToken))
	e := &Entry{AccountID: accountID, AccountUUID: meta.AccountUUID, Email: meta.Email, Plan: meta.Plan, OrgID: meta.OrgID,
		Expiry: cred.Expiry(), RefreshExpiry: cred.RefreshExpiry(), Tier: cred.RateLimitTier,
		Subscription: cred.SubscriptionType, NoRefreshToken: cred.RefreshToken == ""}
	if err := deleteRecovery(name); err != nil {
		return e, fmt.Errorf("vaulted slot %s as %s, but could not clear the slot: %w; "+
			"clear it with `cs recovery clear %s`", slot, accountID, err, slot)
	}
	return e, nil
}

// ClearRecovery deletes one recovery slot. An empty slot is an error, so a
// mistyped slot id is not reported as done; a slot that holds something
// unreadable can still be cleared.
func ClearRecovery(slot string) error {
	name, _, err := readSlot(slot)
	if name == "" || (err != nil && !errors.Is(err, errUnreadableSlot)) {
		return err
	}
	return deleteRecovery(name)
}
