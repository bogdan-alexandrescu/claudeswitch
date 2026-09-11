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
	// HotThreshold is where close watching begins. Well below the trigger,
	// because the decision needs to be taken before the line is crossed, not
	// after — and a fast burn covers the gap between 60 and 85 in minutes.
	HotThreshold = 60.0
	StaleAfter   = 10 * time.Minute

	// ReattributeInterval is how often the daemon re-derives which account is
	// live. It was doing this on every save tick, an extra priority call every
	// two minutes on top of everything else.
	ReattributeInterval = 15 * time.Minute

	// FastBurn is utilization points per minute above which the active account
	// is polled on the short interval whatever its level. Measured 2026-09-10:
	// heavy use moved 8 points in 3 minutes.
	FastBurn = 1.5

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

	// lastOK is when a poll last succeeded. A daemon that keeps running while
	// seeing nothing is the most dangerous state this program has: it decides
	// on figures that stop moving and says nothing, because "stay" is logged at
	// debug level. Tonight it sat at 93% that way.
	lastOK  time.Time
	started time.Time
	// lastCandidateSweep throttles the on-demand re-read of rotation targets.
	lastCandidateSweep time.Time

	// lastSeverity tracks severity per account+limit kind so transitions can be
	// recorded. Keyed "account/kind".
	lastSeverity map[string]string

	// OnSeverityChange, if set, is called for every severity transition.
	OnSeverityChange func(account, kind, from, to string, percent float64)
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
	return b
}

func New(cfg *config.Config, st *state.State, log *slog.Logger) *Poller {
	return &Poller{
		cfg:          cfg,
		st:           st,
		client:       usage.NewClient(),
		budget:       usage.NewBudget(),
		log:          log,
		nextPoll:     map[string]time.Time{},
		lastSeverity: map[string]string{},
		started:      time.Now(),
	}
}

func (p *Poller) Budget() *usage.Budget { return p.budget }

// Blind reports how long it has been since any poll succeeded, and whether that
// is long enough to be a fault rather than a hiccup. Zero duration means a poll
// has succeeded recently.
func (p *Poller) Blind(limit time.Duration) (time.Duration, bool) {
	if p.lastOK.IsZero() {
		// Nothing has ever succeeded. Measure from process start instead, so a
		// daemon that never gets going is caught too.
		p.lastOK = p.started
	}
	d := time.Since(p.lastOK)
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
	if til, locked := p.budget.LockedUntil(); locked {
		p.log.Debug("not reading, but deliberately: holding off until the lock expires",
			"until", til.Format(time.Kitchen), "blind_for", d.Round(time.Second))
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
// from st.Active.
//
// Trusting st.Active here was a real fault: when the daemon's attribution was
// wrong (its startup poll had timed out), it polled the live credential in the
// name of the wrong account. The live account's usage was then written under two
// different names, and two accounts with entirely different utilization showed
// identical numbers (observed 2026-09-10: both reading 54%/14% while actually at
// 100%/29% and 75%/17%). A wrong belief must not silently redirect a read.
func TokenFor(accountID string, activeID string) (string, error) {
	b, err := keychain.Read(keychain.VaultService(accountID))
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
	blob, err := keychain.ReadLive()
	if err != nil {
		return nil, err
	}
	// Read into a scratch record first: until the org id comes back we do not
	// know which configured account this credential belongs to.
	scratch := &state.Account{ID: "active"}
	p.fetchInto(ctx, scratch, blob.ClaudeAIOAuth.AccessToken, true)

	if scratch.Last == nil && scratch.OrgID == "" {
		// We could not read the credential's organization, so we cannot say
		// which account is live. Leave the previous attribution alone and
		// report the failure rather than inventing confidence.
		return p.st.Get(p.attribute("")), fmt.Errorf("could not confirm the active account: %s", scratch.LastErr)
	}
	id := p.attribute(scratch.OrgID)
	acct := p.st.Get(id)
	acct.OrgID = scratch.OrgID
	acct.RefreshExpiry = blob.ClaudeAIOAuth.RefreshExpiry()
	acct.LastErr = scratch.LastErr
	if scratch.Last != nil {
		acct.Last = scratch.Last
		acct.LastAt = scratch.LastAt
		if !acct.BurntTil.IsZero() && time.Now().After(acct.BurntTil) {
			acct.BurntTil = time.Time{}
			acct.BurntWin = ""
		}
	}
	p.st.SetActive(id)
	return acct, nil
}

// attribute maps an organization id to a configured account.
//
// It will NOT guess. An explicit org_id in config wins; failing that, an org id
// already observed for an account wins. If neither matches, the reading is held
// against the pseudo-account "active" and the user is told to pin it. Adopting
// "the first account in priority order" would cheerfully file a personal
// credential under a work account, which is worse than saying nothing.
func (p *Poller) attribute(orgID string) string {
	if orgID == "" {
		if p.st.Active != "" {
			return p.st.Active
		}
		return Unattributed
	}
	for _, a := range p.cfg.Ordered() {
		if a.OrgID == orgID {
			return a.ID
		}
	}
	for _, a := range p.cfg.Ordered() {
		if ex, ok := p.st.Accounts[a.ID]; ok && ex.OrgID == orgID {
			return a.ID
		}
	}
	return Unattributed
}

// Unattributed is the id used when the live credential cannot be matched to a
// configured account.
const Unattributed = "active"

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
		if acct.Last != nil && time.Since(acct.LastAt) < maxAge {
			continue
		}
		tok, err := TokenFor(a.ID, p.st.Active)
		if err != nil {
			acct.LastErr = "no stored credential"
			continue
		}
		if ok, _ := p.budget.Allow(false); !ok {
			break // out of budget; the rest keep what they had
		}
		p.budget.Pace(ctx)
		p.fetchInto(ctx, acct, tok, false)
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
	// Once a minute at most. The decision loop runs every twenty seconds, and
	// re-reading every candidate each time is a burst by another name.
	if time.Since(p.lastCandidateSweep) < time.Minute {
		return 0
	}
	p.lastCandidateSweep = time.Now()
	done := 0
	for _, a := range p.cfg.Ordered() {
		if a.ID == p.st.Active {
			continue
		}
		acct := p.st.Get(a.ID)
		// Only what is actually doubtful: fresh figures need no re-reading, and
		// an account whose reading has outlived its window certainly does.
		if acct.Last != nil && !acct.ExpiredAt(time.Now()) &&
			time.Since(acct.LastAt) < olderThan {
			continue
		}
		tok, err := TokenFor(a.ID, p.st.Active)
		if err != nil {
			continue
		}
		if ok, _ := p.budget.Allow(false); !ok {
			break
		}
		p.budget.Pace(ctx)
		p.fetchInto(ctx, acct, tok, false)
		done++
	}
	return done
}

// Tick performs at most one scheduled poll, respecting the budget. The daemon
// calls it on a short timer; it does its own pacing.
func (p *Poller) Tick(ctx context.Context) {
	now := time.Now()
	for _, a := range p.due(now) {
		// Resolve the credential BEFORE spending from the call budget. An
		// account with no vault entry cannot be polled at all, and charging the
		// budget for it burned real capacity on accounts that were only ever
		// placeholders.
		tok, err := TokenFor(a.ID, p.st.Active)
		if err != nil {
			acct := p.st.Get(a.ID)
			acct.LastErr = "no stored credential"
			p.nextPoll[a.ID] = now.Add(IdleInterval) // do not retry in a tight loop
			continue
		}
		ok, reason := p.budget.Allow(false)
		if !ok {
			p.log.Debug("skipping scheduled poll", "account", a.ID, "reason", string(reason))
			return
		}
		acct := p.st.Get(a.ID)
		p.fetchInto(ctx, acct, tok, false)
		p.schedule(a.ID, now, acct)
		return // one API call per tick keeps the budget honest
	}
}

// due lists accounts whose next poll time has arrived, active first.
func (p *Poller) due(now time.Time) []config.Account {
	var out []config.Account
	for _, a := range p.cfg.Ordered() {
		if t, ok := p.nextPoll[a.ID]; ok && now.Before(t) {
			continue
		}
		if a.ID == p.st.Active {
			out = append([]config.Account{a}, out...)
			continue
		}
		out = append(out, a)
	}
	return out
}

func (p *Poller) schedule(id string, now time.Time, acct *state.Account) {
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
	if id == p.st.Active {
		iv = p.cfg.PollActive.Duration
		if iv <= 0 {
			iv = ActiveInterval
		}
		if acct.Last != nil {
			_, worst := acct.Last.Worst()
			// Poll faster when close to the line, and also when burning fast
			// regardless of level: at 3 points a minute a four-minute gap is
			// twelve points of drift, which is enough to sail past the trigger
			// between polls.
			if worst >= HotThreshold || acct.BurnRate() >= FastBurn {
				iv = p.cfg.PollHot.Duration
				if iv <= 0 {
					iv = ActiveHotInterval
				}
			}
		}
	}
	p.nextPoll[id] = now.Add(iv)
}

func (p *Poller) fetchInto(ctx context.Context, acct *state.Account, token string, priority bool) {
	// Ask every time, for every caller. This check used to run only for
	// priority polls, so an ordinary one could reach the API while the budget
	// was locked — collect a fresh Retry-After: 3600, and re-arm the very lock
	// it had just ignored. That is how a transient refusal became a two-hour
	// outage that renewed itself.
	if ok, reason := p.budget.Allow(priority); !ok {
		acct.LastErr = "not polled: " + string(reason)
		return
	}
	u, err := p.client.Fetch(ctx, token)
	if err != nil {
		if rl, ok := usage.IsRateLimited(err); ok {
			p.budget.Penalize(rl.RetryAfter)
			acct.LastErr = rl.Error()
			// Report the backoff actually applied, not the header value: the
			// endpoint sends Retry-After: 0, and logging that was misleading.
			effective := rl.RetryAfter
			if effective < usage.MinBackoff {
				effective = usage.MinBackoff
			}
			wait, strikes := p.budget.CurrentBackoff()
			p.log.Warn("usage API refused us; backing off",
				"account", acct.ID, "consecutive", strikes, "waiting", wait.Round(time.Second))
			_ = effective
			return
		}
		if usage.IsShapeError(err) {
			p.degrade(err.Error())
		}
		acct.LastErr = err.Error()
		return
	}
	// Keep the previous reading so a burn rate can be computed.
	if acct.Last != nil && !acct.LastAt.IsZero() {
		_, prev := acct.Last.Worst()
		acct.PrevWorst, acct.PrevAt = prev, acct.LastAt
	}
	acct.Last = u
	// Remember the rate while we can still see it. Once polling stalls the pair
	// of readings goes flat and no rate can be derived from it, so the value
	// captured here is what keeps the projection honest through the gap.
	if r := acct.BurnRate(); r > 0 {
		acct.LastRate = r
	} else if _, w := u.Worst(); w < acct.PrevWorst {
		acct.LastRate = 0 // window reset; the old rate describes a dead window
	}
	acct.LastAt = u.FetchedAt
	acct.LastErr = ""
	p.lastOK = u.FetchedAt
	// The server answered, so whatever was refusing us has stopped.
	p.budget.Succeeded()
	if u.OrgID != "" {
		acct.OrgID = u.OrgID
	}
	// Record which seat this reading describes, so a record can never be
	// mistaken for a colleague's in the same organization.
	if seat := seatOfVault(acct.ID); seat != "" {
		acct.Seat = seat
	}
	// A reading that shows headroom clears a burn whose window has reset.
	if !acct.BurntTil.IsZero() && time.Now().After(acct.BurntTil) {
		acct.BurntTil = time.Time{}
		acct.BurntWin = ""
	}
	p.noteSeverity(acct.ID, u)
}

// noteSeverity records transitions in the API's own severity field. Every
// limits[] entry is tracked, not just the active one, since a window can start
// warning before it becomes the binding constraint.
func (p *Poller) noteSeverity(accountID string, u *usage.Usage) {
	for _, l := range u.Limits {
		if l.Severity == "" {
			continue
		}
		key := accountID + "/" + l.Kind
		prev, seen := p.lastSeverity[key]
		if seen && prev == l.Severity {
			continue
		}
		p.lastSeverity[key] = l.Severity
		if !seen {
			continue // first sighting is a baseline, not a transition
		}
		p.log.Info("severity changed", "account", accountID, "limit", l.Kind,
			"from", prev, "to", l.Severity, "percent", l.Percent)
		if p.OnSeverityChange != nil {
			p.OnSeverityChange(accountID, l.Kind, prev, l.Severity, l.Percent)
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
	b, err := keychain.Read(keychain.VaultService(accountID))
	if err != nil || b.Meta == nil {
		return ""
	}
	return b.Meta.Seat()
}
