// Package state persists what the daemon has observed.
//
// The seven-day window outlives restarts and is the constraint that has actually
// been blocking work, so this must survive a reboot. Nothing here is an
// estimate: every field is either something the API reported or something a
// rejection record stated.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Availability is the eligibility verdict for an account.
type Availability string

const (
	Available Availability = "available" // usable now
	Burnt     Availability = "burnt"     // refused; wait for ResetsAt
	Unknown   Availability = "unknown"   // could not be read - never treat as available
	Reserved  Availability = "reserved"  // over its configured reserve
)

// Account is the durable per-account record.
type Account struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id,omitempty"`
	// Seat is the quota pool this record describes: person@organization. The
	// organization alone cannot tell two colleagues apart, and a record filed
	// under the wrong name is acted on as though it were right.
	Seat    string       `json:"seat,omitempty"`
	Last    *usage.Usage `json:"last_usage,omitempty"`
	LastAt  time.Time    `json:"last_at,omitzero"`
	LastErr string       `json:"last_error,omitempty"`
	// PrevWorst and PrevAt are the previous reading, kept so a burn rate can be
	// computed. Utilization can move several points a minute under heavy use, so
	// a reading is a lower bound on where the account actually is.
	PrevWorst float64 `json:"prev_worst,omitempty"`
	// LastRate is the most recent burn rate actually observed, in utilization
	// points per minute. It is kept so a projection survives a spell where the
	// poller cannot produce two distinct readings — which is exactly when the
	// projection matters most.
	LastRate float64   `json:"last_rate,omitempty"`
	PrevAt   time.Time `json:"prev_at,omitzero"`
	BurntTil time.Time `json:"burnt_until,omitzero"`
	BurntWin string    `json:"burnt_window,omitempty"`

	// RefreshExpiry is when this account's refresh token dies. Past that it
	// needs an interactive login, and it cannot even be polled.
	RefreshExpiry time.Time `json:"refresh_expiry,omitzero"`

	// ReadFails counts consecutive polls of this account that failed to read
	// its usage: a network error, a 5xx, a 401/403, an unparseable body. A
	// good reading clears it. A 429 neither counts nor clears — it is the
	// usage endpoint's own burst limit, which recovers on its own within 15
	// minutes (GROUND_TRUTH §42) — and neither does a poll the call budget
	// declined to make. The policy fails over from an active account blind
	// for long enough (IMPROVEMENTS A2).
	ReadFails int `json:"read_fails,omitempty"`
	// FailSince is when the current streak of unreadable polls began: set on
	// the first counted failure, cleared with ReadFails by a good read.
	// Blindness is measured from it, never from LastAt — after a long sleep
	// the last reading is hours old, yet the reads have only been failing for
	// seconds. The daemon clears both at startup: a streak it did not watch
	// is no evidence of how long the account has been unreadable.
	FailSince time.Time `json:"fail_since,omitzero"`
	// TokenExpiry is when the access token last used to poll this account
	// expires. An expired token on an idle session is not trouble: Claude
	// Code refreshes it on the next message.
	TokenExpiry time.Time `json:"token_expiry,omitzero"`
}

// BurnRate estimates utilization points per minute from the last two readings.
// Zero means unknown, or falling (a window reset), which must never be treated
// as evidence of headroom.
func (a *Account) BurnRate() float64 {
	if a.Last == nil || a.PrevAt.IsZero() || a.LastAt.IsZero() {
		return a.rememberedRate()
	}
	mins := a.LastAt.Sub(a.PrevAt).Minutes()
	if mins <= 0 {
		return a.rememberedRate()
	}
	_, worst := a.Last.Worst()
	d := worst - a.PrevWorst
	if d < 0 {
		// Utilization fell, so the window reset. Nothing is burning and any
		// remembered rate describes a window that no longer exists.
		return 0
	}
	if d == 0 {
		// The pair carries no new information — most often because the poller
		// is stuck and both readings are the same one. Falling back to zero here
		// was the flaw: it made Projected() return the stale figure unchanged,
		// so the daemon grew *more* confident the longer it was blind. It sat
		// on a reading of 60% for two hours while the account reached 100%.
		return a.rememberedRate()
	}
	return d / mins
}

// rememberedRate is the last rate actually observed, used when the current pair
// of readings cannot produce one. It is only ever an estimate, but an estimate
// from the last time we could see beats treating a frozen number as the truth.
func (a *Account) rememberedRate() float64 {
	if a.LastRate <= 0 {
		return 0
	}
	// Do not extrapolate indefinitely. Past this the estimate says more about
	// how long we have been blind than about the account, and a person should
	// be looking at it anyway.
	if !a.LastAt.IsZero() && time.Since(a.LastAt) > MaxProjection {
		return 0
	}
	return a.LastRate
}

// Projected is the utilization now, allowing for what has probably been spent
// since the reading was taken. Under heavy use a four-minute-old reading can be
// ten points low, which is enough to miss a trigger entirely.
func (a *Account) Projected(now time.Time) float64 {
	if a.Last == nil {
		return 0
	}
	_, worst := a.Last.Worst()
	rate := a.BurnRate()
	if rate <= 0 {
		return worst
	}
	p := worst + rate*now.Sub(a.LastAt).Minutes()
	if p > 100 {
		return 100
	}
	return p
}

// WithModels is this account as the policy judges it when the config counts
// these models' weekly limits like the weekly window (IMPROVEMENTS I6; see
// usage.Usage.WithModels). from names the model whose limit set the weekly
// figure, unreadable a counted model whose limit has no figure.
//
// The copy's PrevWorst is moved by however much the worst figure moved, so its
// burn rate is the rate the reading itself showed: the previous reading's
// model figure was never kept, and comparing the new effective figure with the
// old unscoped one would invent a burst. The original is never modified, and
// with nothing changed it is returned as is.
func (a *Account) WithModels(models []string) (eff *Account, from, unreadable string) {
	if a == nil || a.Last == nil || len(models) == 0 {
		return a, "", ""
	}
	u, from, unreadable := a.Last.WithModels(models)
	if u == a.Last {
		return a, from, unreadable
	}
	cp := *a
	_, before := a.Last.Worst()
	_, after := u.Worst()
	if !cp.PrevAt.IsZero() {
		cp.PrevWorst += after - before
	}
	cp.Last = u
	return &cp, from, unreadable
}

// WeekLength is the weekly window's span, from which its start is found:
// the API reports only when it resets.
const WeekLength = 7 * 24 * time.Hour

// MinPaceEstimate is how far into the week the pace view waits before
// estimating what will expire unused. Before it, one busy morning
// extrapolates to anything.
const MinPaceEstimate = 24 * time.Hour

// Pace is how the weekly window is being spent against the clock
// (IMPROVEMENTS I8). Display only: no decision reads it.
type Pace struct {
	// Expected is the share of the week elapsed, in percent: what steady use
	// would have spent by now.
	Expected float64
	// Actual is the weekly utilization now, projected forward from the
	// reading when the weekly window is the one burning.
	Actual float64
	// Estimated says AtReset and Unused are set: far enough into the week to
	// extrapolate.
	Estimated bool
	// AtReset is where the weekly window will stand at the reset if use keeps
	// its average pace so far, capped at 100.
	AtReset float64
	// Unused is the share of the week's quota that would expire unused at the
	// reset at that pace.
	Unused   float64
	ResetsAt time.Time
}

// WeeklyPace measures the weekly window against the clock. False when there
// is nothing to measure: no reading, an unknown weekly window, no reset time,
// or a reading of a week that has since reset.
//
// The projection to the reset uses the average rate since the window opened.
// A short-term burn rate (BurnRate, points a minute) describes the current
// session and would project one hot hour across days; the average is what the
// week has actually been. The figure for now does use the existing projection,
// when the weekly window is the one the burn rate measures.
func (a *Account) WeeklyPace(now time.Time) (Pace, bool) {
	if a == nil || a.Last == nil || !a.Last.SevenDay.Known() {
		return Pace{}, false
	}
	r := a.Last.SevenDay.ResetsAt
	if r == nil || r.IsZero() || !now.Before(*r) {
		return Pace{}, false
	}
	elapsed := now.Sub(r.Add(-WeekLength))
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > WeekLength {
		elapsed = WeekLength
	}
	frac := float64(elapsed) / float64(WeekLength)
	actual := a.Last.SevenDay.Pct()
	if which, _ := a.Last.Worst(); which == usage.SevenDayKey {
		if p := a.Projected(now); p > actual {
			actual = p
		}
	}
	p := Pace{Expected: frac * 100, Actual: actual, ResetsAt: *r}
	if elapsed >= MinPaceEstimate && frac > 0 {
		p.Estimated = true
		p.AtReset = actual / frac
		if p.AtReset > 100 {
			p.AtReset = 100
		}
		p.Unused = 100 - p.AtReset
	}
	return p, true
}

// MaxProjection bounds how far a remembered burn rate will be carried forward.
// A projection is a stand-in for a reading, and one this old has stopped being
// evidence about the account.
const MaxProjection = 45 * time.Minute

// HasReading reports whether there is anything to reason about. A nil account
// and one that has never been polled are the same thing to a caller.
func (a *Account) HasReading() bool { return a != nil && a.Last != nil }

// ExpiredAt reports whether this reading has outlived the window it describes.
//
// The API tells us when each window resets, so this needs no extra call: once
// resets_at has passed, whatever the reading said about that window is history.
func (a *Account) ExpiredAt(now time.Time) bool {
	if a.Last == nil {
		return false
	}
	which, _ := a.Last.Worst()
	w := a.Last.FiveHour
	if which == "seven_day" {
		w = a.Last.SevenDay
	}
	if w.ResetsAt == nil || w.ResetsAt.IsZero() {
		return false
	}
	// The reading must predate the reset to be describing the old window.
	return now.After(*w.ResetsAt) && a.LastAt.Before(*w.ResetsAt)
}

// RepollAt is when this account is next worth reading regardless of the idle
// schedule: the moment its binding window resets. An exhausted account becomes
// a usable one at a time we already know, and waiting out a ten-minute idle
// interval to discover that wastes the very headroom we were waiting for.
func (a *Account) RepollAt() time.Time {
	if a.Last == nil {
		return time.Time{}
	}
	which, _ := a.Last.Worst()
	w := a.Last.FiveHour
	if which == "seven_day" {
		w = a.Last.SevenDay
	}
	if w.ResetsAt == nil || w.ResetsAt.IsZero() {
		return time.Time{}
	}
	// A little after, so the server has certainly rolled the window over.
	return w.ResetsAt.Add(10 * time.Second)
}

// Stale reports whether the reading is older than the poll interval allows.
func (a *Account) Stale(maxAge time.Duration) bool {
	return a.Last == nil || time.Since(a.LastAt) > maxAge
}

// Availability folds every observation into one verdict, using the wall clock.
func (a *Account) Availability(reserve float64) Availability {
	return a.AvailabilityAt(reserve, time.Now())
}

// AvailabilityAt is the same decision with the clock injected, so the policy
// engine stays a pure function and clock-jump cases can be tested.
//
// Order matters: a refused account is burnt even if an older reading looked
// fine, and an account that could not be read is never "available".
func (a *Account) AvailabilityAt(reserve float64, now time.Time) Availability {
	if !a.BurntTil.IsZero() && now.Before(a.BurntTil) {
		return Burnt
	}
	if a.Last == nil {
		return Unknown
	}
	// A reading whose window has since reset describes a window that no longer
	// exists. Believing "100% used" about a window that refilled ten minutes ago
	// is how a perfectly good account gets ruled out — and with every account
	// ruled out, there is nothing to rotate to and the tool silently fails at
	// the one thing it does.
	if a.ExpiredAt(now) {
		return Unknown
	}
	if reserve > 0 {
		if _, worst := a.Last.Worst(); worst > reserve {
			return Reserved
		}
	}
	return Available
}

// DefaultProfile names the Claude Code profile every caller means until
// profiles can be configured: the one CLAUDE_CONFIG_DIR resolves to.
const DefaultProfile = "default"

// ProfileState is what belongs to one live credential rather than to an
// account: which account that credential is, and the rotation decisions made
// about it. Each Claude Code profile has its own live credential, so each has
// its own copy. Account readings are not here: an account's quota is the same
// whichever profile spends it.
//
// The JSON names are the ones these fields had at the top level of State, so
// a file from before profiles reads into this same struct (see UnmarshalJSON).
type ProfileState struct {
	Active string `json:"active_account,omitempty"`
	// Pinned suspends automatic rotation until `claudeswitch auto` clears it.
	Pinned string `json:"pinned,omitempty"`
	// LastSwitch feeds the anti-flap cooldown across restarts.
	LastSwitch time.Time `json:"last_switch,omitzero"`
	// ActiveAt is when Active was last *established* — by the daemon attributing
	// the live credential, or by a swap that verified its organization. Active is
	// an observation, not a preference, and both sides can legitimately make it;
	// recency decides, not ownership. Without this, a CLI swap was silently
	// reverted by the daemon's older belief on the next save.
	ActiveAt time.Time `json:"active_at,omitzero"`
	// Item is the live item the daemon last resolved for this profile, and
	// the dir it resolved it from. It outlives the profile's config block,
	// so a profile removed or re-pointed while no daemon ran can still be
	// guarded as a ghost (see ghost.go).
	Item *ItemRef `json:"item_ref,omitempty"`
}

// ItemRef is a resolved live item: its keychain service and Linux credential
// file, with the profile's dir as configured (FromEnv for the implicit one).
type ItemRef struct {
	Service string `json:"service"`
	File    string `json:"credential_file,omitempty"`
	Dir     string `json:"dir,omitempty"`
	FromEnv bool   `json:"from_env,omitempty"`
}

func (in *ProfileState) isZero() bool {
	return in.Active == "" && in.Pinned == "" && in.LastSwitch.IsZero() && in.ActiveAt.IsZero()
}

// State is the whole persisted document.
type State struct {
	Version  int                 `json:"version"`
	Accounts map[string]*Account `json:"accounts"`
	// Profiles is the per-live-credential state, keyed by profile name.
	// DefaultProfile is always present, and so is every profile named to
	// Load (see Load for why).
	Profiles map[string]*ProfileState `json:"profiles,omitempty"`
	// DaemonLive records whether the running daemon will actually perform swaps.
	// Written by the daemon at startup so the CLI can tell the truth about what
	// is going to happen, rather than printing a fixed sentence that goes stale.
	DaemonLive  bool      `json:"daemon_live"`
	DaemonSince time.Time `json:"daemon_since,omitzero"`
	// The running daemon's build, written by the daemon at start, so a newer
	// CLI can tell it is talking to an older daemon (the D10 upgrade gap).
	// Absent on a file last started by a daemon from before the field.
	DaemonVersion   string    `json:"daemon_version,omitempty"`
	DaemonBuild     string    `json:"daemon_build,omitempty"` // VCS revision, "+dirty" when modified
	DaemonBuildTime time.Time `json:"daemon_build_time,omitzero"`
	// DaemonProfiles is every profile the running daemon has loaded, name
	// to dir as configured ("" for none), written at its start and on every
	// config reload. `profile create --seed` waits for its new profile to
	// appear here before seeding (lane 10 security review). Daemon-owned.
	DaemonProfiles map[string]string `json:"daemon_profiles,omitempty"`
	SavedAt        time.Time         `json:"saved_at"`
	// Vaulted is every account id this program has stored a credential for,
	// including ones the config does not mention — `add` will vault an account
	// that is not configured, and says so while it does it.
	//
	// It exists because the duplicate-credential checks enumerated the CONFIG,
	// so an entry `add` had just created outside the config was invisible to
	// them. Two names then came to hold one quota pool undetected, which is the
	// state those checks exist to prevent: both report the same utilization, so
	// rotating between them does nothing, and refreshing one revokes the other.
	// The keychain cannot be listed without a dump that prompts for every item,
	// so what we stored is recorded here as we store it.
	//
	// Reconcile must never prune this: "not in the config" is precisely the
	// case it is here to remember.
	Vaulted []string `json:"vaulted,omitempty"`
	// vaultAdds and vaultDrops are this process's changes to Vaulted since
	// its last save. Vaulted is the CLI's: only the CLI vaults or deletes a
	// credential, so a CLI save applies its own changes to the disk's list
	// and a daemon save keeps the disk's list as it is. Before, each side
	// wrote its own copy, and a daemon's copy from its start brought back an
	// id a CLI delete had dropped (lane 12 security review).
	vaultAdds, vaultDrops map[string]bool

	// DaemonConfigHash is config.ContentHash of the config the running
	// daemon has loaded, written at its start and on every reload. A
	// command that edits the config and must not act before the daemon
	// runs that edit (`account delete`, `profile create --seed`) waits for
	// the hash of what it wrote: a marker from an earlier run, or from the
	// config before the edit, never matches it. Daemon-owned.
	DaemonConfigHash string `json:"daemon_config_hash,omitempty"`

	// Ghosts are the old live items of profiles removed from the config or
	// re-pointed to another dir, kept so every §3 check still consults them
	// (see ghost.go). Keyed by Ghost.Key.
	Ghosts map[string]*Ghost `json:"ghosts,omitempty"`
	// ghostAdds and ghostDrops are this process's changes to Ghosts since its
	// last save; the save applies them to what is on disk (mergeGhosts).
	ghostAdds, ghostDrops map[string]bool

	// Chrome maps an account to the Chrome profile `cs chrome add` made for
	// it (chrome.go). ChromeHints is, per Claude Code profile, the rotation
	// the browser-tool hint was last given for. Only the CLI changes either;
	// a save merges this process's changes onto the disk (mergeChrome).
	Chrome        map[string]*ChromeProfile `json:"chrome_profiles,omitempty"`
	ChromeHints   map[string]string         `json:"chrome_hints,omitempty"`
	chromeTouched map[string]bool
	hintTouched   map[string]bool
	// Emails is the email each account's credential belongs to, recorded
	// when it was vaulted or identified, so a command can name it without
	// reading the keychain. CLI-written and merged like Chrome.
	Emails       map[string]string `json:"emails,omitempty"`
	emailTouched map[string]bool
	// Plans is the subscription behind each account ("Max 20x"), recorded
	// when the CLI vaults or identifies it and when the daemon reads a vault
	// entry that names one, so `cs account list` shows it without the
	// keychain. Either side writes it; a save merges only its own changes.
	Plans       map[string]string `json:"plans,omitempty"`
	planTouched map[string]bool

	path string

	// dropped records ids this process deliberately removed, so the merge does
	// not helpfully restore them from disk. Without it, `forget` and `rename`
	// appeared to work and were undone on the very next save.
	dropped map[string]bool
}

const version = 1

func DefaultPath() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", "claudeswitch", "state.json")
	}
	return "state.json"
}

// Load reads the state file. profiles names every configured profile; each
// is materialised, as the default one always is, so Profile(name) for any of
// them on the loaded state only ever reads the map.
//
// That is the thread-safety rule for Profiles, chosen over a mutex: the
// daemon reads state from more than one goroutine, and a first Profile() that
// inserted would be a map write. A lock inside Profile() would not be enough
// on its own — Save, Reconcile and MarshalJSON range over the map, and State
// is copied by value in MarshalJSON — whereas materialising up front makes the
// map's key set fixed after Load for every name the daemon will ask about.
// Profile() on an unconfigured name still inserts; only single-goroutine
// callers (the CLI) may do that.
func Load(path string, profiles ...string) (*State, error) {
	if path == "" {
		path = DefaultPath()
	}
	s := fresh(path, profiles...)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		// A corrupt state file must not stop the daemon: it is a cache of
		// observations, all of which can be re-observed.
		return fresh(path, profiles...),
			fmt.Errorf("state file %s was unreadable and has been reset: %w", path, err)
	}
	if s.Accounts == nil {
		s.Accounts = map[string]*Account{}
	}
	s.materialise(profiles)
	s.path = path
	return s, nil
}

func fresh(path string, profiles ...string) *State {
	s := &State{Version: version, Accounts: map[string]*Account{}, path: path}
	s.materialise(profiles)
	return s
}

// materialise creates the default profile and every named one that does not
// exist yet. See Load for why.
func (s *State) materialise(profiles []string) {
	s.Default()
	for _, name := range profiles {
		s.Profile(name)
	}
}

// Profile returns the named profile's state, creating it on first use so a
// caller can always write through it.
func (s *State) Profile(name string) *ProfileState {
	if s.Profiles == nil {
		s.Profiles = map[string]*ProfileState{}
	}
	in := s.Profiles[name]
	if in == nil {
		in = &ProfileState{}
		s.Profiles[name] = in
	}
	return in
}

// Default is the DefaultProfile's state. Every caller that predates profiles
// goes through here, so finding them when profiles arrive is a grep.
func (s *State) Default() *ProfileState { return s.Profile(DefaultProfile) }

// SetActive records which account the live credential belongs to, stamping when
// that was established so a merge can tell whose belief is newer.
func (in *ProfileState) SetActive(id string) {
	in.Active = id
	in.ActiveAt = time.Now()
}

// plainState is State without its methods, so the JSON hooks below can use the
// default encoding without recursing into themselves.
type plainState State

// MarshalJSON writes "profiles" only. Until the multi-profile release the
// default profile was mirrored at the top level for binaries that predate
// profiles; that release drops the mirror (D10) and its notes say to restart
// the daemon after upgrading. The old fields are still read: see UnmarshalJSON.
func (s State) MarshalJSON() ([]byte, error) {
	return json.Marshal(plainState(s))
}

// UnmarshalJSON reads both shapes. A file with no default profile but with the
// old top-level fields was written by an older binary (which drops "profiles"
// whenever it saves), so those fields are the default profile. When both are
// present (a 0.4.x-era mirror alongside "profiles"), "profiles" is the
// authority.
//
// Dev builds before D19 wrote the same map as "instances". It is read when
// "profiles" is absent, and the next save writes "profiles" only.
func (s *State) UnmarshalJSON(b []byte) error {
	in := struct {
		plainState
		ProfileState
		DevInstances map[string]*ProfileState `json:"instances"`
	}{plainState: plainState(*s)}
	// Whether the file has a default profile is the question below, so the
	// file alone must answer it.
	in.Profiles = nil
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	if in.Profiles == nil && in.DevInstances != nil {
		in.Profiles = in.DevInstances
	}
	path, dropped := s.path, s.dropped
	*s = State(in.plainState)
	s.path, s.dropped = path, dropped
	if s.Profiles[DefaultProfile] == nil && !in.ProfileState.isZero() {
		legacy := in.ProfileState
		*s.Default() = legacy
	}
	return nil
}

// Drop removes an account record and remembers that it was removed, so a merge
// will not bring it back.
func (s *State) Drop(id string) {
	delete(s.Accounts, id)
	if s.dropped == nil {
		s.dropped = map[string]bool{}
	}
	s.dropped[id] = true
}

// Unattributed is the id readings are filed under when the live credential
// matches no configured account. It is a pseudo-account: it holds an
// observation so the tool can say "something is signed in that you have not
// pinned", and it must never be treated as one of the configured accounts.
//
// It lived as a private constant in two other packages, and the code here —
// which owns the map it is stored in — knew about neither.
const Unattributed = "active"

// Reconcile discards observations that cannot be trusted:
//
//   - records for accounts no longer in the config
//   - records whose organization disagrees with the organization the config
//     pins for that id
//
// The second case is the one that matters. Reusing an id for a different
// account leaves a record describing the OLD account under the NEW name, and
// the policy engine would then treat an exhausted account as having headroom
// and rotate into it. Observed 2026-09-09 after a rename.
//
// pinned maps account id to its configured SEAT ("" when unpinned).
func (s *State) Reconcile(pinned map[string]string) []string {
	var dropped []string
	for id, a := range s.Accounts {
		// The unattributed record is not an account and was never in the
		// config — that is the whole point of it. Reconcile deleted it on every
		// CLI invocation, which both defeated the feature that reports a live
		// credential you have not pinned yet and printed "discarded stale
		// observation for active (not in config)" at someone who had configured
		// nothing of the sort.
		if id == Unattributed {
			continue
		}
		want, known := pinned[id]
		switch {
		case !known:
			dropped = append(dropped, id+" (not in config)")
		case want != "" && a.Seat != "" && a.Seat != want:
			dropped = append(dropped, fmt.Sprintf("%s (record is seat %s, config pins %s)",
				id, usage.ShortSeat(a.Seat), usage.ShortSeat(want)))
		case want != "" && a.Seat == "" && a.OrgID != "" && !strings.HasSuffix(want, "@"+a.OrgID):
			// An older record with no seat, so the organization is all there is
			// to compare. It must be compared against the configured seat's
			// ORGANIZATION: this used to abbreviate the whole seat, which
			// yields the person, and then announce that an organization "is
			// not" someone's account uuid.
			dropped = append(dropped, fmt.Sprintf("%s (record is organization %s, which is not %s)",
				id, short(a.OrgID), short(orgOf(want))))
		default:
			continue
		}
		s.Drop(id)
	}
	for _, in := range s.Profiles {
		if in == nil || in.Active == "" {
			continue
		}
		if _, ok := s.Accounts[in.Active]; !ok {
			in.Active = ""
		}
	}
	return dropped
}

// orgOf is the organization half of a seat, for comparing against a record that
// predates seats and knows only an organization.
func orgOf(seat string) string {
	if _, org, ok := strings.Cut(seat, "@"); ok {
		return org
	}
	return seat
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// AddVaulted records that a credential is stored under this id.
func (s *State) AddVaulted(id string) {
	if s.vaultAdds == nil {
		s.vaultAdds = map[string]bool{}
	}
	s.vaultAdds[id] = true
	delete(s.vaultDrops, id)
	for _, v := range s.Vaulted {
		if v == id {
			return
		}
	}
	s.Vaulted = append(s.Vaulted, id)
}

// DropVaulted forgets an id whose credential has been deleted.
func (s *State) DropVaulted(id string) {
	if s.vaultDrops == nil {
		s.vaultDrops = map[string]bool{}
	}
	s.vaultDrops[id] = true
	delete(s.vaultAdds, id)
	out := s.Vaulted[:0]
	for _, v := range s.Vaulted {
		if v != id {
			out = append(out, v)
		}
	}
	s.Vaulted = out
}

// KnownAccounts is every id worth checking for integrity: the configured ones
// plus anything vaulted outside the config, in config order first so messages
// name accounts in the order a person sees them.
func (s *State) KnownAccounts(configured []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(configured)+len(s.Vaulted))
	for _, id := range configured {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range s.Vaulted {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (s *State) Get(id string) *Account {
	if a, ok := s.Accounts[id]; ok {
		return a
	}
	a := &Account{ID: id}
	s.Accounts[id] = a
	return a
}

// Ownership, so that a CLI command and the daemon cannot lose each other's
// writes:
//
// The rules for Active, Pinned and LastSwitch apply to each profile on its
// own (see mergeProfiles).
//
//   - the daemon owns Accounts[*] and Active — Active is not a preference, it is
//     an observation: whichever account the live credential actually belongs to.
//     An earlier version let the CLI's stored Active override the daemon's
//     observation, which meant a wrong attribution could never self-correct.
//   - the CLI owns Pinned and LastSwitch — the things a person decides
//
// Save takes the state lock, re-reads what is on disk, keeps the other side's
// fields, and writes the union. Whoever writes last no longer wins outright.
type owner int

const (
	OwnerCLI owner = iota
	OwnerDaemon
	// OwnerCLIKeepDaemonFields exists only to keep the switch exhaustive.
	OwnerCLIKeepDaemonFields
)

// Save writes atomically under the state lock, 0600 inside a 0700 directory.
func (s *State) Save() error { return s.SaveAs(OwnerCLI) }

// SaveAs merges according to which side is writing.
func (s *State) SaveAs(as owner) error {
	lk, err := blockingLock(lockPath(stateLockName))
	if err != nil {
		return err
	}
	defer lk.Release()

	if disk, err := readFile(s.path); err == nil && disk != nil {
		s.mergeGhosts(disk)
		s.mergeChrome(disk)
		switch as {
		case OwnerCLIKeepDaemonFields:
			// unreachable; kept for exhaustiveness
			fallthrough
		case OwnerDaemon:
			s.mergeProfiles(disk, func(mine, disk *ProfileState) {
				// Take whichever side observed the live account more recently — a
				// `use` that happened while we were sleeping is newer than our belief.
				if disk.ActiveAt.After(mine.ActiveAt) {
					mine.Active, mine.ActiveAt = disk.Active, disk.ActiveAt
				}
				mine.Pinned = disk.Pinned
				if disk.LastSwitch.After(mine.LastSwitch) {
					mine.LastSwitch = disk.LastSwitch
				}
			})
			// The CLI's list, as it is on disk.
			s.Vaulted = disk.Vaulted
		case OwnerCLI:
			s.mergeProfiles(disk, func(mine, disk *ProfileState) {
				// Same rule from the other side: keep the daemon's Active unless we
				// have just established a newer one ourselves.
				if disk.ActiveAt.After(mine.ActiveAt) {
					mine.Active, mine.ActiveAt = disk.Active, disk.ActiveAt
				}
				// The daemon owns the resolved item; a CLI copy may be stale.
				mine.Item = disk.Item
			})
			s.DaemonLive = disk.DaemonLive
			s.DaemonSince = disk.DaemonSince
			s.DaemonVersion, s.DaemonBuild, s.DaemonBuildTime = disk.DaemonVersion, disk.DaemonBuild, disk.DaemonBuildTime
			s.DaemonProfiles = disk.DaemonProfiles
			s.DaemonConfigHash = disk.DaemonConfigHash
			s.mergeVaulted(disk)
			// Keep the daemon's observations; they are fresher than ours — but
			// never resurrect a record this process deliberately dropped.
			for id, a := range disk.Accounts {
				if s.dropped[id] {
					continue
				}
				if mine, ok := s.Accounts[id]; !ok || a.LastAt.After(mine.LastAt) {
					s.Accounts[id] = a
				}
			}
		}
	}

	s.Version = version
	s.SavedAt = time.Now()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// The disk now has this process's ghost changes; from here on its copy
	// is the base the next save merges onto.
	s.ghostAdds, s.ghostDrops = nil, nil
	s.vaultAdds, s.vaultDrops = nil, nil
	s.chromeTouched, s.hintTouched, s.emailTouched, s.planTouched = nil, nil, nil, nil
	return nil
}

// mergeVaulted is the disk's Vaulted with this process's adds and drops
// applied, in the disk's order, adds last.
func (s *State) mergeVaulted(disk *State) {
	out := make([]string, 0, len(disk.Vaulted)+len(s.vaultAdds))
	seen := map[string]bool{}
	for _, id := range disk.Vaulted {
		if !s.vaultDrops[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range s.Vaulted {
		if s.vaultAdds[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(out) == 0 {
		out = nil
	}
	s.Vaulted = out
}

// mergeProfiles applies merge to every profile both sides have, and adopts
// whole any profile only the disk has: this process never loaded it, so
// nothing it holds about it can be newer.
func (s *State) mergeProfiles(disk *State, merge func(mine, disk *ProfileState)) {
	for name, d := range disk.Profiles {
		if d == nil {
			continue
		}
		if mine := s.Profiles[name]; mine != nil {
			merge(mine, d)
			continue
		}
		*s.Profile(name) = *d
	}
}

// readFile loads state without touching locks or defaults.
func readFile(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Accounts == nil {
		s.Accounts = map[string]*Account{}
	}
	return &s, nil
}
