package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// profileFlagHelp is the --profile flag's help, the same on every command.
const profileFlagHelp = "the Claude Code profile to act on (default: the one this shell's " +
	"CLAUDE_CONFIG_DIR belongs to)"

// pickProfile chooses the Claude Code profile a CLI command acts on
// (docs/PROFILES.md §6).
//
//   - --profile NAME names it outright.
//   - With no [[profile]] blocks there is one implicit profile, resolving from
//     this process's environment exactly as before profiles.
//   - Otherwise the caller's own CLAUDE_CONFIG_DIR picks it: unset (or empty)
//     is the profile that omits dir (D11); set, it is the profile whose dir
//     is that directory. So `/cs use` inside a work session acts on work.
//
// A dir is matched first as written, since the keychain item is hashed from
// the string as given, and then as the directory it names (~ expanded,
// trailing / ignored). An environment that matches nothing, or two profiles
// equally, is an error naming the profiles and the flag: a guess here would
// act on someone else's live credential.
func pickProfile(cfg *config.Config, flagName string) (config.Profile, error) {
	if flagName != "" {
		if in, ok := cfg.ProfileNamed(flagName); ok {
			return in, nil
		}
		return config.Profile{}, fmt.Errorf("no profile named %q; the profiles are %s",
			flagName, strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	all := cfg.EffectiveProfiles()
	if len(cfg.Profiles) == 0 {
		return all[0], nil
	}

	env := os.Getenv(ccdir.EnvConfigDir)
	if env == "" {
		for _, in := range all {
			if in.Dir == "" {
				return in, nil
			}
		}
		return config.Profile{}, fmt.Errorf("this shell runs Claude Code with CLAUDE_CONFIG_DIR unset, "+
			"and no [[profile]] omits dir to stand for that; pass --profile (one of %s)",
			strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	for _, in := range all {
		if in.Dir == env {
			return in, nil
		}
	}
	var match []config.Profile
	key := dirKey(env)
	for _, in := range all {
		if in.Dir != "" && dirKey(in.Dir) == key {
			match = append(match, in)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return config.Profile{}, fmt.Errorf("this shell's CLAUDE_CONFIG_DIR=%s matches no [[profile]] in "+
			"the config; pass --profile (one of %s), or declare this directory as a profile",
			env, strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	var names []string
	for _, in := range match {
		names = append(names, in.Name)
	}
	return config.Profile{}, fmt.Errorf("this shell's CLAUDE_CONFIG_DIR=%s is the directory of more than "+
		"one profile (%s); pass --profile to say which", env, strings.Join(quoteAll(names), ", "))
}

// dirKey is the directory a dir string names, for matching two spellings.
func dirKey(dir string) string {
	if exp, err := ccdir.ExpandHome(dir); err == nil {
		dir = exp
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

func quoteAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// multiProfile reports whether more than one profile is configured, the
// point at which views show one block per profile.
func multiProfile(cfg *config.Config) bool { return len(cfg.EffectiveProfiles()) > 1 }

// refuseOutsidePool is D5: a manual swap stays within the target profile's
// pool. There is no --force, because a manual swap across pools is how one
// credential comes to be live in two profiles (§3).
//
// With no [[profile]] blocks it refuses nothing: there is one profile, and
// `use` never consulted the config before profiles.
func refuseOutsidePool(cfg *config.Config, target config.Profile, accountID string) error {
	if len(cfg.Profiles) == 0 {
		return nil
	}
	for _, id := range target.Pool {
		if id == accountID {
			return nil
		}
	}
	if owner, ok := cfg.ProfileOf(accountID); ok {
		return &appError{Code: codeOutsidePool, Message: fmt.Sprintf("account %q is in the pool of profile %q, not of profile %q; pools are fixed, "+
			"since one credential live in two profiles is logged out by whichever refreshes first. "+
			"To use it there: `claudeswitch use %s --profile %s`",
			accountID, owner, target.Name, accountID, owner)}
	}
	return &appError{Code: codeOutsidePool, Message: fmt.Sprintf("account %q is in no profile's pool, so no profile may use it; "+
		"add it to a pool in the config first", accountID)}
}

// liveTarget is one profile's live credential as a §3 check sees it: the
// item, or why it could not be resolved.
type liveTarget struct {
	name       string
	live       keychain.Live
	unresolved error
	// ghost is set on a removed or re-pointed profile's old item (see
	// withGhosts); name is then a description, never a current profile.
	ghost *state.Ghost
}

// liveTargets resolves every effective profile's live item.
func liveTargets(cfg *config.Config, resolve func(config.Profile) (keychain.Live, error)) []liveTarget {
	var out []liveTarget
	for _, in := range cfg.EffectiveProfiles() {
		live, err := resolve(in)
		out = append(out, liveTarget{name: in.Name, live: live, unresolved: err})
	}
	return out
}

// holdsChecker is the part of the vault the §3 check needs.
// HoldsAccountWhy is HoldsAccount plus, for an unknown a rate-limit lock
// caused, when that lock clears (R1).
type holdsChecker interface {
	HoldsAccountWhy(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool, retryAt time.Time)
}

// liveElsewhereOf is the check before a live write (§3): may accountID be live
// in a profile other than self? It names that profile and the evidence, or
// returns "". The daemon and `use` share it.
//
// State is consulted first, then every other profile's live ITEM, because
// state is only as fresh as the last attribution and a hand login does not
// tell us. An item that cannot be read, or whose account cannot be settled,
// counts as holding it (D18): the safe side of an unknown is not writing. A
// missing item holds nothing. So does a profile whose item could not be
// resolved because none exists under any spelling of its dir (D9: not logged
// in, ErrNotFound); one whose resolution failed for any other reason — a
// lookup error or timeout — is unknown, and refuses.
//
// retryAt is set only for an unknown a rate-limit lock on the token the check
// needed caused: when it clears, so the refusal can say when to try again
// (R1, rateLimitedRefusal).
func liveElsewhereOf(ctx context.Context, v holdsChecker, cfg *config.Config, st *state.State,
	self string, targets []liveTarget, accountID string) (other, why string, retryAt time.Time) {
	for _, o := range targets {
		if o.ghost != nil {
			if o.ghost.Account == accountID {
				return o.name, "it may still be live in " + o.name, time.Time{}
			}
			continue
		}
		if o.name == self {
			continue
		}
		if in := st.Profiles[o.name]; in != nil && in.Active == accountID {
			return o.name, "it is the recorded live account", time.Time{}
		}
	}
	seat := cfg.SeatOf(accountID)
	for _, o := range targets {
		if o.ghost == nil && o.name == self {
			continue
		}
		if o.live == nil {
			if o.unresolved != nil && !errors.Is(o.unresolved, keychain.ErrNotFound) {
				return o.name, "its live credential could not be looked up, so it could not be confirmed absent", time.Time{}
			}
			continue
		}
		holds, known, at := v.HoldsAccountWhy(ctx, o.live, accountID, seat)
		switch {
		case !known:
			return o.name, "it could not be confirmed absent from the live credential", at
		case holds:
			return o.name, "it is in the live credential", time.Time{}
		}
	}
	return "", "", time.Time{}
}

// rateLimitedWhy is R1's wording for a §3 check that could not get an answer
// because of a rate-limit lock: id and where as the caller writes them (the
// CLI quotes, the daemon does not), at shown as local wall-clock time.
func rateLimitedWhy(id, where string, at time.Time) string {
	return fmt.Sprintf("can't confirm %s isn't signed in under %s: the check is rate limited until %s; try again then",
		id, where, at.Local().Format("15:04:05"))
}

// rateLimitedRefusal is the CLI's refusal for it: code live, as any §3
// refusal, with retry_at in the JSON error object.
func rateLimitedRefusal(id, other string, at time.Time) *appError {
	return &appError{Code: codeLive, Message: rateLimitedWhy(fmt.Sprintf("%q", id), whereLiveQ(other), at), RetryAt: at}
}

// profileForAccount is the profile a command putting accountID into a live
// item acts on (use, add, login): picked as pickProfile does, and refused when
// the account belongs to another profile's pool (D5). allowUnconfigured lets
// an id the config does not list through, for add, which vaults such ids and
// says so.
func profileForAccount(cfg *config.Config, flagName, accountID string, allowUnconfigured bool) (config.Profile, error) {
	in, err := pickProfile(cfg, flagName)
	if err != nil {
		return in, err
	}
	if allowUnconfigured && !configured(cfg, accountID) {
		return in, nil
	}
	return in, refuseOutsidePool(cfg, in, accountID)
}

// configured reports whether the config lists an account.
func configured(cfg *config.Config, accountID string) bool {
	for _, a := range cfg.Accounts {
		if a.ID == accountID {
			return true
		}
	}
	return false
}

// envForProfile is the environment to run Claude Code in for a profile:
// nil (inherit) for the implicit one, which is whatever this shell says;
// otherwise this environment with CLAUDE_CONFIG_DIR set to the profile's dir
// exactly as written (unset when it has none), and no
// CLAUDE_SECURESTORAGE_CONFIG_DIR, which a declared profile does not model.
func envForProfile(in config.Profile) []string {
	if in.FromEnv {
		return nil
	}
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, ccdir.EnvConfigDir+"=") || strings.HasPrefix(kv, ccdir.EnvSecureStorageDir+"=") {
			continue
		}
		env = append(env, kv)
	}
	if in.Dir != "" {
		env = append(env, ccdir.EnvConfigDir+"="+in.Dir)
	}
	return env
}

// cliLiveFor resolves a profile's live item for a CLI command. A seam.
var cliLiveFor = liveFor

// liveHolds is the part of the vault liveHolder needs: local, no network.
type liveHolds interface {
	LiveHolds(item keychain.Live, accountID string) (holds, known bool)
}

// liveHolder finds the profile whose live item may hold accountID's vaulted
// credential: held, or unreadable (unknown, so assume it is). A missing item
// holds nothing, and so does a profile not logged in (ErrNotFound); one
// whose item could not be looked up at all is unknown, and reported with a nil
// item. It is the CLI's "never touch a live credential" test across every
// profile; with one profile it is exactly the old IsLive.
func liveHolder(targets []liveTarget, lh liveHolds, accountID string) (string, keychain.Live, bool) {
	for _, t := range targets {
		if t.ghost != nil && t.ghost.Account == accountID {
			return t.name, t.live, true // may still be live in a ghost's item
		}
		if t.live == nil {
			if t.unresolved != nil && !errors.Is(t.unresolved, keychain.ErrNotFound) {
				return t.name, nil, true
			}
			continue
		}
		if holds, known := lh.LiveHolds(t.live, accountID); holds || !known {
			return t.name, t.live, true
		}
	}
	return "", nil, false
}

// checkProfileFlag validates an --profile flag on a view that shows every
// profile by default (status, top, why, plan): "" is every profile.
func checkProfileFlag(cfg *config.Config, name string) error {
	if name == "" {
		return nil
	}
	_, err := pickProfile(cfg, name)
	return err
}
