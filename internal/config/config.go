// Package config loads claudeswitch's declarative account configuration.
//
// An invalid config must never take down a running daemon: Load returns an
// error and the caller keeps its last good copy.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
	// DefaultLandingMargin is how far below the session trigger a switch
	// target's 5-hour window must be (IMPROVEMENTS A1), so a rotation never
	// lands on an account it is about to rotate away from. The weekly window
	// has no margin: it only has to be under its trigger.
	DefaultLandingMargin = 10.0
	// MaxLandingMargin bounds it: past half the scale almost nothing
	// qualifies, and the margin would quietly turn rotation off.
	MaxLandingMargin = 50.0
	// DefaultBlindFailoverPolls is how many consecutive unreadable polls of the
	// active account end DESIGN 4.4's hold (IMPROVEMENTS A2). At poll_active 3m
	// that is about nine minutes (3 × 3m, the policy's age test). A 429 never
	// counts: the usage endpoint's refusals clear on their own in 10–15
	// minutes (GROUND_TRUTH §42) and say nothing about the account.
	DefaultBlindFailoverPolls = 3
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
// minutes: one active account per profile, plus every other configured
// account at the idle rate. Written out because the trade-off is not obvious —
// an extra account costs budget even when it is doing nothing.
//
// A profile counts only when its pool holds an enabled account: an empty
// pool (D13) never has anything active to poll. Hot polling is not counted;
// the shared budget throttles it (docs/PROFILES.md §5 Budget).
func (c *Config) CallsPerWindow() float64 {
	const window = 5 * time.Minute
	enabled := map[string]bool{}
	for _, a := range c.Accounts {
		if a.IsEnabled() {
			enabled[a.ID] = true
		}
	}
	n := len(enabled)
	if n == 0 {
		return 0
	}
	active := 0
	for _, in := range c.EffectiveProfiles() {
		for _, id := range in.Pool {
			if enabled[id] {
				active++
				break
			}
		}
	}
	if active == 0 {
		active = 1 // unreachable with an enabled account; kept so this never divides wrong
	}
	if active > n {
		active = n
	}
	calls := float64(active) * float64(window) / float64(c.PollActive.Duration)
	calls += float64(n-active) * float64(window) / float64(c.PollIdle.Duration)
	return calls
}

// DefaultPollHot is the hot polling cadence. UsageBurstCalls is roughly how
// many usage calls one account's allowance absorbs in a burst before refusing,
// recovering over 10-15 minutes (GROUND_TRUTH §42, multi-profile). At 20s a
// hot account emptied it in about 8 minutes.
const (
	DefaultPollHot  = 60 * time.Second
	UsageBurstCalls = 25
	// DefaultPollHotSetting is DefaultPollHot as a person would type it.
	DefaultPollHotSetting = "60s"
)

// HotLookahead is how far ahead hot polling looks: the account in use is
// polled every poll_hot only while it is moving and, at its current burn
// rate, would reach its trigger within this long (docs/DESIGN.md 4.3c). It
// also bounds a hot spell — at a steady burn, one lasts about this long
// before the account is rotated away — which is what the per-account
// arithmetic below charges for one.
const HotLookahead = 15 * time.Minute

// AccountRates is the per-account arithmetic of the cadence against one
// account's own usage allowance (usage.AccountBurst, usage.AccountRefill,
// GROUND_TRUTH §42). Rates are calls an hour.
type AccountRates struct {
	ActivePerHour, HotPerHour, IdlePerHour float64
	RefillPerHour                          float64
	// HotSpellNet is what one hot spell of HotLookahead spends beyond the
	// refill; Spare is what routine polling holds back for it (hot_reserve).
	HotSpellNet float64
	Spare       int
	// Unseen is unseen_calls_per_hour: what a live account is modelled as
	// losing to Claude Code's own reads.
	Unseen float64
	// PollActive is the poll_active that runs, and ActiveEvery how often the
	// account in use can actually be read in steady state: poll_active, or
	// slower when the live refill (RefillPerHour - Unseen) cannot sustain it.
	PollActive, ActiveEvery time.Duration
}

// OverdueAfter is how old the reading of the account in use may get before a
// routine read of it is overdue and may spend the hot reserve down to the swap
// reserve + 1: poll_active + 1/12 (3m15s at the 3m default). Routine reads at
// poll_active keep the full hot-reserve floor; after a hot spell has spent it,
// the overdue read keeps the reading under the daemon's stale-decision cap
// (min(3 × poll_active, 4m): 4m at the default) while the reserve rebuilds.
func OverdueAfter(pollActive time.Duration) time.Duration { return pollActive + pollActive/12 }

// ActiveStarved reports that the live refill, less unseen_calls_per_hour,
// cannot read the account in use even as often as a read becomes overdue, so
// its reading runs routinely older than poll_active by more than a quarter.
// At the defaults it is about every 2m9s against 2m, which is not.
func (r AccountRates) ActiveStarved() bool {
	return r.PollActive > 0 && r.ActiveEvery > OverdueAfter(r.PollActive)
}

// ActiveDrains and IdleDrains report a steady cadence faster than the refill:
// such an account empties however the machine-wide budget is set.
func (r AccountRates) ActiveDrains() bool { return r.ActivePerHour > r.RefillPerHour+1e-9 }
func (r AccountRates) IdleDrains() bool   { return r.IdlePerHour > r.RefillPerHour+1e-9 }

// HotDrains reports a hot spell that would spend more than the spare
// allowance; the excess would be deferred, so readings would be older than
// poll_hot when it matters.
func (r AccountRates) HotDrains() bool { return r.HotSpellNet > float64(r.Spare)+1e-9 }

// Drains is any of the three.
func (r AccountRates) Drains() bool { return r.ActiveDrains() || r.IdleDrains() || r.HotDrains() }

// AccountCadence computes AccountRates for this config.
func (c *Config) AccountCadence() AccountRates {
	perHour := func(d time.Duration) float64 {
		if d <= 0 {
			return 0
		}
		return float64(time.Hour) / float64(d)
	}
	// What runs: anything under the floor is raised to it on load.
	floor := func(d time.Duration) time.Duration {
		if d > 0 && d < MinPoll {
			return MinPoll
		}
		return d
	}
	r := AccountRates{
		ActivePerHour: perHour(floor(c.PollActive.Duration)),
		HotPerHour:    perHour(c.PollHot.Duration),
		IdlePerHour:   perHour(floor(c.PollIdle.Duration)),
		RefillPerHour: perHour(usage.AccountRefill),
		Spare:         c.HotReserveCalls(),
		Unseen:        c.UnseenPerHour(),
	}
	r.PollActive = floor(c.PollActive.Duration)
	r.ActiveEvery = r.PollActive
	if live := r.RefillPerHour - r.Unseen; live <= 0 {
		r.ActiveEvery = time.Duration(1<<62 - 1)
	} else if every := time.Duration(float64(time.Hour) / live); every > r.ActiveEvery {
		r.ActiveEvery = every
	}
	spell := HotLookahead.Hours()
	r.HotSpellNet = (r.HotPerHour - r.RefillPerHour) * spell
	if r.HotSpellNet < 0 {
		r.HotSpellNet = 0
	}
	return r
}

// SuggestedPollActive is what an error or doctor tells someone to set.
const SuggestedPollActive = "3m"

// HotDrainMinutes is how long a hot cadence takes to spend an account's burst
// allowance, and whether it is faster than the default — the threshold at
// which doctor and status warn. Zero means unset, which is the default.
func HotDrainMinutes(pollHot time.Duration) (int, bool) {
	if pollHot <= 0 || pollHot >= DefaultPollHot {
		return 0, false
	}
	return int((time.Duration(UsageBurstCalls) * pollHot).Round(time.Minute) / time.Minute), true
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

// Margin is the effective landing margin: the configured one, or the default.
func (c *Config) Margin() float64 {
	if c.LandingMargin == nil {
		return DefaultLandingMargin
	}
	return *c.LandingMargin
}

// CountsModel reports whether a model's weekly limit counts like the weekly
// window under this config (IMPROVEMENTS I6).
func (c *Config) CountsModel(name string) bool {
	for _, m := range c.Models {
		if strings.EqualFold(strings.TrimSpace(m), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// validModels refuses a blank model name, which would match nothing and so
// silently count nothing.
func validModels(key string, models []string) error {
	for _, m := range models {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("%s holds a blank model name; list display names such as the ones `cs status --detail` shows", key)
		}
	}
	return nil
}

// Ranges for the advanced model settings.
const (
	MaxHotReserve         = 15
	MaxUnseenCallsPerHour = 20.0
)

// HotReserveCalls is hot_reserve, or its default.
func (c *Config) HotReserveCalls() int {
	if c.HotReserve == nil {
		return usage.DefaultHotReserve
	}
	return *c.HotReserve
}

// UnseenPerHour is unseen_calls_per_hour, or its default.
func (c *Config) UnseenPerHour() float64 {
	if c.UnseenCallsPerHour == nil {
		return usage.DefaultLiveUnseenPerHour
	}
	return *c.UnseenCallsPerHour
}

// BlindPolls is the effective blind-failover threshold; zero means off.
func (c *Config) BlindPolls() int {
	if c.BlindFailoverPolls == nil {
		return DefaultBlindFailoverPolls
	}
	return *c.BlindFailoverPolls
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
	// HotThreshold is the floor for polling the active account at PollHot: it
	// must also be moving within reach of its trigger (DESIGN 4.3c).
	HotThreshold float64  `toml:"hot_threshold"`
	SwitchWhen   string   `toml:"switch_when"`
	Cooldown     Duration `toml:"cooldown"`
	// MaxSwitchWait bounds how long a wanted switch will hold out for an idle
	// gap. Preferring to swap between turns is right; waiting indefinitely is
	// not, because during continuous heavy use — precisely when a limit is
	// approaching — the transcript never goes quiet and the switch would be
	// deferred all the way to the hard floor. Observed 2026-09-10 at 85%.
	MaxSwitchWait Duration `toml:"max_switch_wait"`

	// LandingMargin is the room, in points below the session trigger, a switch
	// target's 5-hour window must have (IMPROVEMENTS A1). Nil means DefaultLandingMargin; zero
	// is a real value and turns the margin off. Read it through Margin().
	LandingMargin *float64 `toml:"landing_margin"`
	// BlindFailoverPolls is how many consecutive unreadable polls of the active
	// account make the daemon fail over to a healthy one (IMPROVEMENTS A2). Nil
	// means DefaultBlindFailoverPolls; zero turns failover off and restores
	// DESIGN 4.4's unconditional hold. Read it through BlindPolls().
	BlindFailoverPolls *int `toml:"blind_failover_polls"`
	// HotReserve and UnseenCallsPerHour are the per-account model's two
	// advanced numbers (owner decision 2026-10-07; DESIGN 4.3c): calls held
	// back from routine polling for a hot spell, and calls an hour a live
	// account is modelled as losing to Claude Code's own reads. Nil means
	// the default. Read them through HotReserveCalls and UnseenPerHour.
	HotReserve         *int     `toml:"hot_reserve"`
	UnseenCallsPerHour *float64 `toml:"unseen_calls_per_hour"`

	// Models names the models whose per-model weekly limits count like the
	// weekly window for triggering and eligibility (IMPROVEMENTS I6), matched
	// against limits[] scope.model.display_name without regard to case. Empty
	// means none: those limits are shown, never acted on. Per profile through
	// ForProfile; read it there, or through CountsModel.
	Models []string `toml:"models"`

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

	// Polling cadence. The usage endpoint's limit is a burst allowance per
	// ACCOUNT that refills (GROUND_TRUTH §42: ~24 calls, then refused for
	// 10–15 minutes). validate() does the arithmetic twice: per account
	// against that allowance (AccountCadence), and for the machine against
	// api_budget, because a locked-out daemon runs blind.
	PollActive Duration `toml:"poll_active"`
	// PollHot is used while the active account is moving and within reach
	// of its trigger (DESIGN 4.3c), where a stale reading actually costs
	// something.
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

	// Profiles are the declared [[profile]] blocks, exactly as written. Most
	// callers want EffectiveProfiles, which adds the implicit default and the
	// unlisted accounts that join it.
	Profiles []Profile `toml:"profile"`

	Path string `toml:"-"`
	// Hash is ContentHash of the bytes this config was decoded from, ""
	// for one built in code or read from no file. The daemon records the
	// hash of the config it runs, so a command that edited the config can
	// wait until the daemon runs exactly that edit (lane 12 re-review).
	Hash string `toml:"-"`

	// set names the top-level settings the file carries or `cs config`
	// changed (MarkSet); Write keeps those and leaves the rest to defaults.
	set map[string]bool
	// raised is each poll setting floorPolls raised, with what it was.
	raised map[string]time.Duration
	// legacy is what the file still carries of account scope and [project]
	// rules, removed in lane 16 (S1): read, ignored, warned about.
	legacy Legacy
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

// ContentHash identifies a config file's content: hex SHA-256.
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Defaults is the config with nothing set: every value a file that omits
// it gets. `cs config schema` reports its defaults from here, so the app and
// the loader cannot disagree.
func Defaults() *Config {
	return &Config{
		SwitchAt:       DefaultSwitchAt,
		SwitchAtWeekly: DefaultSwitchAtWeekly,
		HardFloor:      DefaultHardFloor,
		HotThreshold:   DefaultHotThreshold,
		SwitchWhen:     "idle",
		Cooldown:       Duration{10 * time.Minute},
		MaxSwitchWait:  Duration{30 * time.Second},
		RefreshWindow:  Duration{time.Hour},
		RefreshProbe:   Duration{24 * time.Hour},
		// Three minutes (owner decision 2026-10-07, superseding 2m): twenty
		// calls an hour on the account in use against a live refill of ~28
		// (GROUND_TRUTH §42, less unseen_calls_per_hour), so the hot reserve
		// rebuilds ~8 an hour after a spell. Faster buys nothing while it is
		// not moving, and while it is, poll_hot takes over (docs/DESIGN.md
		// 4.3c). The daemon's stale-decision cap is 4m to match.
		PollActive: Duration{3 * time.Minute},
		// 60s, not 20s: the usage endpoint allows about 25 calls per account in
		// a burst and takes 10-15 minutes to recover, so 20-second hot polling
		// emptied it in about 8 minutes (GROUND_TRUTH §42, multi-profile).
		PollHot:   Duration{DefaultPollHot},
		PollIdle:  Duration{10 * time.Minute},
		APIBudget: 12,
	}
}

// decode reads the file over the defaults, without validating.
func decode(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	c := Defaults()
	c.Path = path
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, fmt.Errorf("no config at %s (run `claudeswitch init`): %w", path, err)
		}
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.Hash = ContentHash(raw)
	md, err := toml.Decode(string(raw), c)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Builds before D19 called these blocks [[instance]]. No release read
	// them, so there is no alias: an old block is refused rather than
	// silently ignored, which would merge every account into one pool.
	if md.IsDefined("instance") {
		return nil, fmt.Errorf("%s: [[instance]] blocks are now called [[profile]]; "+
			"rename each [[instance]] to [[profile]] (the keys inside are unchanged)", path)
	}
	c.legacy = findLegacy(string(raw))
	// Which top-level settings the file actually carries, so Write keeps
	// those and leaves every other one to its default.
	for _, k := range md.Keys() {
		if len(k) == 1 {
			c.MarkSet(k[0])
		}
	}
	return c, nil
}

// MarkSet records that a setting was chosen — by the file or by `cs config`
// — rather than filled in by default, so Write keeps it.
func (c *Config) MarkSet(key string) {
	if c.set == nil {
		c.set = map[string]bool{}
	}
	c.set[key] = true
	// A value chosen now replaces whatever was raised on load; it is
	// checked as it stands (CheckSetting), not as the file had it.
	delete(c.raised, key)
}

// MinPoll is the fastest poll_active or poll_idle that runs: one account's
// usage allowance refills one call per usage.AccountRefill (GROUND_TRUTH §42),
// so a steady cadence faster than that drains it.
const MinPoll = usage.AccountRefill

// OldPinnedPollActive is the poll_active default of 0.3.x–0.5.0, which their
// `cs config` wrote into every file it touched ("1m0s"). Only this exact value
// is dropped by a write; see floorPolls.
const OldPinnedPollActive = time.Minute

// floorPolls raises a poll_active or poll_idle below MinPoll to MinPoll and
// remembers the value it replaced, for Warnings.
//
// Not an error, by owner decision (2026-10-07): every `cs config` on
// 0.3.x–0.5.0 wrote poll_active = "1m0s", because Write pinned the defaults
// it was filled with. Refusing that file would stop the daemon and even the
// `cs config` that could fix it.
func (c *Config) floorPolls() {
	raise := func(key string, d *Duration, def time.Duration) {
		if d.Duration <= 0 || d.Duration >= MinPoll {
			return
		}
		if c.raised == nil {
			c.raised = map[string]time.Duration{}
		}
		c.raised[key] = d.Duration
		d.Duration = MinPoll
		// A poll_active of exactly the old default, 1m0s, is the value
		// 0.3.x–0.5.0 `cs config` pinned on every write, not a choice:
		// unpin it, so the next write heals the file to today's default.
		// Any other value under the floor is the person's own, and is kept
		// as written (Write writes it back and the warning stays), as
		// poll_idle always is.
		if (key == "poll_active" && c.raised[key] == OldPinnedPollActive) || MinPoll == def {
			delete(c.set, key)
		}
	}
	def := Defaults()
	raise("poll_active", &c.PollActive, def.PollActive.Duration)
	raise("poll_idle", &c.PollIdle, def.PollIdle.Duration)
}

// FlooredPoll is a poll_active or poll_idle that runs at MinPoll instead of
// what was asked: raised on load, or set in code below the floor.
type FlooredPoll struct {
	Key      string
	Was, Now time.Duration
	Fix      string // the command that removes the warning
}

func (c *Config) FlooredPolls() []FlooredPoll {
	var out []FlooredPoll
	for _, k := range []struct {
		key, fix string
		d        time.Duration
	}{
		{"poll_active", "cs config poll_active " + SuggestedPollActive, c.PollActive.Duration},
		{"poll_idle", "cs config poll_idle " + shortDuration(Defaults().PollIdle.Duration), c.PollIdle.Duration},
	} {
		if was, ok := c.raised[k.key]; ok {
			out = append(out, FlooredPoll{k.key, was, MinPoll, k.fix})
		} else if k.d > 0 && k.d < MinPoll {
			out = append(out, FlooredPoll{k.key, k.d, MinPoll, k.fix})
		}
	}
	return out
}

// shortDuration is d as a person would type it: "10m", not "10m0s".
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// CheckSetting refuses a value `cs config` is about to write that would not
// run as written.
func (c *Config) CheckSetting(key string) error {
	for _, f := range c.FlooredPolls() {
		if f.Key == key {
			return fmt.Errorf("%s %s is faster than one account's usage allowance refills, so it "+
				"would run at %s anyway (GROUND_TRUTH §42); the minimum is %s", key, f.Was, f.Now, MinPoll)
		}
	}
	return nil
}

// Load reads and validates the config.
func Load(path string) (*Config, error) {
	c, err := decode(path)
	if c != nil {
		c.floorPolls()
	}
	if err != nil {
		return c, err
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", c.Path, err)
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
	for _, f := range c.FlooredPolls() {
		out = append(out, fmt.Sprintf(
			"%s is %s, faster than one account's usage allowance refills (~30 calls an hour per "+
				"account, GROUND_TRUTH §42), so it runs at %s. Fix: %s",
			f.Key, f.Was, f.Now, f.Fix))
	}
	if mins, fast := HotDrainMinutes(c.PollHot.Duration); fast {
		out = append(out, fmt.Sprintf(
			"poll_hot (%s) drains an account's ~%d-call usage allowance in ~%d min, and it takes "+
				"10-15 min to recover. Fix: cs config poll_hot %s",
			c.PollHot.Duration, UsageBurstCalls, mins, DefaultPollHotSetting))
	}
	if c.SwitchAtWeekly > 0 && c.HardFloor < c.SwitchAtWeekly {
		out = append(out, fmt.Sprintf(
			"hard_floor (%g) is below switch_at_weekly (%g), so every weekly rotation "+
				"counts as forced: it skips the %s cooldown and swaps mid-turn rather "+
				"than at an idle gap. Raise hard_floor above %g to keep both.",
			c.HardFloor, c.SwitchAtWeekly, c.Cooldown.String(), c.SwitchAtWeekly))
	}
	return append(out, c.profileWarnings()...)
}

// validate is every check: names (a *NameError listing each bad one with its
// fix) and everything else (validateRest), joined when both fail.
func (c *Config) validate() error {
	rest := c.validateRest()
	ne := c.nameError()
	switch {
	case ne == nil:
		return rest
	case rest == nil:
		return ne
	}
	return errors.Join(ne, rest)
}

func (c *Config) validateRest() error {
	// NaN compares false against every bound below, so it would pass them
	// all; Inf is no threshold either.
	nums := map[string]float64{"switch_at": c.SwitchAt, "switch_at_weekly": c.SwitchAtWeekly,
		"hard_floor": c.HardFloor, "hot_threshold": c.HotThreshold, "landing_margin": c.Margin()}
	for _, a := range c.Accounts {
		nums["account "+a.ID+" reserve"] = a.Reserve
	}
	for _, in := range c.Profiles {
		nums["profile "+in.Name+" switch_at"] = in.SwitchAt
		nums["profile "+in.Name+" switch_at_weekly"] = in.SwitchAtWeekly
		nums["profile "+in.Name+" hard_floor"] = in.HardFloor
		if in.LandingMargin != nil {
			nums["profile "+in.Name+" landing_margin"] = *in.LandingMargin
		}
	}
	for k, v := range nums {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s must be a finite number, got %v", k, v)
		}
	}
	if c.SwitchAt <= 0 || c.SwitchAt > 100 {
		return fmt.Errorf("switch_at must be between 0 and 100, got %v", c.SwitchAt)
	}
	if c.SwitchAtWeekly <= 0 || c.SwitchAtWeekly > 100 {
		return fmt.Errorf("switch_at_weekly must be between 0 and 100, got %v", c.SwitchAtWeekly)
	}
	if c.HardFloor < c.SwitchAt {
		return fmt.Errorf("hard_floor (%v) must be at or above switch_at (%v)", c.HardFloor, c.SwitchAt)
	}
	if m := c.Margin(); m < 0 || m > MaxLandingMargin {
		return fmt.Errorf("landing_margin must be between 0 and %g, got %v", MaxLandingMargin, m)
	}
	if err := validModels("models", c.Models); err != nil {
		return err
	}
	if n := c.HotReserveCalls(); n < 0 || n > MaxHotReserve {
		return fmt.Errorf("hot_reserve must be between 0 and %d, got %d", MaxHotReserve, n)
	}
	if u := c.UnseenPerHour(); u < 0 || u > MaxUnseenCallsPerHour {
		return fmt.Errorf("unseen_calls_per_hour must be between 0 and %g, got %g", MaxUnseenCallsPerHour, u)
	}
	if n := c.BlindPolls(); n < 0 {
		return fmt.Errorf("blind_failover_polls cannot be negative (0 turns failover off), got %d", n)
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
	// Per account (GROUND_TRUTH §42) nothing is refused here: a poll_active or
	// poll_idle under MinPoll was raised to it on load (floorPolls) and is
	// warned about, and a fast poll_hot is kept and warned about too (owner
	// decisions); the per-account allowance defers any excess.
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
	return c.validateProfiles()
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
	b.WriteString("# Thresholds are the tuning surface; `cs status` shows them. A line\n")
	b.WriteString("# starting with # is a default: uncomment it, or `cs config <key> <value>`,\n")
	b.WriteString("# to choose a value of your own.\n\n")
	// A setting is written as a value only when it was chosen — carried by the
	// file it was loaded from, set by `cs config` (MarkSet), or given a
	// non-default value in code — and otherwise as a commented default.
	// Writing every effective value pinned the defaults: each `cs config` on
	// 0.3.x–0.5.0 wrote poll_active = "1m0s", so the default could never
	// change for anyone who had ever changed anything (2026-10-07).
	//
	// Values are effective, not raw: a Config built in code leaves thresholds
	// zero, and zero is not a threshold anyone means — written out literally
	// it produces a file that will not load.
	def := Defaults()
	line := func(key string, chosen bool, value, help string) {
		if c.set[key] || chosen {
			fmt.Fprintf(&b, "%-15s = %s", key, value)
		} else {
			fmt.Fprintf(&b, "# %-13s = %s", key, value)
		}
		if help != "" {
			fmt.Fprintf(&b, "   # %s", help)
		}
		b.WriteString("\n")
	}
	num := func(key string, v, d float64, help string) {
		if v <= 0 {
			v = d
		}
		line(key, v != d, fmt.Sprintf("%g", v), help)
	}
	dur := func(key string, v, d Duration, help string) {
		if v.Duration <= 0 {
			v = d
		}
		// A poll raised to the floor on load is written back as the person
		// wrote it, still warned about — never as the raised value, which
		// would silently replace their setting. A poll_active of exactly
		// OldPinnedPollActive was unpinned by floorPolls (the default old
		// writes pinned) and is dropped, so the file returns to today's
		// default.
		if was, ok := c.raised[key]; ok {
			if c.set[key] {
				v = Duration{was}
			} else {
				v = d // unpinned by floorPolls: back to the default
			}
		}
		line(key, v.Duration != d.Duration, fmt.Sprintf("%q", v.String()), help)
	}
	num("switch_at", c.SwitchAt, def.SwitchAt, "rotate away at this much of the 5-hour window")
	num("switch_at_weekly", c.SwitchAtWeekly, def.SwitchAtWeekly,
		"and at this much of the weekly one: a weekly window spent is gone for days")
	num("hard_floor", c.HardFloor, def.HardFloor, "above this, swap mid-turn rather than wait for idle")
	when := c.SwitchWhen
	if when == "" {
		when = def.SwitchWhen
	}
	line("switch_when", when != def.SwitchWhen, fmt.Sprintf("%q", when), "")
	num("hot_threshold", c.HotThreshold, def.HotThreshold,
		"poll_hot only above this (or burning fast), and only while moving toward the trigger")
	dur("cooldown", c.Cooldown, def.Cooldown, "anti-flap")
	dur("max_switch_wait", c.MaxSwitchWait, def.MaxSwitchWait, "stop waiting for an idle gap after this")
	line("landing_margin", c.LandingMargin != nil, fmt.Sprintf("%g", c.Margin()),
		"a switch target's 5-hour window needs this much room below its trigger")
	line("blind_failover_polls", c.BlindFailoverPolls != nil, fmt.Sprintf("%d", c.BlindPolls()),
		"fail over after this many unreadable polls of the active account; 0 holds")
	// Written only when set, so a config without it reads back unchanged.
	if len(c.Models) > 0 {
		fmt.Fprintf(&b, "models          = %s # these models' weekly limits count like the weekly window\n",
			tomlStrings(c.Models))
	}
	b.WriteString("\n# Keeping vaulted credentials alive. Refreshing revokes the previous token,\n")
	b.WriteString("# so a stored credential goes stale on its own without this.\n")
	dur("refresh_window", c.RefreshWindow, def.RefreshWindow, "")
	dur("refresh_probe", c.RefreshProbe, def.RefreshProbe, "catch a dead refresh token before you need the account")
	if c.AutoRefresh != nil {
		fmt.Fprintf(&b, "auto_refresh    = %t\n", *c.AutoRefresh)
	}
	b.WriteString("\n")

	// Everything `cs config` can change must be handled here, or changing one
	// setting silently deletes the others.
	b.WriteString("# Polling cadence and the usage API call budget.\n")
	dur("poll_active", c.PollActive, def.PollActive, "")
	dur("poll_hot", c.PollHot, def.PollHot, "")
	dur("poll_idle", c.PollIdle, def.PollIdle, "")
	budget := c.APIBudget
	if budget <= 0 {
		budget = def.APIBudget
	}
	line("api_budget", budget != def.APIBudget, fmt.Sprintf("%d", budget), "")
	b.WriteString("\n")

	b.WriteString("# Advanced: the per-account usage model (DESIGN 4.3c).\n")
	line("hot_reserve", c.HotReserve != nil, fmt.Sprintf("%d", c.HotReserveCalls()),
		"calls held back from routine polling for a hot spell")
	line("unseen_calls_per_hour", c.UnseenCallsPerHour != nil, fmt.Sprintf("%g", c.UnseenPerHour()),
		"set aside on a live account for Claude Code's own reads")
	b.WriteString("\n")

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

	c.writeProfiles(&b)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func tomlStrings(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
