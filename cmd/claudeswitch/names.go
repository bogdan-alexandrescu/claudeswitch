package main

import (
	"fmt"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
)

// checkNewAccountID refuses an account id the config would not load, before
// anything is vaulted under it: the id becomes a keychain item's name and a
// line in config.toml, and a vault entry whose config edit then fails is an
// account in limbo. The refusal is invalid_value (docs/APP_CLI.md).
func checkNewAccountID(id string) error {
	return wrapErr(codeInvalidValue, "", config.ValidName("account id", id))
}

// newAccountNotice is what login says before signing in to an account the
// config does not have: where its block goes and, with profiles declared,
// which pool it joins (D20): the profile's own, or the first it lists (D31).
func newAccountNotice(cfg *config.Config, target config.Profile, id string) string {
	s := fmt.Sprintf("  %q is new. Once signed in, it is vaulted and added to %s,\n", id, cfg.Path)
	if pool := poolToJoin(cfg, target); pool != "" {
		if into := joinPool(cfg, pool); into != "" {
			s += fmt.Sprintf("  pinned to the seat that signed in, in pool %q (the first profile %q uses).\n\n",
				into, pool)
			return s
		}
		s += fmt.Sprintf("  pinned to the seat that signed in, in profile %q's pool.\n\n", pool)
		return s
	}
	s += "  pinned to the seat that signed in.\n\n"
	return s
}

// directPool decides, before a `login --direct` starts, which declared
// profile's pool the account joins when the login completes (D20):
//
//   - an account the config already has: none (its pool is settled);
//   - no [[profile]] blocks: none (the implicit profile holds every account);
//   - --profile P: P, which must be declared;
//   - no flag, with a "default" profile: none, so it joins default (D6) as
//     before;
//   - no flag and no "default": refused now, asking for --profile, since the
//     account would otherwise be in no pool and the config edit at the end
//     would not load.
func directPool(cfg *config.Config, id, profName string) (string, error) {
	if hasAccount(cfg, id) || len(cfg.Profiles) == 0 {
		return "", nil
	}
	if profName != "" {
		if _, ok := cfg.ProfileNamed(profName); !ok {
			return "", fmt.Errorf("no profile named %q; the profiles are %s",
				profName, joinQuoted(cfg.ProfileNames()))
		}
		return profName, nil
	}
	if _, ok := cfg.ProfileNamed(config.DefaultProfile); ok {
		return "", nil
	}
	return "", fmt.Errorf("%q is new, and with no profile named %q there is no pool it would join by "+
		"itself; say which with --profile (one of %s)", id, config.DefaultProfile,
		joinQuoted(cfg.ProfileNames()))
}

// completionPool is the pool a completed `login --code` joins: the profile
// recorded when the login started, provided the config still declares it.
func completionPool(cfg *config.Config, pend oauth.Pending) string {
	if pend.Profile == "" || hasAccount(cfg, pend.AccountID) {
		return ""
	}
	for _, in := range cfg.Profiles {
		if in.Name == pend.Profile {
			return in.Name
		}
	}
	return ""
}

func joinQuoted(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

// poolToJoin is the declared profile whose pool a new account signed in or
// added through target joins (D20), or "" with no [[profile]] blocks, where
// the one implicit profile holds every account.
func poolToJoin(cfg *config.Config, target config.Profile) string {
	if len(cfg.Profiles) == 0 || target.FromEnv {
		return ""
	}
	return target.Name
}

// joinTarget is where a new account goes. Profile is D20's: the profile's own
// pool, or the first pool it lists. Pool is one chosen by name (D37): a
// [[pool]] block, or a profile's own pool under the profile's name. None is
// no pool: the profile named default uses it last (D6). The zero value is
// "nothing to edit": an account the config already has keeps its pool.
type joinTarget struct {
	Profile, Pool string
	None          bool
}

// resolveJoin checks --pool and --no-pool. Neither is the zero target, for
// the caller's D20 default.
func resolveJoin(cfg *config.Config, pool string, none bool) (joinTarget, error) {
	switch {
	case pool != "" && none:
		return joinTarget{}, appErr(codeUsage, "", "--pool and --no-pool say different things; give one")
	case none:
		if _, ok := cfg.ProfileNamed(config.DefaultProfile); !ok && len(cfg.Profiles) > 0 {
			return joinTarget{}, appErr(codeUsage, "choose a pool with --pool",
				"--no-pool: with no profile named %q, an account in no pool rotates nowhere", config.DefaultProfile)
		}
		return joinTarget{None: true}, nil
	case pool == "":
		return joinTarget{}, nil
	}
	if _, ok := cfg.PoolNamed(pool); ok {
		return joinTarget{Pool: pool}, nil
	}
	for _, in := range cfg.Profiles {
		if in.Name == pool && len(in.Pools) == 0 {
			return joinTarget{Pool: pool}, nil // its own pool (D35)
		}
	}
	return joinTarget{}, appErr(codeUsage, "the pools are listed by `claudeswitch pool list`",
		"no pool named %q", pool)
}

// completionJoin is where a completed `login --code` puts a new account: the
// pool chosen when the login started (D37), else the profile recorded then
// (D20), provided the config still has it.
func completionJoin(cfg *config.Config, pend oauth.Pending) joinTarget {
	if hasAccount(cfg, pend.AccountID) {
		return joinTarget{}
	}
	switch {
	case pend.NoPool:
		return joinTarget{None: true}
	case pend.JoinPool != "":
		if j, err := resolveJoin(cfg, pend.JoinPool, false); err == nil {
			return j
		}
		return joinTarget{}
	}
	return joinTarget{Profile: completionPool(cfg, pend)}
}
