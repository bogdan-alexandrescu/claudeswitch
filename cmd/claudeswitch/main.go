// Command claudeswitch keeps Claude Code pointed at an account that still has
// quota. This is M1: it observes and reports. It never switches anything.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/notify"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/poller"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/session"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// version is set at build time with -ldflags "-X main.version=…". It must stay
// a var: -X silently does nothing to a const, so every release built so far
// reported the hardcoded string instead of its tag, and there was no way to ask
// a running daemon which build it was.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usageText()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "status":
		err = cmdStatus(args)
	case "watch":
		err = cmdWatch(args)
	case "doctor":
		err = cmdDoctor(args)
	case "history":
		err = cmdHistory(args)
	case "init":
		err = cmdInit(args)
	case "config":
		err = cmdConfig(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "add":
		err = cmdAdd(args)
	case "use":
		err = cmdUse(args)
	case "accounts":
		err = cmdAccounts(args)
	case "plan":
		err = cmdPlan(args)
	case "why":
		err = cmdWhy(args)
	case "top":
		err = cmdTop(args)
	case "audit":
		err = cmdAudit(args)
	case "forget":
		err = cmdForget(args)
	case "rename":
		err = cmdRename(args)
	case "identify":
		err = cmdIdentify(args)
	case "remove":
		err = cmdRemove(args)
	case "session":
		err = cmdSession(args)
	case "setup":
		err = cmdSetup(args)
	case "login":
		err = cmdLogin(args)
	case "statusline":
		err = cmdStatusline(args)
	case "whoami":
		err = cmdWhoami(args)
	case "refresh":
		err = cmdRefresh(args)
	case "version", "-v", "--version":
		fmt.Println("claudeswitch " + version)
	default:
		usageText()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudeswitch: "+err.Error())
		os.Exit(1)
	}
}

func usageText() {
	fmt.Fprint(os.Stderr, `claudeswitch `+version+` — quota-aware account observer (M1: no switching yet)

  status     what every configured account's quota looks like right now
  watch      run the daemon (dry-run by default; --live to act)
  history    deduped rejection history from the transcripts
  session    token usage across every account used in a span of work
  doctor     check the things that have to be true for this to work
  setup      guided first run: vault your accounts, write the config, install
  config     show the settings in force, or change one
  init       write a starter config by hand instead
  uninstall  stop the daemon and remove what claudeswitch installed

  login <id> sign in to an account and vault it, verifying it is the right one
             --direct leaves your live session untouched, whatever happens;
             finish it with: login <id> --code <code>
  add <id>   vault the credential that is live right now, as <id>
  use <id>   swap Claude Code onto a vaulted account (hot; no restart needed)
  accounts   what is in the vault
  plan       the rotation decision right now (changes nothing)
  why        the same decision, account by account, with the reasoning
  top        a live view that refreshes in place (ctrl-c to leave)
  audit      what the daemon has observed, decided and done
  forget     drop an account's recorded observations (not its vault entry)
  remove     delete an account's vault entry and observations
  rename     give a vaulted account a different id, keeping its credential
  statusline one compact line for Claude Code's status line (read-only)
  whoami     which Claude account is live right now
  identify   record which seat each vaulted credential belongs to
  refresh    renew a vaulted account's credential (never the live one)

`)
}

// parseInterleaved parses flags that appear before OR after positional
// arguments, and returns the positionals.
//
// Go's flag package stops at the first non-flag, so `use personal --dry-run`
// would silently ignore the flag. Naively sorting flags to the front is worse:
// it separates `--config` from its value and hands the wrong string to the
// wrong flag. This walks the argument list instead, letting the FlagSet decide
// where each flag's value ends.
func parseInterleaved(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return positional
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func logger(verbose bool) *slog.Logger {
	lvl := slog.LevelInfo
	if verbose {
		lvl = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// load pulls config and state together, tolerating a missing config so that
// `status` still says something useful on a fresh machine.
func load(cfgPath string) (*config.Config, *state.State, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return nil, nil, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	st, serr := state.Load("")
	if serr != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", serr)
	}
	// Throw away observations that cannot be trusted before anything reads them.
	// Pin on the seat: the organization alone cannot distinguish two colleagues.
	pinned := map[string]string{}
	for _, a := range cfg.Accounts {
		pinned[a.ID] = a.Seat()
	}
	if dropped := st.Reconcile(pinned); len(dropped) > 0 {
		for _, why := range dropped {
			fmt.Fprintf(os.Stderr, "note: discarded stale observation for %s\n", why)
		}
		// Persist the cleanup, or the same records are re-discarded (and
		// re-reported) on every single invocation.
		if err := st.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "note: could not persist that cleanup: %v\n", err)
		}
	}
	return cfg, st, nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	refresh := fs.Bool("refresh", true, "fetch a live reading for the active account")
	detail := fs.Bool("detail", false, "per-account detail: bars, reading age, burn rate, binding limit")
	asJSON := fs.Bool("json", false, "machine-readable output")
	maxAge := fs.Duration("max-age", 0,
		"refresh any account whose reading is older than this (default: three poll intervals)")
	verbose := fs.Bool("v", false, "verbose logging")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(*verbose)
	p := poller.New(cfg, st, log)

	// When a daemon is running it owns the polling and the state file. Reading
	// here too would double-spend the API budget and race its writes, so the
	// CLI just reports what the daemon has already observed.
	daemonOwns := state.DaemonRunning()
	// When a daemon is running and keeping up, `cs status` must not re-read
	// anything. It shares one rate budget with the daemon, so a refresh here can
	// trip the burst guard, which locks out the daemon, which makes the readings
	// stale, which makes the next `cs status` refresh again. Only step in when
	// the daemon is plainly not doing its job.
	if daemonOwns && !readingsStale(cfg, st, blindLimit(cfg)) {
		*refresh = false
	}
	if *refresh {
		// Refresh every account whose reading is stale, not merely the active
		// one. Someone running `cs status` is asking what the accounts look
		// like now, and answering for only one of them is answering a different
		// question. The budget is shared with the daemon, so this cannot
		// overspend; anything it cannot afford keeps its previous reading.
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		age := *maxAge
		if age <= 0 {
			// Three poll intervals. A daemon polling every 60s keeps everything
			// well inside this, so `cs status` spends nothing in the common
			// case and only steps in when the daemon is actually behind.
			age = 3 * cfg.PollActive.Duration
			if age <= 0 {
				age = 3 * time.Minute
			}
		}
		if n, err := p.RefreshStale(ctx, age); err != nil {
			fmt.Fprintf(os.Stderr, "note: %v\n", err)
		} else if n > 0 {
			if err := st.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "note: could not save state: %v\n", err)
			}
		}
	}
	degraded, why := p.Degraded()
	if *asJSON {
		return emitJSON(statusJSON(cfg, st, p))
	}
	switches := recentSwitches(5)
	dec := policy.Decide(policy.Input{
		Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch,
		Pinned: st.Pinned, Dir: currentDir(), Lookahead: lookahead(cfg),
	})
	vlt := vault.New(log)
	vaulted := map[string]bool{}
	plans := map[string]string{}
	for _, a := range cfg.Accounts {
		vaulted[a.ID] = vlt.Has(a.ID)
		plans[a.ID] = vlt.PlanOf(a.ID)
	}
	render.Status(os.Stdout, render.Options{
		Cfg: cfg, St: st, Budget: p.Budget(), Degraded: degraded, DegradedWhy: why,
		DaemonOwns: daemonOwns, Vaulted: vaulted, Plans: plans, Detail: *detail,
		Switches: switches, Known: knownAccounts(cfg), Decision: &dec,
	})
	return nil
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	live := fs.Bool("live", false, "actually perform swaps (default: report only)")
	idleGap := fs.Duration("idle-gap", 8*time.Second, "quiet period that counts as between-turns")
	quiet := fs.Bool("quiet", false, "no macOS notifications")
	verbose := fs.Bool("v", false, "verbose logging")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(*verbose)

	// One daemon per machine. Two would fight over state.json.
	dlock, err := state.AcquireDaemonLock()
	if err != nil {
		return err
	}
	defer dlock.Release()

	p := poller.New(cfg, st, log)
	d := detector.New("", log)
	v := vault.New(log)
	nt := notify.New(!*quiet)
	aud, aerr := audit.Open("")
	if aerr != nil {
		return aerr
	}
	// Collect severity transitions so we can find out whether the API's own
	// "warning" is a better trigger than our fixed percentage.
	p.OnSeverityChange = func(account, kind, from, to string, percent float64) {
		pct := percent
		_ = aud.Write(audit.Event{Kind: "severity", Account: account, LimitKind: kind,
			FromSeverity: from, ToSeverity: to, Percent: &pct})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := d.Run(stop); err != nil {
			log.Error("detector stopped", "err", err)
		}
	}()

	mode := "DRY RUN — decisions are reported, nothing is changed"
	if *live {
		mode = "LIVE — swaps will be performed"
	}
	log.Info("watching", "mode", mode, "endpoint", usage.Endpoint, "transcripts", detector.ProjectsRoot())

	st.DaemonLive = *live
	st.DaemonSince = time.Now()
	if _, err := p.PollActive(ctx); err != nil {
		log.Warn("initial active poll failed", "err", err)
	}
	_ = st.SaveAs(state.OwnerDaemon)

	lastDecisionSig := ""
	// wantSwitchSince tracks how long a switch has been wanted but deferred, so
	// the idle-gap preference cannot defer it forever.
	var wantSwitchSince time.Time
	// lastWaitSig stops the exhausted-everything warning repeating every tick.
	lastWaitSig := ""
	lastReattribute := time.Now() // PollActive already ran at startup

	// evaluate runs the policy engine and, when live and the moment is right,
	// performs the swap.
	evaluate := func(trigger string) {
		dec := policy.Decide(policy.Input{
			Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch, Pinned: st.Pinned,
			Dir: d.CurrentDir(), Lookahead: lookahead(cfg),
		})
		// Only record a decision when it says something new. A tick every twenty
		// seconds writing "stay" produced 377 rows in one evening and buried the
		// one rejection that mattered.
		sig := string(dec.Kind) + "|" + dec.Target + "|" + dec.Reason
		if sig != lastDecisionSig {
			lastDecisionSig = sig
			ev := audit.Event{Kind: "decision", Decision: string(dec.Kind), From: st.Active,
				To: dec.Target, Reason: dec.Reason, Forced: dec.Forced, DryRun: !*live}
			if a, ok := st.Accounts[st.Active]; ok && a.Last != nil {
				f, sv := a.Last.FiveHour.Pct(), a.Last.SevenDay.Pct()
				ev.FiveHour, ev.SevenDay = &f, &sv
			}
			_ = aud.Write(ev)
		}

		// Acting on a reading that is too old to trust is how the daemon sat at
		// 93% without rotating: its polls were failing, "stay" is logged at
		// debug level, and nothing said a word. Staleness is now loud.
		if a, ok := st.Accounts[st.Active]; ok && a.Last != nil {
			if age := time.Since(a.LastAt); age > staleDecisionAfter(cfg) {
				log.Warn("deciding on a stale reading — the poller is not keeping up",
					"account", st.Active, "age", age.Round(time.Second),
					"reading", fmt.Sprintf("%.0f%%", a.Projected(time.Now())),
					"last_error", a.LastErr)
			}
		}
		// About to conclude there is nowhere to go? Check that on current
		// figures before acting on it. Ten-minute-old readings of the other
		// accounts are exactly how this ends up stuck at 100% while a reset
		// account sits idle.
		if dec.Kind == policy.Wait {
			if n := p.RefreshCandidates(ctx, 2*time.Minute); n > 0 {
				dec = policy.Decide(policy.Input{
					Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch,
					Pinned: st.Pinned, Dir: d.CurrentDir(), Lookahead: lookahead(cfg),
				})
				if dec.Kind == policy.Switch {
					log.Info("a re-read found room after all", "target", dec.Target,
						"rechecked", n)
				}
			}
		}

		if dec.Kind != policy.Switch {
			wantSwitchSince = time.Time{}
			// Say it once per distinct situation, not every twenty seconds.
			if dec.Kind == policy.Wait && sig != lastWaitSig {
				lastWaitSig = sig
				log.Warn("nowhere to rotate to — every account is out",
					"reason", dec.Reason, "recovers", dec.RecoversAccount,
					"at", dec.RecoversAt.Local().Format("15:04"))
				nt.Exhausted(dec.RecoversAccount, dec.RecoversAt)
			} else if dec.Kind != policy.Wait {
				lastWaitSig = ""
			}
			log.Debug("no rotation", "decision", dec.String(), "trigger", trigger)
			return
		}

		// Prefer an idle gap so a turn is never split across two accounts — but
		// only for so long. A session in continuous use never goes quiet, so
		// holding out indefinitely means the switch never happens until the hard
		// floor, which is the opposite of what this tool is for.
		if wantSwitchSince.IsZero() {
			wantSwitchSince = time.Now()
		}
		waited := time.Since(wantSwitchSince)
		if !dec.Forced && !d.IdleFor(*idleGap) {
			if waited < cfg.MaxSwitchWait.Duration {
				log.Info("switch wanted, session busy — waiting for an idle gap",
					"target", dec.Target, "reason", dec.Reason,
					"waited", waited.Round(time.Second),
					"will_force_after", cfg.MaxSwitchWait.Duration)
				return
			}
			log.Info("switch wanted and the session has not gone idle; going ahead anyway",
				"target", dec.Target, "waited", waited.Round(time.Second))
		}

		if !*live {
			log.Warn("WOULD SWITCH (dry run)", "from", st.Active, "to", dec.Target,
				"because", dec.Reason, "forced", dec.Forced)
			return
		}

		expectOrg := ""
		for _, a := range cfg.Accounts {
			if a.ID == dec.Target {
				expectOrg = a.OrgID
			}
		}
		if expectOrg == "" {
			if a, ok := st.Accounts[dec.Target]; ok {
				expectOrg = a.OrgID
			}
		}
		swapCtx, swapCancel := context.WithTimeout(ctx, 40*time.Second)
		res, serr := v.SwapTo(swapCtx, dec.Target, expectOrg)
		swapCancel()
		if serr != nil {
			log.Error("swap failed", "target", dec.Target, "err", serr,
				"rolled_back", res != nil && res.RolledBack)
			_ = aud.Write(audit.Event{Kind: "error", To: dec.Target, Reason: dec.Reason,
				Err: serr.Error()})
			return
		}
		from := st.Active
		wantSwitchSince = time.Time{}
		st.SetActive(dec.Target)
		st.LastSwitch = time.Now()
		if res.Usage != nil {
			a := st.Get(dec.Target)
			a.Last, a.LastAt, a.OrgID = res.Usage, res.Usage.FetchedAt, res.OrgID
		}
		_ = st.SaveAs(state.OwnerDaemon)
		_ = aud.Write(audit.Event{Kind: "switch", From: from, To: dec.Target,
			Reason: dec.Reason, Forced: dec.Forced})
		log.Info("switched", "from", from, "to", dec.Target, "because", dec.Reason)
		headroom := ""
		if res.Usage != nil {
			_, worst := res.Usage.Worst()
			headroom = fmt.Sprintf("%.0f%% used", worst)
		}
		nt.Switched(from, dec.Target, dec.Reason, headroom)
	}

	evaluate("startup")

	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	save := time.NewTicker(2 * time.Minute)
	defer save.Stop()

	for {
		select {
		case <-sig:
			log.Info("stopping")
			close(stop)
			_ = st.SaveAs(state.OwnerDaemon)
			return nil
		case <-tick.C:
			p.Tick(ctx)
			evaluate("poll")

			// A daemon that cannot see is worse than no daemon: it keeps
			// deciding, on figures that stopped moving. If nothing has been read
			// for long enough that this is a fault rather than a hiccup, say so
			// and stand down — the service manager restarts a process that
			// exits, and a fresh one has a far better chance than a wedged one.
			if blind, tooLong := p.Blind(blindLimit(cfg)); tooLong {
				log.Error("no successful reading for too long — exiting so the service manager can restart me",
					"blind_for", blind.Round(time.Second),
					"limit", blindLimit(cfg))
				nt.Send("blind", "claudeswitch restarting",
					"no usage reading for "+blind.Round(time.Minute).String())
				close(stop)
				_ = st.SaveAs(state.OwnerDaemon)
				return fmt.Errorf("blind for %s", blind.Round(time.Second))
			}
		case <-save.C:
			// Re-derive which account is actually live. Doing this only at
			// startup meant a rate-limited first poll left the daemon believing
			// the wrong account was active for the rest of its life. Doing it on
			// every save tick was the opposite mistake: an extra priority call
			// every two minutes, which starved the scheduled polls.
			if time.Since(lastReattribute) >= poller.ReattributeInterval {
				lastReattribute = time.Now()
				if _, err := p.PollActive(ctx); err != nil {
					log.Debug("could not re-confirm the active account", "err", err)
				}
			}
			if err := st.SaveAs(state.OwnerDaemon); err != nil {
				log.Warn("state save failed", "err", err)
			}
			warnExpiringRefresh(st, nt)
			maintainVault(ctx, v, st, cfg, log, nt, *live)
		case r := <-d.Rejections():
			active := st.Active
			if active == "" {
				active = "active"
			}
			p.ApplyRejection(active, r.Type, r.ResetsAt)
			_ = aud.Write(audit.Event{Kind: "rejection", From: active, Window: r.Type,
				ResetsAt: r.ResetsAt})
			log.Warn("account refused", "account", active, "window", r.Type,
				"clears_in", time.Until(r.ResetsAt).Round(time.Second),
				"detect_latency", r.Latency.Round(time.Millisecond),
				"worst_latency", d.WorstLatency().Round(time.Millisecond))
			_ = st.SaveAs(state.OwnerDaemon)
			evaluate("rejection") // a refusal always re-decides immediately
		}
	}
}

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	n := fs.Int("n", 30, "how many events")
	kind := fs.String("kind", "", "only this kind: decision|switch|rejection|severity|error")
	since := fs.Duration("since", 0, "only events newer than this (e.g. 24h)")
	parseInterleaved(fs, args)

	events, err := audit.Tail("", 100000)
	if err != nil {
		return err
	}
	events = audit.Filter(events, *kind, *since)
	if len(events) > *n {
		events = events[len(events)-*n:]
	}
	if len(events) == 0 {
		fmt.Print("\n  no audit events yet (run `claudeswitch watch`)\n\n")
		return nil
	}
	fmt.Println()
	t := render.NewTable([]string{"WHEN", "WHAT", "DETAIL"})
	// Newest first: the reason for opening the log is almost always the most
	// recent thing in it.
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		kind, detail := e.Kind, ""
		switch e.Kind {
		case "switch":
			kind = render.Good("switch")
			detail = nonEmpty(e.From, "?") + " → " + render.Bold(e.To)
			if e.Reason != "" {
				detail += render.Grey("  " + truncateStr(e.Reason, 40))
			}
		case "rejection":
			kind = render.Bad("refused")
			detail = fmt.Sprintf("%s on %s, cleared %s", e.From, e.Window,
				e.ResetsAt.Local().Format("15:04"))
		case "decision":
			kind = render.Dim("decision")
			detail = render.Dim(e.Decision)
			if e.To != "" {
				detail += render.Dim(" → " + e.To)
			}
			if e.Reason != "" {
				detail += render.Grey("  " + truncateStr(e.Reason, 40))
			}
		case "severity":
			kind = render.Warn("severity")
			pct := ""
			if e.Percent != nil {
				pct = fmt.Sprintf(" at %.0f%%", *e.Percent)
			}
			detail = fmt.Sprintf("%s %s: %s → %s%s", e.Account, e.LimitKind,
				e.FromSeverity, e.ToSeverity, pct)
		case "error":
			kind = render.Bad("error")
			detail = truncateStr(e.Err, 56)
		}
		if e.DryRun {
			detail += render.Dim("  [dry-run]")
		}
		if e.Forced {
			detail += render.Warn("  [forced]")
		}
		t.Add(render.Grey(e.At.Local().Format("01-02 15:04")), kind, detail)
	}
	fmt.Print(t.Render("  "))
	fmt.Println()
	return nil
}

func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	days := fs.Int("days", 21, "how far back to read")
	parseInterleaved(fs, args)

	log := logger(false)
	hits, raw, err := detector.Backfill("", time.Duration(*days)*24*time.Hour, log)
	if err != nil {
		return err
	}
	fmt.Printf("\n  %s\n\n", render.Grey(fmt.Sprintf(
		"%d raw records over %d days → %d real limit hits after dedupe",
		raw, *days, len(hits))))
	if len(hits) == 0 {
		fmt.Printf("  %s\n\n", render.Good("no refusals recorded"))
		return nil
	}
	t := render.NewTable([]string{"WHEN", "WINDOW", "CLEARED"})
	for i := len(hits) - 1; i >= 0; i-- {
		h := hits[i]
		window := h.Type
		if window == "seven_day" {
			window = render.Warn("weekly")
		} else {
			window = "5-hour"
		}
		t.Add(render.Grey(h.ResetsAt.Local().Format("Mon 02 Jan")), window,
			h.ResetsAt.Local().Format("15:04"))
	}
	fmt.Print(t.Render("  "))
	fmt.Println()
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	deep := fs.Bool("verify", false,
		"also confirm every vaulted credential still authenticates (one API call each)")
	parseInterleaved(fs, args)

	ok := func(b bool) string {
		if b {
			return "ok  "
		}
		return "FAIL"
	}
	fmt.Println()

	cfg, cerr := config.Load(*cfgPath)
	fmt.Printf("  [%s] config          %s\n", ok(cerr == nil), cfg.Path)
	if cerr != nil {
		fmt.Printf("         └ %v\n", cerr)
		fmt.Printf("         └ fix: run `claudeswitch init`\n")
	}

	dupState, _ := state.Load("")
	if dup := checkDuplicateCredentials(cfg, dupState); dup != "" {
		fmt.Printf("  [%s] vault entries   %s\n", ok(false), "corrupted")
		fmt.Printf("         └ %s\n", dup)
		fmt.Printf("         └ fix: re-add the affected accounts while each is signed in\n")
	} else if cerr == nil {
		fmt.Printf("  [%s] vault entries   %s\n", ok(true), "each holds its own credential")
	}

	if problem := checkServiceQoS(); problem != "" {
		fmt.Printf("  [%s] service qos     %s\n", ok(false), "throttled")
		fmt.Printf("         └ %s\n", problem)
		fmt.Printf("         └ fix: remove the ProcessType key and reload the agent,\n")
		fmt.Printf("           or re-run install.sh\n")
	} else {
		fmt.Printf("  [%s] service qos     %s\n", ok(true), "not throttled")
	}

	blob, kerr := keychain.ReadLive()
	fmt.Printf("  [%s] credentials     %s\n", ok(kerr == nil), keychain.Backend)
	if kerr != nil {
		fmt.Printf("         └ %v\n", kerr)
		fmt.Printf("         └ fix: approve access when macOS asks, or click Always Allow\n")
	} else {
		o := blob.ClaudeAIOAuth
		fmt.Printf("         └ access token %s, expires %s\n",
			keychain.Redact(o.AccessToken), humanUntil(o.Expiry()))
		fmt.Printf("         └ refresh token expires %s%s\n",
			humanUntil(o.RefreshExpiry()), warnIfSoon(o.RefreshExpiry()))
		fmt.Printf("         └ mcpOAuth present: %v (must be preserved across a swap)\n",
			len(blob.MCPOAuth) > 0)
	}

	if kerr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		u, uerr := usage.NewClient().Fetch(ctx, blob.ClaudeAIOAuth.AccessToken)
		var rl *usage.RateLimitedError
		switch {
		case uerr == nil:
			fmt.Printf("  [ok  ] usage api       5h %.1f%%  7d %.1f%%  org %s\n",
				u.FiveHour.Pct(), u.SevenDay.Pct(), shortID(u.OrgID))
		case errors.As(uerr, &rl):
			fmt.Printf("  [warn] usage api       rate limited, clears in %s (expected, not a fault)\n",
				rl.RetryAfter.Round(time.Second))
		default:
			fmt.Printf("  [FAIL] usage api       %v\n", uerr)
			if usage.IsShapeError(uerr) {
				fmt.Printf("         └ the endpoint changed shape: predictive switching would be disabled\n")
			}
		}
	}

	root := detector.ProjectsRoot()
	entries, derr := os.ReadDir(root)
	fmt.Printf("  [%s] transcripts     %s (%d project dirs)\n", ok(derr == nil), root, len(entries))

	if cfg != nil {
		v := vault.New(logger(false))
		if !cfg.RefreshEnabled() {
			fmt.Printf("  [warn] auto-refresh    OFF (auto_refresh = false) — vaulted tokens will expire\n")
		} else {
			fmt.Printf("  [ok  ] auto-refresh    renews at %s before expiry; probes idle accounts every %s\n",
				cfg.RefreshWindow.Duration, nonEmpty(durOrNever(cfg.RefreshProbe.Duration), "never"))
			for _, a := range cfg.Ordered() {
				if !v.Has(a.ID) {
					continue
				}
				o, err := v.Load(a.ID)
				if err != nil {
					continue
				}
				exp := "unknown"
				if e := o.Expiry(); !e.IsZero() {
					exp = humanUntil(e)
				}
				rt := "present"
				if o.RefreshToken == "" {
					rt = "NONE — cannot be renewed"
				} else if e := o.RefreshExpiry(); !e.IsZero() {
					rt = "expires " + humanUntil(e)
				} else {
					rt = "present, expiry not reported"
				}
				fmt.Printf("         └ %-18s access %s · refresh token %s\n", a.ID, exp, rt)
			}
		}
	}
	if cfg != nil {
		used, avail := cfg.CallsPerWindow(), float64(cfg.APIBudget-1)
		mark := "ok  "
		if used > avail {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] poll cadence    active %s · hot %s · idle %s\n", mark,
			cfg.PollActive.Duration, cfg.PollHot.Duration, cfg.PollIdle.Duration)
		fmt.Printf("         └ %.1f of %.0f usage calls per 5 min (api_budget %d, one held for swaps)\n",
			used, avail, cfg.APIBudget)
	}
	// A vault entry that no longer authenticates is invisible until the moment
	// it is needed — which is the moment it matters most. Behind a flag because
	// it costs a call per account.
	if *deep && cfg != nil {
		v := vault.New(logger(false))
		fmt.Printf("  [    ] credentials     verifying each vaulted account…\n")
		for _, a := range cfg.Ordered() {
			if !v.Has(a.ID) {
				fmt.Printf("         └ %-18s not vaulted\n", a.ID)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cred, lerr := v.Load(a.ID)
			var pr *usage.Profile
			if lerr == nil {
				pr, lerr = v.Identify(ctx, cred.AccessToken)
			}
			cancel()
			switch {
			case lerr != nil:
				fmt.Printf("         └ %-18s FAILS — %v\n", a.ID, truncateStr(lerr.Error(), 44))
				fmt.Printf("           %s\n", "fix: cs login "+a.ID+" --direct")
			case a.Seat() != "" && pr.Seat() != a.Seat():
				fmt.Printf("         └ %-18s WRONG ACCOUNT — holds %s\n", a.ID, pr.Describe())
				fmt.Printf("           %s\n", "fix: cs login "+a.ID+" --direct")
			default:
				fmt.Printf("         └ %-18s ok — %s · %s\n", a.ID, pr.Account.Email, pr.Plan())
			}
		}
	} else if cfg != nil {
		fmt.Printf("  [ok  ] credentials     %d vaulted; `cs doctor --verify` checks each one works\n",
			countVaulted(cfg))
	}
	fmt.Printf("  [ok  ] switching       live rotation is wired; `cs plan` says what it would do\n")
	fmt.Println()
	return nil
}

func humanUntil(t time.Time) string {
	if t.IsZero() {
		// Credentials obtained through login --direct have no refresh-token
		// expiry: the token endpoint does not return one. Nothing depends on it
		// (a zero expiry is never treated as dead), but the early warning before
		// a refresh token dies cannot fire for such an account, so say so rather
		// than print a confident-looking blank.
		return "unknown (not reported for this credential)"
	}
	d := time.Until(t)
	if d < 0 {
		return "EXPIRED"
	}
	if d > 48*time.Hour {
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
	return "in " + d.Round(time.Minute).String()
}

func warnIfSoon(t time.Time) string {
	if !t.IsZero() && time.Until(t) < 7*24*time.Hour {
		return "  ⚠ re-login needed soon, or this account cannot be polled"
	}
	return ""
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	path := fs.String("config", config.DefaultPath(), "where to write")
	parseInterleaved(fs, args)

	if _, err := os.Stat(*path); err == nil {
		return fmt.Errorf("%s already exists; not overwriting", *path)
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o700); err != nil {
		return err
	}
	tmpl := `# claudeswitch. Thresholds are the tuning surface; status shows them.
switch_at        = 85    # rotate away at this much of the 5-hour window
switch_at_weekly = 98    # and at this much of the weekly one — a weekly window
                         # spent is gone for days, a session one refills today
hard_floor       = 99    # above this, swap mid-turn rather than wait for an idle gap
switch_when = "idle"
cooldown    = "10m"

# Work accounts carry the load; personal is the reserve.
priority = ["work-a", "work-b", "work-c", "personal"]

[[account]]
id    = "work-a"
label = "work-a"
scope = "work"

[[account]]
id    = "personal"
label = "personal"
scope = "personal"
reserve = 70        # never auto-used above this utilization
`
	if err := os.WriteFile(*path, []byte(tmpl), 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s — edit the account ids, then run `claudeswitch doctor`\n", *path)
	return nil
}

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	scope := fs.String("scope", "work", `which projects may use it: "work" or "personal"`)
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch add <account-id>\n\n" +
			"Log in to the account first (`claude` → /login), then name what you just logged into.")
	}
	id := positional[0]

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	known := false
	for _, a := range cfg.Accounts {
		if a.ID == id {
			known = true
		}
	}
	if !known {
		fmt.Fprintf(os.Stderr, "note: %q is not in %s yet; vaulting it anyway\n", id, cfg.Path)
	}

	log := logger(false)
	v := vault.New(log)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var configured []string
	expectSeat := ""
	for _, a := range cfg.Accounts {
		configured = append(configured, a.ID)
		if a.ID == id {
			expectSeat = a.Seat()
		}
	}
	// Check against everything we have actually stored, not only what the config
	// mentions. This command will vault an account the config does not list —
	// it says so as it does it — and those entries used to be invisible here, so
	// a second name could be given to a pool that already had one.
	others := st.KnownAccounts(configured)
	e, err := v.Store(ctx, id, expectSeat, others)
	if err != nil {
		return err
	}
	st.AddVaulted(id)
	acct := st.Get(id)
	acct.OrgID = e.OrgID
	acct.RefreshExpiry = e.RefreshExpiry
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	fmt.Printf("\n  ✓ vaulted %s\n", keychain.VaultService(id))
	if email, orgName, _ := cachedIdentity(); orgName != "" {
		fmt.Printf("    account       %s (as Claude Code labels it)\n", email)
		fmt.Printf("    organization  %s\n", orgName)
	}
	fmt.Printf("    org id        %s\n", e.OrgID)
	fmt.Printf("    tier          %s (%s)\n", nonEmpty(e.Tier, "unknown"), nonEmpty(e.Subscription, "?"))
	fmt.Printf("    access token  expires %s\n", humanUntil(e.Expiry))
	if e.NoRefreshToken {
		fmt.Printf("    refresh token NONE ISSUED\n")
		fmt.Printf("\n  ⚠ This credential cannot be renewed. It will work until %s and then\n",
			humanUntil(e.Expiry))
		fmt.Printf("    need an interactive login again. Common with SSO accounts.\n")
		fmt.Printf("    claudeswitch will mark it unavailable rather than rotate into a dead\n")
		fmt.Printf("    credential, but it cannot keep this account alive on its own.\n")
	} else {
		fmt.Printf("    refresh token expires %s%s\n", humanUntil(e.RefreshExpiry), warnIfSoon(e.RefreshExpiry))
	}
	fmt.Printf("    mcpOAuth      not copied into the vault (it belongs to the machine, not the account)\n")
	if !known {
		offerConfigBlock(cfg.Path, id, *scope, e)
	}
	fmt.Println()
	return nil
}

// offerConfigBlock closes the gap between vaulting a credential and being able
// to use it. Until it existed, `add` stored the credential and then left the
// account in limbo: `login` refused it for not being in the config, `accounts`
// did not list it, and the observation `add` had just written was discarded by
// the next command that read the state file.
//
// Everything in the block is already in hand here, and setup has always written
// its own config for the reason given above it: a seat uuid is only knowable
// after signing in, so this is not a file a person can correctly write in
// advance. Printing it and asking them to retype it is how the organization-only
// pin survived as long as it did.
func offerConfigBlock(path, id, scope string, e *vault.Entry) {
	block := fmt.Sprintf("\n[[account]]\nid           = %q\nscope        = %q\n"+
		"account_uuid = %q\norg_id       = %q\n", id, scope, e.AccountUUID, e.OrgID)

	fmt.Printf("\n  %s is not in %s yet:\n\n", id, path)
	for _, line := range strings.Split(strings.Trim(block, "\n"), "\n") {
		fmt.Printf("    %s\n", line)
	}

	// Never prompt when nobody is there to answer. A pipe reaches EOF
	// immediately, and askYes would read that as the default — writing to
	// someone's config because their terminal was not attached.
	if !stdinIsTerminal() {
		fmt.Printf("\n    Add that block, then put %q in the priority list where you\n"+
			"    want it spent. Without that it still rotates, but last.\n", id)
		return
	}
	fmt.Println()
	if !askYes("  write it, and add "+id+" to the priority list?", true) {
		fmt.Printf("\n    Left alone. Add the block yourself when you are ready.\n")
		return
	}
	if err := appendAccount(path, id, block); err != nil {
		fmt.Printf("\n  ⚠ could not write %s: %v\n", path, err)
		fmt.Printf("    The credential is vaulted; add the block above by hand.\n")
		return
	}
	fmt.Printf("\n  ✓ %s now lists %s, last in the priority order.\n", path, id)
	fmt.Printf("    Move it earlier in `priority` to spend it sooner.\n")
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// priorityLine matches the rotation order when it is written on one line, which
// is how this program writes it. Anything else is left alone rather than
// guessed at.
var priorityLine = regexp.MustCompile(`(?m)^priority\s*=\s*\[([^\]]*)\]`)

// appendAccount adds the block to the config and names the account in the
// priority list, last — the position it already occupies implicitly, since
// Ordered() puts unlisted accounts at the end. Being explicit costs nothing and
// makes the order something you can see and edit.
//
// The edit is textual so the rest of the file survives byte for byte: comments
// included, which a regenerated config would lose. It is then parsed back
// before being moved into place, so a config this could not produce cleanly is
// never the one left on disk.
func appendAccount(path, id, block string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := strings.TrimRight(string(raw), "\n") + "\n" + block

	if m := priorityLine.FindSubmatchIndex([]byte(out)); m != nil {
		inner := strings.TrimSpace(out[m[2]:m[3]])
		sep := ", "
		if inner == "" {
			sep = ""
		}
		out = out[:m[3]] + sep + strconv.Quote(id) + out[m[3]:]
	}

	tmp := path + ".claudeswitch-new"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	// Parse what we are about to install, not what we meant to write.
	cfg, err := config.Load(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("the edit would not load, so it was discarded: %w", err)
	}
	found := false
	for _, a := range cfg.Accounts {
		if a.ID == id {
			found = true
		}
	}
	if !found {
		os.Remove(tmp)
		return fmt.Errorf("the edit parsed but %q was not in it; discarded", id)
	}
	return os.Rename(tmp, path)
}

func nonEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func cmdUse(args []string) error {
	fs := flag.NewFlagSet("use", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	dry := fs.Bool("dry-run", false, "say what would happen, change nothing")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch use <account-id>")
	}
	id := positional[0]

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(false)
	v := vault.New(log)

	if !v.Has(id) {
		return fmt.Errorf("account %q is not in the vault. Log in to it, then run `claudeswitch add %s`", id, id)
	}
	expectOrg := ""
	for _, a := range cfg.Accounts {
		if a.ID == id && a.OrgID != "" {
			expectOrg = a.OrgID
		}
	}
	if expectOrg == "" {
		if a, ok := st.Accounts[id]; ok {
			expectOrg = a.OrgID
		}
	}

	if *dry {
		fmt.Printf("\n  would swap to %q (expecting org %s)\n", id, nonEmpty(expectOrg, "any"))
		fmt.Printf("  mcpOAuth would be carried over from the live item, unchanged\n\n")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := v.SwapTo(ctx, id, expectOrg)
	if err != nil {
		if res != nil && res.RolledBack {
			return fmt.Errorf("%w\n  (your previous credential was restored and verified)", err)
		}
		return err
	}
	from := st.Active
	st.SetActive(id)
	// A deliberate manual switch gets the cooldown's protection too, so the
	// daemon does not immediately rotate away from the account you just chose.
	st.LastSwitch = time.Now()
	// And it belongs in the audit log. Recording only the daemon's switches made
	// the history look stale and wrong: every manual swap was invisible.
	// Only when it actually moved. Re-selecting the account already in use is
	// not a switch, and recording it as one makes the history unreadable.
	if from != id {
		if aud, err := audit.Open(""); err == nil {
			_ = aud.Write(audit.Event{Kind: "switch", From: from, To: id,
				Reason: "requested with `cs use`"})
		}
	}
	acct := st.Get(id)
	if res.Usage != nil {
		acct.Last = res.Usage
		acct.LastAt = res.Usage.FetchedAt
		acct.OrgID = res.OrgID
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	fmt.Printf("\n  ✓ now using %s\n", id)
	if res.Usage != nil {
		fmt.Printf("    5-hour %.1f%%   7-day %.1f%%   org %s\n",
			res.Usage.FiveHour.Pct(), res.Usage.SevenDay.Pct(), shortID(res.OrgID))
	} else {
		fmt.Printf("    installed, but usage could not be read to confirm it (rate limited)\n")
	}
	fmt.Printf("    your MCP logins were left untouched\n")
	fmt.Printf("    no restart needed — running sessions pick this up going forward\n\n")
	return nil
}

func cmdAccounts(args []string) error {
	fs := flag.NewFlagSet("accounts", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	v := vault.New(logger(false))
	if *asJSON {
		out := make([]map[string]any, 0, len(cfg.Accounts))
		for _, a := range cfg.Ordered() {
			m := map[string]any{
				"id": a.ID, "scope": a.Scope, "vaulted": v.Has(a.ID),
				"active": a.ID == st.Active,
			}
			if seat, org := v.IdentityOf(a.ID); seat != "" {
				m["seat"], m["org_id"] = seat, org
			}
			if e := v.DescribeOf(a.ID); e != "" {
				m["email"] = e
			}
			if pl := v.PlanOf(a.ID); pl != "" {
				m["plan"] = pl
			}
			out = append(out, m)
		}
		return emitJSON(out)
	}
	fmt.Println()
	t := render.NewTable([]string{"", "ACCOUNT", "SCOPE", "SIGNED IN AS", "PLAN", "ORGANIZATION"})
	for _, a := range cfg.Ordered() {
		org := a.OrgID
		if org == "" {
			if ex, ok := st.Accounts[a.ID]; ok {
				org = ex.OrgID
			}
		}
		who, plan, orgName := "-", "-", ""
		if b, err := keychain.Read(keychain.VaultService(a.ID)); err == nil && b.Meta != nil {
			who = nonEmpty(b.Meta.Email, "-")
			plan = nonEmpty(b.Meta.Plan, "-")
			orgName = b.Meta.OrgName
		}
		if orgName == "" {
			orgName = shortID(org)
		}
		mark, name := " ", a.Name()
		if a.ID == st.Active {
			mark, name = render.Good("▸"), render.Bold(name)
		}
		if !v.Has(a.ID) {
			name = render.Dim(a.Name())
			who, plan, orgName = render.Dim("not vaulted"), "-", "-"
		}
		t.Add(mark, name, nonEmpty(a.Scope, "-"), who, plan, nonEmpty(orgName, "-"))
	}
	fmt.Print(t.Render("  "))
	fmt.Println()
	return nil
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	refresh := fs.Bool("refresh", true, "read the active account's usage first")
	planJSON := fs.Bool("json", false, "machine-readable output")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(false)
	if *refresh && !state.DaemonRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		p := poller.New(cfg, st, log)
		if _, err := p.PollActive(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "note: %v\n", err)
		}
		_ = st.Save()
	}

	dir := currentDir()
	d := policy.Decide(policy.Input{
		Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch, Pinned: st.Pinned,
		Dir: dir, Lookahead: lookahead(cfg),
	})

	if *planJSON {
		return emitJSON(map[string]any{"decision": decisionJSON(d), "dir": dir})
	}
	fmt.Println()
	if dir != "" {
		if pr, ok := cfg.ProjectFor(dir); ok {
			fmt.Printf("  here      %s\n", dir)
			if len(pr.Eligible) > 0 {
				fmt.Printf("  allowed   %s only\n", strings.Join(pr.Eligible, ", "))
			}
			if len(pr.Prefer) > 0 {
				fmt.Printf("  prefer    %s\n", strings.Join(pr.Prefer, ", "))
			}
		}
	}
	fmt.Printf("  decision  %s\n", d.Kind)
	fmt.Printf("  because   %s\n", d.Reason)
	if d.Kind == policy.Switch {
		fmt.Printf("  target    %s\n", d.Target)
		if d.Forced {
			fmt.Printf("  timing    immediately, mid-turn (past the %.0f%% hard floor)\n", cfg.HardFloor)
		} else {
			fmt.Printf("  timing    at the next idle gap between turns\n")
		}
	}
	if d.Kind == policy.Wait && d.RecoversAccount != "" {
		fmt.Printf("  recovers  %s in %s (at %s)\n", d.RecoversAccount,
			time.Until(d.RecoversAt).Round(time.Minute), d.RecoversAt.Local().Format("15:04"))
	}
	fmt.Println()
	switch {
	case !state.DaemonRunning():
		fmt.Printf("  no daemon is running, so nothing will act on this.\n")
		fmt.Printf("  start one with `claudeswitch watch`, or install it with ./install.sh\n")
	case st.DaemonLive:
		fmt.Printf("  a LIVE daemon is running: it will act on a decision like this.\n")
	default:
		fmt.Printf("  a dry-run daemon is running: it will log this decision and change nothing.\n")
		fmt.Printf("  re-install with ./install.sh --live to let it act.\n")
	}
	fmt.Println()
	return nil
}

// cmdForget removes an account's observations. Useful when an account is
// renamed, removed from config, or was recorded under a wrong attribution.
func cmdForget(args []string) error {
	fs := flag.NewFlagSet("forget", flag.ExitOnError)
	stale := fs.Bool("stale", false, "drop every recorded account that is no longer in the config")
	cfgPath := fs.String("config", "", "path to config.toml")
	positional := parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	var dropped []string
	if *stale {
		known := map[string]bool{}
		for _, a := range cfg.Accounts {
			known[a.ID] = true
		}
		for id := range st.Accounts {
			if !known[id] {
				st.Drop(id)
				dropped = append(dropped, id)
			}
		}
		if !known[st.Active] {
			st.Active = ""
		}
	}
	for _, id := range positional {
		if _, ok := st.Accounts[id]; ok {
			st.Drop(id)
			dropped = append(dropped, id)
			if st.Active == id {
				st.Active = ""
			}
		}
	}
	if len(dropped) == 0 {
		fmt.Print("\n  nothing to forget\n\n")
		return nil
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("\n  forgot %v\n  (vault entries are untouched; run `claudeswitch status` to re-read)\n\n", dropped)
	return nil
}

// warnExpiringRefresh nags before an idle account's refresh token dies. A dead
// refresh token means the account cannot be swapped to OR polled, and the only
// cure is an interactive login.
func warnExpiringRefresh(st *state.State, nt *notify.Notifier) {
	for id, a := range st.Accounts {
		if a.RefreshExpiry.IsZero() {
			continue
		}
		if d := time.Until(a.RefreshExpiry); d > 0 && d < 5*24*time.Hour {
			nt.RefreshExpiring(id, d)
		}
	}
}

// slBar renders a fixed-width fill bar for the status line. Solid Unicode
// blocks rather than the ASCII [####....] that `status` uses: the status line is
// glanced at rather than read, and the filled glyphs carry at a glance.
func slBar(v float64, w int) string {
	f := int(v/100*float64(w) + 0.5)
	if f > w {
		f = w
	}
	if f < 0 {
		f = 0
	}
	return strings.Repeat("▓", f) + strings.Repeat("░", w-f)
}

// slUntil is a compact time-to-reset: "5d9h", "3h39m", "40m", "now". The
// countdown is the actionable half of a utilization figure — 76% with four hours
// to run and 76% with twenty minutes to run are different situations.
func slUntil(t *time.Time, now time.Time) string {
	if t == nil || t.IsZero() {
		return "?"
	}
	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}
	if d >= 24*time.Hour {
		days := int(d.Hours()) / 24
		if hrs := int(d.Hours()) % 24; hrs > 0 {
			return fmt.Sprintf("%dd%dh", days, hrs)
		}
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// slAge formats how long ago a reading was taken.
func slAge(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// Colour bands for the status line. These track what claudeswitch will actually
// DO rather than arbitrary quartiles, so the colour and the appended words agree:
// green is comfortable, yellow is worth knowing, orange means a rotation is
// coming at cfg.SwitchAt, red means a mid-turn swap at cfg.HardFloor.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiOrange = "\033[38;5;208m" // no orange in the 8-colour set; 256-colour cube
	ansiRed    = "\033[31m"
)

// slColorEnabled reports whether to emit escapes. Honours NO_COLOR (the de facto
// standard) so the line stays usable when captured to a file or piped.
func slColorEnabled() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("CLAUDESWITCH_NO_COLOR") != "" {
		return false
	}
	return true
}

// slBand returns the colour for a utilization figure.
func slBand(v float64, cfg *config.Config) string {
	switch {
	case v >= cfg.HardFloor:
		return ansiRed
	case v >= cfg.SwitchAt:
		return ansiOrange
	case v >= slYellowAt:
		return ansiYellow
	default:
		return ansiGreen
	}
}

// slYellowAt is the one band boundary not already in config: the point at which
// a window is worth a glance though nothing is imminent.
const slYellowAt = 60.0

func slPaint(colour, s string, on bool) string {
	if !on || colour == "" {
		return s
	}
	return colour + s + ansiReset
}

// slWindow renders one usage window. The "▸" marks the binding window — the one
// that will bite first — so two close figures do not have to be compared by eye.
func slWindow(label string, w usage.Window, binding bool, trend string, now time.Time,
	cfg *config.Config, colour bool) string {
	mark := ""
	if binding {
		mark = "▸"
	}
	if !w.Known() {
		return mark + label + slPaint(ansiDim, " no reading", colour)
	}
	band := slBand(w.Pct(), cfg)
	// The bar and the figure carry the same colour: they are two readings of one
	// number, and colouring only one of them invites reading them as different.
	return fmt.Sprintf("%s%s %s %s %s",
		mark, label,
		slPaint(band, slBar(w.Pct(), 10), colour),
		slPaint(band, fmt.Sprintf("%.0f%%%s", w.Pct(), trend), colour),
		slPaint(ansiDim, slUntil(w.ResetsAt, now), colour))
}

// cmdStatusline prints one compact line for Claude Code's status line. It is
// strictly read-only: no polling, no state writes, no API calls, because it
// runs on every render.
//
// Shape: name · session <bar> <pct><trend> <reset> · week <bar> <pct> <reset>
// followed by exception notes only when they apply, so the normal line stays
// quiet and anything appended means something needs attention.
func cmdStatusline(args []string) error {
	fs := flag.NewFlagSet("statusline", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	parseInterleaved(fs, args)

	cfg, err := config.Load(*cfgPath)
	if err != nil && cfg == nil {
		return nil // never break someone's prompt over a config problem
	}
	st, err := state.Load("")
	if err != nil || st.Active == "" {
		fmt.Print("claudeswitch  no account selected")
		return nil
	}
	a, ok := st.Accounts[st.Active]
	if !ok || a.Last == nil {
		fmt.Printf("%s  no reading yet", st.Active)
		return nil
	}
	name := st.Active
	for _, c := range cfg.Accounts {
		if c.ID == st.Active {
			name = c.Name()
		}
	}
	now := time.Now()
	colour := slColorEnabled()

	if !a.BurntTil.IsZero() && now.Before(a.BurntTil) {
		fmt.Printf("%s  %s  week %s %s",
			slPaint(ansiDim, name, colour),
			slPaint(ansiRed, "refused until "+a.BurntTil.Local().Format("15:04"), colour),
			slPaint(slBand(a.Last.SevenDay.Pct(), cfg), slBar(a.Last.SevenDay.Pct(), 10), colour),
			slPaint(slBand(a.Last.SevenDay.Pct(), cfg),
				fmt.Sprintf("%.0f%% %s", a.Last.SevenDay.Pct(), slUntil(a.Last.SevenDay.ResetsAt, now)), colour))
		return nil
	}

	bindKey, worst := a.Last.Worst()

	// Trend on the binding window only. Two arrows compete for attention, and
	// the binding window is the one whose direction matters.
	trend := ""
	if !a.PrevAt.IsZero() {
		switch {
		case worst > a.PrevWorst+0.5:
			trend = "↑"
		case worst < a.PrevWorst-0.5:
			trend = "↓"
		}
	}
	var t5, t7 string
	switch bindKey {
	case usage.FiveHourKey:
		t5 = trend
	case usage.SevenDayKey:
		t7 = trend
	}

	parts := []string{
		slPaint(ansiDim, name, colour),
		slWindow("session", a.Last.FiveHour, bindKey == usage.FiveHourKey, t5, now, cfg, colour),
		slWindow("week", a.Last.SevenDay, bindKey == usage.SevenDayKey, t7, now, cfg, colour),
	}

	// What claudeswitch is about to do, in words — decided by the policy engine
	// rather than by the threshold alone.
	//
	// Saying "switching now" because a number crossed a line promises relief
	// that may not be coming: when every account is exhausted there is nowhere
	// to go, and the useful fact is when quota returns instead.
	if worst >= cfg.SwitchAt {
		dec := policy.Decide(policy.Input{
			Cfg: cfg, St: st, Now: now, LastSwitch: st.LastSwitch,
			Pinned: st.Pinned, Dir: currentDir(), Lookahead: lookahead(cfg),
		})
		switch dec.Kind {
		case policy.Switch:
			if dec.Forced {
				parts = append(parts, "· switching to "+dec.Target)
			} else {
				parts = append(parts, "· rotating to "+dec.Target)
			}
		case policy.Wait:
			switch {
			case dec.RecoversAt.IsZero():
				parts = append(parts, "· all accounts out")
			case time.Until(dec.RecoversAt) < time.Hour:
				parts = append(parts, fmt.Sprintf("· all out, quota in %dm",
					int(time.Until(dec.RecoversAt).Minutes())+1))
			default:
				parts = append(parts, "· all out, quota at "+
					dec.RecoversAt.Local().Format("15:04"))
			}
		default:
			// Over the trigger but holding: pinned, or inside the cooldown.
			parts = append(parts, "· holding")
		}
	}

	// A stopped poller otherwise looks exactly like a healthy account: the
	// figures simply stop moving, and nothing on the line says so.
	if !a.LastAt.IsZero() {
		if age := now.Sub(a.LastAt); age > 10*time.Minute {
			parts = append(parts, slPaint(ansiYellow, "· read "+slAge(age)+" ago", colour))
		}
	}

	fmt.Print(strings.Join(parts, "  "))
	return nil
}

// maintainVault keeps vaulted credentials usable.
//
// Two jobs, and the first matters more. Claude Code refreshes the live session
// on its own schedule, and that REVOKES the token our vault copy holds — so the
// entry for the active account rots within hours unless we notice the drift and
// re-capture. That costs nothing and cannot fail dangerously.
//
// The second job is refreshing genuinely idle accounts before their access
// tokens expire. That one calls the token endpoint, which rotates and revokes,
// so it is never attempted on the account that is currently live.
func maintainVault(ctx context.Context, v *vault.Vault, st *state.State, cfg *config.Config,
	log *slog.Logger, nt *notify.Notifier, live bool) {

	if st.Active != "" && v.Has(st.Active) {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		changed, err := v.SyncActive(sctx, st.Active, cfg.SeatOf(st.Active))
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
					log.Info("the live credential belongs to a different account than we thought",
						"was", st.Active, "is", owner)
					st.Active = owner
				} else {
					log.Info("the live credential is an account we do not know; clearing",
						"was", st.Active, "live_seat", foreign.GotOrg)
					st.Active = ""
				}
			} else {
				log.Warn("could not sync the active account's vault entry",
					"account", st.Active, "err", err)
			}
		case changed:
			log.Info("vault entry for the active account was refreshed underneath us and re-captured",
				"account", st.Active)
		}
	}

	// Refreshing is a real mutation of a credential, so it stays behind --live
	// exactly like a swap does.
	// Refreshing runs in dry-run too. Dry-run means "do not rotate"; letting
	// every stored credential expire while watching would be a strange reading
	// of that, and a refresh never changes which account is in use.
	if !cfg.RefreshEnabled() {
		return
	}
	for _, a := range cfg.Ordered() {
		if a.ID == st.Active || !v.Has(a.ID) {
			continue
		}
		// Belt and braces: never refresh whatever is actually live, whatever
		// state believes about which account that is.
		if v.IsLive(a.ID) {
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
		_, err := v.Refresh(rctx, a.ID, cfg.SeatOf(a.ID), false, false)
		cancel()
		if err != nil {
			var needsLogin *oauth.NeedsLoginError
			if errors.As(err, &needsLogin) {
				log.Error("account needs an interactive login", "account", a.ID, "detail", err)
				nt.Send("relogin:"+a.ID, a.ID+" needs a login",
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
				st.Get(a.ID).LastErr = err.Error()
				if serr := st.Save(); serr != nil {
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

// cmdWhoami answers "which account is Claude Code actually using?"
//
// The authority is the credential itself, not Claude Code's opinion of it.
// `claude auth status` reports the cached oauthAccount block in ~/.claude.json,
// which does NOT move when the credential is swapped — after a swap it happily
// reports the previous account (observed 2026-09-09, while the live credential
// belonged to a different organization entirely). So the organization is read
// from the usage API using the live token, and the cached name is shown only as
// a label, with a warning when the two disagree.
func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	parseInterleaved(fs, args)

	cfg, _ := config.Load(*cfgPath)

	blob, err := keychain.ReadLive()
	if err != nil {
		return fmt.Errorf("cannot read the live credential: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	u, uerr := vault.New(logger(false)).FetchLive(ctx, blob.ClaudeAIOAuth.AccessToken)

	cachedEmail, cachedOrgName, cachedOrgID := cachedIdentity()

	fmt.Println()
	if uerr != nil {
		if rl, ok := usage.IsRateLimited(uerr); ok {
			fmt.Printf("  cannot confirm right now: the usage API is rate limited (%s).\n\n",
				rl.RetryAfter.Round(time.Second))
		} else {
			fmt.Printf("  cannot confirm which account this credential belongs to: %v\n\n", uerr)
		}
		fmt.Printf("  Claude Code's cached view says %s / %s — but that is a cache and does\n",
			cachedEmail, cachedOrgName)
		fmt.Printf("  not follow a credential swap, so do not vault anything on its word.\n\n")
		return nil
	}

	if pr, perr := vault.New(logger(false)).Identify(ctx, blob.ClaudeAIOAuth.AccessToken); perr == nil {
		fmt.Printf("  signed in as  %s (%s)\n", pr.Account.Email, pr.Account.Name)
		fmt.Printf("  account uuid  %s   <- this is the quota pool, not the org\n", pr.Account.UUID)
		fmt.Printf("  organization  %s  %s\n", pr.Organization.Name, shortID(pr.Organization.UUID))
	}
	fmt.Printf("  live credential belongs to organization %s\n", u.OrgID)
	if cachedOrgName != "" {
		fmt.Printf("  Claude Code shows  %s / %s\n", cachedEmail, cachedOrgName)
	}
	if cachedOrgID != "" && cachedOrgID != u.OrgID {
		fmt.Printf("\n  ⚠ those disagree. The credential is what counts; Claude Code's cached\n")
		fmt.Printf("    account block does not move when the credential is swapped.\n")
	}

	known := ""
	for _, c := range cfg.Accounts {
		if c.OrgID != "" && c.OrgID == u.OrgID {
			known = c.Name()
		}
	}
	fmt.Println()
	if known != "" {
		fmt.Printf("  → known to claudeswitch as %q.\n", known)
	} else {
		fmt.Printf("  → this organization is NOT in your config: a new account.\n")
		fmt.Printf("    Vault it with:  claudeswitch add <name>\n")
	}
	fmt.Println()
	return nil
}

// cachedIdentity reads what Claude Code believes about the signed-in account.
// It is a label, not a fact: the block is written at login and is not updated
// when the credential underneath it changes.
func cachedIdentity() (email, orgName, orgID string) {
	out, err := exec.Command("claude", "auth", "status").Output()
	if err != nil {
		return "", "", ""
	}
	var st struct {
		Email   string `json:"email"`
		OrgName string `json:"orgName"`
		OrgID   string `json:"orgId"`
	}
	if json.Unmarshal(out, &st) != nil {
		return "", "", ""
	}
	return st.Email, st.OrgName, st.OrgID
}

// cmdRefresh renews one vaulted account's credential.
//
// Deliberately explicit rather than automatic-only: the refresh path rotates and
// revokes a real credential, so its first exercise should be a decision someone
// made on purpose, on an account where failure is cheap.
func cmdRefresh(args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	allowActive := fs.Bool("allow-active", false,
		"permit refreshing the account currently in use (revokes the token your session is using)")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch refresh <account-id> [--allow-active]")
	}
	id := positional[0]

	cfg, _, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(false)
	v := vault.New(log)
	if !v.Has(id) {
		return fmt.Errorf("account %q is not in the vault", id)
	}
	// Ask the Keychain, not the state file. State can be empty or stale, and the
	// cost of being wrong here is revoking the token the running session uses.
	isActive := v.IsLive(id)

	if isActive && !*allowActive {
		fmt.Printf("\n  %q is the account currently in use.\n", id)
		fmt.Printf("  Refreshing revokes the token your running Claude Code session holds.\n")
		fmt.Printf("  claudeswitch would write the new token to both the vault and the live\n")
		fmt.Printf("  item, so in principle nothing breaks — but if that write fails you are\n")
		fmt.Printf("  logged out and need `claude auth login`.\n\n")
		fmt.Printf("  Pass --allow-active if that is what you want.\n\n")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	before, _ := v.Load(id)
	e, err := v.Refresh(ctx, id, cfg.SeatOf(id), isActive, *allowActive)
	if err != nil {
		var needsLogin *oauth.NeedsLoginError
		if errors.As(err, &needsLogin) {
			fmt.Printf("\n  ✗ %s cannot be refreshed.\n", id)
			fmt.Printf("    %v\n", err)
			fmt.Printf("\n    This is the answer to whether this account can be kept alive:\n")
			fmt.Printf("    it cannot, and it will need `claude auth login` each time its\n")
			fmt.Printf("    access token expires.\n\n")
			return nil
		}
		return err
	}

	fmt.Printf("\n  ✓ refreshed %s\n", id)
	fmt.Printf("    org           %s\n", e.OrgID)
	fmt.Printf("    new expiry    %s\n", humanUntil(e.Expiry))
	if before != nil {
		fmt.Printf("    access token  %s → %s\n",
			keychain.Redact(before.AccessToken), "(new, stored)")
		rotated := "unchanged"
		if after, err := v.Load(id); err == nil && after.RefreshToken != before.RefreshToken {
			rotated = "rotated and persisted"
		}
		fmt.Printf("    refresh token %s\n", rotated)
	}
	if isActive {
		fmt.Printf("    live item     updated, mcpOAuth preserved\n")
	}
	fmt.Println()
	return nil
}

// cmdRename moves a vaulted account to a new id.
//
// Account ids turn out to change often — a placeholder becomes a real name, or
// a name turns out to describe the wrong thing. Doing that by hand means
// copying a credential between Keychain items, which is exactly the kind of
// manual credential handling this tool exists to avoid.
func cmdRename(args []string) error {
	fs := flag.NewFlagSet("rename", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	positional := parseInterleaved(fs, args)
	if len(positional) != 2 {
		return fmt.Errorf("usage: claudeswitch rename <old-id> <new-id>")
	}
	oldID, newID := positional[0], positional[1]
	if oldID == newID {
		return fmt.Errorf("those are the same id")
	}

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	v := vault.New(logger(false))
	if !v.Has(oldID) {
		return fmt.Errorf("no vault entry for %q", oldID)
	}
	if v.Has(newID) {
		return fmt.Errorf("a vault entry for %q already exists; remove it first", newID)
	}
	if state.DaemonRunning() {
		return fmt.Errorf("stop the daemon before renaming (it owns the state file):\n" +
			"  launchctl unload ~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist")
	}

	// Copy the credential under the new name, keeping the organization
	// annotation so the identity guards keep working.
	blob, err := keychain.Read(keychain.VaultService(oldID))
	if err != nil {
		return err
	}
	meta := blob.Meta
	if meta == nil {
		meta = &keychain.Meta{}
	}
	meta.AccountID = newID
	moved := &keychain.Blob{ClaudeAIOAuth: blob.ClaudeAIOAuth, Meta: meta}
	if err := keychain.Write(keychain.VaultService(newID), moved); err != nil {
		return fmt.Errorf("could not create the new vault entry (the old one is untouched): %w", err)
	}
	// Write() already verified the read-back, so the new entry is good before
	// the old one goes.
	if err := keychain.Delete(keychain.VaultService(oldID)); err != nil {
		fmt.Fprintf(os.Stderr, "note: created %q but could not remove %q: %v\n",
			newID, oldID, err)
	}

	if a, ok := st.Accounts[oldID]; ok {
		a.ID = newID
		st.Accounts[newID] = a
		st.Drop(oldID)
	}
	if st.Active == oldID {
		// A rename does not change which account is live, so keep the original
		// timestamp rather than claiming a fresh observation.
		at := st.ActiveAt
		st.SetActive(newID)
		st.ActiveAt = at
	}
	if st.Pinned == oldID {
		st.Pinned = newID
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	fmt.Printf("\n  ✓ renamed %s → %s\n", oldID, newID)
	fmt.Printf("    credential moved, organization annotation kept (%s)\n", shortID(meta.OrgID))
	fmt.Printf("\n  now update %s: change the id (and any priority entry)\n", cfg.Path)
	fmt.Printf("    from  id = %q\n", oldID)
	fmt.Printf("    to    id = %q\n\n", newID)
	return nil
}

// cmdLogin signs in to an account and vaults it, end to end.
//
// The four-step dance — log in, check whoami, add, switch back — went wrong
// repeatedly on 2026-09-09: the browser decided which organization the login
// landed on, and running `add` before checking meant a stranger's credential
// was filed under a familiar name. This does the checking, and puts the
// previous account back afterwards.
func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	sso := fs.Bool("sso", false, "use the SSO login flow")
	direct := fs.Bool("direct", false,
		"obtain the credential ourselves, without touching the live session. Which "+
			"organization comes back is decided by the browser session, not by this flag")
	code := fs.String("code", "",
		"complete a --direct login started earlier, with the code from the callback page")
	pinOrg := fs.Bool("pin-org", false,
		"send organization_uuid in the authorize URL (accepted but ignored; for experiment only)")
	prompt := fs.String("prompt", "",
		"authorize prompt parameter: \"select_account\" to force the account chooser, \"login\" to force re-authentication")
	email := fs.String("email", "", "login_hint: the address of the account you want")
	browser := fs.String("browser", "",
		"open the authorize URL in this browser application, e.g. Safari or \"Brave Browser\". "+
			"A browser you are not normally signed into has its own cookie jar, which is what "+
			"gets you a different account")
	keep := fs.Bool("keep", false, "stay on the newly signed-in account instead of switching back")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch login <account-id> [--sso] [--keep]")
	}
	id := positional[0]

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := logger(false)
	v := vault.New(log)

	wantOrg := ""
	known := false
	for _, a := range cfg.Accounts {
		if a.ID == id {
			known = true
			// The seat — this person in this organization — is the quota pool.
			wantOrg = a.Seat()
		}
	}
	if !known {
		return fmt.Errorf("%q is not in %s; add an [[account]] block for it first", id, cfg.Path)
	}

	// Remember what to come back to. Only an account we can actually restore
	// counts — a vaulted credential, not merely whatever is live now.
	restoreTo := ""
	if st.Active != "" && st.Active != id && v.Has(st.Active) {
		restoreTo = st.Active
	}

	if *code != "" {
		return loginComplete(cfg, st, v, *code)
	}
	if *direct {
		return loginDirect(cfg, st, v, id, wantOrg, *pinOrg, oauth.Extra{Prompt: *prompt, LoginHint: *email}, *browser)
	}

	fmt.Println()
	if wantOrg != "" {
		fmt.Printf("  About to sign in and vault it as %q.\n\n", id)
		// A seat, not an organization: one person in one organization. Calling
		// it an organization made the refusal that follows read as nonsense,
		// since the same person in two organizations differs only in the half
		// the label denied was there.
		fmt.Printf("  That account is seat %s — one person in one organization,\n", shortSeat(wantOrg))
		fmt.Printf("  which is what owns a quota pool.\n")
		fmt.Printf("  The login lands on whichever organization your BROWSER is currently in\n")
		fmt.Printf("  and cannot be asked for one, so switch claude.ai to that organization\n")
		fmt.Printf("  first, or nothing will be stored. For an SSO-backed organization,\n")
		fmt.Printf("  `claudeswitch login %s --sso` is what reaches it.\n\n", id)
	}
	fmt.Printf("  Press enter to run the login, or Ctrl-C to stop: ")
	_, _ = fmt.Scanln()

	loginArgs := []string{"auth", "login"}
	if *sso {
		loginArgs = append(loginArgs, "--sso")
	}
	cmd := exec.Command("claude", loginArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("`claude auth login` did not complete: %w", err)
	}

	if email, orgName, _ := cachedIdentity(); orgName != "" {
		fmt.Printf("\n  signed in as  %s\n  organization  %s\n", email, orgName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	var configuredIDs []string
	for _, a := range cfg.Accounts {
		configuredIDs = append(configuredIDs, a.ID)
	}
	// Including what was vaulted outside the config — see cmdAdd.
	e, serr := v.Store(ctx, id, wantOrg, st.KnownAccounts(configuredIDs))
	if serr != nil {
		fmt.Printf("\n  ✗ not vaulted.\n     %v\n", serr)
		restoreActive(v, st, restoreTo)
		return nil
	}

	st.AddVaulted(id)
	acct := st.Get(id)
	acct.OrgID, acct.RefreshExpiry = e.OrgID, e.RefreshExpiry
	st.SetActive(id)
	fmt.Printf("\n  ✓ vaulted %s (org %s, %s)\n", id, shortID(e.OrgID), nonEmpty(e.Tier, "tier unknown"))
	if e.NoRefreshToken {
		fmt.Printf("    ⚠ no refresh token issued — this account cannot be renewed and will\n")
		fmt.Printf("      need signing in again when its access token expires %s\n", humanUntil(e.Expiry))
	}

	if !*keep && restoreTo != "" {
		restoreActive(v, st, restoreTo)
	} else if restoreTo != "" {
		fmt.Printf("    staying on %s (--keep); `claudeswitch use %s` switches back\n", id, restoreTo)
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	fmt.Println()
	return nil
}

// restoreActive puts the previously-live account back, so signing in to vault a
// second account does not silently leave the user working on it.
func restoreActive(v *vault.Vault, st *state.State, to string) {
	if to == "" {
		return
	}
	from := st.Active
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	expect := ""
	if a, ok := st.Accounts[to]; ok {
		expect = a.OrgID
	}
	if _, err := v.SwapTo(ctx, to, expect); err != nil {
		fmt.Printf("    ⚠ could not switch back to %s: %v\n", to, err)
		fmt.Printf("      run `claudeswitch use %s`\n", to)
		return
	}
	st.SetActive(to)
	if from != to {
		if aud, err := audit.Open(""); err == nil {
			_ = aud.Write(audit.Event{Kind: "switch", From: from, To: to,
				Reason: "restored after vaulting another account"})
		}
	}
	fmt.Printf("    switched back to %s\n", to)
}

// loginDirect runs the OAuth flow itself, without touching the live session.
//
// It cannot choose the organization. organization_uuid is accepted by the
// authorize endpoint and then ignored — measured, see docs/GROUND_TRUTH.md —
// so the credential that comes back belongs to whichever organization the
// browser session is signed into. The parameter is still sent because it costs
// nothing, but nothing may be promised on the strength of it, and what comes
// back is verified against the account's seat before being vaulted.
//
// Originally
// and vaults the result without ever installing it as the live credential.
//
// Delegating to `claude auth login` has two problems this avoids: it replaces
// the live credential as a side effect, and it gives you whichever organization
// the browser happens to be in — which on this machine was the same one three
// times running, no matter what the browser was showing.
func loginDirect(cfg *config.Config, st *state.State, v *vault.Vault, id, wantOrg string, pinOrg bool, extra oauth.Extra, browser string) error {
	// An unpinned account is fine here. --direct was originally about pinning the
	// organization, which turned out not to work at all; what it actually buys is
	// that the credential is obtained and vaulted WITHOUT the live one ever being
	// touched. That is worth having whether or not the organization is known in
	// advance, and it is how a brand-new account gets its org recorded.
	flow, err := oauth.BeginAuthWith(wantOrg, extra)
	if pinOrg {
		flow, err = oauth.BeginAuthPinned(wantOrg)
	}
	if err != nil {
		return err
	}
	if err := flow.Save(id, wantOrg, pinOrg); err != nil {
		return fmt.Errorf("could not remember this login attempt: %w", err)
	}

	if browser != "" {
		// Opening it ourselves removes the step that kept going wrong: pasting
		// the URL from an older attempt, whose state no longer matches.
		if err := exec.Command("open", "-a", browser, flow.URL).Run(); err != nil {
			fmt.Printf("\n  could not open %s (%v) — open this by hand:\n\n%s\n\n", browser, err, flow.URL)
		} else {
			fmt.Printf("\n  Opened in %s. Approve it there, then copy the code it gives you.\n\n", browser)
		}
	} else {
		fmt.Printf("\n  Open this, approve it, and copy the code it gives you:\n\n")
		fmt.Printf("%s\n\n", flow.URL)
	}
	switch {
	case wantOrg == "":
		fmt.Printf("  %q has no organization recorded yet, so whichever account your browser\n", id)
		fmt.Printf("  session is signed into will be vaulted under that name, and its\n")
		fmt.Printf("  organization recorded. An account already vaulted under another name is\n")
		fmt.Printf("  refused, so you cannot file the same account twice.\n")
	default:
		// Say this plainly. The help used to promise the organization was
		// pinned, which it never was: organization_uuid is accepted and then
		// ignored. Someone trusting that would sign in with whatever session
		// their browser happened to hold and believe they had captured a
		// different account.
		fmt.Printf("  This wants organization %s.\n", wantOrg)
		fmt.Printf("  The organization CANNOT be requested — the endpoint accepts the parameter\n")
		fmt.Printf("  and ignores it — so you get whichever account your browser session is\n")
		fmt.Printf("  signed into. Make sure that is the right one; use --browser to pick a\n")
		fmt.Printf("  browser signed in as a different account.\n")
		fmt.Printf("  What comes back is checked against the account's seat, and refused if it\n")
		fmt.Printf("  does not match, so a wrong session cannot be filed under this name.\n")
	}
	if extra.Prompt != "" || extra.LoginHint != "" {
		fmt.Printf("  Sending prompt=%s login_hint=%s — if the endpoint honours these you will\n",
			nonEmpty(extra.Prompt, "-"), nonEmpty(extra.LoginHint, "-"))
		fmt.Printf("  be asked which account to use instead of it silently reusing the session.\n")
	}
	fmt.Printf("  Then finish with:\n\n")
	fmt.Printf("    claudeswitch login %s --code <the-code>\n\n", id)
	fmt.Printf("  Your live session is not affected by any of this, whatever happens.\n")
	fmt.Printf("  The attempt expires at %s (in %s).\n\n",
		time.Now().Add(oauth.PendingTTL).Format("15:04"), oauth.PendingTTL)
	return nil
}

// loginComplete finishes a --direct login with the pasted code. It is a
// separate invocation because the paste cannot happen in a non-interactive
// shell, which is where this tool is usually driven from.
func loginComplete(cfg *config.Config, st *state.State, v *vault.Vault, code string) error {
	flow, id, wantOrg, pinned, err := oauth.LoadPending()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tok, err := oauth.NewClient().Exchange(ctx, flow, code)
	if err != nil {
		return fmt.Errorf("%w\n  (nothing was stored; your session is untouched)\n"+
			"  Note: the code must come from the most recent URL — an older one will not match.", err)
	}
	// The code is single-use, so the attempt is spent either way.
	_ = oauth.ClearPending()

	var others []string
	for _, a := range cfg.Accounts {
		others = append(others, a.ID)
	}
	e, err := v.StoreTokens(ctx, id, wantOrg, tok, others)
	if err != nil {
		var wrong *vault.WrongOrgError
		if errors.As(err, &wrong) {
			fmt.Printf("\n  ✗ that login came back as organization %s, not %s.\n",
				wrong.GotOrg, wrong.WantOrg)
			fmt.Printf("    Nothing was stored, and your session is untouched.\n\n")
			if pinned {
				fmt.Printf("    organization_uuid was sent and the request succeeded, so the\n")
				fmt.Printf("    parameter is accepted — and ignored. The browser session decides\n")
				fmt.Printf("    which organization a login returns, and nothing overrides it.\n\n")
			} else {
				fmt.Printf("    The browser session decided, as it does when the organization is\n")
				fmt.Printf("    not pinned. (--pin-org does not help: the parameter is accepted\n")
				fmt.Printf("    but has no effect.)\n\n")
			}
			return nil
		}
		return err
	}

	acct := st.Get(id)
	acct.OrgID, acct.RefreshExpiry = e.OrgID, e.RefreshExpiry
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	fmt.Printf("\n  ✓ vaulted %s (organization %s)\n", id, shortID(e.OrgID))
	fmt.Printf("    access token expires %s\n", humanUntil(e.Expiry))
	if e.NoRefreshToken {
		fmt.Printf("    ⚠ no refresh token issued — this account cannot be renewed\n")
	} else {
		fmt.Printf("    refresh token expires %s\n", humanUntil(e.RefreshExpiry))
	}
	fmt.Printf("    your live session was never touched\n\n")
	return nil
}

// cmdIdentify backfills seat identity onto vault entries that lack it.
//
// Entries written before 2026-09-10 recorded only an organization, which is not
// a quota pool: a team organization has one seat per member, each with separate
// limits. Until an entry knows its seat, SyncActive cannot safely tell "this
// account's token was refreshed" from "a colleague signed in", so it refuses to
// touch it. This asks each stored credential who it belongs to and writes that
// down — no logins required, since the credentials already work.
func cmdIdentify(args []string) error {
	fs := flag.NewFlagSet("identify", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	force := fs.Bool("force", false, "re-read every account even if already identified")
	positional := parseInterleaved(fs, args)

	cfg, _, err := load(*cfgPath)
	if err != nil {
		return err
	}
	v := vault.New(logger(false))

	var ids []string
	if len(positional) > 0 {
		ids = positional
	} else {
		for _, a := range cfg.Ordered() {
			if v.Has(a.ID) {
				ids = append(ids, a.ID)
			}
		}
	}

	fmt.Println()
	var deferred []string
	for _, id := range ids {
		// Re-record when anything we now store is missing, not only the seat:
		// entries written before a field existed would otherwise never gain it.
		seat, _ := v.IdentityOf(id)
		if seat != "" && v.PlanOf(id) != "" && !*force {
			fmt.Printf("  %-16s %s  %s\n", id, v.DescribeOf(id), v.PlanOf(id))
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pr, err := v.RecordIdentity(ctx, id)
		cancel()
		if err != nil {
			if _, rl := usage.IsRateLimited(err); rl {
				deferred = append(deferred, id)
				continue
			}
			fmt.Printf("  %-16s could not identify: %v\n", id, err)
			continue
		}
		fmt.Printf("  %-16s %s  %s  (seat %s)\n", id, pr.Account.Email, pr.Plan(), shortID(pr.Account.UUID))
	}
	if len(deferred) > 0 {
		fmt.Printf("\n  %v not done yet: the usage API call budget is spent.\n", deferred)
		fmt.Printf("  Run `claudeswitch identify` again in a few minutes.\n")
	}
	fmt.Println()
	return nil
}

// cmdRemove deletes an account's stored credential.
//
// Separate from `forget`, which only drops observations. This throws the
// credential away, which cannot be undone — the account has to be signed in to
// again — so it names what is being destroyed before doing it.
func cmdRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch remove <account-id>")
	}
	id := positional[0]

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if state.DaemonRunning() {
		return fmt.Errorf("stop the daemon before removing an account (it owns the state file):\n" +
			"  launchctl unload ~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist")
	}
	v := vault.New(logger(false))
	if !v.Has(id) {
		return fmt.Errorf("no vault entry for %q", id)
	}

	who := v.DescribeOf(id)
	_, org := v.IdentityOf(id)
	twin := otherHoldingSameCredential(cfg, st, id)
	if v.IsLive(id) && twin == "" {
		return fmt.Errorf("%q holds the credential Claude Code is using right now.\n"+
			"  Switch to another account first (`claudeswitch use <other>`), then remove it", id)
	}
	if v.IsLive(id) && twin != "" {
		// The one case where removing a live entry is safe, and the only way
		// out of the state `doctor` calls corrupted. Liveness is decided by
		// comparing access tokens, so two entries sharing one token are both
		// "live" at once: switching to the other cannot release this one,
		// because the other holds the identical token. Refusing here left the
		// duplicate impossible to delete by the command that deletes things.
		fmt.Printf("\n  note: %s holds this same credential, so deleting this copy leaves it\n", twin)
		fmt.Printf("        vaulted under that name. The live session is unaffected.\n")
	}

	if err := keychain.Delete(keychain.VaultService(id)); err != nil {
		return err
	}
	st.Drop(id)
	st.DropVaulted(id)
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	fmt.Printf("\n  ✓ removed %s", id)
	if who != "" {
		fmt.Printf(" — %s", who)
		if org != "" {
			fmt.Printf(" in organization %s", shortID(org))
		}
	}
	fmt.Printf("\n    the credential is gone; signing in to that account again is the only way back\n")
	for _, a := range cfg.Accounts {
		if a.ID == id {
			fmt.Printf("\n    %s still lists it — remove its [[account]] block and any priority entry\n", cfg.Path)
		}
	}
	fmt.Println()
	return nil
}

// otherHoldingSameCredential returns another vaulted id whose stored access
// token is byte-identical to this one's, or "" when this entry is the only copy.
//
// Two names for one credential is a corrupt state rather than a supported one —
// both report the same utilization, so rotating between them does nothing, and
// refreshing one revokes the other. But while it exists it has to be
// repairable, and repair means deleting one of them.
func otherHoldingSameCredential(cfg *config.Config, st *state.State, id string) string {
	var configured []string
	if cfg != nil {
		for _, a := range cfg.Accounts {
			configured = append(configured, a.ID)
		}
	}
	ids := configured
	if st != nil {
		ids = st.KnownAccounts(configured)
	}
	return findTwin(id, ids, vaultedToken)
}

// vaultedToken is the stored access token for an id, empty when there is none.
func vaultedToken(id string) string {
	b, err := keychain.Read(keychain.VaultService(id))
	if err != nil || b.ClaudeAIOAuth == nil {
		return ""
	}
	return b.ClaudeAIOAuth.AccessToken
}

// findTwin is the comparison on its own, with the keychain passed in, so the
// rule can be tested without one.
func findTwin(id string, ids []string, tokenOf func(string) string) string {
	mine := tokenOf(id)
	if mine == "" {
		return ""
	}
	for _, other := range ids {
		if other == id {
			continue
		}
		if tokenOf(other) == mine {
			return other
		}
	}
	return ""
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// cmdSession reports a stretch of work across whichever accounts served it.
//
// Rotation makes this the only place the question can be answered: each account
// knows its own usage, nobody owns the total, and `/usage` only ever shows the
// one you happen to be signed into.
func cmdSession(args []string) error {
	fs := flag.NewFlagSet("session", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	since := fs.Duration("since", 0, "how far back to look (default: since the first switch today, else 8h)")
	full := fs.Bool("detail", false, "per-account token breakdown and models")
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseInterleaved(fs, args)

	_, st, err := load(*cfgPath)
	if err != nil {
		return err
	}

	to := time.Now()
	from := to.Add(-8 * time.Hour)
	switch {
	case *since > 0:
		from = to.Add(-*since)
	case !st.LastSwitch.IsZero() && st.LastSwitch.After(to.Add(-24*time.Hour)):
		// Default to something meaningful: the span the current rotation covers,
		// widened to catch the work that led up to it.
		if c := st.LastSwitch.Add(-8 * time.Hour); c.After(from) {
			from = c
		}
	}

	s, err := session.Build("", from, to, st.Active)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	if *asJSON {
		shares := make([]map[string]any, 0, len(s.Shares))
		for _, sh := range s.Shares {
			shares = append(shares, map[string]any{
				"account": sh.Account, "tokens": sh.Tokens.Total(),
				"messages": sh.Messages, "active_seconds": int(sh.Active.Seconds()),
				"input": sh.Tokens.Input, "output": sh.Tokens.Output,
				"cache_read": sh.Tokens.CacheRead, "cache_write": sh.Tokens.CacheCreation,
			})
		}
		return emitJSON(map[string]any{
			"from": s.From, "to": s.To, "accounts": shares,
			"total_tokens": s.Total.Total(), "messages": s.Messages,
			"switches": len(s.Switches),
		})
	}

	fmt.Printf("\n  %s → %s  (%s)\n\n",
		s.From.Local().Format("Mon 15:04"), s.To.Local().Format("15:04"),
		render.Duration(s.Span()))

	if s.Messages == 0 {
		fmt.Printf("  no recorded usage in that span\n\n")
		return nil
	}

	total := s.Total.Total()
	t := render.NewTable([]string{"ACCOUNT", "TOKENS", "SHARE", "MESSAGES", "ACTIVE"}, 1, 2, 3, 4)
	for _, sh := range s.Shares {
		share := 0.0
		if total > 0 {
			share = 100 * float64(sh.Tokens.Total()) / float64(total)
		}
		t.Add(sh.Account, humanTokens(sh.Tokens.Total()),
			fmt.Sprintf("%.0f%%", share), fmt.Sprintf("%d", sh.Messages),
			render.Duration(sh.Active))
		if *full {
			tk := sh.Tokens
			t.Add("", render.Grey(fmt.Sprintf("in %s · out %s · cache %s/%s",
				humanTokens(tk.Input), humanTokens(tk.Output),
				humanTokens(tk.CacheRead), humanTokens(tk.CacheCreation))), "", "", "")
			for m, n := range sh.Models {
				t.Add("", render.Grey(fmt.Sprintf("%s × %d", m, n)), "", "", "")
			}
		}
	}
	if s.UnattrCount > 0 {
		t.Add(render.Dim("(unattributed)"), humanTokens(s.Unattributed.Total()), "",
			fmt.Sprintf("%d", s.UnattrCount), "")
	}
	t.Add(render.Bold("total"), render.Bold(humanTokens(total)), "",
		render.Bold(fmt.Sprintf("%d", s.Messages)), render.Duration(s.Span()))
	fmt.Print(t.Render("  "))
	fmt.Printf("  %s\n", render.Grey(fmt.Sprintf("out %s · thinking %s",
		humanTokens(s.Total.Output), humanTokens(s.Total.Thinking))))

	if len(s.Switches) > 0 {
		fmt.Printf("\n  %s\n", render.Dim(fmt.Sprintf("%d SWITCH(ES) IN THIS SPAN", len(s.Switches))))
		sw := render.NewTable(nil)
		for i := len(s.Switches) - 1; i >= 0; i-- {
			e := s.Switches[i]
			sw.Add(render.Grey(e.At.Local().Format("15:04")),
				nonEmpty(e.From, "?")+" → "+render.Bold(e.To),
				render.Grey(truncateStr(e.Reason, 42)))
		}
		fmt.Print(sw.Render("  "))
	} else {
		fmt.Printf("\n  %s\n", render.Grey("no switches in this span"))
	}
	fmt.Println()
	return nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// probeDue reports whether an idle account is overdue a liveness probe. The
// vault records when each entry was last written, which is also when it was last
// refreshed.
func probeDue(v *vault.Vault, accountID string, every time.Duration) bool {
	last := v.VaultedAt(accountID)
	if last.IsZero() {
		return true // never recorded: probe once and find out
	}
	return time.Since(last) >= every
}

// durOrNever renders a duration, or nothing when it is disabled.
func durOrNever(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// ---- cs setup ----------------------------------------------------------
//
// Onboarding is the hardest part of this tool by a distance. Vaulting four
// accounts took roughly ten attempts the first time, almost all of them lost to
// one fact: a login returns whichever account the BROWSER SESSION holds, and
// nothing in the request can steer it. Someone meeting this cold would give up.
//
// So setup does the whole thing: finds what is already signed in, walks each
// additional account through a browser that will actually produce a different
// one, writes a config with the real seats pinned — which cannot be written by
// hand, since a seat uuid is only knowable after signing in — and offers to
// install the daemon.

func ask(prompt, def string) string {
	fmt.Print(prompt)
	if def != "" {
		fmt.Printf(" [%s]", def)
	}
	fmt.Print(": ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askYes(prompt string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	a := strings.ToLower(ask(prompt+" ("+d+")", ""))
	if a == "" {
		return def
	}
	return a == "y" || a == "yes"
}

// browsers lists installed applications, so setup can suggest one the user is
// probably not signed into rather than describing the idea abstractly.
func browsers() []string {
	var out []string
	for _, b := range []string{"Safari", "Google Chrome", "Brave Browser", "Firefox", "Microsoft Edge", "Arc"} {
		if _, err := os.Stat("/Applications/" + b + ".app"); err == nil {
			out = append(out, b)
		}
	}
	return out
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "where to write the config")
	parseInterleaved(fs, args)

	// Terminal first: piping into setup is the more fundamental mistake, and
	// saying "stop the daemon" to someone who cannot answer prompts is noise.
	if !isTerminal() {
		return fmt.Errorf("setup asks questions, so it needs a terminal.\n" +
			"  Run it directly:  cs setup\n" +
			"  (from Claude Code, run it in a terminal rather than with `!`)")
	}
	if state.DaemonRunning() {
		return fmt.Errorf("stop the daemon first — it owns the state file:\n" +
			"  launchctl unload ~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist")
	}

	log := logger(false)
	v := vault.New(log)
	st, _ := state.Load("")

	fmt.Printf("\n  claudeswitch setup\n")
	fmt.Printf("  ──────────────────\n\n")
	fmt.Printf("  This vaults each Claude account you want to rotate between, then writes\n")
	fmt.Printf("  a config and offers to install the daemon. Your current login is never\n")
	fmt.Printf("  disturbed — credentials are fetched directly, not by signing you in and out.\n\n")

	var added []vaultedAccount
	seats := map[string]string{}

	// Anything already vaulted counts: setup can be re-run without starting over.
	existingCfg, _ := config.Load(*cfgPath)
	for _, a := range existingCfg.Ordered() {
		if !v.Has(a.ID) {
			continue
		}
		seat, org := v.IdentityOf(a.ID)
		if seat == "" {
			continue
		}
		seats[seat] = a.ID
		added = append(added, vaultedAccount{a.ID, v.DescribeOf(a.ID), v.PlanOf(a.ID), seat, org, ""})
		fmt.Printf("  already vaulted: %-16s %s  %s\n", a.ID, v.DescribeOf(a.ID), v.PlanOf(a.ID))
	}
	if len(added) > 0 {
		fmt.Println()
	}

	// 1. The account signed in right now, which needs no browser work at all.
	ctx := context.Background()
	if blob, err := keychain.ReadLive(); err == nil {
		ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
		pr, perr := v.Identify(ictx, blob.ClaudeAIOAuth.AccessToken)
		cancel()
		if perr == nil {
			if existing, dup := seats[pr.Seat()]; dup {
				fmt.Printf("  Signed in now: %s (%s) — already vaulted as %q.\n\n",
					pr.Account.Email, pr.Plan(), existing)
			} else {
				fmt.Printf("  Signed in now: %s · %s · %s\n", pr.Account.Email,
					pr.Organization.Name, pr.Plan())
				if askYes("  Vault it", true) {
					id := ask("  Name it", suggestName(pr, seats))
					e, serr := v.Store(ctx, id, "", idsOf(added))
					if serr != nil {
						fmt.Printf("    ✗ %v\n\n", serr)
					} else {
						seats[pr.Seat()] = id
						added = append(added, vaultedAccount{id, pr.Account.Email, pr.Plan(),
							pr.Seat(), e.OrgID, pr.Organization.Name})
						fmt.Printf("    ✓ vaulted %s\n\n", id)
					}
				} else {
					fmt.Println()
				}
			}
		}
	}

	// 2. Further accounts, each needing a browser session that is not the
	//    current one.
	bl := browsers()
	for {
		if !askYes(fmt.Sprintf("  Add another account (%d vaulted so far)", len(added)), len(added) < 2) {
			break
		}
		id := ask("  Name it", "")
		if id == "" {
			continue
		}
		if v.Has(id) {
			fmt.Printf("    %q is already vaulted; pick another name\n\n", id)
			continue
		}

		browser := ""
		if len(bl) > 0 {
			fmt.Printf("\n  A login returns whichever account your BROWSER is signed into, and\n")
			fmt.Printf("  nothing in the request can override that. So use a browser you are NOT\n")
			fmt.Printf("  normally signed into — separate applications keep separate cookies.\n")
			fmt.Printf("  Installed: %s\n", strings.Join(bl, ", "))
			browser = ask("  Open in which browser (blank to print the URL)", bl[0])
		}

		flow, ferr := oauth.BeginAuthWith("", oauth.Extra{Prompt: "login"})
		if ferr != nil {
			return ferr
		}
		if browser != "" {
			if err := exec.Command("open", "-a", browser, flow.URL).Run(); err != nil {
				fmt.Printf("    could not open %s: %v\n    %s\n", browser, err, flow.URL)
			} else {
				fmt.Printf("    opened in %s — sign in as the account you want, approve it\n", browser)
			}
		} else {
			fmt.Printf("\n%s\n\n", flow.URL)
		}

		code := ask("  Paste the code", "")
		if code == "" {
			fmt.Printf("    skipped; nothing stored\n\n")
			continue
		}
		xctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		tok, xerr := oauth.NewClient().Exchange(xctx, flow, code)
		cancel()
		if xerr != nil {
			fmt.Printf("    ✗ %v\n\n", xerr)
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		e, serr := v.StoreTokens(sctx, id, "", tok, idsOf(added))
		cancel()
		if serr != nil {
			fmt.Printf("    ✗ %v\n\n", serr)
			continue
		}
		seat, org := v.IdentityOf(id)
		seats[seat] = id
		added = append(added, vaultedAccount{id, v.DescribeOf(id), v.PlanOf(id), seat, org, ""})
		fmt.Printf("    ✓ vaulted %s — %s  %s\n\n", id, v.DescribeOf(id), nonEmpty(v.PlanOf(id), ""))
		_ = e
	}

	if len(added) == 0 {
		fmt.Printf("\n  Nothing vaulted, so there is nothing to configure.\n\n")
		return nil
	}

	// 3. The config, with every seat pinned to what was actually vaulted.
	cfg := &config.Config{
		SwitchAt: config.DefaultSwitchAt, HardFloor: config.DefaultHardFloor,
		SwitchWhen:    "idle",
		Cooldown:      config.Duration{Duration: 10 * time.Minute},
		MaxSwitchWait: config.Duration{Duration: 90 * time.Second},
		RefreshWindow: config.Duration{Duration: time.Hour},
		RefreshProbe:  config.Duration{Duration: 24 * time.Hour},
	}
	fmt.Printf("\n  Rotation order — earlier accounts are spent first.\n")
	for i, a := range added {
		fmt.Printf("    %d. %-16s %s  %s\n", i+1, a.id, a.email, a.plan)
	}
	order := ask("  Order (ids, comma separated)", strings.Join(idsOf(added), ","))
	for _, id := range strings.Split(order, ",") {
		id = strings.TrimSpace(id)
		for _, a := range added {
			if a.id != id {
				continue
			}
			acct := config.Account{ID: a.id, AccountUUID: strings.SplitN(a.seat, "@", 2)[0], OrgID: a.org}
			if a.email != "" {
				acct.Comment = a.email
				if a.plan != "" {
					acct.Comment += " · " + a.plan
				}
			}
			cfg.Priority = append(cfg.Priority, a.id)
			cfg.Accounts = append(cfg.Accounts, acct)
		}
	}
	if len(cfg.Accounts) == 0 {
		return fmt.Errorf("no valid ids in %q", order)
	}

	if last := &cfg.Accounts[len(cfg.Accounts)-1]; askYes(
		fmt.Sprintf("\n  Hold %q back as a reserve, never auto-spent past 70%%", last.ID), true) {
		last.Reserve = 70
		last.Scope = "personal"
	}

	if err := cfg.Write(*cfgPath); err != nil {
		return err
	}
	fmt.Printf("\n  ✓ wrote %s\n", *cfgPath)

	st.Reconcile(pinnedOf(cfg))
	_ = st.Save()

	fmt.Printf("\n  The daemon watches usage and rotates before you hit a wall. It starts in\n")
	fmt.Printf("  dry-run: it reports the swap it would make and changes nothing, so you can\n")
	fmt.Printf("  check its judgement first.\n")
	if askYes("  Install and start it now", true) {
		cmd := exec.Command("./install.sh")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("    could not run ./install.sh (%v)\n", err)
			fmt.Printf("    run it by hand from the claudeswitch directory\n")
		}
	} else {
		fmt.Printf("\n  When you are ready:  ./install.sh          (dry-run)\n")
		fmt.Printf("                       ./install.sh --live   (acts)\n")
	}

	fmt.Printf("\n  Next:  cs status     what every account has left\n")
	fmt.Printf("         cs plan       what the daemon would do right now\n")
	fmt.Printf("         cs session    usage across every account in a span of work\n\n")
	return nil
}

func pinnedOf(c *config.Config) map[string]string {
	m := map[string]string{}
	for _, a := range c.Accounts {
		m[a.ID] = a.Seat()
	}
	return m
}

// vaultedAccount is what setup has stored so far.
type vaultedAccount struct {
	id, email, plan, seat, org, orgName string
}

func idsOf(items []vaultedAccount) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}

// suggestName proposes an id from the account itself, so the common case is a
// keypress rather than a decision.
func suggestName(pr *usage.Profile, taken map[string]string) string {
	base := strings.ToLower(pr.Organization.Name)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '-', r == '_':
			return '-'
		}
		return -1
	}, base)
	base = strings.Trim(base, "-")
	if base == "" || strings.Contains(base, "@") {
		base = strings.SplitN(pr.Account.Email, "@", 2)[0]
	}
	for _, id := range taken {
		if id == base {
			return base + "-2"
		}
	}
	return base
}

// isTerminal reports whether we can prompt: stdin must be a terminal, and not
// merely a character device — /dev/null is one, which let `cs top` draw escape
// codes into a pipe.
func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return isTTY(os.Stdin)
}

// canDraw reports whether the OUTPUT is a terminal, which is what a full-screen
// view actually needs.
func canDraw() bool { return isTTY(os.Stdout) }

// isTTY asks for the window size. TIOCGWINSZ is the one terminal ioctl spelled
// the same on Darwin and Linux; the termios constants are not.
func isTTY(f *os.File) bool {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return err == nil && ws != nil
}

// currentDir is where the CLI is being run, which is the directory a project
// rule should be judged against for `cs plan`. The daemon uses the session's
// working directory from the transcripts instead, since it is not run from
// anywhere in particular.
func currentDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// staleDecisionAfter is when a reading is too old to decide on: three poll
// intervals, so a single missed poll is unremarkable but a pattern is not.
func staleDecisionAfter(cfg *config.Config) time.Duration {
	iv := cfg.PollActive.Duration
	if iv <= 0 {
		iv = 4 * time.Minute
	}
	return 3 * iv
}

// recentSwitches returns the last n rotations, oldest first, for the status
// view. Reading them from the audit log rather than tracking them separately
// keeps one record of what happened.
func recentSwitches(n int) []audit.Event {
	events, err := audit.Tail("", 20000)
	if err != nil {
		return nil
	}
	var out []audit.Event
	for i := len(events) - 1; i >= 0 && len(out) < n; i-- {
		if events[i].Kind == "switch" {
			out = append([]audit.Event{events[i]}, out...)
		}
	}
	return out
}

// knownAccounts is the set of ids still configured, so history can mark the ones
// that have since been renamed or removed.
func knownAccounts(cfg *config.Config) map[string]bool {
	m := map[string]bool{}
	for _, a := range cfg.Accounts {
		m[a.ID] = true
	}
	return m
}

// blindLimit is how long the daemon may go without a successful reading before
// treating itself as wedged. Ten poll intervals: long enough that ordinary
// timeouts and rate-limit backoffs pass without a restart, short enough that a
// genuinely stuck daemon is replaced within minutes rather than hours.
func blindLimit(cfg *config.Config) time.Duration {
	iv := cfg.PollActive.Duration
	if iv <= 0 {
		iv = time.Minute
	}
	if d := 10 * iv; d > 2*time.Minute {
		return d
	}
	return 2 * time.Minute
}

// readingsStale reports whether the active account's figure is old enough that
// the daemon cannot be keeping up.
func readingsStale(cfg *config.Config, st *state.State, limit time.Duration) bool {
	a, ok := st.Accounts[st.Active]
	if !ok || a == nil || a.Last == nil {
		return true
	}
	return time.Since(a.LastAt) > limit
}

// lookahead is how far ahead the policy engine should look when deciding.
//
// Deciding on where an account is *now* is always late: the reading is already a
// lower bound, the next poll is up to one interval away, and the swap may then
// wait for an idle gap. Covering all three means the decision is taken while
// there is still room below the trigger, which is what a threshold under 100 is
// for.
func lookahead(cfg *config.Config) time.Duration {
	poll := cfg.PollHot.Duration
	if poll <= 0 {
		poll = 30 * time.Second
	}
	return poll + cfg.MaxSwitchWait.Duration
}

// cmdWhy explains the current decision account by account.
//
// "Why did it not switch?" has been the question behind every problem worth
// debugging here, and answering it has meant reading the state file and the
// audit log by hand. This is that, built in.
func cmdWhy(args []string) error {
	fs := flag.NewFlagSet("why", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	dir := currentDir()
	dec, verdicts := policy.Explain(policy.Input{
		Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch,
		Pinned: st.Pinned, Dir: dir, Lookahead: lookahead(cfg),
	})

	if *asJSON {
		return emitJSON(map[string]any{
			"decision": decisionJSON(dec),
			"accounts": verdictsJSON(verdicts),
			"dir":      dir,
		})
	}
	render.Why(os.Stdout, render.WhyOptions{
		Cfg: cfg, St: st, Decision: dec, Verdicts: verdicts, Dir: dir,
	})
	return nil
}

func decisionJSON(d policy.Decision) map[string]any {
	m := map[string]any{"kind": string(d.Kind), "reason": d.Reason}
	if d.Target != "" {
		m["target"] = d.Target
	}
	if d.Forced {
		m["forced"] = true
	}
	if !d.RecoversAt.IsZero() {
		m["recovers_account"] = d.RecoversAccount
		m["recovers_at"] = d.RecoversAt
	}
	return m
}

func verdictsJSON(vs []policy.Verdict) []map[string]any {
	out := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		m := map[string]any{
			"id": v.ID, "eligible": v.Eligible, "why": v.Why,
			"utilization": v.Worst, "window": v.Window,
		}
		if v.Active {
			m["active"] = true
		}
		if !v.ClearsAt.IsZero() {
			m["clears_at"] = v.ClearsAt
		}
		out = append(out, m)
	}
	return out
}

// emitJSON writes a value as indented JSON. Every read command offers it, so a
// status line, an alert or a dashboard need not parse text meant for people.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// statusJSON is the machine-readable form of what `cs status` shows. Keeping the
// shape flat and named makes it usable from jq without a manual.
func statusJSON(cfg *config.Config, st *state.State, p *poller.Poller) map[string]any {
	accounts := make([]map[string]any, 0, len(cfg.Accounts))
	v := vault.New(logger(false))
	for _, a := range cfg.Ordered() {
		m := map[string]any{
			"id": a.ID, "scope": a.Scope, "vaulted": v.Has(a.ID),
			"active": a.ID == st.Active,
		}
		if plan := v.PlanOf(a.ID); plan != "" {
			m["plan"] = plan
		}
		if email := v.DescribeOf(a.ID); email != "" {
			m["email"] = email
		}
		acct := st.Accounts[a.ID]
		if acct != nil && acct.Last != nil {
			which, worst := acct.Last.Worst()
			m["five_hour"] = acct.Last.FiveHour.Pct()
			m["seven_day"] = acct.Last.SevenDay.Pct()
			m["binding_window"] = which
			m["utilization"] = worst
			m["projected"] = acct.Projected(time.Now())
			m["read_at"] = acct.LastAt
			// Apply the trigger here too. "available" beside 100% is true of the
			// raw availability check and useless to a reader or a script: what
			// is being asked is whether this account could actually serve.
			st := string(acct.Availability(a.Reserve))
			if st == "available" && acct.Projected(time.Now()) >= cfg.SwitchAt {
				st = "no_headroom"
			}
			if acct.ExpiredAt(time.Now()) {
				st = "window_reset"
			}
			m["state"] = st
			m["usable"] = st == "available"
			if rate := acct.BurnRate(); rate > 0 {
				m["burn_per_min"] = rate
			}
			if b := acct.Last.Binding(); b != nil {
				m["severity"] = b.Severity
				if b.ResetsAt != nil {
					m["clears_at"] = *b.ResetsAt
				}
			}
		} else {
			m["state"] = "unknown"
			m["usable"] = false
		}
		accounts = append(accounts, m)
	}
	degraded, why := p.Degraded()
	out := map[string]any{
		"active":   st.Active,
		"accounts": accounts,
		"thresholds": map[string]any{
			"switch_at": cfg.SwitchAt, "hard_floor": cfg.HardFloor,
			"switch_when": cfg.SwitchWhen, "max_switch_wait": cfg.MaxSwitchWait.String(),
		},
		"daemon_running":  state.DaemonRunning(),
		"api_calls_spare": p.Budget().Remaining(),
	}
	if degraded {
		out["degraded"] = why
	}
	if !st.LastSwitch.IsZero() {
		out["last_switch"] = st.LastSwitch
	}
	return out
}

// settings are the tunable values, described once so `cs config` can list them,
// set them and explain them without a second copy drifting out of step.
type setting struct {
	name string
	get  func(*config.Config) string
	set  func(*config.Config, string) error
	help string
}

func durSetter(f func(*config.Config) *config.Duration) func(*config.Config, string) error {
	return func(c *config.Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%q is not a duration (try 90s, 5m, 2h)", v)
		}
		*f(c) = config.Duration{Duration: d}
		return nil
	}
}

func pctSetter(f func(*config.Config) *float64) func(*config.Config, string) error {
	return func(c *config.Config, v string) error {
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err != nil {
			return fmt.Errorf("%q is not a percentage", v)
		}
		*f(c) = n
		return nil
	}
}

func settings() []setting {
	return []setting{
		{"switch_at", func(c *config.Config) string { return fmt.Sprintf("%g", c.SwitchAt) },
			pctSetter(func(c *config.Config) *float64 { return &c.SwitchAt }),
			"rotate away at this much of the 5-hour window"},
		{"switch_at_weekly", func(c *config.Config) string { return fmt.Sprintf("%g", c.SwitchAtWeekly) },
			pctSetter(func(c *config.Config) *float64 { return &c.SwitchAtWeekly }),
			"...and at this much of the weekly one"},
		{"hard_floor", func(c *config.Config) string { return fmt.Sprintf("%g", c.HardFloor) },
			pctSetter(func(c *config.Config) *float64 { return &c.HardFloor }),
			"above this, swap mid-turn rather than wait for an idle gap"},
		{"switch_when", func(c *config.Config) string { return c.SwitchWhen },
			func(c *config.Config, v string) error { c.SwitchWhen = v; return nil },
			`"idle" to prefer swapping between turns, or "immediate"`},
		{"max_switch_wait", func(c *config.Config) string { return c.MaxSwitchWait.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.MaxSwitchWait }),
			"stop waiting for an idle gap after this"},
		{"cooldown", func(c *config.Config) string { return c.Cooldown.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.Cooldown }),
			"minimum gap between rotations, to stop flapping"},
		{"poll_active", func(c *config.Config) string { return c.PollActive.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.PollActive }),
			"how often to read the account in use"},
		{"poll_hot", func(c *config.Config) string { return c.PollHot.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.PollHot }),
			"...and when it is near the trigger or burning fast"},
		{"poll_idle", func(c *config.Config) string { return c.PollIdle.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.PollIdle }),
			"how often to read the others"},
		{"api_budget", func(c *config.Config) string { return strconv.Itoa(c.APIBudget) },
			func(c *config.Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("%q is not a number", v)
				}
				c.APIBudget = n
				return nil
			}, "usage calls per 5 minutes, across every process"},
		{"refresh_window", func(c *config.Config) string { return c.RefreshWindow.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.RefreshWindow }),
			"renew a credential this long before it expires"},
		{"refresh_probe", func(c *config.Config) string { return c.RefreshProbe.String() },
			durSetter(func(c *config.Config) *config.Duration { return &c.RefreshProbe }),
			"also renew idle accounts this often, to catch a dead refresh token"},
	}
}

// cmdConfig shows the settings in force, or changes one.
//
// There are a dozen knobs now, and editing TOML by hand to change one means
// finding the file, knowing the key, and getting no validation until the daemon
// next starts. This reads them back, writes one, and refuses anything the
// validator would reject.
func cmdConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional := parseInterleaved(fs, args)

	cfg, err := config.Load(*cfgPath)
	if err != nil && cfg == nil {
		return err
	}
	all := settings()

	if len(positional) == 0 {
		if *asJSON {
			m := map[string]any{"path": cfg.Path}
			for _, s := range all {
				m[s.name] = s.get(cfg)
			}
			return emitJSON(m)
		}
		fmt.Printf("\n  %s\n\n", cfg.Path)
		t := make([][3]string, 0, len(all))
		w := 0
		for _, s := range all {
			if len(s.name) > w {
				w = len(s.name)
			}
			t = append(t, [3]string{s.name, s.get(cfg), s.help})
		}
		for _, r := range t {
			fmt.Printf("  %-*s  %-8s  %s\n", w, r[0], r[1], r[2])
		}
		fmt.Printf("\n  change one with:  cs config <name> <value>\n\n")
		return nil
	}

	name := positional[0]
	var target *setting
	for i := range all {
		if all[i].name == name {
			target = &all[i]
		}
	}
	if target == nil {
		return fmt.Errorf("no setting called %q. `cs config` lists them", name)
	}
	if len(positional) == 1 {
		fmt.Println(target.get(cfg))
		return nil
	}

	was := target.get(cfg)
	if err := target.set(cfg, positional[1]); err != nil {
		return err
	}
	// Validate before writing, so a bad value is refused rather than stored and
	// discovered when the daemon next fails to start.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%s would make the config invalid:\n  %w", name, err)
	}
	if err := cfg.Write(cfg.Path); err != nil {
		return err
	}
	fmt.Printf("\n  %s  %s → %s\n", name, was, target.get(cfg))
	fmt.Printf("  written to %s\n", cfg.Path)
	if state.DaemonRunning() {
		fmt.Printf("  restart the daemon to apply it:  ./install.sh --live\n")
	}
	fmt.Println()
	return nil
}

func countVaulted(cfg *config.Config) int {
	v := vault.New(logger(false))
	n := 0
	for _, a := range cfg.Accounts {
		if v.Has(a.ID) {
			n++
		}
	}
	return n
}

// cmdTop is `cs status` that redraws itself.
//
// Deliberately not a framework: it reads the same state the daemon writes and
// re-renders, so there is one source of truth and one layout to maintain. It
// makes no API calls of its own — the daemon is already polling, and a second
// poller competing for the same rate budget is how this program got into
// trouble before.
func cmdTop(args []string) error {
	fs := flag.NewFlagSet("top", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	every := fs.Duration("every", 2*time.Second, "how often to redraw")
	parseInterleaved(fs, args)

	if !canDraw() {
		return fmt.Errorf("`cs top` draws to a terminal; use `cs status` when piping")
	}
	if *every < time.Second {
		*every = time.Second
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	// Alternate screen, hidden cursor; both restored on the way out however we
	// leave, including a signal.
	fmt.Print("\033[?1049h\033[?25l")
	restore := func() { fmt.Print("\033[?25h\033[?1049l") }
	defer restore()

	tick := time.NewTicker(*every)
	defer tick.Stop()

	draw := func() {
		cfg, st, err := load(*cfgPath)
		if err != nil {
			return
		}
		var buf strings.Builder
		p := poller.New(cfg, st, logger(false))
		dec := policy.Decide(policy.Input{
			Cfg: cfg, St: st, Now: time.Now(), LastSwitch: st.LastSwitch,
			Pinned: st.Pinned, Dir: currentDir(), Lookahead: lookahead(cfg),
		})
		vlt := vault.New(logger(false))
		vaulted, plans := map[string]bool{}, map[string]string{}
		for _, a := range cfg.Accounts {
			vaulted[a.ID], plans[a.ID] = vlt.Has(a.ID), vlt.PlanOf(a.ID)
		}
		degraded, why := p.Degraded()
		render.Status(&buf, render.Options{
			Cfg: cfg, St: st, Budget: p.Budget(), Degraded: degraded, DegradedWhy: why,
			DaemonOwns: state.DaemonRunning(), Vaulted: vaulted, Plans: plans,
			Switches: recentSwitches(5), Known: knownAccounts(cfg), Decision: &dec,
		})
		// Home the cursor and clear as we go, rather than clearing first: a
		// clear-then-draw flickers.
		fmt.Print("\033[H\033[J")
		fmt.Print(buf.String())
		fmt.Printf("  %s\n", render.Grey(fmt.Sprintf(
			"refreshed %s · every %s · ctrl-c to leave",
			time.Now().Format("15:04:05"), *every)))
	}

	draw()
	for {
		select {
		case <-sig:
			restore()
			return nil
		case <-tick.C:
			draw()
		}
	}
}

// cmdUninstall removes what claudeswitch put on the machine.
//
// It leaves the vaulted credentials alone unless asked: they are the part that
// took effort to obtain, and someone stopping the daemon usually wants it
// stopped rather than their logins thrown away. It also never touches Claude
// Code's own credential.
func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	creds := fs.Bool("credentials", false, "also delete the vaulted credentials")
	yes := fs.Bool("yes", false, "do not ask")
	parseInterleaved(fs, args)

	cfg, _, _ := load(*cfgPath)
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "xyz.claudeswitch.daemon.plist")
	unit := filepath.Join(home, ".config", "systemd", "user", "claudeswitch.service")
	stateDir := filepath.Join(home, ".local", "state", "claudeswitch")

	fmt.Printf("\n  This will remove:\n")
	fmt.Printf("    · the daemon service and its logs\n")
	fmt.Printf("    · %s\n", stateDir)
	if *creds {
		fmt.Printf("    · %s\n", render.Bad("every vaulted credential — you would have to sign in again"))
	} else {
		fmt.Printf("  Keeping:\n")
		fmt.Printf("    · your vaulted credentials (pass --credentials to remove them too)\n")
		fmt.Printf("    · your config at %s\n", cfg.Path)
		fmt.Printf("    · Claude Code's own credential, always\n")
	}
	if !*yes {
		if !isTerminal() {
			return fmt.Errorf("pass --yes to uninstall without being asked")
		}
		if !askYes("\n  Go ahead", false) {
			fmt.Printf("  nothing removed\n\n")
			return nil
		}
	}

	// Service first, so nothing is running while the rest goes.
	if runtime.GOOS == "darwin" {
		_ = exec.Command("launchctl", "unload", plist).Run()
		if err := os.Remove(plist); err == nil {
			fmt.Printf("  removed %s\n", plist)
		}
	} else {
		_ = exec.Command("systemctl", "--user", "disable", "--now", "claudeswitch.service").Run()
		if err := os.Remove(unit); err == nil {
			fmt.Printf("  removed %s\n", unit)
		}
	}

	if *creds {
		v := vault.New(logger(false))
		for _, a := range cfg.Accounts {
			if !v.Has(a.ID) {
				continue
			}
			if err := keychain.Delete(keychain.VaultService(a.ID)); err == nil {
				fmt.Printf("  removed the credential for %s\n", a.ID)
			}
		}
	}

	if err := os.RemoveAll(stateDir); err == nil {
		fmt.Printf("  removed %s\n", stateDir)
	}

	fmt.Printf("\n  Done. The binary is still at %s — delete it and the `cs` symlink\n", selfPathOrGuess())
	fmt.Printf("  if you want it gone entirely.\n\n")
	return nil
}

func selfPathOrGuess() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "~/.local/bin/claudeswitch"
}

// checkServiceQoS catches the launchd setting that silently disables the whole
// daemon. ProcessType Background reads as the obviously correct choice for a
// poller — it is what the key is for — but in that QoS band a `security` child
// never completes a keychain read. Measured on macOS 15: 0.1s from a shell,
// 2.1s from a plain launchd agent, and never at all under Background. The
// daemon then polls nothing while looking perfectly healthy, which cost a
// night to find once and should never cost anyone a second one.
//
// It returns a description of the problem, or "" when there is none.
func checkServiceQoS() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "xyz.claudeswitch.daemon.plist")
	b, err := os.ReadFile(plist)
	if err != nil {
		return "" // not installed as a service; nothing to check
	}
	if !strings.Contains(string(b), "ProcessType") {
		return ""
	}
	for _, band := range []string{"Background", "Adaptive"} {
		if strings.Contains(string(b), ">"+band+"<") {
			return fmt.Sprintf(
				"%s sets ProcessType to %s — keychain reads never complete in that band",
				plist, band)
		}
	}
	return ""
}

// checkDuplicateCredentials finds vault entries holding the same credential as
// each other, or one whose stored seat is not the seat its config entry pins.
//
// Either state is quietly catastrophic. Two entries sharing a credential report
// identical utilization, so rotating between them does nothing; and because a
// refresh revokes the token it was given, refreshing one destroys the other.
// That is how three accounts here came to need an interactive login on the same
// morning. It is cheap to detect and was never being looked for.
func checkDuplicateCredentials(cfg *config.Config, st *state.State) string {
	if cfg == nil {
		return ""
	}
	pin := map[string]string{}
	var configured []string
	for _, a := range cfg.Accounts {
		configured = append(configured, a.ID)
		pin[a.ID] = a.Seat()
	}
	// Anything `add` vaulted outside the config counts too. Enumerating only the
	// config is what let two names come to hold one quota pool here: `add` will
	// vault an unconfigured account, and the entry it created was then invisible
	// to the check meant to catch exactly that.
	ids := configured
	if st != nil {
		ids = st.KnownAccounts(configured)
	}

	seen := map[string]string{} // access token -> first account id holding it
	var problems []string
	for _, id := range ids {
		b, err := keychain.Read(keychain.VaultService(id))
		if err != nil || b.ClaudeAIOAuth == nil {
			continue // not vaulted, or unreadable; other checks cover that
		}
		if tok := b.ClaudeAIOAuth.AccessToken; tok != "" {
			if other, dup := seen[tok]; dup {
				problems = append(problems,
					fmt.Sprintf("%s and %s hold the same credential", other, id))
			} else {
				seen[tok] = id
			}
		}
		want := pin[id]
		if want != "" && b.Meta != nil && b.Meta.Seat() != "" && b.Meta.Seat() != want {
			problems = append(problems, fmt.Sprintf(
				"%s holds a credential for seat %s, but is pinned to %s",
				id, shortSeat(b.Meta.Seat()), shortSeat(want)))
		}
	}
	return strings.Join(problems, "; ")
}

// shortSeat abbreviates person@organization without discarding either half.
//
// This used to take the first eight characters of the whole seat, which is the
// account uuid and nothing else — so the one message whose entire purpose is to
// contrast two seats rendered as "holds a credential for seat bbbbbbbb, but is
// pinned to bbbbbbbb" whenever the difference was the organization. Which is
// the common case: the same person in two organizations is two quota pools.
func shortSeat(seat string) string {
	person, org, ok := strings.Cut(seat, "@")
	if !ok {
		return shortID(seat)
	}
	return shortID(person) + "@" + shortID(org)
}
