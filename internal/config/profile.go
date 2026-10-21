package config

import (
	"fmt"
	"strings"
)

// DefaultProfile is the profile unlisted accounts join (D6), and the name of
// the implicit profile when no [[profile]] blocks are declared.
const DefaultProfile = "default"

// Profile is one Claude Code profile claudeswitch drives: a config dir with
// its own live credential, and the fixed pool of accounts it rotates within
// (docs/PROFILES.md §4, §8 D1).
//
//	[[profile]]
//	name = "default"            # no dir: Claude Code runs with CLAUDE_CONFIG_DIR unset
//	pool = ["personal", "a4"]
//
//	[[profile]]
//	name      = "work"
//	dir       = "~/.claude-work" # CLAUDE_CONFIG_DIR=~/.claude-work claude
//	pool      = ["work-1", "work-2"]
//	switch_at = 75               # overrides the global value for this profile only
//
// dir is written exactly as the person sets CLAUDE_CONFIG_DIR when launching
// Claude Code with it, because the keychain item's name is a hash of that
// string. Omitting dir means CLAUDE_CONFIG_DIR unset, which is NOT the same as
// dir = "~/.claude": Claude Code reads the bare "Claude Code-credentials" item
// in the first case and a suffixed one in the second, and keeps .claude.json in
// the home directory in the first case and inside ~/.claude in the second.
//
// Declaring both is a config error (lane 10 security review; D12 allowed it
// with a warning): they share ~/.claude and its transcripts. So is any pair
// of profiles on one folder, or one inside another's (validateFolders). Only
// one profile may omit dir.
type Profile struct {
	Name string `toml:"name"`
	Dir  string `toml:"dir"`
	// Pool is, as declared, the profile's own pool (D35: a pool named after
	// the profile, used by it alone). In EffectiveProfiles it is every
	// account the profile may use, its tiers in order.
	Pool []string `toml:"pool"`
	// Pools names the [[pool]] blocks the profile uses, in the order it uses
	// them (D31, D33). A profile sets Pool or Pools, not both.
	Pools []string `toml:"pools"`
	// Threshold overrides (D4). Zero means unset: fall back to the global.
	SwitchAt       float64 `toml:"switch_at"`
	SwitchAtWeekly float64 `toml:"switch_at_weekly"`
	HardFloor      float64 `toml:"hard_floor"`
	// LandingMargin overrides the global landing_margin. A pointer, because
	// zero is a real override (no margin) rather than "unset".
	LandingMargin *float64 `toml:"landing_margin"`
	// Prefer overrides the global prefer (IMPROVEMENTS F1). "" inherits it.
	Prefer string `toml:"prefer"`
	// Models overrides the global models list (IMPROVEMENTS I6). Nil
	// inherits it; an empty list counts no model's limit in this profile.
	Models []string `toml:"models"`
	// Chrome is the Chrome profile folder ("Default", "Profile 2") Claude in
	// Chrome is used from in this profile (IMPROVEMENTS C2), for accounts
	// with no Chrome profile of their own. Empty: Chrome's last-used one.
	Chrome string `toml:"chrome"`
	// Paths are the folders this profile is picked for (IMPROVEMENTS F4):
	// globs, see paths.go. `cs run` with no name and `cs profile which`
	// read them; two profiles' paths may not overlap.
	Paths []string `toml:"paths"`

	// FromEnv marks the implicit profile of a config with no [[profile]]
	// blocks. Its dir is whatever this process's CLAUDE_CONFIG_DIR says, which
	// is today's single-profile behaviour. Never set on a declared profile.
	FromEnv bool `toml:"-"`

	// Set by EffectiveProfiles only. Tiers is the accounts of each pool the
	// profile uses, in its order (D33); PoolNames names them, "" for the
	// accounts no pool lists, which the profile named default uses last (D6).
	Tiers     [][]string `toml:"-"`
	PoolNames []string   `toml:"-"`
}

// Pool is a named group of accounts that profiles list to use (D31). An
// account is in at most one pool (D32).
//
//	[[pool]]
//	name     = "team"
//	accounts = ["team-a", "team-b"]
type Pool struct {
	Name     string   `toml:"name"`
	Accounts []string `toml:"accounts"`
}

// PoolNamed is the [[pool]] block with this name.
func (c *Config) PoolNamed(name string) (Pool, bool) {
	for _, p := range c.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return Pool{}, false
}

// PoolOf names the pool an account is in: a [[pool]] block, or the profile
// whose own pool lists it (D35). False for an account no pool lists.
func (c *Config) PoolOf(accountID string) (string, bool) {
	for _, p := range c.Pools {
		for _, id := range p.Accounts {
			if id == accountID {
				return p.Name, true
			}
		}
	}
	for _, in := range c.Profiles {
		for _, id := range in.Pool {
			if id == accountID {
				return in.Name, true
			}
		}
	}
	return "", false
}

// ProfilesOf names every profile that may use an account, in declaration
// order. Since D31 there can be several; one at a time holds it (§3).
func (c *Config) ProfilesOf(accountID string) []string {
	var out []string
	for _, in := range c.EffectiveProfiles() {
		for _, id := range in.Pool {
			if id == accountID {
				out = append(out, in.Name)
				break
			}
		}
	}
	return out
}

// InPool reports whether profile may use an account.
func (c *Config) InPool(profile, accountID string) bool {
	in, ok := c.ProfileNamed(profile)
	if !ok {
		return false
	}
	for _, id := range in.Pool {
		if id == accountID {
			return true
		}
	}
	return false
}

// SubscribersOf names the profiles that list a [[pool]] block.
func (c *Config) SubscribersOf(pool string) []string {
	var out []string
	for _, in := range c.Profiles {
		for _, p := range in.Pools {
			if p == pool {
				out = append(out, in.Name)
				break
			}
		}
	}
	return out
}

// EffectiveProfiles is every profile with its effective pool: the declared
// ones, each with the accounts of the pools it uses in its order (D31, D33)
// — its own pool (D35) or the [[pool]] blocks it lists — and, for "default",
// each account no pool names after them (D6); or, with none declared, one
// implicit "default" holding every account and resolving its dir from the
// environment.
func (c *Config) EffectiveProfiles() []Profile {
	if len(c.Profiles) == 0 {
		pool := make([]string, 0, len(c.Accounts))
		for _, a := range c.Accounts {
			pool = append(pool, a.ID)
		}
		return []Profile{{Name: DefaultProfile, Pool: pool, FromEnv: true,
			Tiers: [][]string{pool}, PoolNames: []string{""}}}
	}
	listed := map[string]bool{}
	for _, in := range c.Profiles {
		for _, id := range in.Pool {
			listed[id] = true
		}
	}
	for _, p := range c.Pools {
		for _, id := range p.Accounts {
			listed[id] = true
		}
	}
	out := make([]Profile, len(c.Profiles))
	for i, in := range c.Profiles {
		var tiers [][]string
		var names []string
		if len(in.Pool) > 0 {
			tiers = append(tiers, append([]string(nil), in.Pool...))
			names = append(names, in.Name)
		}
		for _, name := range in.Pools {
			if p, ok := c.PoolNamed(name); ok {
				tiers = append(tiers, append([]string(nil), p.Accounts...))
				names = append(names, name)
			}
		}
		if in.Name == DefaultProfile {
			var stray []string
			for _, a := range c.Accounts {
				if !listed[a.ID] {
					stray = append(stray, a.ID)
				}
			}
			if len(stray) > 0 {
				tiers = append(tiers, stray)
				names = append(names, "")
			}
		}
		in.Pool = nil
		for _, t := range tiers {
			in.Pool = append(in.Pool, t...)
		}
		in.Tiers, in.PoolNames = tiers, names
		out[i] = in
	}
	return out
}

// ProfileNames lists every effective profile's name, in declaration order.
func (c *Config) ProfileNames() []string {
	var out []string
	for _, in := range c.EffectiveProfiles() {
		out = append(out, in.Name)
	}
	return out
}

// ProfileNamed is the effective profile with this name.
func (c *Config) ProfileNamed(name string) (Profile, bool) {
	for _, in := range c.EffectiveProfiles() {
		if in.Name == name {
			return in, true
		}
	}
	return Profile{}, false
}

// ProfileOf names the first profile that may use an account. Since D31
// several can: anything that decides on it asks ProfilesOf or InPool.
func (c *Config) ProfileOf(accountID string) (string, bool) {
	for _, in := range c.EffectiveProfiles() {
		for _, id := range in.Pool {
			if id == accountID {
				return in.Name, true
			}
		}
	}
	return "", false
}

func (c *Config) declared(name string) Profile {
	for _, in := range c.Profiles {
		if in.Name == name {
			return in
		}
	}
	return Profile{}
}

func orGlobal(override, global float64) float64 {
	if override > 0 {
		return override
	}
	return global
}

// SwitchAtFor is a profile's effective session trigger. An unknown name gets
// the global value.
func (c *Config) SwitchAtFor(profile string) float64 {
	return orGlobal(c.declared(profile).SwitchAt, c.SwitchAt)
}

// SwitchAtWeeklyFor is a profile's effective weekly trigger.
func (c *Config) SwitchAtWeeklyFor(profile string) float64 {
	return orGlobal(c.declared(profile).SwitchAtWeekly, c.SwitchAtWeekly)
}

// HardFloorFor is a profile's effective hard floor.
func (c *Config) HardFloorFor(profile string) float64 {
	return orGlobal(c.declared(profile).HardFloor, c.HardFloor)
}

// LandingMarginFor is a profile's effective landing margin.
func (c *Config) LandingMarginFor(profile string) float64 {
	if m := c.declared(profile).LandingMargin; m != nil {
		return *m
	}
	return c.Margin()
}

// PreferFor is a profile's effective prefer (IMPROVEMENTS F1).
func (c *Config) PreferFor(profile string) string {
	if p := c.declared(profile).Prefer; p != "" {
		return p
	}
	return c.Preference()
}

// TriggerForProfile is TriggerFor with a profile's overrides applied.
func (c *Config) TriggerForProfile(profile, window string) float64 {
	return c.ForProfile(profile).TriggerFor(window)
}

// ForProfile is a shallow copy of the config with a profile's thresholds in
// place of the global ones, so code that takes a *Config (the policy engine)
// sees the effective values without knowing profiles exist.
//
// The copy is read-only: it shares Accounts, Priority, Projects and Profiles
// with the original, so writing through it changes both. Never Write or
// mutate it.
func (c *Config) ForProfile(profile string) *Config {
	cp := *c
	cp.SwitchAt = c.SwitchAtFor(profile)
	cp.SwitchAtWeekly = c.SwitchAtWeeklyFor(profile)
	cp.HardFloor = c.HardFloorFor(profile)
	m := c.LandingMarginFor(profile)
	cp.LandingMargin = &m
	cp.Models = c.ModelsFor(profile)
	cp.Prefer = c.PreferFor(profile)
	return &cp
}

// ChromeFor is the Chrome profile folder a profile names, "" when none
// (Chrome's last-used profile then applies). There is no global value.
func (c *Config) ChromeFor(profile string) string { return c.declared(profile).Chrome }

// ModelsFor is a profile's effective models list: its own when it declares
// one, even empty, else the global one.
func (c *Config) ModelsFor(profile string) []string {
	if in := c.declared(profile); in.Models != nil {
		return in.Models
	}
	return c.Models
}

// validatePools checks the [[pool]] blocks (D31, D32) and returns, for each
// account they list, the pool that lists it.
func (c *Config) validatePools(accounts map[string]Account) (map[string]string, error) {
	poolOf := map[string]string{}
	seen := map[string]bool{}
	for _, p := range c.Pools {
		if p.Name == "" {
			return nil, fmt.Errorf("every [[pool]] needs a name")
		}
		if err := ValidName("pool name", p.Name); err != nil {
			return nil, err
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("pool %q is declared twice", p.Name)
		}
		seen[p.Name] = true
		for _, id := range p.Accounts {
			if _, ok := accounts[id]; !ok {
				return nil, fmt.Errorf("pool %q names unknown account %q", p.Name, id)
			}
			if other, ok := poolOf[id]; ok {
				if other == p.Name {
					return nil, fmt.Errorf("pool %q lists account %q twice", p.Name, id)
				}
				return nil, fmt.Errorf("account %q is in both pool %q and pool %q; an account "+
					"belongs to one pool (profiles share a pool by listing it)", id, other, p.Name)
			}
			poolOf[id] = p.Name
		}
	}
	return poolOf, nil
}

func (c *Config) validateProfiles() error {
	accounts := map[string]Account{}
	for _, a := range c.Accounts {
		accounts[a.ID] = a
	}
	declaredPool, err := c.validatePools(accounts)
	if err != nil {
		return err
	}
	if len(c.Profiles) == 0 {
		return nil
	}
	names := map[string]bool{}
	unsetBy := "" // the profile that omits dir
	poolOwner := map[string]string{}
	for _, in := range c.Profiles {
		if in.Name == "" {
			return fmt.Errorf("every [[profile]] needs a name")
		}
		if names[in.Name] {
			return fmt.Errorf("profile %q is declared twice", in.Name)
		}
		names[in.Name] = true

		// Two profiles without a dir are one profile to Claude Code: they
		// read the same bare keychain item, so one credential would be live in
		// both. Two that merely share a directory (no dir and "~/.claude", one
		// dir spelled two ways, a link) have separate items but share their
		// files, which validateFolders refuses.
		if in.Dir == "" {
			if unsetBy != "" {
				return fmt.Errorf("profiles %q and %q both omit dir; only one profile can be "+
					"the one Claude Code runs with CLAUDE_CONFIG_DIR unset", unsetBy, in.Name)
			}
			unsetBy = in.Name
		} else if !relativeOK(in.Dir) {
			// Relative to what? The CLI's working directory and the launchd
			// daemon's differ, so the two would resolve it to different
			// folders and disagree about the profile (lane 10 re-review).
			return fmt.Errorf("profile %q: dir %q is relative, so it would name a different folder "+
				"for each process that reads it; write it absolute (/…) or from your home (~/…)",
				in.Name, in.Dir)
		}

		if len(in.Pool) > 0 && len(in.Pools) > 0 {
			return fmt.Errorf("profile %q sets both pool and pools; use pools, and put its own "+
				"accounts in a [[pool]] it lists", in.Name)
		}
		if len(in.Pool) > 0 {
			if _, clash := c.PoolNamed(in.Name); clash {
				return fmt.Errorf("profile %q has its own pool, which is named after it, and a "+
					"[[pool]] is also named %q; rename one", in.Name, in.Name)
			}
		}
		for _, id := range in.Pool {
			if _, ok := accounts[id]; !ok {
				return fmt.Errorf("profile %q: pool names unknown account %q", in.Name, id)
			}
			if other, ok := declaredPool[id]; ok {
				return fmt.Errorf("account %q is in both pool %q and profile %q's own pool; an "+
					"account belongs to one pool (profiles share a pool by listing it)", id, other, in.Name)
			}
			if other, ok := poolOwner[id]; ok {
				if other == in.Name {
					return fmt.Errorf("profile %q lists account %q twice", in.Name, id)
				}
				return fmt.Errorf("account %q is in the own pools of both profile %q and profile %q; "+
					"an account belongs to one pool: to share it, put it in a [[pool]] both "+
					"profiles list", id, other, in.Name)
			}
			poolOwner[id] = in.Name
		}
		listedPools := map[string]bool{}
		for _, name := range in.Pools {
			if _, ok := c.PoolNamed(name); !ok {
				return fmt.Errorf("profile %q lists pool %q, which no [[pool]] block declares", in.Name, name)
			}
			if listedPools[name] {
				return fmt.Errorf("profile %q lists pool %q twice", in.Name, name)
			}
			listedPools[name] = true
		}

		for _, o := range []struct {
			key string
			v   float64
		}{{"switch_at", in.SwitchAt}, {"switch_at_weekly", in.SwitchAtWeekly}, {"hard_floor", in.HardFloor}} {
			if o.v < 0 || o.v > 100 {
				return fmt.Errorf("profile %q: %s must be between 0 and 100, got %v", in.Name, o.key, o.v)
			}
		}
		if err := validModels(fmt.Sprintf("profile %q: models", in.Name), in.Models); err != nil {
			return err
		}
		if err := validPrefer(fmt.Sprintf("profile %q: prefer", in.Name), in.Prefer); err != nil {
			return err
		}
		if in.Chrome != "" {
			if err := ValidChromeFolder(in.Chrome); err != nil {
				return fmt.Errorf("profile %q: chrome: %v", in.Name, err)
			}
		}
		if m := in.LandingMargin; m != nil && (*m < 0 || *m > MaxLandingMargin) {
			return fmt.Errorf("profile %q: landing_margin must be between 0 and %g, got %v",
				in.Name, MaxLandingMargin, *m)
		}
		if at, floor := c.SwitchAtFor(in.Name), c.HardFloorFor(in.Name); floor < at {
			return fmt.Errorf("profile %q: hard_floor (%v) must be at or above switch_at (%v)",
				in.Name, floor, at)
		}
	}

	if err := c.validateFolders(); err != nil {
		return err
	}
	if err := c.validatePaths(); err != nil {
		return err
	}

	if !names[DefaultProfile] {
		var stray []string
		for _, a := range c.Accounts {
			if _, ok := poolOwner[a.ID]; ok {
				continue
			}
			if _, ok := declaredPool[a.ID]; !ok && a.IsEnabled() {
				stray = append(stray, a.ID)
			}
		}
		if len(stray) > 0 {
			return fmt.Errorf("account(s) %s are in no profile's pool, and there is no profile "+
				"named %q for them to join; add them to a pool, declare a %q profile, or "+
				"set enabled = false", strings.Join(quoteAll(stray), ", "), DefaultProfile, DefaultProfile)
		}
	}
	return nil
}

// profileWarnings is the per-profile part of Warnings: the hard_floor check
// for each profile that overrides either side of it. (Two profiles on one
// directory were D12's warning here; since the lane 10 security review they
// are a config error, validateFolders.)
func (c *Config) profileWarnings() []string {
	var out []string
	if len(c.Profiles) > 0 {
		for _, p := range c.Pools {
			if len(p.Accounts) > 0 && len(c.SubscribersOf(p.Name)) == 0 {
				out = append(out, fmt.Sprintf("pool %q is listed by no profile, so %s rotate nowhere; "+
					"add it to a profile's pools", p.Name, strings.Join(quoteAll(p.Accounts), ", ")))
			}
		}
	}
	for _, in := range c.Profiles {
		weekly, floor := c.SwitchAtWeeklyFor(in.Name), c.HardFloorFor(in.Name)
		if in.SwitchAtWeekly == 0 && in.HardFloor == 0 {
			continue // the global warning already covers it
		}
		if floor < weekly {
			out = append(out, fmt.Sprintf("profile %q: hard_floor (%g) is below switch_at_weekly "+
				"(%g), so every weekly rotation there counts as forced", in.Name, floor, weekly))
		}
	}
	return out
}

func quoteAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// writeProfiles prints the declared blocks as written. Effective pools are
// not written: an unlisted account joining default is a rule, and writing it
// out would turn it into a listing the person never made.
func (c *Config) writeProfiles(b *strings.Builder) {
	for _, p := range c.Pools {
		b.WriteString("\n[[pool]]\n")
		fmt.Fprintf(b, "name     = %q\n", p.Name)
		fmt.Fprintf(b, "accounts = %s\n", tomlStrings(p.Accounts))
	}
	if len(c.Profiles) == 0 {
		return
	}
	b.WriteString("\n# Claude Code profiles. Each rotates over the pools it lists, in order; an\n")
	b.WriteString("# account is in one pool, and profiles that list one pool share it, one at a\n")
	b.WriteString("# time. Accounts in no pool join the profile named \"default\".\n")
	b.WriteString("# dir is CLAUDE_CONFIG_DIR exactly as you launch Claude Code with it;\n")
	b.WriteString("# no dir means CLAUDE_CONFIG_DIR unset, which is not the same as \"~/.claude\".\n")
	b.WriteString("# Two profiles may not share a folder, or nest one inside another.\n")
	for _, in := range c.Profiles {
		b.WriteString("\n[[profile]]\n")
		fmt.Fprintf(b, "name = %q\n", in.Name)
		if in.Dir != "" {
			fmt.Fprintf(b, "dir  = %q\n", in.Dir)
		} else {
			b.WriteString("# no dir: Claude Code runs with CLAUDE_CONFIG_DIR unset\n")
		}
		if len(in.Pool) > 0 {
			fmt.Fprintf(b, "pool = %s\n", tomlStrings(in.Pool))
		}
		if len(in.Pools) > 0 {
			fmt.Fprintf(b, "pools = %s\n", tomlStrings(in.Pools))
		}
		if in.SwitchAt > 0 {
			fmt.Fprintf(b, "switch_at        = %g\n", in.SwitchAt)
		}
		if in.SwitchAtWeekly > 0 {
			fmt.Fprintf(b, "switch_at_weekly = %g\n", in.SwitchAtWeekly)
		}
		if in.HardFloor > 0 {
			fmt.Fprintf(b, "hard_floor       = %g\n", in.HardFloor)
		}
		if in.LandingMargin != nil {
			fmt.Fprintf(b, "landing_margin   = %g\n", *in.LandingMargin)
		}
		// Nil inherits the global list; an empty one turns it off here, so
		// the two are written differently.
		if in.Models != nil {
			fmt.Fprintf(b, "models           = %s\n", tomlStrings(in.Models))
		}
		if in.Prefer != "" {
			fmt.Fprintf(b, "prefer           = %q\n", in.Prefer)
		}
		if in.Chrome != "" {
			fmt.Fprintf(b, "chrome           = %q\n", in.Chrome)
		}
		if len(in.Paths) > 0 {
			fmt.Fprintf(b, "paths            = %s\n", tomlStrings(in.Paths))
		}
	}
}
