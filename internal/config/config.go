// Package config loads claudeswitch's declarative account configuration.
//
// An invalid config must never take down a running daemon: Load returns an
// error and the caller keeps its last good copy.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Defaults are the tuning surface. They are settled decisions (see
// BUILD_PROMPT.md) but must remain configurable and visible in `status`.
const (
	DefaultSwitchAt = 85.0 // session (5-hour) utilization % at which we rotate away
	// DefaultSwitchAtWeekly is much higher than the session trigger on purpose.
	// The two windows cost different things to spend: 85% of a 5-hour window is
	// nearly gone and refills the same afternoon, while 85% of a weekly window
	// still holds days of work. Judging both by one number retired accounts that
	// had plenty left.
	//
	// The remaining margin is small but not thin: the policy engine projects
	// PollHot + MaxSwitchWait ahead at the observed burn rate, so a window is
	// rotated out of before it is spent rather than after.
	DefaultSwitchAtWeekly = 98.0
	// DefaultHardFloor stays above both triggers. Below the weekly one it would
	// be met by every weekly rotation, which silently turns off both the
	// anti-flap cooldown and the preference for swapping between turns.
	DefaultHardFloor = 99.0 // above this, swap mid-turn rather than wait for idle
	DefaultReserve   = 70.0 // personal is ineligible for overflow above this
	// DefaultHotThreshold is where close watching begins, as a percentage of
	// whichever window is worse. It is far below either trigger on purpose:
	// watching starts before the decision matters. Raise it when the active
	// account's own allowance is under pressure — at 20-second polling one
	// account can spend more than the whole call budget (2026-09-17).
	DefaultHotThreshold = 60.0
)

type Account struct {
	ID    string `toml:"id"`
	Label string `toml:"label"`
	Scope string `toml:"scope"`
	// AccountUUID pins this entry to a Claude SEAT — the thing that actually owns
	// a quota pool. Prefer it to OrgID: a team organization has one seat per
	// member, each with separate limits, so an organization does not identify an
	// account (ground truth §32).
	AccountUUID string `toml:"account_uuid"`
	// OrgID is the older, coarser pin. Kept for entries that predate seat
	// identity and for display; it is not sufficient on its own.
	OrgID   string  `toml:"org_id"`
	Reserve float64 `toml:"reserve"` // 0 means "no reserve"
	Enabled *bool   `toml:"enabled"`
	// Comment is written above the entry by `cs setup`. It is not read back.
	Comment string `toml:"-"`
}

func (a Account) IsEnabled() bool { return a.Enabled == nil || *a.Enabled }

// CallsPerWindow is how many usage-API calls this cadence needs per five
// minutes: the active account, plus every other configured account at the idle
// rate. Written out because the trade-off is not obvious — an extra account
// costs budget even when it is doing nothing.
func (c *Config) CallsPerWindow() float64 {
	const window = 5 * time.Minute
	n := 0
	for _, a := range c.Accounts {
		if a.IsEnabled() {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	calls := float64(window) / float64(c.PollActive.Duration)
	calls += float64(n-1) * float64(window) / float64(c.PollIdle.Duration)
	return calls
}

// RefreshEnabled reports whether vaulted credentials should be kept alive.
func (c *Config) RefreshEnabled() bool { return c.AutoRefresh == nil || *c.AutoRefresh }

// Name is the account's single name. It is always the id.
func (a Account) Name() string { return a.ID }

// Seat is the quota pool this entry is pinned to: one person within one
// organization. Both halves are required, because neither alone identifies a
// pool — two people in one organization have separate quota (different
// subscriptions and plans), and one person in two organizations likewise.
// Empty means unpinned, which the guards treat as "cannot verify".
// SeatOf is the quota pool an account id names, or "" when the config has not
// pinned one. Callers that are about to decide whose credential they are
// holding need this rather than the organization on its own.
func (c *Config) SeatOf(accountID string) string {
	for _, a := range c.Accounts {
		if a.ID == accountID {
			return a.Seat()
		}
	}
	return ""
}

// AccountBySeat is the reverse of SeatOf: which configured account owns this
// quota pool, or "" if none does. Used when the live credential turns out to
// belong to someone other than who state believed — the seat is the answer, and
// this turns it back into a name we can act on.
func (c *Config) AccountBySeat(seat string) string {
	if seat == "" {
		return ""
	}
	for _, a := range c.Accounts {
		if a.Seat() == seat {
			return a.ID
		}
	}
	return ""
}

// TriggerFor is the utilization at which this window is considered spent. The
// two windows are judged against their own thresholds rather than a single one,
// because a 5-hour window that refills this afternoon and a weekly one that is
// gone until next week are not the same kind of loss.
func (c *Config) TriggerFor(window string) float64 {
	if window == usage.SevenDayKey && c.SwitchAtWeekly > 0 {
		return c.SwitchAtWeekly
	}
	// Unset falls back to the session trigger rather than to zero. A zero
	// threshold is not a threshold — it would mark every account over the line
	// the moment it had used anything at all, and leave nothing to rotate to.
	// Config.Load fills the default, but a Config built in code has no such
	// guarantee and must not be able to express that.
	return c.SwitchAt
}

func (a Account) Seat() string {
	if a.AccountUUID == "" || a.OrgID == "" {
		return ""
	}
	return a.AccountUUID + "@" + a.OrgID
}

type Config struct {
	SwitchAt float64 `toml:"switch_at"`
	// SwitchAtWeekly is the same idea for the seven-day window. Kept separate
	// because the two windows recover on completely different timescales.
	SwitchAtWeekly float64 `toml:"switch_at_weekly"`
	HardFloor      float64 `toml:"hard_floor"`
	// HotThreshold is the utilization at which the active account is polled at
	// PollHot rather than PollActive.
	HotThreshold float64  `toml:"hot_threshold"`
	SwitchWhen   string   `toml:"switch_when"`
	Cooldown     Duration `toml:"cooldown"`
	// MaxSwitchWait bounds how long a wanted switch will hold out for an idle
	// gap. Preferring to swap between turns is right; waiting indefinitely is
	// not, because during continuous heavy use — precisely when a limit is
	// approaching — the transcript never goes quiet and the switch would be
	// deferred all the way to the hard floor. Observed 2026-09-10 at 85%.
	MaxSwitchWait Duration `toml:"max_switch_wait"`

	// AutoRefresh keeps vaulted credentials alive. Nil means on.
	//
	// It runs in dry-run too: dry-run means "do not rotate", and letting every
	// stored credential expire while watching would be a strange reading of
	// that. It does mutate credentials — a refresh revokes the previous token —
	// so it can be turned off outright.
	AutoRefresh *bool `toml:"auto_refresh"`
	// RefreshWindow is how close to expiry a token gets before it is renewed.
	RefreshWindow Duration `toml:"refresh_window"`
	// RefreshProbe refreshes each idle account this often even when its token is
	// nowhere near expiry, so a dead refresh token surfaces on its own schedule
	// rather than at the moment the account is needed. Zero disables it.
	RefreshProbe Duration `toml:"refresh_probe"`

	// Polling cadence. The usage endpoint's limit is a burst allowance that
	// refills rather than a sustained cap — measured at one call every 20
	// seconds for eleven consecutive calls without a refusal — so a brisk
	// cadence is fine and the earlier four-minute default was self-imposed.
	// validate() still does the arithmetic, because a tight enough loop can
	// trip the burst guard and a locked-out daemon runs blind.
	PollActive Duration `toml:"poll_active"`
	// PollHot is used when the active account is near the trigger or burning
	// fast, where a stale reading actually costs something.
	PollHot Duration `toml:"poll_hot"`
	// PollIdle is for accounts nobody is using. Their utilization can only fall,
	// so they need checking just often enough to notice a recovery.
	PollIdle Duration `toml:"poll_idle"`
	// APIBudget is how many usage calls per five minutes claudeswitch allows
	// itself, across every process. The measured ceiling is about 5; the default
	// leaves room because a lockout is expensive.
	APIBudget int       `toml:"api_budget"`
	Priority  []string  `toml:"priority"`
	Accounts  []Account `toml:"account"`

	// Projects restricts which account scopes may serve which directories.
	//
	// Without this, `scope` is a label and nothing more: work quota can fund
	// personal work and vice versa, silently. For anyone whose employer cares
	// where their Claude usage is billed, that is the difference between a tool
	// they can use and one they cannot.
	Projects map[string]Project `toml:"project"`

	Path string `toml:"-"`
}

// Duration lets the TOML carry "10m" instead of a nanosecond count.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func DefaultPath() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "claudeswitch", "config.toml")
	}
	return "config.toml"
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	c := &Config{
		SwitchAt:       DefaultSwitchAt,
		SwitchAtWeekly: DefaultSwitchAtWeekly,
		HardFloor:      DefaultHardFloor,
		HotThreshold:   DefaultHotThreshold,
		SwitchWhen:     "idle",
		Cooldown:       Duration{10 * time.Minute},
		MaxSwitchWait:  Duration{30 * time.Second},
		RefreshWindow:  Duration{time.Hour},
		RefreshProbe:   Duration{24 * time.Hour},
		PollActive:     Duration{60 * time.Second},
		PollHot:        Duration{20 * time.Second},
		PollIdle:       Duration{10 * time.Minute},
		APIBudget:      12,
		Path:           path,
	}
	if _, err := toml.DecodeFile(path, c); err != nil {
		if os.IsNotExist(err) {
			return c, fmt.Errorf("no config at %s (run `claudeswitch init`): %w", path, err)
		}
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Validate is the exported entry point, so a caller changing a value can check
// it before writing rather than discovering the problem at next startup.
func (c *Config) Validate() error { return c.validate() }

// Warnings are configurations that load and run but do not do what their
// numbers suggest. They are not errors: refusing to start over one would lock
// someone out of their own accounts over a preference, and every combination
// here is one a person could deliberately want.
//
// validate() only ever compared hard_floor against the session trigger, so a
// floor beneath the weekly one passed in silence — and quietly disabled both
// the cooldown and the idle-gap preference for every weekly rotation, since
// each one clears the floor by definition.
func (c *Config) Warnings() []string {
	var out []string
	if c.SwitchAtWeekly > 0 && c.HardFloor < c.SwitchAtWeekly {
		out = append(out, fmt.Sprintf(
			"hard_floor (%g) is below switch_at_weekly (%g), so every weekly rotation "+
				"counts as forced: it skips the %s cooldown and swaps mid-turn rather "+
				"than at an idle gap. Raise hard_floor above %g to keep both.",
			c.HardFloor, c.SwitchAtWeekly, c.Cooldown.String(), c.SwitchAtWeekly))
	}
	return out
}

func (c *Config) validate() error {
	if c.SwitchAt <= 0 || c.SwitchAt > 100 {
		return fmt.Errorf("switch_at must be between 0 and 100, got %v", c.SwitchAt)
	}
	if c.SwitchAtWeekly <= 0 || c.SwitchAtWeekly > 100 {
		return fmt.Errorf("switch_at_weekly must be between 0 and 100, got %v", c.SwitchAtWeekly)
	}
	if c.HardFloor < c.SwitchAt {
		return fmt.Errorf("hard_floor (%v) must be at or above switch_at (%v)", c.HardFloor, c.SwitchAt)
	}
	if c.HotThreshold < 0 || c.HotThreshold > 100 {
		return fmt.Errorf("hot_threshold must be between 0 and 100, got %v", c.HotThreshold)
	}
	if c.RefreshWindow.Duration < 0 || c.RefreshProbe.Duration < 0 {
		return fmt.Errorf("refresh_window and refresh_probe cannot be negative")
	}
	if c.RefreshEnabled() && c.RefreshWindow.Duration == 0 {
		return fmt.Errorf("refresh_window must be set when auto_refresh is on " +
			"(a zero window would refresh on every check)")
	}
	// Poll cadence against the call budget. Getting this wrong is not a
	// cosmetic error: a daemon that exhausts the budget stops seeing anything
	// and keeps deciding on stale numbers.
	if c.APIBudget < 2 {
		return fmt.Errorf("api_budget must be at least 2 (one scheduled call, one reserved for swaps)")
	}
	if c.PollActive.Duration <= 0 || c.PollIdle.Duration <= 0 || c.PollHot.Duration <= 0 {
		return fmt.Errorf("poll_active, poll_hot and poll_idle must all be positive")
	}
	if used, avail := c.CallsPerWindow(), float64(c.APIBudget-1); used > avail {
		return fmt.Errorf(
			"this cadence needs %.1f calls per 5 minutes but only %.0f are available "+
				"(api_budget %d, one reserved for swaps).\n"+
				"  Slow poll_active (now %s) or poll_idle (now %s), or raise api_budget — "+
				"the burst ceiling measured around 15 per 5 minutes, and tripping it locks the daemon out for 5 minutes",
			used, avail, c.APIBudget, c.PollActive.Duration, c.PollIdle.Duration)
	}
	if c.SwitchWhen != "idle" && c.SwitchWhen != "immediate" {
		return fmt.Errorf("switch_when must be \"idle\" or \"immediate\", got %q", c.SwitchWhen)
	}
	seen := map[string]bool{}
	for _, a := range c.Accounts {
		if a.ID == "" {
			return fmt.Errorf("every [[account]] needs an id")
		}
		if a.Label != "" && a.Label != a.ID {
			return fmt.Errorf("account %q sets label = %q; labels are no longer used and a "+
				"second name only causes confusion. Remove the label line", a.ID, a.Label)
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate account id %q", a.ID)
		}
		seen[a.ID] = true
		if a.Reserve < 0 || a.Reserve > 100 {
			return fmt.Errorf("account %q: reserve must be between 0 and 100", a.ID)
		}
	}
	for _, id := range c.Priority {
		if !seen[id] {
			return fmt.Errorf("priority names unknown account %q", id)
		}
	}
	// A project rule that no account can satisfy would silently strand that
	// directory, so say so at load time rather than at rotation time.
	scopes := map[string]bool{}
	for _, a := range c.Accounts {
		scopes[a.Scope] = true
	}
	for pattern, pr := range c.Projects {
		for _, e := range pr.Eligible {
			if e == "*" || scopes[e] {
				continue
			}
			return fmt.Errorf("project %q allows scope %q, but no configured account has it "+
				"(scopes in use: %s)", pattern, e, strings.Join(keysOf(scopes), ", "))
		}
	}
	return nil
}

// Ordered returns the enabled accounts in rotation order: those named in
// priority first, then any others in declaration order.
func (c *Config) Ordered() []Account {
	byID := map[string]Account{}
	for _, a := range c.Accounts {
		byID[a.ID] = a
	}
	var out []Account
	used := map[string]bool{}
	for _, id := range c.Priority {
		if a, ok := byID[id]; ok && a.IsEnabled() {
			out = append(out, a)
			used[id] = true
		}
	}
	for _, a := range c.Accounts {
		if !used[a.ID] && a.IsEnabled() {
			out = append(out, a)
		}
	}
	return out
}

// Write renders a config file. It is generated rather than templated so that
// `cs setup` produces exactly the accounts it vaulted, with their real seats
// pinned — the pins cannot be written by hand in advance, since you only learn
// an account's uuid by signing in to it.
func (c *Config) Write(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# claudeswitch — written by `cs setup`. Safe to edit.\n")
	b.WriteString("# Thresholds are the tuning surface; `cs status` shows them.\n\n")
	// Thresholds are written at their effective values, not their raw ones. A
	// Config built in code rather than loaded leaves them zero, and zero is not
	// a threshold anyone means — written out literally it produces a file that
	// will not load, which is the one thing a generated config must never do.
	or := func(v, def float64) float64 {
		if v <= 0 {
			return def
		}
		return v
	}
	fmt.Fprintf(&b, "switch_at       = %g     # rotate away at this much of the 5-hour window\n",
		or(c.SwitchAt, DefaultSwitchAt))
	fmt.Fprintf(&b, "switch_at_weekly = %g    # and at this much of the weekly one — a weekly window\n",
		or(c.SwitchAtWeekly, DefaultSwitchAtWeekly))
	b.WriteString("                         # spent is gone for days, a session one refills today\n")
	fmt.Fprintf(&b, "hard_floor      = %g     # above this, swap mid-turn rather than wait for idle\n",
		or(c.HardFloor, DefaultHardFloor))
	fmt.Fprintf(&b, "switch_when     = %q\n", c.SwitchWhen)
	fmt.Fprintf(&b, "hot_threshold   = %g     # poll every poll_hot above this, rather than poll_active\n",
		or(c.HotThreshold, DefaultHotThreshold))
	fmt.Fprintf(&b, "cooldown        = %q   # anti-flap\n", c.Cooldown.String())
	fmt.Fprintf(&b, "max_switch_wait = %q   # stop waiting for an idle gap after this\n", c.MaxSwitchWait.String())
	b.WriteString("\n# Keeping vaulted credentials alive. Refreshing revokes the previous token,\n")
	b.WriteString("# so a stored credential goes stale on its own without this.\n")
	fmt.Fprintf(&b, "refresh_window  = %q\n", c.RefreshWindow.String())
	fmt.Fprintf(&b, "refresh_probe   = %q   # catch a dead refresh token before you need the account\n",
		c.RefreshProbe.String())
	if c.AutoRefresh != nil {
		fmt.Fprintf(&b, "auto_refresh    = %t\n", *c.AutoRefresh)
	}
	b.WriteString("\n")

	// Polling is written only when set: a Config built in code (as `setup`
	// builds one) leaves it zero, and zero written out would be a real value
	// rather than "use the default". Everything `cs config` can change must be
	// written here, or changing one setting silently deletes the others.
	if c.PollActive.Duration > 0 || c.PollHot.Duration > 0 || c.PollIdle.Duration > 0 || c.APIBudget > 0 {
		b.WriteString("# Polling cadence and the usage API call budget.\n")
		if c.PollActive.Duration > 0 {
			fmt.Fprintf(&b, "poll_active     = %q\n", c.PollActive.String())
		}
		if c.PollHot.Duration > 0 {
			fmt.Fprintf(&b, "poll_hot        = %q\n", c.PollHot.String())
		}
		if c.PollIdle.Duration > 0 {
			fmt.Fprintf(&b, "poll_idle       = %q\n", c.PollIdle.String())
		}
		if c.APIBudget > 0 {
			fmt.Fprintf(&b, "api_budget      = %d\n", c.APIBudget)
		}
		b.WriteString("\n")
	}

	b.WriteString("# Rotation order. Earlier accounts are spent first.\n")
	b.WriteString("priority = [")
	for i, id := range c.Priority {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", id)
	}
	b.WriteString("]\n")

	for _, a := range c.Accounts {
		b.WriteString("\n")
		if a.Comment != "" {
			for _, line := range strings.Split(a.Comment, "\n") {
				fmt.Fprintf(&b, "# %s\n", line)
			}
		}
		b.WriteString("[[account]]\n")
		fmt.Fprintf(&b, "id           = %q\n", a.ID)
		if a.Enabled != nil {
			fmt.Fprintf(&b, "enabled      = %t\n", *a.Enabled)
		}
		if a.Scope != "" {
			fmt.Fprintf(&b, "scope        = %q\n", a.Scope)
		}
		if a.Reserve > 0 {
			fmt.Fprintf(&b, "reserve      = %g\n", a.Reserve)
		}
		if a.AccountUUID != "" {
			fmt.Fprintf(&b, "account_uuid = %q\n", a.AccountUUID)
		}
		if a.OrgID != "" {
			fmt.Fprintf(&b, "org_id       = %q\n", a.OrgID)
		}
	}

	patterns := make([]string, 0, len(c.Projects))
	for pattern := range c.Projects {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		pr := c.Projects[pattern]
		fmt.Fprintf(&b, "\n[project.%s]\n", tomlKey(pattern))
		if len(pr.Eligible) > 0 {
			fmt.Fprintf(&b, "eligible = %s\n", tomlStrings(pr.Eligible))
		}
		if len(pr.Prefer) > 0 {
			fmt.Fprintf(&b, "prefer   = %s\n", tomlStrings(pr.Prefer))
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tomlKey quotes a table key. A project key is a path, and a path is full of
// characters a bare TOML key cannot hold.
func tomlKey(k string) string {
	return strconv.Quote(k)
}

func tomlStrings(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// Project is a rule about which accounts may serve a directory.
type Project struct {
	// Eligible lists the account scopes allowed here. Empty means no
	// restriction.
	Eligible []string `toml:"eligible"`
	// Prefer orders scopes within the eligible set, ahead of the global
	// priority. Useful for "work first here, personal first there" without
	// forbidding either.
	Prefer []string `toml:"prefer"`
}

// expandHome resolves a leading ~ so rules can be written the way people think
// about their own directories.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// matchDir reports whether a directory falls under a pattern.
//
// Patterns are paths, optionally ending in "**" to include everything beneath.
// filepath.Match alone is not enough: it does not cross separators, so
// "~/work/**" would fail to match "~/work/a/b". A prefix test is both simpler
// and closer to what someone writing the rule means.
func matchDir(pattern, dir string) bool {
	pattern = filepath.Clean(expandHome(pattern))
	dir = filepath.Clean(dir)

	if strings.HasSuffix(pattern, string(filepath.Separator)+"**") || strings.HasSuffix(pattern, "**") {
		base := filepath.Clean(strings.TrimSuffix(strings.TrimSuffix(pattern, "**"), string(filepath.Separator)))
		return dir == base || strings.HasPrefix(dir, base+string(filepath.Separator))
	}
	if pattern == dir {
		return true
	}
	// A bare directory still covers what is inside it: "~/work" meaning only
	// that exact directory and not its contents would surprise everyone.
	if strings.HasPrefix(dir, pattern+string(filepath.Separator)) {
		return true
	}
	ok, err := filepath.Match(pattern, dir)
	return err == nil && ok
}

// ProjectFor returns the rule covering a directory, and whether one was found.
// The most specific matching pattern wins, so a rule for a subdirectory can
// override a broader one above it.
func (c *Config) ProjectFor(dir string) (Project, bool) {
	if dir == "" || len(c.Projects) == 0 {
		return Project{}, false
	}
	best, bestLen, found := Project{}, -1, false
	for pattern, pr := range c.Projects {
		if !matchDir(pattern, dir) {
			continue
		}
		if n := len(filepath.Clean(expandHome(pattern))); n > bestLen {
			best, bestLen, found = pr, n, true
		}
	}
	return best, found
}

// ScopeAllowed reports whether an account scope may serve a directory.
func (c *Config) ScopeAllowed(scope, dir string) bool {
	pr, ok := c.ProjectFor(dir)
	if !ok || len(pr.Eligible) == 0 {
		return true
	}
	for _, e := range pr.Eligible {
		if e == scope || e == "*" {
			return true
		}
	}
	return false
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
