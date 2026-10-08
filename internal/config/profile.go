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
	Name string   `toml:"name"`
	Dir  string   `toml:"dir"`
	Pool []string `toml:"pool"`
	// Threshold overrides (D4). Zero means unset: fall back to the global.
	SwitchAt       float64 `toml:"switch_at"`
	SwitchAtWeekly float64 `toml:"switch_at_weekly"`
	HardFloor      float64 `toml:"hard_floor"`
	// LandingMargin overrides the global landing_margin. A pointer, because
	// zero is a real override (no margin) rather than "unset".
	LandingMargin *float64 `toml:"landing_margin"`
	// Models overrides the global models list (IMPROVEMENTS I6). Nil
	// inherits it; an empty list counts no model's limit in this profile.
	Models []string `toml:"models"`
	// Chrome is the Chrome profile folder ("Default", "Profile 2") Claude in
	// Chrome is used from in this profile (IMPROVEMENTS C2), for accounts
	// with no Chrome profile of their own. Empty: Chrome's last-used one.
	Chrome string `toml:"chrome"`

	// FromEnv marks the implicit profile of a config with no [[profile]]
	// blocks. Its dir is whatever this process's CLAUDE_CONFIG_DIR says, which
	// is today's single-profile behaviour. Never set on a declared profile.
	FromEnv bool `toml:"-"`
}

// EffectiveProfiles is every profile with its effective pool: the declared
// ones, with each account no pool names added to "default" (D6) after the
// accounts it lists; or, with none declared, one implicit "default" holding
// every account and resolving its dir from the environment.
func (c *Config) EffectiveProfiles() []Profile {
	if len(c.Profiles) == 0 {
		pool := make([]string, 0, len(c.Accounts))
		for _, a := range c.Accounts {
			pool = append(pool, a.ID)
		}
		return []Profile{{Name: DefaultProfile, Pool: pool, FromEnv: true}}
	}
	listed := map[string]bool{}
	for _, in := range c.Profiles {
		for _, id := range in.Pool {
			listed[id] = true
		}
	}
	out := make([]Profile, len(c.Profiles))
	for i, in := range c.Profiles {
		in.Pool = append([]string(nil), in.Pool...)
		if in.Name == DefaultProfile {
			for _, a := range c.Accounts {
				if !listed[a.ID] {
					in.Pool = append(in.Pool, a.ID)
				}
			}
		}
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

// ProfileOf names the profile whose pool holds an account.
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

func (c *Config) validateProfiles() error {
	if len(c.Profiles) == 0 {
		return nil
	}
	accounts := map[string]Account{}
	for _, a := range c.Accounts {
		accounts[a.ID] = a
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

		for _, id := range in.Pool {
			if _, ok := accounts[id]; !ok {
				return fmt.Errorf("profile %q: pool names unknown account %q", in.Name, id)
			}
			if other, ok := poolOwner[id]; ok {
				if other == in.Name {
					return fmt.Errorf("profile %q lists account %q twice", in.Name, id)
				}
				return fmt.Errorf("account %q is in the pools of both profile %q and profile %q; "+
					"pools must not overlap, since a credential live in two profiles is revoked "+
					"by whichever refreshes it first", id, other, in.Name)
			}
			poolOwner[id] = in.Name
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

	if !names[DefaultProfile] {
		var stray []string
		for _, a := range c.Accounts {
			if _, ok := poolOwner[a.ID]; !ok && a.IsEnabled() {
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
	if len(c.Profiles) == 0 {
		return
	}
	b.WriteString("\n# Claude Code profiles. Each rotates only within its own pool; pools must\n")
	b.WriteString("# not overlap. Accounts in no pool join the profile named \"default\".\n")
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
		if in.Chrome != "" {
			fmt.Fprintf(b, "chrome           = %q\n", in.Chrome)
		}
	}
}
