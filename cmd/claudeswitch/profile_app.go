package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// `cs profile set` and `cs profile pool` (IMPROVEMENTS M3): per-profile
// overrides and pool membership, as parse-checked text edits.

func noSuchProfile(cfg *config.Config, name string) error {
	return appErr(codeNotFound, "the profiles are "+strings.Join(quoteAll(cfg.ProfileNames()), ", "),
		"no profile named %q", name)
}

// declaredIndex is the index of the [[profile]] called name, or -1.
func declaredIndex(cfg *config.Config, name string) int {
	for i, in := range cfg.Profiles {
		if in.Name == name {
			return i
		}
	}
	return -1
}

// profileOverride is a profile's own value for a per-profile setting, as
// `cs config` writes values, and whether it sets one.
func profileOverride(cfg *config.Config, name, key string) string {
	v, _ := profileOverrideOK(cfg, name, key)
	return v
}

func profileOverrideOK(cfg *config.Config, name, key string) (string, bool) {
	i := declaredIndex(cfg, name)
	if i < 0 {
		return "", false
	}
	in := cfg.Profiles[i]
	switch key {
	case "switch_at":
		return fmtPct(in.SwitchAt), in.SwitchAt > 0
	case "switch_at_weekly":
		return fmtPct(in.SwitchAtWeekly), in.SwitchAtWeekly > 0
	case "hard_floor":
		return fmtPct(in.HardFloor), in.HardFloor > 0
	case "landing_margin":
		if in.LandingMargin == nil {
			return "", false
		}
		return fmtPct(*in.LandingMargin), true
	case "models":
		if in.Models == nil {
			return "", false
		}
		return strings.Join(in.Models, ","), true
	case "prefer":
		return in.Prefer, in.Prefer != ""
	case "chrome":
		return in.Chrome, in.Chrome != ""
	case "paths":
		return strings.Join(in.Paths, ","), in.Paths != nil
	}
	return "", false
}

func profileKeys() []string {
	var out []string
	for _, s := range settings() {
		if s.profile {
			out = append(out, s.name)
		}
	}
	return out
}

// profileSet is `cs profile set <profile> <key> <value>`: a per-profile
// override (D4). "inherit" (or "") removes it; for models, "none" is an
// empty list, counting no model in this profile.
func profileSet(w io.Writer, cfgPath, name, key, value string, asJSON bool) error {
	cfg, err := loadForEdit(cfgPath)
	if err != nil {
		return err
	}
	i := declaredIndex(cfg, name)
	if key == "paths" {
		if i < 0 {
			if _, ok := cfg.ProfileNamed(name); ok {
				return appErr(codeNotFound, "declare profiles first: claudeswitch profile create <name>",
					"profile %q is not declared in the config", name)
			}
			return noSuchProfile(cfg, name)
		}
		return profileSetPaths(w, cfg, i, name, value, asJSON)
	}
	if i < 0 && key == "chrome" {
		if _, ok := cfg.ProfileNamed(name); ok {
			return appErr(codeNotFound, "with no [[profile]] blocks, give accounts a Chrome profile of their own: "+
				"cs chrome add <account> --existing <name>", "profile %q is not declared in the config", name)
		}
		return noSuchProfile(cfg, name)
	}
	if key == "chrome" {
		return profileSetChrome(w, cfg, i, name, value, asJSON)
	}
	if i < 0 {
		if _, ok := cfg.ProfileNamed(name); ok {
			return appErr(codeNotFound, "with no [[profile]] blocks there are only the global settings: "+
				"cs config "+key+" <value>", "profile %q is not declared in the config", name)
		}
		return noSuchProfile(cfg, name)
	}
	s, ok := findSetting(key)
	if !ok || !s.profile {
		return appErr(codeNotFound, "per-profile settings: "+strings.Join(profileKeys(), ", "),
			"%q is not a per-profile setting", key)
	}
	unset := value == "" || value == "inherit"
	in := &cfg.Profiles[i]
	rendered := ""
	pct := func(dst *float64) error {
		if unset {
			*dst = 0
			return nil
		}
		n, err := parsePct(value)
		if err != nil {
			return err
		}
		if n <= 0 || n > 100 {
			return fmt.Errorf("%s must be in (0, 100], got %g", key, n)
		}
		*dst, rendered = n, fmtPct(n)
		return nil
	}
	switch key {
	case "switch_at":
		err = pct(&in.SwitchAt)
	case "switch_at_weekly":
		err = pct(&in.SwitchAtWeekly)
	case "hard_floor":
		err = pct(&in.HardFloor)
	case "landing_margin":
		if unset {
			in.LandingMargin = nil
			break
		}
		var n float64
		if n, err = parsePct(value); err == nil {
			in.LandingMargin, rendered = &n, fmtPct(n)
		}
	case "prefer":
		in.Prefer = ""
		if !unset {
			in.Prefer, rendered = strings.TrimSpace(value), strconv.Quote(strings.TrimSpace(value))
		}
	case "models":
		switch {
		case unset:
			in.Models = nil
		case value == "none":
			in.Models, rendered = []string{}, tomlList(nil)
		default:
			in.Models = splitList(value)
			rendered = tomlList(in.Models)
		}
	}
	if err != nil {
		return appErr(codeInvalidValue, settingRange(s), "profile %q: %v", name, err)
	}
	if err := cfg.Validate(); err != nil {
		return appErr(codeInvalidValue, settingRange(s), "profile %q: %s would make the config invalid: %v",
			name, key, err)
	}
	want, wantSet := profileOverrideOK(cfg, name, key)
	ref := blockRef{"profile", name}
	err = editConfigText(cfg.Path, func(text string) (string, error) {
		if unset {
			return deleteKey(text, ref, key)
		}
		return setKey(text, ref, key, rendered)
	}, func(back *config.Config) error {
		if got, set := profileOverrideOK(back, name, key); got != want || set != wantSet {
			return fmt.Errorf("the edit parsed but profile %q's %s did not come out as asked; discarded", name, key)
		}
		return nil
	})
	if err != nil {
		return err
	}
	eff := s.get(cfg.ForProfile(name))
	if asJSON {
		var override any
		if wantSet {
			override = want
		}
		return emitTo(w, map[string]any{"profile": name, "key": key, "override": override, "effective": eff,
			"path": cfg.Path})
	}
	if wantSet {
		fmt.Fprintf(w, "  profile %s: %s = %s\n", name, key, nonEmpty(want, "(none)"))
	} else {
		fmt.Fprintf(w, "  profile %s: %s inherits the global value (%s)\n", name, key, eff)
	}
	return nil
}

// accountLiveIn says where accountID is or may be live among the profiles
// (and ghosts) consider accepts: the profile state records it live, or its
// live item holds it or cannot be read (D18, unknown counts as live). A
// seam: the default reads the keychain's live items.
var accountLiveIn = func(cfg *config.Config, st *state.State, accountID string,
	consider func(name string, ghost bool) bool) (string, bool) {
	for name, ps := range st.Profiles {
		if ps != nil && ps.Active == accountID && consider(name, false) {
			return name, true
		}
	}
	var targets []liveTarget
	for _, t := range cliGhostTargets(cfg, st) {
		if consider(t.name, t.ghost != nil) {
			targets = append(targets, t)
		}
	}
	holder, _, live := liveHolder(targets, vault.New(logger(false)), accountID)
	return holder, live
}

// liveRefusal is the error for an account live where it must not be.
func liveRefusal(id, holder, doing string) error {
	hint := fmt.Sprintf("switch profile %s to another account first: claudeswitch use <other> --profile %s",
		holder, holder)
	if isGhostName(holder) {
		hint = "once that old credential is signed out this clears by itself, or: claudeswitch profile forget <name>"
	}
	return &appError{Code: codeLive, Hint: hint, Message: fmt.Sprintf(
		"account %q is or may be live in %s, so it was not %s: one credential live in two profiles "+
			"is logged out by whichever refreshes first", id, whereLiveQ(holder), doing)}
}

const poolUsage = "usage: claudeswitch profile pool <profile> add <account> | " +
	"profile pool <profile> remove <account> [--to <profile>]"

// profilePool is `cs profile pool <profile> add|remove <account>`, on a
// profile's own pool (D35). An account is in one pool (D32): add refuses
// one another pool lists, so moving one is remove then add, or remove --to
// in one edit. An account live (or maybe live, D18) in the profile it leaves
// is refused (§3), and so is one live anywhere but the profile it joins. A
// profile that lists pools has no own pool: `cs pool` edits those.
func profilePool(w io.Writer, cfgPath, name, verb, id, to string, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if !hasAccount(cfg, id) {
		return appErr(codeNotFound, "", "no account %q in the config", id)
	}
	i := declaredIndex(cfg, name)
	if i < 0 {
		if _, ok := cfg.ProfileNamed(name); ok {
			return appErr(codeNotFound, "declare profiles first: claudeswitch profile create <name>",
				"profile %q is not declared in the config, so it has no pool to edit", name)
		}
		return noSuchProfile(cfg, name)
	}
	if lists := cfg.Profiles[i].Pools; len(lists) > 0 {
		return appErr(codeUsage, fmt.Sprintf("change a pool it lists: claudeswitch pool add %s %s, or what it "+
			"lists: claudeswitch profile pools %s <pool>...", lists[0], id, name),
			"profile %q lists pools (%s), so it has no pool of its own to edit", name, strings.Join(lists, ", "))
	}
	listedBy := func(c *config.Config, acct string) string {
		for _, in := range c.Profiles {
			if slices.Contains(in.Pool, acct) {
				return in.Name
			}
		}
		return ""
	}
	_, listed := cfg.PoolOf(id)
	src, dst := "", ""
	implicit := false // remove: src holds the account by D6 only
	switch verb {
	case "add":
		if owner := listedBy(cfg, id); owner == name {
			return emitPool(w, cfg, name, id, verb, asJSON, false)
		} else if owner != "" {
			return appErr(codeInOtherPool,
				fmt.Sprintf("move it: claudeswitch profile pool %s remove %s --to %s", owner, id, name),
				"account %q is in the pool of profile %q; an account is in one pool", id, owner)
		}
		if pool, ok := cfg.PoolOf(id); ok {
			return appErr(codeInOtherPool,
				fmt.Sprintf("share that pool: claudeswitch profile pools %s %s; or move the account: "+
					"claudeswitch pool move %s --to %s", name, pool, id, name),
				"account %q is in pool %q; an account is in one pool (profiles share a pool by listing it)", id, pool)
		}
		dst = name
		if declaredIndex(cfg, config.DefaultProfile) >= 0 {
			src = config.DefaultProfile // it is in no pool: default's by D6
		}
	case "remove":
		if listedBy(cfg, id) != name {
			// An account no pool lists is default's by D6 alone: moving it
			// out of default is adding it to the other pool, and default's
			// pool has nothing to remove.
			if listed || name != config.DefaultProfile {
				return appErr(codeNotFound, "", "the pool of profile %q does not list %q", name, id)
			}
			implicit = true
		}
		src, dst = name, to
		if to != "" {
			if to == name {
				return appErr(codeUsage, poolUsage, "--to names the profile it is leaving")
			}
			j := declaredIndex(cfg, to)
			if j < 0 {
				return noSuchProfile(cfg, to)
			}
			if lists := cfg.Profiles[j].Pools; len(lists) > 0 {
				return appErr(codeUsage, fmt.Sprintf("move it into a pool %s lists: claudeswitch pool move %s --to %s",
					to, id, lists[0]), "profile %q lists pools (%s), so it has no pool of its own to move %q into",
					to, strings.Join(lists, ", "), id)
			}
		} else if name == config.DefaultProfile {
			return appErr(codeUsage,
				fmt.Sprintf("move it: claudeswitch profile pool %s remove %s --to <profile>", name, id),
				"an account in no pool joins profile %q anyway (D6), so removing it from that pool alone "+
					"changes nothing", config.DefaultProfile)
		} else if declaredIndex(cfg, config.DefaultProfile) < 0 {
			if acct := accountByID(cfg, id); acct.IsEnabled() {
				return appErr(codeWouldOrphan,
					fmt.Sprintf("move it to another profile in the same step: claudeswitch profile pool %s remove %s --to <profile>", name, id),
					"removing %q from the pool of profile %q would leave it in no pool, which the config refuses", id, name)
			}
		}
	default:
		return appErr(codeUsage, poolUsage, "unknown pool verb %q", verb)
	}

	// §3: not live in the profile it leaves, and not live anywhere but the
	// profile it joins.
	if src != "" && src != dst {
		if holder, live := accountLiveIn(cfg, st, id, func(n string, ghost bool) bool { return n == src }); live {
			return liveRefusal(id, holder, "moved")
		}
	}
	if dst != "" {
		if holder, live := accountLiveIn(cfg, st, id, func(n string, ghost bool) bool { return ghost || n != dst }); live {
			return liveRefusal(id, holder, "added to profile "+dst+"'s pool")
		}
	}

	err = editConfigText(cfg.Path, func(text string) (string, error) {
		if src != "" && verb == "remove" && !implicit {
			out, _, err := mapArray(text, blockRef{"profile", src}, "pool", func(v []string) []string {
				return slices.DeleteFunc(v, func(s string) bool { return s == id })
			})
			if err != nil {
				return "", err
			}
			text = out
		}
		if dst != "" {
			out, found, err := mapArray(text, blockRef{"profile", dst}, "pool", func(v []string) []string {
				return append(v, id)
			})
			if err != nil {
				return "", err
			}
			if !found {
				out, err = setKey(text, blockRef{"profile", dst}, "pool", tomlList([]string{id}))
				if err != nil {
					return "", err
				}
			}
			text = out
		}
		return text, nil
	}, func(back *config.Config) error {
		// In dst's own pool, which is named after it; or, with no dst, in
		// no pool, so default's by D6.
		got, listed := back.PoolOf(id)
		if dst != "" && got != dst {
			return fmt.Errorf("the edit parsed but %q landed in pool %q, not profile %q's; discarded", id, got, dst)
		}
		if dst == "" && listed {
			return fmt.Errorf("the edit parsed but %q is still in pool %q; discarded", id, got)
		}
		return nil
	})
	if err != nil {
		return err
	}
	back, _ := config.Load(cfg.Path)
	if back == nil {
		back = cfg
	}
	return emitPool(w, back, name, id, verb, asJSON, true)
}

func accountByID(cfg *config.Config, id string) config.Account {
	for _, a := range cfg.Accounts {
		if a.ID == id {
			return a
		}
	}
	return config.Account{}
}

func emitPool(w io.Writer, cfg *config.Config, name, id, verb string, asJSON, changed bool) error {
	owner, _ := cfg.ProfileOf(id)
	if asJSON {
		pools := map[string][]string{}
		for _, in := range cfg.EffectiveProfiles() {
			pools[in.Name] = append([]string{}, in.Pool...)
		}
		return emitTo(w, map[string]any{"account": id, "profile": orNull(owner), "changed": changed, "pools": pools})
	}
	if !changed {
		fmt.Fprintf(w, "  %s is already in profile %s's pool\n", id, name)
		return nil
	}
	fmt.Fprintf(w, "  %s is now in profile %s's pool; a running daemon picks this up by itself\n",
		id, nonEmpty(owner, "(none)"))
	return nil
}

// profileJSON is one profile as `profile list --json` reports it.
func profileJSON(cfg *config.Config, st *state.State, in config.Profile) map[string]any {
	m := map[string]any{
		"name": in.Name, "dir": orNull(in.Dir), "from_env": in.FromEnv, "pool": append([]string{}, in.Pool...),
		"declared": declaredIndex(cfg, in.Name) >= 0,
	}
	// What the config's pools list, without the accounts default holds by
	// D6 alone (lane 15): those move by being added elsewhere. pools names
	// them, in the order the profile uses them (D33): its own pool under its
	// name (D35), or the [[pool]] blocks it lists.
	listed, pools := []string{}, []string{}
	if i := declaredIndex(cfg, in.Name); i >= 0 {
		if own := cfg.Profiles[i]; hasOwnPool(cfg, own) && len(own.Pool) == 0 {
			pools = append(pools, in.Name) // its own pool, empty: no tier
		}
		for t, name := range in.PoolNames {
			if name != "" {
				listed = append(listed, in.Tiers[t]...)
				pools = append(pools, name)
			}
		}
	}
	m["listed"], m["pools"] = listed, pools
	// IMPROVEMENTS F4: the folders this profile is picked for, as written.
	m["paths"] = append([]string{}, in.Paths...)
	live, pinned := "", ""
	if ps := st.Profiles[in.Name]; ps != nil {
		live, pinned = ps.Active, ps.Pinned
	}
	m["live"], m["pinned"] = orNull(live), orNull(pinned)
	signedIn := "yes"
	if _, err := cliLiveFor(in); err != nil {
		signedIn = "unknown"
		if errors.Is(err, keychain.ErrNotFound) {
			signedIn = "no"
		}
	}
	m["signed_in"] = signedIn
	overrides := map[string]any{}
	for _, k := range profileKeys() {
		if v, ok := profileOverrideOK(cfg, in.Name, k); ok {
			overrides[k] = v
		}
	}
	m["overrides"] = overrides
	m["thresholds"] = thresholdsJSON(cfg.ForProfile(in.Name))
	// IMPROVEMENTS C2: the Chrome profile this profile names (null: Chrome's
	// last used), and the live account's resolved one.
	local := readChromeLocal()
	folder := cfg.ChromeFor(in.Name)
	m["chrome"], m["chrome_name"] = orNull(folder), orNull(local.nameOf(folder))
	m["chrome_resolved"] = nil
	if live != "" {
		m["chrome_resolved"] = resolveChrome(cfg, st, local, in.Name, live).json()
	}
	return m
}

// profileListJSON is `profile list --json`.
func profileListJSON(w io.Writer, cfgPath string) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	var list []map[string]any
	for _, in := range cfg.EffectiveProfiles() {
		list = append(list, profileJSON(cfg, st, in))
	}
	var ghosts []map[string]any
	for _, g := range allGhosts(cfg, st) {
		ghosts = append(ghosts, map[string]any{"profile": g.Profile, "account": g.Account, "why": g.Why,
			"since": g.Since})
	}
	if ghosts == nil {
		ghosts = []map[string]any{}
	}
	return emitTo(w, map[string]any{"profiles": list, "ghosts": ghosts, "pools": poolListJSON(cfg)["pools"]})
}
