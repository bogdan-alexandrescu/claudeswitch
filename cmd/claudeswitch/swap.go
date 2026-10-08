package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// freshenWindow is how close to expiry a swap target's access token may be
// before it is refreshed first (IMPROVEMENTS I3). Installed with minutes
// left, it would make Claude Code refresh at once — racing whatever else
// holds the same refresh token.
const freshenWindow = 10 * time.Minute

// swapperWith is the one vault call a plain swap needs (restoreActive).
type swapperWith interface {
	SwapToWith(ctx context.Context, item keychain.Live, accountID, expectOrg string, o vault.SwapOptions) (*vault.SwapResult, error)
}

// swapVault is what a swap needs from the vault, for the daemon and `use`.
type swapVault interface {
	SwapToWith(ctx context.Context, item keychain.Live, accountID, expectOrg string, o vault.SwapOptions) (*vault.SwapResult, error)
	NeedsRefresh(accountID string, window time.Duration) bool
	HoldsAccount(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool)
	RefreshIn(ctx context.Context, accountID, wantSeat string, holder keychain.Live, allowActive bool) (*vault.Entry, error)
}

// swapInto installs target in a profile's live item, after the §3 check
// (target live in no other profile) has passed. It freshens the target if
// it is about to expire, then swaps naming the outgoing account so its
// credential is captured, not lost (I1).
func swapInto(ctx context.Context, v swapVault, cfg *config.Config, profName, outgoing string,
	item keychain.Live, target, expectOrg string, log *slog.Logger) (*vault.SwapResult, error) {
	freshenTarget(ctx, v, cfg, item, target, log)
	return v.SwapToWith(ctx, item, target, expectOrg, swapOptions(cfg, profName, outgoing, target))
}

// swapOptions tells a swap what it needs to file the credential it
// overwrites: the profile, the outgoing account and its seat, the target's
// seat, and which configured account owns any other seat. Every swap path
// (the daemon, `use`, switching back after a login) builds them here.
func swapOptions(cfg *config.Config, profName, outgoing, target string) vault.SwapOptions {
	return vault.SwapOptions{
		Profile:      profName,
		Outgoing:     outgoing,
		OutgoingSeat: cfg.SeatOf(outgoing),
		TargetSeat:   cfg.SeatOf(target),
		SeatOwner:    cfg.AccountBySeat,
	}
}

// freshenTarget refreshes a swap target whose access token expires within
// freshenWindow. A refresh revokes the token wherever it is live, so it is
// done only when the target is known not to be in this profile's item; the
// caller has already established it is live in no other (D18). A failure is
// logged and the swap goes ahead: the old token may still work, and the
// swap's own verification decides.
func freshenTarget(ctx context.Context, v swapVault, cfg *config.Config, item keychain.Live,
	target string, log *slog.Logger) {
	if !v.NeedsRefresh(target, freshenWindow) {
		return
	}
	// By seat, not token: the target may be live here under a token the vault
	// never saw (Claude Code refreshed it, or a hand login), and a refresh
	// would revoke it.
	if holds, known := v.HoldsAccount(ctx, item, target, cfg.SeatOf(target)); holds || !known {
		log.Info("not refreshing the swap target first: it may be live in this profile",
			"target", target, "known", known)
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if _, err := v.RefreshIn(rctx, target, cfg.SeatOf(target), nil, false); err != nil {
		log.Warn("could not refresh the swap target before installing it; swapping anyway",
			"target", target, "err", err)
		return
	}
	log.Info("refreshed the swap target before installing it: its token was about to expire",
		"target", target)
}
