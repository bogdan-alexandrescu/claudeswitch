// Package poller keeps each account's usage reading current within the API's
// call budget.
//
// The scheduling rests on one observation (ground truth #12): an idle account's
// utilization can only go DOWN. Nothing is spending it, so it needs polling only
// often enough to notice that a burnt account has recovered. All the urgency is
// on the active account, which is the only one that can climb toward a limit.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Cadence. The first version was far too eager: polling the active account
// every 75 seconds is 4 calls per 5 minutes, the entire budget, so scheduled
// polls of INACTIVE accounts never got a slot. The daemon ran twelve hours
// without once reading the account it would have rotated to — blind to whether
// its own fallback had any headroom — while logging 155 rate-limit warnings.
//
// Utilization moves slowly, and the rejection detector catches a real wall in
// milliseconds regardless, so fast polling buys nothing and costs the budget
// that inactive accounts need.
const (
	ActiveInterval    = 4 * time.Minute
	ActiveHotInterval = 90 * time.Second // once the active account nears the trigger
	IdleInterval      = 20 * time.Minute
	// HotThreshold is the default for config's hot_threshold, used when a Config
	// does not set one. It is where close watching begins. Well below the trigger,
	// because the decision needs to be taken before the line is crossed, not
	// after — and a fast burn covers the gap between 60 and 85 in minutes.
	HotThreshold = 60.0
	StaleAfter   = 10 * time.Minute

	// ReattributeInterval is how often the daemon re-derives which account is
	// live. It was doing this on every save tick, an extra priority call every
	// two minutes on top of everything else.
	ReattributeInterval = 15 * time.Minute

	// FastBurn is utilization points per minute above which a moving account
	// within reach of its trigger is polled hot even below hot_threshold.
	// Measured 2026-09-10: heavy use moved 8 points in 3 minutes.
	FastBurn = 1.5

	// MovingBurn is the slowest burn, in points a minute, that counts as
	// moving for the hot cadence (docs/DESIGN.md 4.3c). StillAfter is how
	// long without a rise in the reading ends "moving": readings are whole
	// points, so at half a point a minute one in two minute-apart readings
	// shows no change, and a pair that matches is not yet a pause.
	MovingBurn = 0.1
	StillAfter = 5 * time.Minute

	// ParkRecheck is how soon an account parked on an expired token (R2) is
	// looked at again: a local read of its credential, no call. Claude Code
	// renews a busy profile's token, and the daemon an idle one's, on their
	// own schedules; this only notices the renewal.
	ParkRecheck = time.Minute

	// burstSpacing separates calls made in one on-demand sweep. The endpoint's
	// limit is a burst allowance; spacing is what keeps a multi-account refresh
	// from tripping it.
	// Measured: one call every 20 seconds sustained twelve in a row without a
	// refusal, while five in quick succession tripped it. Three seconds is well
	// inside the former and nowhere near the latter.
	burstSpacing = 3 * time.Second
)

type Poller struct {
	cfg    *config.Config
	st     *state.State
	client *usage.Client
	budget *usage.Budget
	log    *slog.Logger

	// degraded is set when the API's shape changes. Predictive switching must
	// stop and say so; the detector carries on alone.
	degraded    bool
	degradedWhy string

	nextPoll map[string]time.Time

	// now is the clock; a seam so a simulated day can run on a fake one.
	now func() time.Time
	// hot is whether each account's last scheduling chose poll_hot.
	hot map[string]bool
	// lastIv is each account's last scheduled interval, so cooling from hot
	// drifts back to poll_active rather than jumping.
	lastIv map[string]time.Duration
	// lastMoved is when each account's reading was last seen to rise.
	lastMoved map[string]time.Time
	// held is when a budget refusal lets each account be polled again.
	held map[string]time.Time

	// lastOK is when a poll last succeeded. A daemon that keeps running while
	// seeing nothing is the most dangerous state this program has: it decides
	// on figures that stop moving and says nothing, because "stay" is logged at
	// debug level. Tonight it sat at 93% that way.
	lastOK  time.Time
	started time.Time

	// lastSeverity tracks severity per account+limit kind so transitions can be
	// recorded. Keyed "account/kind".
	lastSeverity map[string]string

	// OnSeverityChange, if set, is called for every severity transition.
	OnSeverityChange func(account, kind, from, to string, percent float64)

	// lives is each profile's live credential, set by the daemon. An
	// profile with none set uses this process's environment, which is the
	// implicit profile's item when no profiles are configured.
	lives map[string]keychain.Live

	// Busy reports whether a profile's detector has seen transcript activity
	// recently. With more than one profile, only a busy profile's active
	// account is polled hot (docs/PROFILES.md §8 D3). Nil means busy.
	Busy func(profile string) bool

	// candidateSweep throttles RefreshCandidatesIn per profile.
	candidateSweep map[string]time.Time
	// crossPool is the last cross-pool finding said per profile.
	crossPool map[string]string
}

// readVault reads an account's vault entry. A seam, so tests never reach the
// keychain.
var readVault = func(accountID string) (*keychain.Blob, error) {
	return keychain.Read(keychain.VaultService(accountID))
}

// SetLive names the live credential a profile's re-attribution reads, and
// the one an unvaulted active account of that profile is polled through.
func (p *Poller) SetLive(profile string, live keychain.Live) {
	if p.lives == nil {
		p.lives = map[string]keychain.Live{}
	}
	p.lives[profile] = live
}

func (p *Poller) liveOf(profile string) keychain.Live {
	if l, ok := p.lives[profile]; ok && l != nil {
		return l
	}
	if len(p.cfg.Profiles) == 0 && profile == state.DefaultProfile {
		return keychain.EnvLive()
	}
	return noLive(profile)
}

// noLive is the live credential of a profile whose item has not been
// resolved: reading it fails, so nothing is attributed or polled through it.
type noLive string

func (n noLive) Name() string { return "unresolved profile " + string(n) }
func (n noLive) Read() (*keychain.Blob, error) {
	return nil, fmt.Errorf("profile %q: its live credential has not been resolved", string(n))
}
func (n noLive) Write(*keychain.Blob) error {
	return fmt.Errorf("profile %q: its live credential has not been resolved", string(n))
}

// activeIn names the profile whose live credential holds accountID. It only
// reads the profile map, never inserts (state.Load materialised every
// configured profile).
func (p *Poller) activeIn(accountID string) (string, bool) {
	if accountID == "" {
		return "", false
	}
	for _, name := range p.cfg.ProfileNames() {
		if in := p.st.Profiles[name]; in != nil && in.Active == accountID {
			return name, true
		}
	}
	return "", false
}

func (p *Poller) isActive(accountID string) bool {
	_, ok := p.activeIn(accountID)
	return ok
}

// activeOf is a profile's active account, read without inserting.
func (p *Poller) activeOf(profile string) string {
	if in := p.st.Profiles[profile]; in != nil {
		return in.Active
	}
	return ""
}

// tokenFor is TokenFor with the live fallback taken from the profile that
// has the account live, never from this process's environment.
func (p *Poller) tokenFor(accountID string) (string, error) {
	tok, _, _, err := p.tokenInfo(accountID)
	return tok, err
}

// countFailure adds one unreadable poll to acct's streak, starting the
// streak's clock on the first.
func (p *Poller) countFailure(acct *state.Account) {
	if acct.ReadFails == 0 || acct.FailSince.IsZero() {
		acct.FailSince = time.Now()
	}
	acct.ReadFails++
}

// staleCopy reports whether token is not the one live in the profile that
// holds accountID — a vault copy superseded by Claude Code's own refresh.
// Unknown (not active anywhere, or the live item unreadable) is not stale:
// the failure then counts.
func (p *Poller) staleCopy(accountID, token string) bool {
	inst, ok := p.activeIn(accountID)
	if !ok {
		return false
	}
	live, err := p.liveOf(inst).Read()
	if err != nil || live == nil || live.ClaudeAIOAuth == nil {
		return false
	}
	return live.ClaudeAIOAuth.AccessToken != token
}

// tokenInfo is tokenFor with the access token's and the refresh token's
// expiries, each zero when unknown.
func (p *Poller) tokenInfo(accountID string) (string, time.Time, time.Time, error) {
	b, err := readVault(accountID)
	if err == nil {
		// The entry names the plan it was vaulted with; recorded so `cs
		// account list` can show it without the keychain (lane 15).
		if b.Meta != nil {
			p.st.SetPlan(accountID, b.Meta.Plan)
		}
		return b.ClaudeAIOAuth.AccessToken, b.ClaudeAIOAuth.Expiry(), b.ClaudeAIOAuth.RefreshExpiry(), nil
	}
	prof, ok := p.activeIn(accountID)
	if !ok {
		return "", time.Time{}, time.Time{}, err
	}
	live, lerr := p.liveOf(prof).Read()
	if lerr != nil {
		return "", time.Time{}, time.Time{}, err
	}
	return live.ClaudeAIOAuth.AccessToken, live.ClaudeAIOAuth.Expiry(), live.ClaudeAIOAuth.RefreshExpiry(), nil
}

// sharedBudgetFor returns the process-wide budget with the configured
// allowance applied. The allowance belongs in config because the true ceiling
// was measured once, imprecisely, and anyone who learns better should be able to
// say so without rebuilding.
func sharedBudgetFor(cfg *config.Config) *usage.Budget {
	b := usage.Shared()
	if cfg != nil && cfg.APIBudget > 0 {
		b.SetAllowance(cfg.APIBudget)
	}
	if cfg != nil {
		if err := b.SetModel(cfg.HotReserveCalls(), cfg.UnseenPerHour()); err != nil {
			slog.Default().Warn("usage model setting not applied", "err", err)
		}
	}
	return b
}

func New(cfg *config.Config, st *state.State, log *slog.Logger) *Poller {
	return &Poller{
		cfg:    cfg,
		st:     st,
		client: usage.NewClient(),
		// The shared, file-backed budget. It was meant to be this since the
		// budget became cross-process on 2026-09-10, but the poller kept a
		// private one: the daemon's polls, most of the program's spend, were
		// invisible to every other caller and to api-calls.json.
		budget:       sharedBudgetFor(cfg),
		log:          log,
		nextPoll:     map[string]time.Time{},
		now:          time.Now,
		hot:          map[string]bool{},
		lastIv:       map[string]time.Duration{},
		lastMoved:    map[string]time.Time{},
		lastSeverity: map[string]string{},
		started:      time.Now(),
	}
}

func (p *Poller) Budget() *usage.Budget { return p.budget }

// SetConfig replaces the config the poller schedules from, for a daemon that
// reloaded it. An added account has no schedule yet, which makes it due on the
// next tick; a removed one loses its schedule. Must be called from the goroutine
// that drives the poller — there is no locking here, as there is none anywhere
// else in it.
func (p *Poller) SetConfig(cfg *config.Config) {
	p.cfg = cfg
	if cfg.APIBudget > 0 {
		p.budget.SetAllowance(cfg.APIBudget)
	}
	if err := p.budget.SetModel(cfg.HotReserveCalls(), cfg.UnseenPerHour()); err != nil {
		p.log.Warn("usage model setting not applied", "err", err)
	}
	keep := map[string]bool{}
	for _, a := range cfg.Accounts {
		keep[a.ID] = true
	}
	for id := range p.nextPoll {
		if !keep[id] {
			delete(p.nextPoll, id)
		}
	}
}

// Blind reports how long it has been since any poll succeeded, and whether that
// is long enough to be a fault rather than a hiccup. Zero duration means a poll
// has succeeded recently.
func (p *Poller) Blind(limit time.Duration) (time.Duration, bool) {
	if p.lastOK.IsZero() {
		// Nothing has ever succeeded. Measure from process start instead, so a
		// daemon that never gets going is caught too.
		p.lastOK = p.started
	}
	d := p.now().Sub(p.lastOK)
	if d <= limit {
		return d, false
	}

	// Not reading because we are deliberately waiting is not blindness. The
	// watchdog exists to catch a wedged daemon, and a daemon serving out a
	// rate-limit lock is the opposite of wedged — it is doing exactly what it
	// was told to do.
	//
	// Conflating the two built a machine that could not recover: the API asked
	// for an hour, the watchdog gave up after ten minutes, the service manager
	// restarted the process, and the fresh one inherited the same lock and was
	// killed again. Twelve restarts in two hours, not one reading among them,
	// while utilization went from 60% to 100% unseen.
	if til, n := p.budget.AnyLocked(); n > 0 {
		p.log.Debug("not reading, but deliberately: holding off until the lock expires",
			"until", til.Format(time.Kitchen), "locked", n, "blind_for", d.Round(time.Second))
		return d, false
	}
	return d, true
}

// Degraded reports whether predictive switching must be treated as unavailable.
func (p *Poller) Degraded() (bool, string) { return p.degraded, p.degradedWhy }

// TokenFor resolves the access token to present when polling an account.
//
// It reads the account's own vault entry, and uses the live credential only when
// that entry actually holds the live token — compared directly, never inferred
// from st.Default().Active.
//
// Trusting st.Default().Active here was a real fault: when the daemon's attribution was
// wrong (its startup poll had timed out), it polled the live credential in the
// name of the wrong account. The live account's usage was then written under two
// different names, and two accounts with entirely different utilization showed
// identical numbers (observed 2026-09-10: both reading 54%/14% while actually at
// 100%/29% and 75%/17%). A wrong belief must not silently redirect a read.
func TokenFor(accountID string, activeID string) (string, error) {
	b, err := readVault(accountID)
	if err != nil {
		// No vault entry. Only fall back to the live credential if this really
		// is the account we think is active, and say so if it is not.
		if accountID != activeID {
			return "", err
		}
		live, lerr := keychain.ReadLive()
		if lerr != nil {
			return "", err
		}
		return live.ClaudeAIOAuth.AccessToken, nil
	}
	return b.ClaudeAIOAuth.AccessToken, nil
}

// PollActive reads the live credential's usage and attributes it to a configured
// account. This is the one call that must always be possible, so it may spend
// the reserved slot.
func (p *Poller) PollActive(ctx context.Context) (*state.Account, error) {
	return p.PollActiveIn(ctx, state.DefaultProfile)
}

// PollActiveIn is PollActive for one profile: it reads that profile's live
// credential and records the account it holds as that profile's active one.
// Every other profile's attribution is left alone.
func (p *Poller) PollActiveIn(ctx context.Context, profile string) (*state.Account, error) {
	blob, err := p.liveOf(profile).Read()
	if err != nil {
		return nil, err
	}
	// Read into a scratch record first: until the org id comes back we do not
	// know which configured account this credential belongs to.
	scratch := scratchFor(profile)
	// Whoever is refused on this token next — the vault included — backs off
	// no further than the live cap.
	p.budget.MarkLive(blob.ClaudeAIOAuth.AccessToken)
	reason := p.fetchInto(ctx, scratch, blob.ClaudeAIOAuth.AccessToken, blob.ClaudeAIOAuth.Expiry(), usage.Swap)

	if reason == usage.ReasonExpired {
		// Parked (R2): no call, so nothing is learnt about attribution and
		// nothing counts as a failure. The account attributed here says why
		// it is not being read, unless it already says it needs a sign-in.
		if prev := p.attributeIn(profile, ""); prev != Unattributed {
			a := p.st.Get(prev)
			a.TokenExpiry = blob.ClaudeAIOAuth.Expiry()
			a.RefreshExpiry = blob.ClaudeAIOAuth.RefreshExpiry()
			park(a, scratch.LastErr)
			return a, fmt.Errorf("not reading the live credential: %s", scratch.LastErr)
		}
		return nil, fmt.Errorf("not reading the live credential: %s", scratch.LastErr)
	}
	if scratch.Last == nil && scratch.OrgID == "" {
		// We could not read the credential's organization, so we cannot say
		// which account is live. Leave the previous attribution alone and
		// report the failure rather than inventing confidence.
		//
		// The failed read still counts against the account already attributed
		// here (IMPROVEMENTS A2): it is that account being unreadable, through
		// the very token the session runs on.
		prev := p.attributeIn(profile, "")
		if prev != Unattributed && scratch.ReadFails > 0 {
			a := p.st.Get(prev)
			p.countFailure(a)
			a.LastErr = scratch.LastErr
			a.TokenExpiry = blob.ClaudeAIOAuth.Expiry()
		}
		return p.st.Get(prev), fmt.Errorf("could not confirm the active account: %s", scratch.LastErr)
	}
	id := p.attributeIn(profile, scratch.OrgID)
	acct := p.st.Get(id)
	acct.OrgID = scratch.OrgID
	acct.RefreshExpiry = blob.ClaudeAIOAuth.RefreshExpiry()
	acct.TokenExpiry = blob.ClaudeAIOAuth.Expiry()
	acct.LastErr = scratch.LastErr
	if scratch.Last != nil {
		acct.ReadFails, acct.FailSince = 0, time.Time{}
		p.noteMovement(id, acct.Last, scratch.Last)
		acct.Last = scratch.Last
		acct.LastAt = scratch.LastAt
		if !acct.BurntTil.IsZero() && p.now().After(acct.BurntTil) {
			acct.BurntTil = time.Time{}
			acct.BurntWin = ""
		}
	}
	p.noteCrossPool(profile, id)
	ps := p.st.Profile(profile)
	ps.SetActive(id)
	if scratch.OrgID != "" {
		ps.LiveKey, ps.LiveOrg = usage.CredKey(blob.ClaudeAIOAuth.AccessToken), scratch.OrgID
	}
	// This was the account's poll: its next scheduled one counts from here,
	// rather than spending a second call of its allowance on the same figure.
	if scratch.Last != nil && id != Unattributed {
		p.schedule(id, p.now(), acct)
	}
	return acct, nil
}

// noteCrossPool says when a profile is found holding an account from
// another profile's pool (D17: record the truth, say it loudly; the next
// decision moves it back into its own pool if the target is free), or one
// live in a second profile (§3: whichever refreshes first revokes the
// other's copy). Once per distinct finding: re-attribution runs on every
// save tick, and the same error each time is noise that buries the next one.
func (p *Poller) noteCrossPool(profile, id string) {
	owner, ok := p.cfg.ProfileOf(id)
	foreign := ok && owner != profile
	other, two := p.activeIn(id)
	two = two && other != profile && id != Unattributed
	if !two {
		other = ""
	}
	if p.crossPool == nil {
		p.crossPool = map[string]string{}
	}
	if !foreign && !two {
		delete(p.crossPool, profile)
		return
	}
	sig := id + "|" + owner + "|" + other
	if p.crossPool[profile] == sig {
		return
	}
	p.crossPool[profile] = sig
	if foreign {
		p.log.Error("this profile holds an account from another profile's pool",
			"profile", profile, "account", id, "pool_of", owner)
	}
	if two {
		p.log.Error("one account is live in two profiles; the first to refresh will log the other out",
			"account", id, "profile", profile, "also_in", other)
	}
}

// poolOf is a profile's effective pool.
func (p *Poller) poolOf(profile string) []string {
	if in, ok := p.cfg.ProfileNamed(profile); ok {
		return in.Pool
	}
	return nil
}

// attribute maps an organization id to a configured account.
//
// It will NOT guess. An explicit org_id in config wins; failing that, an org id
// already observed for an account wins. If neither matches, the reading is held
// against the pseudo-account "active" and the user is told to pin it. Adopting
// "the first account in priority order" would cheerfully file a personal
// credential under a work account, which is worse than saying nothing.
func (p *Poller) attribute(orgID string) string { return p.attributeIn(state.DefaultProfile, orgID) }

// attributeIn is attribute for one profile. Its own pool is searched first,
// so two accounts sharing an organization in different pools resolve to the
// one this profile rotates; then every account, since what is live is a fact
// whichever pool it is from. With no profiles configured the pool is every
// account and the two passes find the same answer.
func (p *Poller) attributeIn(profile, orgID string) string {
	if orgID == "" {
		if a := p.activeOf(profile); a != "" {
			return a
		}
		return Unattributed
	}
	inPool := map[string]bool{}
	for _, id := range p.poolOf(profile) {
		inPool[id] = true
	}
	for _, pass := range []func(string) bool{
		func(id string) bool { return inPool[id] },
		func(string) bool { return true },
	} {
		for _, a := range p.cfg.Ordered() {
			if pass(a.ID) && a.OrgID == orgID {
				return a.ID
			}
		}
		for _, a := range p.cfg.Ordered() {
			if ex, ok := p.st.Accounts[a.ID]; ok && pass(a.ID) && ex.OrgID == orgID {
				return a.ID
			}
		}
	}
	return Unattributed
}

// Unattributed is the id used when the live credential cannot be matched to a
// configured account.
const Unattributed = state.Unattributed

// RefreshStale reads any account whose figure is older than maxAge, and reports
// how many it managed. It is what `cs status` calls: an explicit request for the
// current picture, rather than the daemon's own paced schedule.
//
// It spends from the shared budget like everything else, so it cannot crowd out
// the daemon; an account it cannot afford simply keeps the reading it had.
func (p *Poller) RefreshStale(ctx context.Context, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		maxAge = 90 * time.Second
	}
	done := 0
	for _, a := range p.cfg.Ordered() {
		acct := p.st.Get(a.ID)
		if acct.Last != nil && p.now().Sub(acct.LastAt) < maxAge {
			continue
		}
		tok, exp, _, err := p.tokenInfo(a.ID)
		if err != nil {
			acct.LastErr = "no stored credential"
			continue
		}
		p.budget.Pace(ctx)
		switch p.fetchInto(ctx, acct, tok, exp, usage.Scheduled) {
		case usage.ReasonOK:
		case usage.ReasonLockout, usage.ReasonAccount, usage.ReasonExpired:
			continue // this account is backing off or parked; the others are not
		default:
			return done, nil // out of budget; the rest keep what they had
		}
		if acct.Last != nil {
			done++
		}
	}
	return done, nil
}

// RefreshCandidates re-reads every account that is not the active one, for the
// moment a decision is about to turn on whether any of them has room.
//
// Declaring "nothing else is usable" from ten-minute-old figures is the worst
// mistake this program can make: it is the difference between rotating and
// sitting at 100% believing there is nowhere to go. When the answer matters,
// the figures are worth a call.
func (p *Poller) RefreshCandidates(ctx context.Context, olderThan time.Duration) int {
	return p.RefreshCandidatesIn(ctx, olderThan, state.DefaultProfile)
}

// RefreshCandidatesIn is RefreshCandidates for one profile: only its pool is
// re-read, since nothing else is a candidate there, and its own active
// account is skipped. Each profile is throttled on its own.
func (p *Poller) RefreshCandidatesIn(ctx context.Context, olderThan time.Duration, profile string) int {
	// Once a minute at most. The decision loop runs every twenty seconds, and
	// re-reading every candidate each time is a burst by another name.
	if p.now().Sub(p.candidateSweep[profile]) < time.Minute {
		return 0
	}
	if p.candidateSweep == nil {
		p.candidateSweep = map[string]time.Time{}
	}
	p.candidateSweep[profile] = p.now()
	inPool := map[string]bool{}
	for _, id := range p.poolOf(profile) {
		inPool[id] = true
	}
	active := p.activeOf(profile)
	done := 0
	for _, a := range p.cfg.Ordered() {
		if a.ID == active || !inPool[a.ID] {
			continue
		}
		acct := p.st.Get(a.ID)
		// Only what is actually doubtful: fresh figures need no re-reading, and
		// an account whose reading has outlived its window certainly does.
		if acct.Last != nil && !acct.ExpiredAt(p.now()) &&
			p.now().Sub(acct.LastAt) < olderThan {
			continue
		}
		tok, exp, _, err := p.tokenInfo(a.ID)
		if err != nil {
			continue
		}
		p.budget.Pace(ctx)
		switch p.fetchInto(ctx, acct, tok, exp, usage.Scheduled) {
		case usage.ReasonOK:
			done++
		case usage.ReasonLockout, usage.ReasonAccount, usage.ReasonExpired:
			continue
		default:
			return done
		}
	}
	return done
}

// Tick performs at most one scheduled poll, respecting the budget. The daemon
// calls it on a short timer; it does its own pacing.
func (p *Poller) Tick(ctx context.Context) {
	now := p.now()
	for _, a := range p.due(now) {
		// Resolve the credential BEFORE spending from the call budget. An
		// account with no vault entry cannot be polled at all, and charging the
		// budget for it burned real capacity on accounts that were only ever
		// placeholders.
		tok, exp, rexp, err := p.tokenInfo(a.ID)
		if err != nil {
			acct := p.st.Get(a.ID)
			acct.LastErr = "no stored credential"
			p.nextPoll[a.ID] = now.Add(IdleInterval) // do not retry in a tight loop
			continue
		}
		acct := p.st.Get(a.ID)
		acct.TokenExpiry = exp
		// What the credential says of its refresh token, as the live poll
		// records it: `account list` reports it without the keychain
		// (IMPROVEMENTS F5). Zero (not reported) is unknown.
		acct.RefreshExpiry = rexp
		// A hot poll may spend the allowance routine polls hold back for it.
		// Routine reads keep the full floor, so the reserve rebuilds (at the
		// 3m default, 20 an hour against a live refill of ~28). Only an
		// overdue read of the account in use (config.OverdueAfter) may spend
		// it, down to the swap reserve + 1 — after a hot spell has spent the
		// reserve — so its reading never reaches the stale-decision cap.
		priority := usage.Scheduled
		overdueAt := time.Time{}
		// A quiet profile's live account is read at poll_idle (R4), so it is
		// never overdue at poll_active and never spends the hot reserve.
		if prof, active := p.activeIn(a.ID); active && !p.quiet(prof) {
			overdueAt = acct.LastAt.Add(p.overdueAfter())
			switch {
			case p.hot[a.ID]:
				priority = usage.Hot
			case !overdueAt.IsZero() && !acct.LastAt.IsZero() && !now.Before(overdueAt):
				priority = usage.Overdue
			}
		}
		switch reason := p.fetchInto(ctx, acct, tok, exp, priority); reason {
		case usage.ReasonOK:
			p.schedule(a.ID, now, acct)
			return // one API call per tick keeps the budget honest
		case usage.ReasonExpired:
			// Parked (R2): no call was made. Look again shortly, for the
			// renewed token, and give the tick to the next account.
			p.hold(a.ID, now.Add(ParkRecheck))
			continue
		case usage.ReasonLockout:
			// Only this account is backing off. Come back to it when its lock
			// ends, and give the tick to the next account that is due.
			if til, locked := p.budget.LockedUntil(tok); locked {
				p.hold(a.ID, til)
			}
			continue
		case usage.ReasonAccount:
			// This account's own allowance is down to its reserve (§42).
			// Come back when it has refilled enough, and give the tick to
			// the next account: the others' allowances are their own.
			til := p.budget.AccountReadyAt(tok, priority)
			// A routine read of the account in use that is refused now comes
			// back no later than when it becomes overdue, if the allowance
			// allows an overdue read by then.
			if priority == usage.Scheduled && !overdueAt.IsZero() && !acct.LastAt.IsZero() {
				if alt := laterOf(overdueAt, p.budget.AccountReadyAt(tok, usage.Overdue)); alt.Before(til) {
					til = alt
				}
			}
			p.hold(a.ID, til)
			continue
		default:
			p.log.Debug("skipping scheduled poll", "account", a.ID, "reason", string(reason))
			return
		}
	}
}

// mayPollHot reports whether a profile's active account may be polled at
// poll_hot. With more than one profile, only a busy one may (D3): two hot
// profiles would compete for one budget, and a quiet profile is not
// spending, so a reading that is a little old costs it nothing. With one
// profile there is nothing to compete with, and hot polling is as it always
// was.
func (p *Poller) mayPollHot(profile string) bool {
	if len(p.cfg.EffectiveProfiles()) < 2 || p.Busy == nil {
		return true
	}
	return p.Busy(profile)
}

// quiet reports whether a profile's sessions are known not to be busy (D15):
// its live account is then read at poll_idle, not poll_active (R4), and the
// daemon may renew its live token (R2). Unknown (no Busy) is not quiet.
func (p *Poller) quiet(profile string) bool {
	return p.Busy != nil && !p.Busy(profile)
}

// overdueAfter is how old the reading of the account in use may get before
// a routine read of it is Overdue: config.OverdueAfter (3m15s at the 3m
// default). With the overdue read's wait for its allowance it must stay below
// the daemon's stale-decision cap (4m at the default), or every evaluation
// after a hot spell logs "deciding on a stale reading".
func (p *Poller) overdueAfter() time.Duration {
	iv := p.cfg.PollActive.Duration
	if iv <= 0 {
		iv = ActiveInterval
	}
	return config.OverdueAfter(iv)
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// hold puts off an account's next poll until til because the budget refused
// it: a 429's lock, or its own allowance. Unlike a schedule, a hold is not
// pulled in when the account becomes the one in use.
func (p *Poller) hold(id string, til time.Time) {
	p.nextPoll[id] = til
	if p.held == nil {
		p.held = map[string]time.Time{}
	}
	p.held[id] = til
}

// due lists accounts whose next poll time has arrived, active first.
//
// An account that has just become the one in use may still carry an idle
// schedule, up to poll_idle away; it is due once its reading is poll_active
// old instead, unless the budget is holding it off.
func (p *Poller) due(now time.Time) []config.Account {
	var out []config.Account
	for _, a := range p.cfg.Ordered() {
		p.seed(a.ID)
		if t, ok := p.nextPoll[a.ID]; ok && now.Before(t) && !p.overdueActive(a.ID, now) {
			continue
		}
		if p.isActive(a.ID) {
			out = append([]config.Account{a}, out...)
			continue
		}
		out = append(out, a)
	}
	return out
}

func (p *Poller) overdueActive(id string, now time.Time) bool {
	if now.Before(p.held[id]) {
		return false
	}
	prof, active := p.activeIn(id)
	if !active || p.quiet(prof) {
		return false // a quiet profile's account is read at poll_idle (R4)
	}
	acct, ok := p.st.Accounts[id]
	if !ok || acct.LastAt.IsZero() {
		return false
	}
	iv := p.cfg.PollActive.Duration
	if iv <= 0 {
		iv = ActiveInterval
	}
	return !now.Before(acct.LastAt.Add(iv))
}

// seed starts an account with no schedule yet -- a restarted daemon's --
// from its last reading (R4): due when that reading is its routine interval
// old, rather than at once. Every account read in the last few minutes was
// otherwise read again in the first ticks after a restart. One never read
// stays unscheduled, and so due.
func (p *Poller) seed(id string) {
	if _, ok := p.nextPoll[id]; ok {
		return
	}
	acct, ok := p.st.Accounts[id]
	if !ok || acct.LastAt.IsZero() {
		return
	}
	p.nextPoll[id] = acct.LastAt.Add(p.routineInterval(id))
}

// routineInterval is an account's cadence with nothing moving: poll_active
// for the account in use in a busy (or unknown) profile, poll_idle for every
// other account, a quiet profile's live one included (R4).
func (p *Poller) routineInterval(id string) time.Duration {
	if prof, active := p.activeIn(id); active && !p.quiet(prof) {
		if iv := p.cfg.PollActive.Duration; iv > 0 {
			return iv
		}
		return ActiveInterval
	}
	if iv := p.cfg.PollIdle.Duration; iv > 0 {
		return iv
	}
	return IdleInterval
}

// ResumeIn lets a restarted daemon skip a profile's startup read (R4): when
// its live token is the one last attributed there (ProfileState.LiveKey),
// unexpired, and the account it holds was read within poll_active, the
// attribution and the reading both stand. The account's schedule starts from
// that reading. It reports whether it resumed; false means read as before.
func (p *Poller) ResumeIn(profile string) bool {
	ps := p.st.Profiles[profile]
	if ps == nil || ps.Active == "" || ps.Active == Unattributed || ps.LiveKey == "" {
		return false
	}
	blob, err := p.liveOf(profile).Read()
	if err != nil || blob == nil || blob.ClaudeAIOAuth == nil {
		return false
	}
	o := blob.ClaudeAIOAuth
	now := p.now()
	if usage.CredKey(o.AccessToken) != ps.LiveKey || usage.Expired(o.Expiry(), now) {
		return false
	}
	acct, ok := p.st.Accounts[ps.Active]
	if !ok || acct.Last == nil || acct.LastAt.IsZero() {
		return false
	}
	iv := p.cfg.PollActive.Duration
	if iv <= 0 {
		iv = ActiveInterval
	}
	if now.Sub(acct.LastAt) >= iv {
		return false
	}
	p.budget.MarkLive(o.AccessToken)
	acct.TokenExpiry, acct.RefreshExpiry = o.Expiry(), o.RefreshExpiry()
	p.seed(ps.Active)
	return true
}

// noteMovement records when an account's reading rose, for the hot cadence.
// A fall is a window reset: whatever was moving has stopped.
func (p *Poller) noteMovement(id string, prev, cur *usage.Usage) {
	if prev == nil || cur == nil || p.lastMoved == nil {
		return
	}
	_, was := prev.Worst()
	_, is := cur.Worst()
	switch {
	case is > was:
		p.lastMoved[id] = cur.FetchedAt
	case is < was:
		delete(p.lastMoved, id)
	}
}

// stillAfter is how long without a rise ends "moving" at this burn rate: the
// time a point and a half takes at that rate — a whole-point reading must
// show a rise within it if the burn goes on — but at least two hot polls and
// at most StillAfter. A fixed five minutes kept a spell that had stopped
// polling hot for five more calls, spending the hot reserve on a reading that
// was standing still, and left the next routine read waiting on its refill
// past the daemon's 3m stale-decision cap (final review, 2026-10-07).
func (p *Poller) stillAfter(rate float64) time.Duration {
	hot := p.cfg.PollHot.Duration
	if hot <= 0 {
		hot = ActiveHotInterval
	}
	d := time.Duration(1.5 / rate * float64(time.Minute))
	return max(2*hot, min(d, StillAfter))
}

// isHot is the movement-driven test (docs/DESIGN.md 4.3c): the account is
// moving — its reading rose within StillAfter, at MovingBurn or faster — and
// at that rate it reaches its trigger within config.HotLookahead. Standing
// still near the line is not hot: nothing is changing, and a faster reading
// would say the same thing. hot_threshold stays a floor, crossed early only
// by a fast burn.
func (p *Poller) isHot(id string, now time.Time, acct *state.Account) bool {
	if acct.Last == nil {
		return false
	}
	moved, ok := p.lastMoved[id]
	if !ok {
		return false
	}
	rate := acct.BurnRate()
	if rate < MovingBurn || now.Sub(moved) >= p.stillAfter(rate) {
		return false
	}
	five, seven := p.cfg.SwitchAt, p.cfg.SwitchAtWeekly
	if five <= 0 {
		five = config.DefaultSwitchAt
	}
	if seven <= 0 {
		seven = config.DefaultSwitchAtWeekly
	}
	_, worst, over := acct.Last.WorstAgainst(five, seven)
	if -over > rate*config.HotLookahead.Minutes() {
		return false // moving, but the trigger is out of reach
	}
	floor := p.cfg.HotThreshold
	if floor <= 0 {
		floor = HotThreshold
	}
	return worst >= floor || rate >= FastBurn
}

func (p *Poller) schedule(id string, now time.Time, acct *state.Account) {
	delete(p.held, id)
	// An exhausted account becomes usable at a time the API already told us.
	// Waiting out a ten-minute idle interval to notice means ten minutes of
	// believing there is nowhere to rotate to, while there is.
	if at := acct.RepollAt(); at.After(now) {
		defer func() {
			if cur, ok := p.nextPoll[id]; !ok || at.Before(cur) {
				p.nextPoll[id] = at
			}
		}()
	}

	iv := p.cfg.PollIdle.Duration
	if iv <= 0 {
		iv = IdleInterval
	}
	hot := false
	// Active in any profile is active: it is the one being spent there. A
	// quiet profile's is read at poll_idle (R4) unless it is moving within
	// reach of its trigger where hot polling is allowed (one profile, D16).
	if prof, active := p.activeIn(id); active && (!p.quiet(prof) || (p.mayPollHot(prof) && p.isHot(id, now, acct))) {
		iv = p.cfg.PollActive.Duration
		if iv <= 0 {
			iv = ActiveInterval
		}
		if p.mayPollHot(prof) && p.isHot(id, now, acct) {
			hot = true
			iv = p.cfg.PollHot.Duration
			if iv <= 0 {
				iv = ActiveHotInterval
			}
		} else if prev := p.lastIv[id]; prev > 0 && 2*prev < iv {
			// Cooling: drift back by doubling, so a pause between turns
			// does not drop the close watch all at once.
			iv = 2 * prev
		}
	}
	if p.hot != nil {
		p.hot[id] = hot
	}
	if p.lastIv != nil {
		p.lastIv[id] = iv
	}
	p.nextPoll[id] = now.Add(iv)
}

// fetchInto reads one account's usage into acct. It reports ReasonOK when a call
// was made, whatever its outcome, and otherwise why the budget refused it.
//
// The budget is consulted here and only here. Callers used to ask it first and
// then call this, which asked again — so every poll was recorded twice, and the
// window allowed half the calls its allowance says.
//
// A token past its expiry (exp; zero is unknown) is never sent (R2): the
// endpoint answers one with 429, not 401 (GROUND_TRUTH §46), and every such
// call was a strike. It reports ReasonExpired, with no call, no strike and no
// failure counted, and LastErr says so.
func (p *Poller) fetchInto(ctx context.Context, acct *state.Account, token string, exp time.Time, priority usage.Priority) usage.Reason {
	if usage.Expired(exp, p.now()) {
		park(acct, (&usage.ExpiredError{At: exp}).Error())
		return usage.ReasonExpired
	}
	// Ask every time, for every caller. This check used to run only for
	// priority polls, so an ordinary one could reach the API while the budget
	// was locked — collect a fresh Retry-After: 3600, and re-arm the very lock
	// it had just ignored. That is how a transient refusal became a two-hour
	// outage that renewed itself.
	if ok, reason := p.budget.Allow(token, priority); !ok {
		acct.LastErr = "not polled: " + string(reason)
		return reason
	}
	u, err := p.client.Fetch(ctx, token)
	if err != nil {
		if rl, ok := usage.IsRateLimited(err); ok {
			if p.isActive(acct.ID) || acct.ID == "active" {
				// "active" is PollActive's scratch record: the live
				// credential before it is attributed.
				p.budget.PenalizeLive(token, rl.RetryAfter)
			} else {
				p.budget.Penalize(token, rl.RetryAfter)
			}
			acct.LastErr = rl.Error()
			// Report the backoff actually applied, not the header value: the
			// endpoint sends Retry-After: 0, which means nothing (§42).
			// And the header itself (R3): a long one is the server saying
			// the refusal will last, and the backoff steps on it.
			wait, strikes := p.budget.CurrentBackoff(token)
			p.log.Warn("usage API refused us; backing off",
				"account", acct.ID, "consecutive", strikes, "waiting", wait.Round(time.Second),
				"retry_after", rl.RetryAfter.Round(time.Second))
			return usage.ReasonOK
		}
		if usage.IsShapeError(err) {
			p.degrade(err.Error())
		}
		acct.LastErr = err.Error()
		// An unreadable poll, for the blind-failover count (IMPROVEMENTS A2).
		// The 429 above is not one: it is the endpoint's own burst limit and
		// clears by itself (GROUND_TRUTH §42). Nor is our own cancellation.
		//
		// Nor is a rejected token that is only a stale copy: Claude Code's own
		// refresh revokes the vault's copy of the active account's token until
		// the daemon re-captures it. If the profile holding this account has
		// a different token live, the 401 is about our copy, not the account.
		if ctx.Err() == nil && !(usage.IsTokenRejected(err) && p.staleCopy(acct.ID, token)) {
			p.countFailure(acct)
		}
		return usage.ReasonOK
	}
	acct.ReadFails, acct.FailSince = 0, time.Time{}
	// Keep the previous reading so a burn rate can be computed.
	if acct.Last != nil && !acct.LastAt.IsZero() {
		_, prev := acct.Last.Worst()
		acct.PrevWorst, acct.PrevAt = prev, acct.LastAt
	}
	p.noteMovement(acct.ID, acct.Last, u)
	acct.Last = u
	// LastAt moves before the rate is taken: until it does it still equals
	// the PrevAt just set, the pair spans no time, and the rate came out 0 on
	// every poll, so last_rate was never recorded (fixed in 0.6.1).
	acct.LastAt = u.FetchedAt
	// Remember the rate while we can still see it. Once polling stalls the pair
	// of readings goes flat and no rate can be derived from it, so the value
	// captured here is what keeps the projection honest through the gap.
	if r := acct.BurnRate(); r > 0 {
		acct.LastRate = r
	} else if _, w := u.Worst(); w < acct.PrevWorst {
		acct.LastRate = 0 // window reset; the old rate describes a dead window
	}
	noteRise(acct, u)
	acct.LastErr = ""
	p.lastOK = u.FetchedAt
	// The server answered, so whatever was refusing us has stopped.
	p.budget.Succeeded(token)
	if u.OrgID != "" {
		acct.OrgID = u.OrgID
	}
	// Record which seat this reading describes, so a record can never be
	// mistaken for a colleague's in the same organization.
	if seat := seatOfVault(acct.ID); seat != "" {
		acct.Seat = seat
	}
	// A reading that shows headroom clears a burn whose window has reset.
	if !acct.BurntTil.IsZero() && p.now().After(acct.BurntTil) {
		acct.BurntTil = time.Time{}
		acct.BurntWin = ""
	}
	key := acct.ID
	if acct.SeverityKey != "" {
		key = acct.SeverityKey
	}
	p.noteSeverity(key, u)
	return usage.ReasonOK
}

// park records why an account is not being read: its token expired (R2). A
// needs-login verdict already there is kept -- it says more, and the sign-in
// banner reads it.
func park(acct *state.Account, why string) {
	if state.NeedsLoginErr(acct.LastErr) {
		return
	}
	acct.LastErr = why
}

// scratchFor is the record a profile's live credential is read into before
// it is attributed. Its ID is "active" (state.Unattributed) for every
// profile; its severity is tracked per profile, so two profiles' live
// accounts at different severities do not read as one account flapping.
func scratchFor(profile string) *state.Account {
	return &state.Account{ID: state.Unattributed, SeverityKey: state.Unattributed + "@" + profile}
}

// noteRise records the rate a rising pair of readings shows, and when, for
// the runway forecast (IMPROVEMENTS F2): it carries that rate over a flat
// pair only while the rise is recent. A fall is a window reset, after which
// the old rate describes a window that no longer exists. Called with the
// new reading in acct.Last and the previous one in PrevWorst/PrevAt.
func noteRise(acct *state.Account, u *usage.Usage) {
	if acct.PrevAt.IsZero() {
		return
	}
	_, w := u.Worst()
	mins := u.FetchedAt.Sub(acct.PrevAt).Minutes()
	switch {
	case w > acct.PrevWorst && mins > 0:
		acct.RiseRate, acct.RiseAt = (w-acct.PrevWorst)/mins, u.FetchedAt
	case w < acct.PrevWorst:
		acct.RiseRate, acct.RiseAt = 0, time.Time{}
	}
}

// noteSeverity records transitions in the API's own severity field. Every
// limits[] entry is tracked, not just the active one, since a window can start
// warning before it becomes the binding constraint.
func (p *Poller) noteSeverity(accountID string, u *usage.Usage) {
	for _, l := range u.Limits {
		if l.Severity == "" {
			continue
		}
		// Per-model weekly limits share a kind, so the model is part of the
		// key: otherwise two of them would read as one limit flapping.
		name := l.Kind
		if m := l.ModelName(); m != "" {
			name += ":" + m
		}
		key := accountID + "/" + name
		prev, seen := p.lastSeverity[key]
		if seen && prev == l.Severity {
			continue
		}
		p.lastSeverity[key] = l.Severity
		if !seen {
			continue // first sighting is a baseline, not a transition
		}
		p.log.Info("severity changed", "account", accountID, "limit", name,
			"from", prev, "to", l.Severity, "percent", l.Pct())
		if p.OnSeverityChange != nil {
			p.OnSeverityChange(accountID, name, prev, l.Severity, l.Pct())
		}
	}
}

// degrade disables predictive switching loudly and permanently for this run.
func (p *Poller) degrade(why string) {
	if p.degraded {
		return
	}
	p.degraded = true
	p.degradedWhy = why
	p.log.Error("usage API shape changed: predictive switching DISABLED, falling back to reactive-only",
		"detail", why, "endpoint", usage.Endpoint)
}

// ApplyRejection records an authoritative refusal from the transcript detector.
// Its resetsAt overrides whatever the poller believed.
func (p *Poller) ApplyRejection(accountID, window string, resetsAt time.Time) {
	acct := p.st.Get(accountID)
	if resetsAt.After(acct.BurntTil) {
		acct.BurntTil = resetsAt
		acct.BurntWin = window
	}
	p.log.Warn("account refused", "account", accountID, "window", window,
		"resets_at", resetsAt.Format(time.RFC3339))
}

// seatOfVault reads the seat annotation from an account's vault entry. Local,
// no network.
func seatOfVault(accountID string) string {
	b, err := readVault(accountID)
	if err != nil || b.Meta == nil {
		return ""
	}
	return b.Meta.Seat()
}
