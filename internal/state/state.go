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

// State is the whole persisted document.
type State struct {
	Version  int                 `json:"version"`
	Accounts map[string]*Account `json:"accounts"`
	Active   string              `json:"active_account,omitempty"`
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
	// DaemonLive records whether the running daemon will actually perform swaps.
	// Written by the daemon at startup so the CLI can tell the truth about what
	// is going to happen, rather than printing a fixed sentence that goes stale.
	DaemonLive  bool      `json:"daemon_live"`
	DaemonSince time.Time `json:"daemon_since,omitzero"`
	SavedAt     time.Time `json:"saved_at"`
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

func Load(path string) (*State, error) {
	if path == "" {
		path = DefaultPath()
	}
	s := &State{Version: version, Accounts: map[string]*Account{}, path: path}
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
		return &State{Version: version, Accounts: map[string]*Account{}, path: path},
			fmt.Errorf("state file %s was unreadable and has been reset: %w", path, err)
	}
	if s.Accounts == nil {
		s.Accounts = map[string]*Account{}
	}
	s.path = path
	return s, nil
}

// SetActive records which account the live credential belongs to, stamping when
// that was established so a merge can tell whose belief is newer.
func (s *State) SetActive(id string) {
	s.Active = id
	s.ActiveAt = time.Now()
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
	if s.Active != "" {
		if _, ok := s.Accounts[s.Active]; !ok {
			s.Active = ""
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
	for _, v := range s.Vaulted {
		if v == id {
			return
		}
	}
	s.Vaulted = append(s.Vaulted, id)
}

// DropVaulted forgets an id whose credential has been deleted.
func (s *State) DropVaulted(id string) {
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
		switch as {
		case OwnerCLIKeepDaemonFields:
			// unreachable; kept for exhaustiveness
			fallthrough
		case OwnerDaemon:
			// Take whichever side observed the live account more recently — a
			// `use` that happened while we were sleeping is newer than our belief.
			if disk.ActiveAt.After(s.ActiveAt) {
				s.Active, s.ActiveAt = disk.Active, disk.ActiveAt
			}
			s.Pinned = disk.Pinned
			if disk.LastSwitch.After(s.LastSwitch) {
				s.LastSwitch = disk.LastSwitch
			}
		case OwnerCLI:
			// Same rule from the other side: keep the daemon's Active unless we
			// have just established a newer one ourselves.
			if disk.ActiveAt.After(s.ActiveAt) {
				s.Active, s.ActiveAt = disk.Active, disk.ActiveAt
			}
			s.DaemonLive = disk.DaemonLive
			s.DaemonSince = disk.DaemonSince
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
	return os.Rename(tmp, s.path)
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
