package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/poller"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// The daemon drives every configured Claude Code profile from one process
// (docs/PROFILES.md §5): one detector and one decision loop per
// profile, one shared poller and call budget.
//
// # Who touches state
//
// Exactly one goroutine: the one running daemon.run (before it, the one that
// built the daemon). The state, the poller, the vault calls, the notifier and
// every profileLoop field other than det belong to it. Detectors run in their own
// goroutines and reach it only through channels: each profile has a forwarder
// that tags the detector's rejections with the profile and hands them to
// run's select. Nothing else is shared. That is the whole concurrency rule,
// chosen over a mutex because State has no lock of its own and is read in too
// many places (policy, poller, render) to guard each one: state.SaveAs can
// insert profiles it finds on disk (mergeProfiles), and a map write racing
// any of those reads is a crash, not a stale value.
//
// The detector's own methods (IdleFor, LastActivity,
// WorstLatency) take its internal lock, so calling them from run is safe.

// daemonPoller is the poller as the daemon drives it.
type daemonPoller interface {
	Tick(ctx context.Context)
	PollActiveIn(ctx context.Context, profile string) (*state.Account, error)
	RefreshCandidatesIn(ctx context.Context, olderThan time.Duration, profile string) int
	ApplyRejection(accountID, window string, resetsAt time.Time)
	Blind(limit time.Duration) (time.Duration, bool)
	SetLive(profile string, live keychain.Live)
	// SetConfig hands the poller a reloaded config; see daemon.reload.
	SetConfig(cfg *config.Config)
}

// daemonVault is the vault as the daemon uses it. Every method that touches a
// live credential names the profile's item.
type daemonVault interface {
	// SwapToWith installs an account, capturing what it overwrites and
	// holding Claude Code's credential locks (lane 6).
	SwapToWith(ctx context.Context, item keychain.Live, accountID, expectOrg string, o vault.SwapOptions) (*vault.SwapResult, error)
	Has(accountID string) bool
	SyncActiveIn(ctx context.Context, item keychain.Live, accountID, wantSeat string) (bool, error)
	// LiveHolds: does item hold accountID's vaulted token (local, no
	// network)? known is false when the item could not be read; a missing
	// item is known to hold nothing.
	LiveHolds(item keychain.Live, accountID string) (holds, known bool)
	// HoldsAccount: does item hold accountID by any token (a hand login
	// included)? May spend one identity lookup. known false means unsettled.
	HoldsAccount(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool)
	// HoldsAccountWhy is HoldsAccount with, for an unknown a rate-limit lock
	// caused, when the lock clears (R1).
	HoldsAccountWhy(ctx context.Context, item keychain.Live, accountID, wantSeat string) (holds, known bool, retryAt time.Time)
	NeedsRefresh(accountID string, window time.Duration) bool
	VaultedAt(accountID string) time.Time
	RefreshIn(ctx context.Context, accountID, wantSeat string, holder keychain.Live, allowActive bool) (*vault.Entry, error)
}

// activity is a profile's transcript detector.
type activity interface {
	Run(stop <-chan struct{}) error
	Rejections() <-chan detector.Rejection
	IdleFor(time.Duration) bool
	LastActivity() time.Time
	WorstLatency() time.Duration
}

type auditor interface{ Write(audit.Event) error }

// readingsLog is where the daemon keeps past readings (internal/readings,
// IMPROVEMENTS F7). Record writes a reading once however often it is handed
// the same one.
type readingsLog interface {
	Record(account string, at time.Time, u *usage.Usage) error
}

type notifier interface {
	Send(key, title, message string)
	Switched(from, to, reason, headroom string)
	Exhausted(recovers string, at time.Time)
	RefreshExpiring(account string, in time.Duration)
}

// profileLoop is one profile's decision loop: its own detector, live item,
// pool, thresholds and the memory evaluate keeps between ticks.
type profileLoop struct {
	name string
	conf config.Profile
	pool []string
	// cfg is config.ForProfile(name): the profile's own thresholds. Read
	// only; it shares slices with the daemon's config.
	cfg *config.Config
	// live is the profile's live credential, nil while it cannot be
	// resolved (D9: not logged in yet). A profile without one is watched
	// but never decided for, and resolution is retried on every save tick.
	live       keychain.Live
	unresolved error
	det        activity
	projects   string // the detector's root, "" for the environment's
	log        *slog.Logger

	// wantSwitchSince tracks how long a switch has been wanted but deferred, so
	// the idle-gap preference cannot defer it forever.
	wantSwitchSince time.Time
	// wantSwitchSig is which switch that clock is for: failover or not, and
	// the target. A different one restarts the clock.
	wantSwitchSig   string
	lastDecisionSig string
	// lastWaitSig stops the exhausted-everything warning repeating every tick.
	lastWaitSig string
	// lastRefusalSig and lastCrossPoolSig do the same for a refused swap and
	// for holding another pool's account: said once per distinct finding.
	lastRefusalSig   string
	lastCrossPoolSig string
	// lastUnpinSig says a dry run's pin lift once (IMPROVEMENTS F3);
	// runwayWarned holds the runway notice to once per approach (F2).
	lastUnpinSig string
	runwayWarned bool

	// hold keeps a config reload that removed this profile's active account
	// from turning into a swap; see activeHold.
	hold activeHold

	// quit stops this profile's detector and forwarder (D21: a profile a
	// reload removes is stopped alone). detDone and fwdDone close when each
	// has returned. All three are nil until launch; stopped is set by
	// stopProfile, after which a rejection still in flight is dropped.
	quit, detDone, fwdDone chan struct{}
	stopped                bool
}

type daemon struct {
	cfg     *config.Config
	st      *state.State
	p       daemonPoller
	v       daemonVault
	aud     auditor
	nt      notifier
	log     *slog.Logger
	live    bool
	idleGap time.Duration
	profs   []*profileLoop

	// save persists state as the daemon; saveCLI is the CLI-side save
	// maintainVault has always used for a needs-login note. Seams, so tests
	// never write the real state file.
	save    func() error
	saveCLI func() error
	// resolve re-resolves a profile's live item; see profileLoop.live.
	resolve func(config.Profile) (keychain.Live, error)

	lastReattribute time.Time

	// reloader and cfgChanged re-read the config when it changes on disk. The
	// watcher only signals; the reload runs in run's select, on the goroutine
	// that owns the config, the poller and the state. Both nil disables it.
	reloader   *configReloader
	cfgChanged <-chan struct{}

	// projects and newDet build the detector of a profile a reload starts,
	// as buildProfiles did for the ones there at startup.
	projects func(config.Profile) string
	newDet   func(root string, log *slog.Logger) activity
	// stop is the daemon's own stop channel and rej the channel every
	// profile's forwarder hands rejections to run on; both are set by
	// startDetectors.
	stop <-chan struct{}
	rej  chan rejection

	// itemRef names a live item concretely, for a ghost (default: the
	// item's own ItemRef); ghostItem reopens one (default keychain.LiveItem).
	itemRef   func(keychain.Live) (service, file string, ok bool)
	ghostItem func(service, file string) keychain.Live

	// readings keeps each reading for `cs history --usage`; nil keeps none.
	readings readingsLog

	// appNotifies reports whether the macOS app's heartbeat is current
	// (state.AppNotifyingAt): it then posts rotation notices itself and the
	// daemon skips its own. Nil, as in tests and before 0.6.1: never.
	appNotifies func(now time.Time) bool
}

// recordReadings hands each configured account's current reading to the
// readings log. A failure is logged and never stops the daemon.
func (d *daemon) recordReadings() {
	if d.readings == nil {
		return
	}
	for _, a := range d.cfg.Accounts {
		acct := d.st.Accounts[a.ID]
		if acct == nil || acct.Last == nil || acct.LastAt.IsZero() {
			continue
		}
		if err := d.readings.Record(a.ID, acct.LastAt, acct.Last); err != nil {
			d.log.Warn("could not record a reading", "account", a.ID, "err", err)
		}
	}
}

// liveFor resolves a configured profile's live item: the item Claude Code
// reads under this process's environment for the implicit profile, exactly as
// before profiles, and the profile's own resolved item otherwise.
func liveFor(in config.Profile) (keychain.Live, error) {
	if in.FromEnv {
		return keychain.EnvLive(), nil
	}
	r, err := profile.Resolve(in)
	if err != nil {
		return nil, err
	}
	return keychain.LiveItem(r.Service, r.CredentialFile), nil
}

// projectsFor is where a profile's transcripts live. "" for the implicit
// profile, which the detector resolves from the environment as it always has.
func projectsFor(in config.Profile) string {
	if in.FromEnv {
		return ""
	}
	r, _ := profile.Resolve(in) // the paths come back even when the item does not
	return r.Projects
}

// buildProfiles makes one loop per effective profile. newDet builds a
// detector for a transcripts root ("" meaning the environment's).
func buildProfiles(cfg *config.Config, log *slog.Logger,
	resolve func(config.Profile) (keychain.Live, error),
	projects func(config.Profile) string,
	newDet func(root string, log *slog.Logger) activity) []*profileLoop {

	var out []*profileLoop
	for _, in := range cfg.EffectiveProfiles() {
		out = append(out, newProfileLoop(cfg, in, log, resolve, projects, newDet))
	}
	return out
}

// newProfileLoop builds one profile's loop: its detector (not yet running) and
// its live item, resolved now.
func newProfileLoop(cfg *config.Config, in config.Profile, log *slog.Logger,
	resolve func(config.Profile) (keychain.Live, error),
	projects func(config.Profile) string,
	newDet func(root string, log *slog.Logger) activity) *profileLoop {

	il := &profileLoop{
		name: in.Name, conf: in, pool: in.Pool,
		cfg: cfg.ForProfile(in.Name),
		log: log.With("profile", in.Name),
	}
	il.hold = activeHold{log: il.log}
	root := projects(in)
	il.projects = root
	if root == "" && !in.FromEnv {
		il.det = quietDetector{}
	} else {
		il.det = newDet(root, il.log)
	}
	live, err := resolve(in)
	if err != nil {
		il.unresolved = err
	} else {
		il.live = live
	}
	return il
}

// newDetector is the daemon's real detector for a transcripts root.
func newDetector(root string, log *slog.Logger) activity { return detector.New(root, log) }

// quietDetector stands in for a profile whose transcripts directory could
// not be worked out at all: it never reports activity or a refusal.
type quietDetector struct{}

func (quietDetector) Run(stop <-chan struct{}) error {
	<-stop
	return nil
}
func (quietDetector) Rejections() <-chan detector.Rejection { return nil }
func (quietDetector) IdleFor(time.Duration) bool            { return true }
func (quietDetector) LastActivity() time.Time               { return time.Time{} }
func (quietDetector) WorstLatency() time.Duration           { return 0 }

// multi reports whether more than one profile is configured. Notifications
// name the profile only then: with one, the name says nothing.
func (d *daemon) multi() bool { return len(d.profs) > 1 }

// tag prefixes a notification line with the profile when that says something.
func (d *daemon) tag(il *profileLoop, s string) string {
	if !d.multi() {
		return s
	}
	return il.name + ": " + s
}

// profState is a profile's state. Load materialised every configured
// profile, so this only reads the map.
func (d *daemon) profState(il *profileLoop) *state.ProfileState { return d.st.Profile(il.name) }

// activeElsewhere names another profile whose live credential holds
// accountID, or "".
func (d *daemon) activeElsewhere(name, accountID string) string {
	for _, il := range d.profs {
		if il.name == name {
			continue
		}
		if in := d.st.Profiles[il.name]; in != nil && in.Active == accountID {
			return il.name
		}
	}
	return ""
}

func (d *daemon) activeAnywhere(accountID string) bool {
	for _, il := range d.profs {
		if in := d.st.Profiles[il.name]; in != nil && in.Active == accountID {
			return true
		}
	}
	for _, g := range d.st.GhostList() {
		if g.Account == accountID {
			return true
		}
	}
	return false
}

// busy reports whether a profile's detector saw transcript activity within
// poll_active: the poller's D3 test for whether a hot profile is spending.
func (d *daemon) busy(name string) bool {
	window := d.cfg.PollActive.Duration
	if window <= 0 {
		window = poller.ActiveInterval
	}
	for _, il := range d.profs {
		if il.name == name {
			last := il.det.LastActivity()
			return !last.IsZero() && time.Since(last) < window
		}
	}
	return false
}

// start is the daemon's startup: announce, attribute every profile's live
// credential, persist, decide once.
func (d *daemon) start(ctx context.Context) {
	// First, before any network call: replace the profiles a previous
	// daemon recorded with this one's, and save. Until then state.json
	// says the old daemon's set, and a seed waiting for its profile could
	// take a stale entry for "loaded" (lane 10 re-review).
	d.recordLoaded()
	_ = d.save()

	mode := "DRY RUN — decisions are reported, nothing is changed"
	if d.live {
		mode = "LIVE — swaps will be performed"
	}
	for _, il := range d.profs {
		switch {
		case il.live == nil:
			il.log.Error("this profile's live credential cannot be resolved; watching it, never rotating it",
				"err", il.unresolved)
		case len(il.pool) == 0 && !il.conf.FromEnv:
			il.log.Warn("this profile's pool is empty; it will never be rotated")
		}
		if il.live != nil {
			d.p.SetLive(il.name, il.live)
			d.recordItem(il)
		}
	}
	if d.multi() {
		for _, il := range d.profs {
			il.log.Info("profile", "pool", il.pool, "dir", il.conf.Dir)
		}
	}
	transcripts := detector.ProjectsRoot()
	if !d.profs[0].conf.FromEnv || d.multi() {
		transcripts = ""
		for _, il := range d.profs {
			if transcripts != "" {
				transcripts += ", "
			}
			transcripts += il.name + "=" + il.projects
		}
	}
	d.log.Info("watching", "mode", mode, "endpoint", usage.Endpoint, "transcripts", transcripts)

	d.st.DaemonLive = d.live
	d.st.DaemonSince = time.Now()
	// A failure streak recorded before this process started is not time this
	// process watched the account be unreadable — a restart or a long sleep
	// sits inside it. Blind failover counts only what this daemon observes.
	for _, a := range d.st.Accounts {
		a.ReadFails, a.FailSince = 0, time.Time{}
	}
	// Recorded so a newer CLI can tell it is talking to an older daemon.
	b := currentBuild()
	d.st.DaemonVersion, d.st.DaemonBuild, d.st.DaemonBuildTime = b.Version, b.Revision, b.Time
	for _, il := range d.profs {
		if il.live == nil {
			continue
		}
		if _, err := d.p.PollActiveIn(ctx, il.name); err != nil {
			il.log.Warn("initial active poll failed", "err", err)
		}
	}
	d.lastReattribute = time.Now() // PollActive already ran at startup
	d.recordLoaded()
	_ = d.save()

	for _, il := range d.profs {
		d.evaluate(ctx, il, "startup")
	}
}

// evaluate runs the policy engine for one profile and, when live and the
// moment is right, performs the swap into that profile's live item.
func (d *daemon) evaluate(ctx context.Context, il *profileLoop, trigger string) {
	// A declared profile with an empty pool (D13) is never decided for. The
	// implicit profile always is, so a config with no accounts still says
	// "no accounts configured" as it always did.
	if il.live == nil || (len(il.pool) == 0 && !il.conf.FromEnv) {
		return
	}
	ist := d.profState(il)
	input := func() policy.Input {
		return policy.Input{
			Cfg: il.cfg, St: d.st, Now: time.Now(), LastSwitch: ist.LastSwitch, Pinned: ist.Pinned,
			PinHard: ist.PinHard, Lookahead: lookahead(il.cfg), Pool: il.pool, Live: ist,
			// Whether an expired token explains an unreadable active account
			// (IMPROVEMENTS A2): on an idle session it does.
			SessionBusy: !il.det.IdleFor(d.idleGap),
		}
	}
	dec := il.hold.gate(policy.Decide(input()), ist.Active, il.cfg)
	if dec.Unpin != "" {
		d.liftPin(il, ist, dec.Unpin)
	}
	d.noteRunway(il, input())
	// Only record a decision when it says something new. A tick every twenty
	// seconds writing "stay" produced 377 rows in one evening and buried the
	// one rejection that mattered.
	sig := string(dec.Kind) + "|" + dec.Target + "|" + dec.Reason
	if sig != il.lastDecisionSig {
		il.lastDecisionSig = sig
		ev := audit.Event{Kind: "decision", Profile: il.name, Decision: string(dec.Kind), From: ist.Active,
			To: dec.Target, Reason: dec.Reason, Forced: dec.Forced, DryRun: !d.live}
		if a, ok := d.st.Accounts[ist.Active]; ok && a.Last != nil {
			f, sv := a.Last.FiveHour.Pct(), a.Last.SevenDay.Pct()
			ev.FiveHour, ev.SevenDay = &f, &sv
		}
		_ = d.aud.Write(ev)
	}

	// Acting on a reading that is too old to trust is how the daemon sat at
	// 93% without rotating: its polls were failing, "stay" is logged at
	// debug level, and nothing said a word. Staleness is now loud.
	if a, ok := d.st.Accounts[ist.Active]; ok && a.Last != nil {
		if age := time.Since(a.LastAt); age > staleDecisionAfter(il.cfg) {
			il.log.Warn("deciding on a stale reading — the poller is not keeping up",
				"account", ist.Active, "age", age.Round(time.Second),
				"reading", fmt.Sprintf("%.0f%%", a.Projected(time.Now())),
				"last_error", a.LastErr)
		}
	}
	// About to conclude there is nowhere to go? Check that on current
	// figures before acting on it. Ten-minute-old readings of the other
	// accounts are exactly how this ends up stuck at 100% while a reset
	// account sits idle.
	if dec.Kind == policy.Wait {
		if n := d.p.RefreshCandidatesIn(ctx, 2*time.Minute, il.name); n > 0 {
			dec = il.hold.gate(policy.Decide(input()), ist.Active, il.cfg)
			if dec.Kind == policy.Switch {
				il.log.Info("a re-read found room after all", "target", dec.Target,
					"rechecked", n)
			}
		}
	}

	if dec.Kind != policy.Switch {
		il.wantSwitchSince = time.Time{}
		il.lastRefusalSig = ""
		// Say it once per distinct situation, not every twenty seconds.
		if dec.Kind == policy.Wait && sig != il.lastWaitSig {
			il.lastWaitSig = sig
			il.log.Warn("nowhere to rotate to — every account is out",
				"reason", dec.Reason, "recovers", dec.RecoversAccount,
				"at", dec.RecoversAt.Local().Format("15:04"))
			d.exhausted(il, dec.RecoversAccount, dec.RecoversAt)
		} else if dec.Kind != policy.Wait {
			il.lastWaitSig = ""
		}
		il.log.Debug("no rotation", "decision", dec.String(), "trigger", trigger)
		return
	}

	// Prefer an idle gap so a turn is never split across two accounts — but
	// only for so long. A session in continuous use never goes quiet, so
	// holding out indefinitely means the switch never happens until the hard
	// floor, which is the opposite of what this tool is for.
	// The wait belongs to one wanted switch. A blind failover that becomes an
	// ordinary rotation (or the reverse), or a new target, starts its own
	// clock: otherwise time spent waiting out a never-forced failover would
	// let the ordinary switch go ahead mid-turn at once.
	if want := fmt.Sprintf("%t|%s", dec.Failover, dec.Target); want != il.wantSwitchSig {
		il.wantSwitchSig, il.wantSwitchSince = want, time.Time{}
	}
	if il.wantSwitchSince.IsZero() {
		il.wantSwitchSince = time.Now()
	}
	waited := time.Since(il.wantSwitchSince)
	if !dec.Forced && !il.det.IdleFor(d.idleGap) {
		// A blind failover never splits a turn, however long it has waited
		// (owner decision 2026-10-07): nothing says the work itself is in
		// trouble, only that we cannot see the account.
		if dec.Failover {
			il.log.Info("blind failover wanted, session busy — waiting for an idle gap, never mid-turn",
				"target", dec.Target, "reason", dec.Reason, "waited", waited.Round(time.Second))
			return
		}
		if waited < il.cfg.MaxSwitchWait.Duration {
			il.log.Info("switch wanted, session busy — waiting for an idle gap",
				"target", dec.Target, "reason", dec.Reason,
				"waited", waited.Round(time.Second),
				"will_force_after", il.cfg.MaxSwitchWait.Duration)
			return
		}
		il.log.Info("switch wanted and the session has not gone idle; going ahead anyway",
			"target", dec.Target, "waited", waited.Round(time.Second))
	}

	if !d.live {
		il.log.Warn("WOULD SWITCH (dry run)", "from", ist.Active, "to", dec.Target,
			"because", dec.Reason, "forced", dec.Forced)
		return
	}

	// §3: one credential may be live in at most one profile. Pools are
	// disjoint, so rotation alone cannot get here; a manual login or a config
	// reload can. Refuse before anything is written.
	if other, how, at := d.liveElsewhere(ctx, il, dec.Target); other != "" {
		msg := fmt.Sprintf("refusing to install %s in profile %s: %s (%s), "+
			"and one credential live in two profiles is revoked by whichever refreshes first",
			dec.Target, il.name, how, whereLive(other))
		if !at.IsZero() {
			how = rateLimitedWhy(dec.Target, whereLive(other), at)
			msg = fmt.Sprintf("refusing to install %s in profile %s: %s", dec.Target, il.name, how)
		}
		// A refusal persists until the other profile lets go, and this runs
		// every tick: say it once per distinct finding.
		if sig := dec.Target + "|" + other + "|" + how; sig != il.lastRefusalSig {
			il.lastRefusalSig = sig
			il.log.Error("REFUSED A SWAP: account may be live in another profile",
				"target", dec.Target, "live_in", other, "evidence", how, "reason", dec.Reason)
			_ = d.aud.Write(audit.Event{Kind: "error", Profile: il.name, From: ist.Active, To: dec.Target,
				Reason: dec.Reason, Err: msg})
			d.nt.Send("conflict:"+il.name+":"+dec.Target, "claudeswitch refused a swap", msg)
		}
		return
	}
	il.lastRefusalSig = ""

	expectOrg := ""
	for _, a := range d.cfg.Accounts {
		if a.ID == dec.Target {
			expectOrg = a.OrgID
		}
	}
	if expectOrg == "" {
		if a, ok := d.st.Accounts[dec.Target]; ok {
			expectOrg = a.OrgID
		}
	}
	swapCtx, swapCancel := context.WithTimeout(ctx, 80*time.Second)
	res, serr := swapInto(swapCtx, d.v, d.cfg, il.name, ist.Active, il.live, dec.Target, expectOrg, il.log)
	swapCancel()
	if errors.Is(serr, vault.ErrLockBusy) {
		// Claude Code is refreshing this profile's credential. Nothing was
		// written; the next tick decides again and tries again.
		il.log.Info("swap deferred: Claude Code holds its credential lock; retrying next tick",
			"target", dec.Target)
		return
	}
	if serr != nil {
		il.log.Error("swap failed", "target", dec.Target, "err", serr,
			"rolled_back", res != nil && res.RolledBack)
		_ = d.aud.Write(audit.Event{Kind: "error", Profile: il.name, To: dec.Target, Reason: dec.Reason,
			Err: serr.Error()})
		return
	}
	from := ist.Active
	il.wantSwitchSince = time.Time{}
	ist.SetActive(dec.Target)
	ist.LastSwitch = time.Now()
	ist.LastFrom = from
	if res.Usage != nil {
		a := d.st.Get(dec.Target)
		a.Last, a.LastAt, a.OrgID = res.Usage, res.Usage.FetchedAt, res.OrgID
	}
	_ = d.save()
	_ = d.aud.Write(audit.Event{Kind: "switch", Profile: il.name, From: from, To: dec.Target,
		Reason: dec.Reason, Forced: dec.Forced})
	il.log.Info("switched", "from", from, "to", dec.Target, "because", dec.Reason)
	headroom := ""
	if res.Usage != nil {
		_, worst := res.Usage.Worst()
		headroom = fmt.Sprintf("%.0f%% used", worst)
	}
	// 0.6.1: while the app's heartbeat is current it posts the rotation
	// notice itself, with Undo and Pin here; a second, plain one from here
	// would be noise. Only this notice is left to it.
	if d.appNotifies != nil && d.appNotifies(time.Now()) {
		il.log.Info("rotation notice left to the app", "from", from, "to", dec.Target)
	} else {
		d.nt.Switched(d.tag(il, from), dec.Target, dec.Reason, headroom)
	}
	// IMPROVEMENTS C1: the save above merged in the CLI's Chrome mappings.
	// C2: when only the profile's chrome applies, it asks for a sign-in.
	if msg := chromeSwitchNotice(il.cfg, d.st, readChromeLocal(), il.name, from, dec.Target); msg != "" {
		il.log.Info(msg)
		d.nt.Send("chrome:"+il.name+":"+from+":"+dec.Target, "Claude in Chrome", msg)
	}
}

// liftPin is the pin safety valve (IMPROVEMENTS F3): the pinned account was
// refused, ran out or needs a sign-in, so the decision was made unpinned
// (policy.PinLifted) and the pin is cleared, said in the log, a notification
// and the audit log (kind unpin). A dry run changes nothing and says once
// what it would have done.
func (d *daemon) liftPin(il *profileLoop, ist *state.ProfileState, why string) {
	pinned := ist.Pinned
	if !d.live {
		if sig := pinned + "|" + why; sig != il.lastUnpinSig {
			il.lastUnpinSig = sig
			il.log.Warn("WOULD LIFT THE PIN (dry run)", "account", pinned, "because", why)
			_ = d.aud.Write(audit.Event{Kind: "unpin", Profile: il.name, From: pinned, Reason: why, DryRun: true})
		}
		return
	}
	il.lastUnpinSig = ""
	ist.LiftPin()
	_ = d.save()
	_ = d.aud.Write(audit.Event{Kind: "unpin", Profile: il.name, From: pinned, Reason: why})
	il.log.Warn("pin lifted; rotating as usual", "account", pinned, "because", why)
	d.nt.Send("unpin:"+il.name+":"+pinned, "claudeswitch lifted a pin", d.tag(il, why))
}

// RunwayWarning is how near the pool's runway (IMPROVEMENTS F2) must come
// before the daemon says so.
const RunwayWarning = 2 * time.Hour

// noteRunway notifies once when the profile's pool is forecast to run dry
// within RunwayWarning at this pace. It warns again only after the forecast
// has moved off: dry beyond the warning, or a reset coming first. A runway
// that is unknown, idle or already dry neither warns nor re-arms; running
// out is the exhausted notice's to say.
func (d *daemon) noteRunway(il *profileLoop, in policy.Input) {
	now := in.Now
	in.Unavailable = bestExclusions(d.cfg, d.st, il.name, now)
	r := policy.Forecast(in)
	switch {
	case r.Kind == policy.RunwayRefills, r.Kind == policy.RunwayDry && r.DryAt.Sub(now) > RunwayWarning:
		il.runwayWarned = false
	case r.Kind == policy.RunwayDry && r.DryAt.After(now) && !il.runwayWarned:
		il.runwayWarned = true
		msg := fmt.Sprintf("pool runs dry at %s at this pace (in %s)",
			r.DryAt.Local().Format("15:04"), r.DryAt.Sub(now).Round(time.Minute))
		il.log.Warn("pool runs dry soon", "at", r.DryAt.Local().Format("15:04"),
			"pace_per_min", fmt.Sprintf("%.2f", r.Pace))
		d.nt.Send("runway:"+il.name, il.name+" pool runs dry soon", msg)
	}
}

// exhausted announces that a profile has nowhere to rotate to. With more
// than one profile the throttle key carries the profile, so one profile
// running dry does not silence another's.
func (d *daemon) exhausted(il *profileLoop, recovers string, at time.Time) {
	if !d.multi() {
		d.nt.Exhausted(recovers, at)
		return
	}
	msg := "no account in its pool has quota left"
	if recovers != "" {
		msg = recovers + " recovers at " + at.Local().Format("15:04")
	}
	d.nt.Send("exhausted:"+il.name, il.name+": all accounts are burnt", msg)
}

// rejection is a refusal tagged with the profile whose transcripts showed it.
type rejection struct {
	il *profileLoop
	r  detector.Rejection
}

// onRejection attributes a refusal to the account live in the profile whose
// detector saw it, and re-decides that profile at once.
func (d *daemon) onRejection(ctx context.Context, il *profileLoop, r detector.Rejection) {
	active := d.profState(il).Active
	if active == "" {
		active = "active"
	}
	d.p.ApplyRejection(active, r.Type, r.ResetsAt)
	_ = d.aud.Write(audit.Event{Kind: "rejection", Profile: il.name, From: active, Window: r.Type,
		ResetsAt: r.ResetsAt})
	il.log.Warn("account refused", "account", active, "window", r.Type,
		"clears_in", time.Until(r.ResetsAt).Round(time.Second),
		"detect_latency", r.Latency.Round(time.Millisecond),
		"worst_latency", il.det.WorstLatency().Round(time.Millisecond))
	_ = d.save()
	d.evaluate(ctx, il, "rejection") // a refusal always re-decides immediately
}

// reattribute re-derives which account each profile's live credential holds,
// and retries resolving any profile whose item could not be found.
func (d *daemon) reattribute(ctx context.Context) {
	for _, il := range d.profs {
		if il.live == nil {
			live, err := d.resolve(il.conf)
			if err != nil {
				il.unresolved = err
				il.log.Debug("this profile's live credential still cannot be resolved", "err", err)
				continue
			}
			il.live, il.unresolved = live, nil
			d.p.SetLive(il.name, live)
			d.recordItem(il)
			il.log.Info("this profile's live credential is now resolved; rotating it", "item", live.Name())
		}
		if _, err := d.p.PollActiveIn(ctx, il.name); err != nil {
			il.log.Debug("could not re-confirm the active account", "err", err)
		}
	}
}

// maintainVault keeps vault entries alive.
//
// Two jobs, and the first matters more. Claude Code refreshes the live session
// on its own schedule, and that REVOKES the token our vault copy holds — so the
// entry for each profile's active account rots within hours unless we notice
// the drift and re-capture it from that profile's own live item. That costs
// nothing and cannot fail dangerously.
//
// The second job is refreshing genuinely idle accounts before their access
// tokens expire. That one calls the token endpoint, which rotates and revokes,
// so it is never attempted on an account live in any profile.
func (d *daemon) maintainVault(ctx context.Context) {
	cfg, v, log := d.cfg, d.v, d.log
	for _, il := range d.profs {
		if il.live == nil {
			continue
		}
		ist := d.profState(il)
		if ist.Active == "" || !v.Has(ist.Active) {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		changed, err := v.SyncActiveIn(sctx, il.live, ist.Active, cfg.SeatOf(ist.Active))
		cancel()
		switch {
		case err != nil:
			var foreign *vault.ForeignCredentialError
			if errors.As(err, &foreign) {
				// Someone signed in to a different account. That is not an
				// error, and it must not cause a write — but the daemon's idea
				// of which account is live is now wrong, so drop it and let the
				// next poll re-derive it from the credential itself.
				// The error carries the seat the live credential actually
				// belongs to, so in almost every case we already know the right
				// answer. Clearing to "" threw it away and left the daemon with
				// no active account, whereupon the policy picked the first one
				// in priority order — an account at 100% — swapped its
				// credential in, and immediately rotated away again. That cycle
				// repeated every sixteen minutes, and each turn of it performed
				// two real credential swaps for no reason.
				if owner := cfg.AccountBySeat(foreign.GotOrg); owner != "" {
					il.log.Info("the live credential belongs to a different account than we thought",
						"was", ist.Active, "is", owner)
					d.noteCrossPool(il, owner, ist.Active)
					ist.Active = owner
				} else {
					il.log.Info("the live credential is an account we do not know; clearing",
						"was", ist.Active, "live_seat", foreign.GotOrg)
					ist.Active = ""
				}
			} else {
				il.log.Warn("could not sync the active account's vault entry",
					"account", ist.Active, "err", err)
			}
		case changed:
			il.log.Info("vault entry for the active account was refreshed underneath us and re-captured",
				"account", ist.Active)
		}
	}

	// Refreshing runs in dry-run too. Dry-run means "do not rotate"; letting
	// every stored credential expire while watching would be a strange reading
	// of that, and a refresh never changes which account is in use.
	if !cfg.RefreshEnabled() {
		return
	}
	for _, a := range cfg.Ordered() {
		if d.activeAnywhere(a.ID) || !v.Has(a.ID) {
			continue
		}
		// Belt and braces: never refresh whatever is actually live in any
		// profile, whatever state believes about which account that is.
		if d.liveAnywhere(a.ID) {
			continue
		}

		why := ""
		switch {
		case v.NeedsRefresh(a.ID, cfg.RefreshWindow.Duration):
			why = "token near expiry"
		case cfg.RefreshProbe.Duration > 0 && probeDue(v, a.ID, cfg.RefreshProbe.Duration):
			// A periodic probe exists because the token endpoint does not report
			// when a REFRESH token expires. Without it a dead refresh token
			// stays invisible until the moment the account is needed.
			why = "periodic probe"
		default:
			continue
		}

		rctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		_, err := v.RefreshIn(rctx, a.ID, cfg.SeatOf(a.ID), nil, false)
		cancel()
		if err != nil {
			var needsLogin *oauth.NeedsLoginError
			if errors.As(err, &needsLogin) {
				log.Error("account needs an interactive login", "account", a.ID, "detail", err)
				d.nt.Send("relogin:"+a.ID, a.ID+" needs a login",
					"its refresh token is spent; run `cs login "+a.ID+" --direct`")
				// Record it where `status` looks. A spent refresh token is the
				// one condition here a person has to act on, and it was only
				// ever logged: the row went on showing whatever the last poll
				// said — a stale rate-limit message, or "window reset ·
				// re-reading" forever — while the account could not be renewed
				// at all. stateOf checks needsLogin first, but only ever sees
				// LastErr, so an error that is not written there is invisible.
				//
				// Safe to write on the first failure because a successful poll
				// clears LastErr. invalid_grant does not always mean the token
				// is spent — a refresh revokes the token it was given, so one
				// made with a copy that another process has already rotated
				// fails the same way while the account is perfectly healthy.
				// Observed here: three of these two minutes apart, then the
				// account polled fine and went back to "available" on its own.
				// Surfacing it and letting the next good poll retract it beats
				// staying quiet, since the alternative failure is someone
				// spending an interactive login they did not need.
				d.st.Get(a.ID).LastErr = err.Error()
				if serr := d.saveCLI(); serr != nil {
					log.Warn("could not record that an account needs a login",
						"account", a.ID, "err", serr)
				}
				continue
			}
			log.Warn("refresh failed", "account", a.ID, "err", err, "why", why)
			continue
		}
		log.Info("refreshed an idle account", "account", a.ID, "why", why)
	}
}

// liveAnywhere asks every resolved profile's live item whether it may hold
// accountID's vaulted credential: held, or unreadable (unknown, so assume
// live and do not refresh). A missing item — a profile not logged in —
// holds nothing and stops no one's refresh.
func (d *daemon) liveAnywhere(accountID string) bool {
	// Ghosts included: refreshing an account still live in a removed
	// profile's old item would revoke the token that session holds.
	for _, t := range d.ghostTargets() {
		if t.live == nil {
			continue
		}
		if holds, known := d.v.LiveHolds(t.live, accountID); holds || !known {
			return true
		}
	}
	return false
}

// liveElsewhere is the check before a live write (§3): may accountID be live
// in a profile other than il? See liveElsewhereOf, which `use` shares.
func (d *daemon) liveElsewhere(ctx context.Context, il *profileLoop, accountID string) (string, string, time.Time) {
	return liveElsewhereOf(ctx, d.v, d.cfg, d.st, il.name, d.ghostTargets(), accountID)
}

// whereLive phrases where a §3 check found an account: a current profile by
// name, or a ghost by its own description.
func whereLive(other string) string {
	if isGhostName(other) {
		return other
	}
	return "profile " + other
}

// whereLiveQ is whereLive with a current profile's name quoted, as the
// CLI's messages write it.
func whereLiveQ(other string) string {
	if w := whereLive(other); w != "profile "+other {
		return w
	}
	return fmt.Sprintf("profile %q", other)
}

// noteCrossPool says loudly when a profile is found holding an account
// from another profile's pool (owner decision D17: record the truth, log an
// error; the next decision swaps back into its own pool if it can).
//
// The vault sync runs every two minutes, so a finding is said once and again
// only when it changes; an account back in this profile's own pool and live
// nowhere else clears it.
//
// was is the account the daemon thought was live. Empty (or unattributed),
// the credential was an account the config did not know until it was added
// — the state `add --from S --profile T` leaves, the owner's choice (lane
// 16) — so finding it in another pool is expected and said at Info; this
// loop moves off it. A credential that changed from one configured account
// to another pool's stays an error.
func (d *daemon) noteCrossPool(il *profileLoop, accountID, was string) {
	owner, ok := d.cfg.ProfileOf(accountID)
	foreign := ok && owner != il.name
	other := d.activeElsewhere(il.name, accountID)
	if !foreign && other == "" {
		il.lastCrossPoolSig = ""
		return
	}
	sig := accountID + "|" + owner + "|" + other
	if sig == il.lastCrossPoolSig {
		return
	}
	il.lastCrossPoolSig = sig
	if foreign && other == "" && (was == "" || was == state.Unattributed) {
		il.log.Info("this profile holds an account from another profile's pool; moving off it",
			"account", accountID, "pool_of", owner)
	} else if foreign {
		il.log.Error("this profile holds an account from another profile's pool",
			"account", accountID, "pool_of", owner)
	}
	if other != "" {
		il.log.Error("one account is live in two profiles; the first to refresh will log the other out",
			"account", accountID, "also_in", other)
	}
}

// startDetectors starts every profile's detector. It runs before start, as
// the single detector always did: a detector seeds its offsets when it starts,
// so a refusal written during the startup poll is caught rather than skipped.
//
// Each profile's detector and forwarder run until that profile's own quit
// channel closes: when a reload stops the profile (stopProfile), or when
// the daemon stops (stopProfiles). stop is the daemon's, kept so profiles a
// reload starts later are tied to it too.
func (d *daemon) startDetectors(stop <-chan struct{}) {
	d.stop = stop
	if d.rej == nil {
		d.rej = make(chan rejection)
	}
	for _, il := range d.profs {
		d.launch(il)
	}
}

// launch starts one profile's detector and its forwarder.
func (d *daemon) launch(il *profileLoop) {
	if il.quit != nil || d.rej == nil {
		return
	}
	il.quit, il.detDone, il.fwdDone = make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(il.detDone)
		if err := il.det.Run(il.quit); err != nil {
			il.log.Error("detector stopped", "err", err)
		}
	}()
	go func() {
		defer close(il.fwdDone)
		forward(il, d.stop, il.quit, d.rej)
	}()
}

// detectorStopWait bounds how long stopping a profile waits for its
// detector to return. A detector checks its stop channel between events, so
// it returns at once unless it is in the middle of a scan; past the bound it
// is left to finish and return by itself.
const detectorStopWait = 5 * time.Second

// stopProfile stops one profile's detector and forwarder. It runs on run's
// goroutine, between two iterations of the loop, so no swap or decision of
// that profile is in progress: swaps are made synchronously inside evaluate.
//
// Order: mark it stopped (a rejection already handed to run is then dropped),
// close quit, wait for the forwarder (prompt: it selects on quit, and run is
// not receiving meanwhile, so it cannot be mid-send), wait for the detector
// (bounded), then take its live item from the poller. The detector's own
// channel is never closed — it is only ever received from — so nothing can
// send on a closed channel.
func (d *daemon) stopProfile(il *profileLoop) {
	if il.stopped {
		return
	}
	il.stopped = true
	if il.quit != nil {
		close(il.quit)
		<-il.fwdDone
		select {
		case <-il.detDone:
		case <-time.After(detectorStopWait):
			il.log.Warn("the transcript detector has not stopped yet; it will exit when its scan ends")
		}
	}
	d.p.SetLive(il.name, nil)
}

// stopProfiles stops every running profile, as the daemon exits.
func (d *daemon) stopProfiles() {
	for _, il := range d.profs {
		d.stopProfile(il)
	}
}

// run is the daemon's loop. Every state access happens here (see the top of
// this file); detectors and their forwarders run beside it and only send.
func (d *daemon) run(ctx context.Context, stop chan struct{}, sig <-chan os.Signal,
	tick, save <-chan time.Time) error {

	if d.rej == nil {
		d.startDetectors(stop)
	}

	for {
		select {
		case <-sig:
			d.log.Info("stopping")
			close(stop)
			d.stopProfiles()
			_ = d.save()
			return nil
		case <-tick:
			d.p.Tick(ctx)
			d.recordReadings()
			for _, il := range d.profs {
				d.evaluate(ctx, il, "poll")
			}

			// A daemon that cannot see is worse than no daemon: it keeps
			// deciding, on figures that stopped moving. If nothing has been read
			// for long enough that this is a fault rather than a hiccup, say so
			// and stand down — the service manager restarts a process that
			// exits, and a fresh one has a far better chance than a wedged one.
			if blind, tooLong := d.p.Blind(blindLimit(d.cfg)); tooLong {
				d.log.Error("no successful reading for too long — exiting so the service manager can restart me",
					"blind_for", blind.Round(time.Second),
					"limit", blindLimit(d.cfg))
				d.nt.Send("blind", "claudeswitch restarting",
					"no usage reading for "+blind.Round(time.Minute).String())
				close(stop)
				d.stopProfiles()
				_ = d.save()
				return fmt.Errorf("blind for %s", blind.Round(time.Second))
			}
		case <-save:
			// Re-derive which account is actually live. Doing this only at
			// startup meant a rate-limited first poll left the daemon believing
			// the wrong account was active for the rest of its life. Doing it on
			// every save tick was the opposite mistake: an extra priority call
			// every two minutes, which starved the scheduled polls.
			if time.Since(d.lastReattribute) >= poller.ReattributeInterval {
				d.lastReattribute = time.Now()
				d.reattribute(ctx)
				// At the same cadence: release ghosts whose old item no
				// longer holds their account.
				d.releaseGhosts(ctx)
			}
			d.recordReadings() // the re-attribution polls above
			if err := d.save(); err != nil {
				d.log.Warn("state save failed", "err", err)
			}
			warnExpiringRefresh(d.st, d.nt)
			d.maintainVault(ctx)
		case <-d.cfgChanged:
			d.reload(ctx)
		case r := <-d.rej:
			if r.il.stopped {
				continue // its profile was stopped by a reload; nothing to charge
			}
			d.onRejection(ctx, r.il, r.r)
		}
	}
}

// reload applies a changed config file. It runs on run's goroutine, which owns
// the config, the poller and the state.
//
// It applies everything: new and removed accounts, pins, priorities, every
// threshold, each profile's pool and overrides, and the set of profiles
// itself (D21). A profile added is started, one removed is stopped, and one
// whose dir changed (or that moved between the environment's dir and a
// declared one) is stopped and started again, re-resolving its live item.
//
// Never mid-swap: a swap is made synchronously inside evaluate, on this same
// goroutine, so a reload is only ever processed between two of them.
//
// State: a removed profile's ProfileState stays in state.json. state never
// drops a profile (a save merges the disk's profiles back in), and the
// record is harmless — nothing reads a profile the config does not name —
// and is what the profile resumes from if it is declared again. A
// re-pointed profile's active account is cleared, since it described the
// old dir's credential, and the profile holds (activeHold) until its new
// live credential is attributed, rather than swapping on no information.
func (d *daemon) reload(ctx context.Context) {
	if d.reloader == nil {
		return
	}
	next, _ := d.reloader.reload()
	if next == d.cfg {
		return // unloadable: the previous config stays in force
	}
	added, removed, repointed := profileSetDiff(d.cfg, next)

	// Stop what went or moved, before the new config is adopted.
	restart := map[string]bool{}
	for _, n := range repointed {
		restart[n] = true
	}
	gone := map[string]bool{}
	for _, n := range removed {
		gone[n] = true
	}
	kept := d.profs[:0:0]
	for _, il := range d.profs {
		if !gone[il.name] && !restart[il.name] {
			kept = append(kept, il)
			continue
		}
		why, ghostWhy := "removed from the config", state.GhostRemoved
		if restart[il.name] {
			why, ghostWhy = "its dir changed", state.GhostRepointed
		}
		// Its old item may still hold the account last live there: keep
		// it in every §3 check (owner decision, lane 7 security review).
		d.makeGhost(il, ghostWhy)
		d.stopProfile(il)
		if !restart[il.name] {
			// Removed: the ghost (if any) now carries its account; clear the
			// record so a release or forget is not undone at the next start.
			forgetProfileRecord(d.st, il.name)
		}
		d.log.Info("profile stopped", "profile", il.name, "because", why)
	}
	d.profs = kept

	before := map[string]string{}
	for _, il := range d.profs {
		before[il.name] = d.profState(il).Active
	}
	d.cfg = next
	d.p.SetConfig(next)
	byName := map[string]config.Profile{}
	for _, in := range next.EffectiveProfiles() {
		byName[in.Name] = in
	}
	for _, il := range d.profs {
		il.cfg = next.ForProfile(il.name)
		if in, ok := byName[il.name]; ok {
			il.pool = in.Pool
			il.conf = in // same dir and FromEnv: anything else was restarted above
		}
	}

	// Start what came or moved, then put the loops in config order.
	start := map[string]bool{}
	for _, n := range append(append([]string{}, added...), repointed...) {
		start[n] = true
	}
	var started []*profileLoop
	running := map[string]*profileLoop{}
	for _, il := range d.profs {
		running[il.name] = il
	}
	for _, in := range next.EffectiveProfiles() {
		if !start[in.Name] {
			continue
		}
		il := d.startProfile(next, in, restart[in.Name])
		running[in.Name] = il
		started = append(started, il)
	}
	d.profs = d.profs[:0:0]
	for _, in := range next.EffectiveProfiles() {
		if il := running[in.Name]; il != nil {
			d.profs = append(d.profs, il)
		}
	}

	// Observations the new pins contradict are discarded, as every command does
	// at startup; a newly pinned account keeps its own.
	if dropped := d.st.Reconcile(pinnedOf(next)); len(dropped) > 0 {
		d.log.Info("discarded observations the reloaded config contradicts", "accounts", dropped)
	}
	for _, il := range started {
		// Attribute a newly started profile's live credential at once, as
		// startup does.
		if il.live != nil {
			if _, err := d.p.PollActiveIn(ctx, il.name); err != nil {
				il.log.Info("could not attribute this profile's live credential yet", "err", err)
			}
		}
	}
	for _, il := range d.profs {
		if start[il.name] {
			continue // started above: a hold, if any, was set there
		}
		il.hold.afterReload(before[il.name], d.profState(il).Active)
		if !il.hold.holding() {
			continue
		}
		il.log.Warn("the reloaded config no longer has the active account; holding, not swapping, "+
			"until the live credential is attributed again", "account", before[il.name])
		if il.live != nil {
			if _, err := d.p.PollActiveIn(ctx, il.name); err != nil {
				il.log.Debug("could not re-attribute the live credential yet", "err", err)
			}
		}
		d.lastReattribute = time.Now()
	}
	d.recordLoaded()
	_ = d.save()
	for _, il := range d.profs {
		d.evaluate(ctx, il, "config")
	}
}

// recordLoaded writes the profiles this daemon runs into state, name to dir
// as configured: the marker `profile create --seed` waits for, so a seed is
// made only once the daemon's §3 checks know the new profile.
func (d *daemon) recordLoaded() {
	m := make(map[string]string, len(d.profs))
	for _, il := range d.profs {
		m[il.name] = il.conf.Dir
	}
	d.st.DaemonProfiles = m
	// And which config it runs, by content: `account delete` and a seed
	// wait for the hash of the config they wrote (lane 12 re-review).
	d.st.DaemonConfigHash = d.cfg.Hash
}

// startProfile builds, registers and launches a profile a reload added
// or re-pointed. It runs on run's goroutine; the profile's state record is
// created here if it has none. A profile that state already records an
// active account for — re-pointed, or removed and declared again — forgets
// it (it described another item, or a past one) and holds until its live
// credential is attributed. repointed only says why, for the log.
func (d *daemon) startProfile(cfg *config.Config, in config.Profile, repointed bool) *profileLoop {
	resolve, projects, newDet := d.resolve, d.projects, d.newDet
	if resolve == nil {
		resolve = liveFor
	}
	if projects == nil {
		projects = projectsFor
	}
	if newDet == nil {
		newDet = newDetector
	}
	il := newProfileLoop(cfg, in, d.log, resolve, projects, newDet)
	ist := d.st.Profile(in.Name)
	// Whatever state says was active here describes an item this loop has
	// not looked at: the old dir's, or the profile's before it was removed
	// and declared again. Forget it and hold until the item is attributed.
	if ist.Active != "" {
		prev := ist.Active
		ist.Active = ""
		il.hold.afterReload(prev, "")
	}
	if il.live != nil {
		d.p.SetLive(il.name, il.live)
		d.recordItem(il)
	}
	d.dropGhostsOfItem(il)
	d.launch(il)
	item := ""
	if il.live != nil {
		item = il.live.Name()
	}
	d.log.Info("profile started", "profile", in.Name, "dir", in.Dir, "pool", in.Pool, "item", item)
	switch {
	case il.live == nil:
		il.log.Error("this profile's live credential cannot be resolved; watching it, never rotating it",
			"err", il.unresolved)
	case len(il.pool) == 0 && !in.FromEnv:
		il.log.Warn("this profile's pool is empty; it will never be rotated")
	}
	return il
}

// profileSetDiff says how the profile set differs between two configs, by
// name: profiles that came, went, or whose dir moved (including between the
// environment's dir and a declared one). Pools and thresholds are not part of
// it; those apply to a running profile in place.
func profileSetDiff(old, next *config.Config) (added, removed, repointed []string) {
	was := map[string]config.Profile{}
	for _, in := range old.EffectiveProfiles() {
		was[in.Name] = in
	}
	seen := map[string]bool{}
	for _, in := range next.EffectiveProfiles() {
		seen[in.Name] = true
		w, ok := was[in.Name]
		switch {
		case !ok:
			added = append(added, in.Name)
		case w.Dir != in.Dir || w.FromEnv != in.FromEnv:
			repointed = append(repointed, in.Name)
		}
	}
	for _, in := range old.EffectiveProfiles() {
		if !seen[in.Name] {
			removed = append(removed, in.Name)
		}
	}
	return added, removed, repointed
}

// forward hands one detector's rejections to run, tagged with the profile,
// until the daemon stops or the profile does (quit). It touches nothing but
// the channels.
func forward(il *profileLoop, stop, quit <-chan struct{}, out chan<- rejection) {
	in := il.det.Rejections()
	if in == nil {
		return
	}
	for {
		select {
		case <-stop:
			return
		case <-quit:
			return
		case r, ok := <-in:
			if !ok {
				return
			}
			select {
			case out <- rejection{il: il, r: r}:
			case <-stop:
				return
			case <-quit:
				return
			}
		}
	}
}
