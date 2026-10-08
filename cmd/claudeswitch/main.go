// Command claudeswitch keeps Claude Code pointed at an account that still has
// quota. This is M1: it observes and reports. It never switches anything.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
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
	asJSON := jsonRequested(args)
	promptsOff = asJSON || flagPresent(args, "yes")
	legacyQuiet = quietCommand(cmd, args)

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
	case "recovery":
		err = cmdRecovery(args)
	case "profile":
		err = cmdProfile(args)
	case "run":
		err = cmdRun(args)
	case "remove":
		err = cmdRemove(args)
	case "account":
		err = cmdAccount(args)
	case "priority":
		err = cmdPriority(args)
	case "daemon":
		err = cmdDaemon(args)
	case "session":
		err = cmdSession(args)
	case "setup":
		err = cmdSetup(args)
	case "login":
		err = cmdLogin(args)
	case "statusline":
		if len(args) > 0 && (args[0] == "install" || args[0] == "uninstall") {
			err = cmdStatuslineManage(args[0], args[1:])
		} else {
			err = cmdStatusline(args)
		}
	case "context":
		err = cmdContext(args)
	case "whoami":
		err = cmdWhoami(args)
	case "refresh":
		err = cmdRefresh(args)
	case "chrome":
		err = cmdChrome(args)
	case "version", "-v", "--version":
		err = cmdVersion(os.Stdout, args)
	default:
		usageText()
		os.Exit(2)
	}
	if err != nil {
		if asJSON {
			writeJSONError(os.Stdout, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "claudeswitch: "+err.Error())
		os.Exit(1)
	}
}

func usageText() {
	fmt.Fprint(os.Stderr, `claudeswitch `+version+` — keeps Claude Code on an account that still has quota

  status     what every configured account's quota looks like right now
  watch      run the daemon (dry-run by default; --live to act)
  history    deduped rejection history from the transcripts
  session    token usage across every account used in a span of work
  doctor     check the things that have to be true for this to work
  setup      guided first run: vault your accounts, write the config, install
  config     show the settings in force, or change one; config clean removes
             the scope lines and [project] tables older configs carry
  init       write a starter config by hand instead
  uninstall  stop the daemon and remove what claudeswitch installed

  login <id> sign in to an account and vault it, verifying it is the right one
             --direct leaves your live session untouched, whatever happens;
             finish it with: login <id> --code <code>
  add <id>   vault the credential that is live right now, as <id>
             (--from <profile>: the one live in that profile)
  use <id>   swap Claude Code onto a vaulted account (hot; no restart needed)
  accounts   what is in the vault
  plan       the rotation decision right now (changes nothing)
  why        the same decision, account by account, with the reasoning
  top        a live view that refreshes in place (ctrl-c to leave)
  audit      what the daemon has observed, decided and done
  forget     drop an account's recorded observations (not its vault entry)
  remove     delete an account everywhere: credential, config block, pool and
             priority entries, observations (--yes to skip the question)
  rename     give a vaulted account a different id, keeping its credential
  account    list | rename | delete | pin | unpin | priority
  priority <id>...
             set the rotation order
  daemon     status | start | stop | restart | live | dry-run | install | uninstall
             the background service, as install.sh sets it up
  statusline one compact line for Claude Code's status line (read-only)
             install / uninstall set it in ~/.claude/settings.json
  context    quota context for a Claude Code session start (read-only)
  whoami     which Claude account is live right now
  identify   record which seat each vaulted credential belongs to
  refresh    renew a vaulted account's credential (never the live one)
  recovery   credentials a swap kept because it could not tell whose they were;
             restore <slot> <account> vaults one, clear <slot> deletes one
  profile create <name> [--dir PATH] [--pool a,b] [--seed <account>]
             make a Claude Code profile: its dir, your settings, CLAUDE.md,
             skills, commands and agents linked from ~/.claude, your MCP
             servers copied, its [[profile]] block; --seed signs it in with a
             vaulted account live nowhere else
  profile list
             each profile's dir, pool and live account
  profile forget <name>
             release the guard on a removed or re-pointed profile's old credential
  profile pool <name> add|remove <account> [--to <profile>]
             change a profile's pool (pools never overlap)
  profile set <name> <key> <value|inherit>
             a per-profile switch_at, switch_at_weekly, hard_floor,
             landing_margin or models
  run <profile> [-- claude args]
             start Claude Code in a profile (CLAUDE_CONFIG_DIR set for it)
  chrome add <account>
             open a Chrome profile for that account, for Claude in Chrome
  chrome [<account>]
             open it (no account: the one live in this shell's profile)
  chrome list | forget <account>

  Several Claude Code profiles ([[profile]] in the config): use, add, login,
  whoami, status, top, why and plan take --profile NAME. Without it, a command
  acts on the profile this shell's CLAUDE_CONFIG_DIR belongs to (use, add,
  login, whoami), or shows every profile (status, top, why, plan).

  For scripts and the menu-bar app: config, profile, account, priority,
  remove, recovery, use, add, login --direct/--code and daemon take --json,
  never prompt, and fail with {"error":{"code","message","hint"}}
  (docs/APP_CLI.md). version --json names the contract version.

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
	// With --json a bad flag must still answer with the error object, not
	// with usage text and exit 2: parse without exiting, then answer.
	asJSON := jsonRequested(args)
	if asJSON {
		fs.Init(fs.Name(), flag.ContinueOnError)
		fs.SetOutput(io.Discard)
	}
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if asJSON {
				writeJSONError(os.Stdout, appErr(codeUsage, "", "%s: %v", fs.Name(), err))
				exitProcess(1)
			}
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
	return loadWith(config.Load, cfgPath)
}

// loadWith is load with another config loader (rename's lenient one).
func loadWith(loader func(string) (*config.Config, error), cfgPath string) (*config.Config, *state.State, error) {
	cfg, err := loader(cfgPath)
	if err != nil && cfg == nil {
		return nil, nil, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	warnLegacy(os.Stderr, cfg)
	// Every configured profile is materialised at load, so the daemon's
	// goroutines only ever read the profile map (see state.Load).
	st, serr := state.Load("", cfg.ProfileNames()...)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", serr)
	}
	// An older daemon may still read state fields this binary no longer
	// writes (D10); every command that goes through here says so.
	warnStaleDaemon(st)
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
	only := fs.String("profile", "", "show only this Claude Code profile (default: every one)")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if err := checkProfileFlag(cfg, *only); err != nil {
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
	vlt := vault.New(log)
	vaulted := map[string]bool{}
	plans := map[string]string{}
	for _, a := range cfg.Accounts {
		vaulted[a.ID] = vlt.Has(a.ID)
		plans[a.ID] = vlt.PlanOf(a.ID)
	}
	renderStatus(os.Stdout, cfg, st, render.Options{
		Budget: p.Budget(), Degraded: degraded, DegradedWhy: why,
		DaemonOwns: daemonOwns, Vaulted: vaulted, Plans: plans, Detail: *detail,
		Known: knownAccounts(cfg),
	}, recentSwitches(5), time.Now(), *only)
	fmt.Print(ghostLines(st.GhostList()))
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

	// One loop per profile, each with its own detector and live item. With
	// no [[profile]] blocks there is one, resolved from this process's
	// environment exactly as before profiles (docs/PROFILES.md §7).
	d := &daemon{
		cfg: cfg, st: st, p: p, v: v, aud: aud, nt: nt, log: log,
		live: *live, idleGap: *idleGap,
		save:     func() error { return st.SaveAs(state.OwnerDaemon) },
		saveCLI:  st.Save,
		resolve:  liveFor,
		projects: projectsFor,
		newDet:   newDetector,
		profs:    buildProfiles(cfg, log, liveFor, projectsFor, newDetector),
	}
	p.Busy = d.busy

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	d.startDetectors(stop)

	// The config is re-read when it changes, so an account that login or add
	// writes into it is polled without a restart. The watcher only signals; the
	// reload itself happens in d.run's select, on the goroutine that owns cfg,
	// the poller and the state.
	d.reloader = newConfigReloader(cfg.Path, cfg, log)
	if t, err := configTarget(cfg.Path); err == nil {
		cleanStaleConfigTemps(t)
	}
	cfgChanged, werr := watchConfig(cfg.Path, 500*time.Millisecond, stop, log)
	if werr != nil {
		log.Warn("cannot watch the config; changes to it need a daemon restart", "path", cfg.Path, "err", werr)
	}
	d.cfgChanged = cfgChanged

	d.adoptOfflineGhosts()
	d.start(ctx)

	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	save := time.NewTicker(2 * time.Minute)
	defer save.Stop()
	return d.run(ctx, stop, sig, tick.C, save.C)
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

// doctorDeps are doctor's reaches into the keychain and the network, so a test
// can run every check without either.
type doctorDeps struct {
	readLive   func() (*keychain.Blob, error)
	fetchUsage func(ctx context.Context, token string) (*usage.Usage, error)
	resolve    func(config.Profile) (profile.Resolved, error)
	duplicates func(*config.Config, *state.State) string
	// verifyOne signs in as a vaulted account: whether it is vaulted, and who
	// its credential belongs to.
	verifyOne func(id string) (vaulted bool, pr *usage.Profile, err error)
	vaulted   func(*config.Config) int
	// recovery lists the kept recovery copies (IMPROVEMENTS I1a); nil skips
	// the check.
	recovery func(*config.Config) []vault.RecoveryItem
	// ghosts lists the ghosts still guarding; nil skips the check.
	ghosts func() []*state.Ghost
}

var doctorSeams = doctorDeps{
	readLive: keychain.ReadLive,
	fetchUsage: func(ctx context.Context, token string) (*usage.Usage, error) {
		return usage.NewClient().Fetch(ctx, token)
	},
	resolve:    profile.Resolve,
	duplicates: checkDuplicateCredentials,
	vaulted:    countVaulted,
	recovery: func(cfg *config.Config) []vault.RecoveryItem {
		return vault.ListRecovery(cfg.ProfileNames())
	},
	ghosts: func() []*state.Ghost {
		st, err := state.Load("")
		if err != nil || st == nil {
			return nil
		}
		return st.GhostList()
	},
	verifyOne: func(id string) (bool, *usage.Profile, error) {
		v := vault.New(logger(false))
		if !v.Has(id) {
			return false, nil, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cred, err := v.Load(id)
		if err != nil {
			return true, nil, err
		}
		pr, err := v.Identify(ctx, cred.AccessToken)
		return true, pr, err
	},
}

// failTally passes doctor's output through and counts its FAIL marks, so the
// exit status follows from what was printed (D14): a check cannot print FAIL
// and still let doctor succeed. Each mark is written by one Fprintf, so it is
// never split across two writes.
type failTally struct {
	w io.Writer
	n int
}

func (f *failTally) Write(p []byte) (int, error) {
	f.n += bytes.Count(p, []byte("[FAIL]"))
	return f.w.Write(p)
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	deep := fs.Bool("verify", false,
		"also confirm every vaulted credential still authenticates (one API call each)")
	parseInterleaved(fs, args)
	return runDoctor(os.Stdout, *cfgPath, *deep)
}

// runDoctor prints every check and fails when any printed FAIL. Warnings and
// info lines never fail it.
func runDoctor(stdout io.Writer, cfgPath string, deep bool) error {
	tally := &failTally{w: stdout}
	w := io.Writer(tally)
	deps := doctorSeams

	ok := func(b bool) string {
		if b {
			return "ok  "
		}
		return "FAIL"
	}
	fmt.Fprintln(w)

	cfg, cerr := config.Load(cfgPath)
	shown := cfgPath
	if cfg != nil {
		shown = cfg.Path
	} else if shown == "" {
		shown = config.DefaultPath()
	}
	// A config that does not parse comes back nil: report it, never crash on it.
	fmt.Fprintf(w, "  [%s] config          %s\n", ok(cerr == nil), shown)
	if cerr != nil {
		fmt.Fprintf(w, "         └ %v\n", cerr)
		fmt.Fprintf(w, "         └ fix: run `claudeswitch init`\n")
	}
	if cfg != nil && cerr == nil {
		doctorWarnings(w, cfg)
	}

	dupState, _ := state.Load("")
	if dup := deps.duplicates(cfg, dupState); dup != "" {
		fmt.Fprintf(w, "  [%s] vault entries   %s\n", ok(false), "corrupted")
		fmt.Fprintf(w, "         └ %s\n", dup)
		fmt.Fprintf(w, "         └ fix: re-add the affected accounts while each is signed in\n")
	} else if cerr == nil {
		fmt.Fprintf(w, "  [%s] vault entries   %s\n", ok(true), "each holds its own credential")
	}
	// Kept recovery copies are a warning: nothing is broken, but each may be
	// the only copy of a login (IMPROVEMENTS I1a).
	if deps.recovery != nil && cfg != nil && cerr == nil {
		fmt.Fprint(w, doctorRecoveryLine(deps.recovery(cfg), time.Now()))
	}
	// Ghosts are a warning: an old credential still guarded (lane 7).
	if deps.ghosts != nil {
		fmt.Fprint(w, doctorGhostLine(deps.ghosts()))
	}

	// A daemon older than this binary may read state fields it no longer
	// writes (D10). Only a running daemon is checked.
	if daemonRunning() && dupState != nil {
		me := currentBuild()
		if line := staleDaemonLine(dupState, me, true, runtime.GOOS); line != "" {
			fmt.Fprintf(w, "  [FAIL] daemon          older than this binary (%s)\n", me)
			fmt.Fprintf(w, "         └ %s\n", strings.TrimPrefix(line, "! "))
		} else {
			fmt.Fprintf(w, "  [ok  ] daemon          running, not older than this binary (%s)\n", me)
		}
	}

	if problem := checkServiceQoS(); problem != "" {
		fmt.Fprintf(w, "  [%s] service qos     %s\n", ok(false), "throttled")
		fmt.Fprintf(w, "         └ %s\n", problem)
		fmt.Fprintf(w, "         └ fix: remove the ProcessType key and reload the agent,\n")
		fmt.Fprintf(w, "           or re-run install.sh\n")
	} else {
		fmt.Fprintf(w, "  [%s] service qos     %s\n", ok(true), "not throttled")
	}

	blob, kerr := deps.readLive()
	if kerr == nil && (blob == nil || blob.ClaudeAIOAuth == nil) {
		kerr = errors.New("the live item holds no Claude credential")
	}
	fmt.Fprintf(w, "  [%s] credentials     %s\n", ok(kerr == nil), keychain.Backend)
	if kerr != nil {
		fmt.Fprintf(w, "         └ %v\n", kerr)
		fmt.Fprintf(w, "         └ fix: approve access when macOS asks, or click Always Allow\n")
	} else {
		o := blob.ClaudeAIOAuth
		fmt.Fprintf(w, "         └ access token %s, expires %s\n",
			keychain.Redact(o.AccessToken), humanUntil(o.Expiry()))
		fmt.Fprintf(w, "         └ refresh token expires %s%s\n",
			humanUntil(o.RefreshExpiry()), warnIfSoon(o.RefreshExpiry()))
		fmt.Fprintf(w, "         └ mcpOAuth present: %v (must be preserved across a swap)\n",
			len(blob.MCPOAuth) > 0)
	}

	if kerr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		u, uerr := deps.fetchUsage(ctx, blob.ClaudeAIOAuth.AccessToken)
		var rl *usage.RateLimitedError
		switch {
		case uerr == nil:
			fmt.Fprintf(w, "  [ok  ] usage api       5h %.1f%%  7d %.1f%%  org %s\n",
				u.FiveHour.Pct(), u.SevenDay.Pct(), shortID(u.OrgID))
		case errors.As(uerr, &rl):
			fmt.Fprintf(w, "  [warn] usage api       rate limited, clears in %s (expected, not a fault)\n",
				rl.RetryAfter.Round(time.Second))
		default:
			fmt.Fprintf(w, "  [FAIL] usage api       %v\n", uerr)
			if usage.IsShapeError(uerr) {
				fmt.Fprintf(w, "         └ the endpoint changed shape: predictive switching would be disabled\n")
			}
		}
	}

	if cfg != nil {
		doctorProfiles(w, cfg, deps.resolve)
	}

	root := detector.ProjectsRoot()
	entries, derr := os.ReadDir(root)
	fmt.Fprintf(w, "  [%s] transcripts     %s (%d project dirs)\n", ok(derr == nil), root, len(entries))

	if cfg != nil {
		if !cfg.RefreshEnabled() {
			fmt.Fprintf(w, "  [warn] auto-refresh    OFF (auto_refresh = false) — vaulted tokens will expire\n")
		} else {
			v := vault.New(logger(false))
			fmt.Fprintf(w, "  [ok  ] auto-refresh    renews at %s before expiry; probes idle accounts every %s\n",
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
				fmt.Fprintf(w, "         └ %-18s access %s · refresh token %s\n", a.ID, exp, rt)
			}
		}
	}
	if cfg != nil {
		for _, line := range pollCadenceLines(cfg) {
			fmt.Fprintln(w, line)
		}
	}
	// A vault entry that no longer authenticates is invisible until the moment
	// it is needed — which is the moment it matters most. Behind a flag because
	// it costs a call per account.
	if deep && cfg != nil {
		fmt.Fprintf(w, "  [    ] credentials     verifying each vaulted account…\n")
		bad, tried := 0, 0
		for _, a := range cfg.Ordered() {
			vaulted, pr, lerr := deps.verifyOne(a.ID)
			if !vaulted {
				fmt.Fprintf(w, "         └ %-18s not vaulted\n", a.ID)
				continue
			}
			tried++
			switch {
			case lerr != nil:
				bad++
				fmt.Fprintf(w, "         └ %-18s FAILS — %v\n", a.ID, truncateStr(lerr.Error(), 44))
				fmt.Fprintf(w, "           %s\n", "fix: cs login "+a.ID+" --direct")
			case a.Seat() != "" && pr.Seat() != a.Seat():
				bad++
				fmt.Fprintf(w, "         └ %-18s WRONG ACCOUNT — holds %s\n", a.ID, pr.Describe())
				fmt.Fprintf(w, "           %s\n", "fix: cs login "+a.ID+" --direct")
			default:
				fmt.Fprintf(w, "         └ %-18s ok — %s · %s\n", a.ID, pr.Account.Email, pr.Plan())
			}
		}
		if bad > 0 {
			fmt.Fprintf(w, "  [FAIL] credentials     %d of %d vaulted account(s) do not sign in as configured\n",
				bad, tried)
		}
	} else if cfg != nil {
		fmt.Fprintf(w, "  [ok  ] credentials     %d vaulted; `cs doctor --verify` checks each one works\n",
			deps.vaulted(cfg))
	}
	fmt.Fprintf(w, "  [ok  ] switching       live rotation is wired; `cs plan` says what it would do\n")
	if ok, path := statuslineInstalled(); ok {
		fmt.Fprintf(w, "  [ok  ] status line     set in %s\n", path)
	} else {
		fmt.Fprintf(w, "  [info] status line     not set; `cs statusline install` adds it\n")
	}
	if pluginInstalled() {
		fmt.Fprintf(w, "  [ok  ] claude plugin   installed\n")
	} else {
		fmt.Fprintf(w, "  [info] claude plugin   not installed; see README, \"Inside Claude Code\"\n")
	}
	fmt.Fprintln(w)
	return doctorExit(tally.n)
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

[[account]]
id    = "personal"
label = "personal"
reserve = 70        # never auto-used above this utilization

# Several Claude Code profiles (one per CLAUDE_CONFIG_DIR) each rotate within
# their own pool; pools must not overlap, and accounts in no pool join the
# profile named "default". Without [[profile]] blocks there is one profile,
# whichever CLAUDE_CONFIG_DIR the environment names.
#
# dir is CLAUDE_CONFIG_DIR exactly as you launch Claude Code with it. Omit it
# for the profile you run with CLAUDE_CONFIG_DIR unset: that is NOT the same
# as dir = "~/.claude", which Claude Code keys to a different credential.
# Declaring both is an error: they share ~/.claude. No two profiles may share
# a folder, or nest one inside another.
#
# [[profile]]
# name = "default"            # no dir: CLAUDE_CONFIG_DIR unset
# pool = ["personal"]
#
# [[profile]]
# name      = "work"
# dir       = "~/.claude-work"
# pool      = ["work-a"]
# switch_at = 75              # overrides the global value for this profile
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
	profName := fs.String("profile", "", "the profile whose pool the account joins, and without --from "+
		"the one whose live credential is vaulted (default: the one this shell's CLAUDE_CONFIG_DIR belongs to)")
	from := fs.String("from", "", "the Claude Code profile whose live credential to vault, when it is not "+
		"--profile (the app's \"Save the login Claude Code is using in\")")
	force := fs.Bool("force", false,
		"re-add even when the live credential looks staler than the one already vaulted under that name")
	asJSON := fs.Bool("json", false, "machine-readable output; never prompts")
	positional := parseInterleaved(fs, args)
	var out map[string]any
	err := humanToStderr(*asJSON, func() error {
		var err error
		out, err = addAccount(*cfgPath, *profName, *from, *force, positional)
		return err
	})
	if err != nil || !*asJSON {
		return err
	}
	return emitJSON(out)
}

// addAccount is `cs add`: it vaults the live credential, and returns the
// account as `add --json` and `login --code --json` report it.
//
// from (lane 16, B2) is the profile whose live credential is read; profV is
// the one whose pool a new account joins. Either defaults to the other, and
// both to the profile this shell's CLAUDE_CONFIG_DIR names. A credential
// read from one profile and filed in another's pool is still live in the
// first: §3 holds, since no profile swaps in an account live elsewhere, and
// the first rotates off it (it is outside that pool).
func addAccount(cfgPathV, profV, fromV string, forceV bool, positional []string) (map[string]any, error) {
	cfgPath, profName, force := &cfgPathV, &profV, &forceV
	cfg, st, err := load(*cfgPath)
	if err != nil {
		return nil, err
	}
	if fromV == "" {
		fromV = *profName
	} else if _, ok := cfg.ProfileNamed(fromV); !ok {
		return nil, noSuchProfile(cfg, fromV)
	}
	if *profName == "" {
		*profName = fromV
	}
	// The source is settled before the name: a suggested name comes from
	// whichever account is live in it.
	source, err := pickProfile(cfg, fromV)
	if err != nil {
		return nil, err
	}
	var configured []string
	for _, a := range cfg.Accounts {
		configured = append(configured, a.ID)
	}
	// Everything we have actually stored, not only what the config mentions.
	// Entries vaulted outside the config used to be invisible here, so a second
	// name could be given to a pool that already had one.
	others := st.KnownAccounts(configured)

	identity := func() (string, string, string) { return addIdentity(source) }
	id, err := chooseAddName(positional, isTerminal(), identity, others, ask)
	if err != nil {
		return nil, err
	}
	// The id must be new or already the target's: one another pool lists is
	// refused (outside_pool). Filing it in the target while it is live in
	// the source is the owner's choice (lane 16): §3 keeps the target from
	// swapping it in until the source has moved off it.
	target, err := profileForAccount(cfg, *profName, id, true)
	if err != nil {
		return nil, err
	}
	live, err := cliLiveFor(source)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", source.Name, err)
	}

	log := logger(false)
	v := vault.New(log)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Re-adding a name refreshes it in place — but only with the same seat. An
	// unpinned config does not make the name free: the entry says who it is.
	existed := v.Has(id)
	vaultedSeat := ""
	if existed {
		vaultedSeat, _ = v.IdentityOf(id)
	}
	// Re-adding replaces a credential, so never with a worse one. The live
	// credential can be the older of the two: the daemon refreshes vaulted
	// entries, and a refresh rotates the refresh token, so the copy Claude Code
	// still holds may already be revoked.
	guard := func(live, vaulted *keychain.OAuth) error {
		if *force {
			return nil
		}
		if why := liveIsWorse(vaulted, live); why != "" {
			return staleReAddError(id, why)
		}
		return nil
	}
	e, err := v.StoreGuardedFrom(ctx, live, id, addExpectSeat(cfg.SeatOf(id), vaultedSeat), others, guard)
	if err != nil {
		return nil, addError(id, err)
	}
	st.AddVaulted(id)
	st.SetEmail(id, e.Email)
	st.SetPlan(id, e.Plan)
	acct := st.Get(id)
	acct.OrgID = e.OrgID
	acct.RefreshExpiry = e.RefreshExpiry
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}

	fmt.Printf("\n  ✓ %s\n", addHeadline(id, existed))
	if email, orgName, _ := addIdentity(source); orgName != "" {
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
	// Until this wrote the block itself, `add` stored the credential and left
	// the account in limbo: `login` refused it for not being in the config,
	// `accounts` did not list it, and the next command that read the state file
	// discarded what `add` had just recorded. The seat is only knowable after
	// signing in, which is why it is written here rather than asked for.
	reportSeatRecorded(cfg, id, e, poolToJoin(cfg, target))
	note := crossProfileNote(source.Name, target.Name)
	if note != "" {
		fmt.Printf("    from          profile %s: %s\n", source.Name, note)
	}
	fmt.Println()
	out := vaultedAccountJSON(cfg.Path, id, e, !hasAccount(cfg, id))
	out["from"] = source.Name
	out["note"] = orNull(note)
	return out, nil
}

// crossProfileNote is what `add --from S --profile T` tells the person when
// S and T differ (owner decision, lane 16): the account stays signed in in
// S until S's daemon loop moves off it (it is outside S's accounts now), and
// T's loop swaps it in only after that (§3). "" when they are the same.
func crossProfileNote(source, target string) string {
	if source == target {
		return ""
	}
	return "still signed in in " + source + " — " + source + " will move off it; " + target + " can use it after"
}

// chooseAddName decides what to vault the live credential under. Given a name,
// that is it. Without one, it suggests a name from the live account and asks —
// and with nobody at the terminal to answer, refuses rather than filing the
// credential under a guess.
func chooseAddName(positional []string, interactive bool,
	identity func() (email, orgName, orgID string), taken []string,
	prompt func(q, def string) string) (string, error) {

	switch len(positional) {
	case 1:
		if err := checkNewAccountID(positional[0]); err != nil {
			return "", err
		}
		return positional[0], nil
	case 0:
	default:
		return "", fmt.Errorf("usage: claudeswitch add [<name>]\n\n" +
			"Log in to the account first (`claude` → /login), then add what you just logged into.")
	}
	email, orgName, orgID := "", "", ""
	suggested := ""
	takenMap := map[string]string{}
	for _, id := range taken {
		takenMap[id] = id
	}
	pr := &usage.Profile{}
	if email, orgName, orgID = identity(); email != "" {
		pr.Account.Email = email
		pr.Organization.Name = orgName
		pr.Organization.UUID = orgID
		suggested = suggestName(pr, takenMap)
	}
	if !interactive {
		// The app asks for the name itself: the suggestion travels in the
		// hint, alone, so it can be offered as the default.
		return "", &appError{Code: codeNameRequired, Hint: suggested,
			Message: "no name given, and nobody at a terminal to confirm one.\n" +
				"  Name the account that is live right now:\n      cs add <name>"}
	}
	if email == "" {
		return "", fmt.Errorf("could not tell which account is live, so there is no name to suggest.\n" +
			"  Name it yourself:\n      cs add <name>")
	}
	who := email
	if orgName != "" {
		who += " in " + orgName
	}
	name := strings.TrimSpace(prompt("  name for "+who, suggested))
	if name == "" {
		return "", fmt.Errorf("no name given; nothing was stored")
	}
	if err := checkNewAccountID(name); err != nil {
		return "", err
	}
	return name, nil
}

// liveIsWorse says why the live credential should not replace the vaulted one,
// or "" when it is at least as good. Worse means: it cannot be renewed and the
// vaulted one can; or its refresh token runs out sooner; or, with neither
// refresh expiry known, its access token runs out sooner.
func liveIsWorse(vaulted, live *keychain.OAuth) string {
	if vaulted == nil || live == nil || vaulted.AccessToken == live.AccessToken {
		return ""
	}
	if vaulted.RefreshToken != "" && live.RefreshToken == "" {
		return "the live credential has no refresh token, and the vaulted one does"
	}
	vr, lr := vaulted.RefreshExpiry(), live.RefreshExpiry()
	if !vr.IsZero() && !lr.IsZero() {
		if vr.After(lr) {
			return fmt.Sprintf("the vaulted refresh token expires %s, the live one %s",
				humanUntil(vr), humanUntil(lr))
		}
		return ""
	}
	if va, la := vaulted.Expiry(), live.Expiry(); !va.IsZero() && va.After(la) {
		return fmt.Sprintf("the vaulted access token expires %s, the live one %s — the vaulted "+
			"credential is the newer of the two", humanUntil(va), humanUntil(la))
	}
	return ""
}

// staleReAddError refuses a re-add that would make the entry worse.
func staleReAddError(id, why string) error {
	return fmt.Errorf("not replacing %s: %s.\n"+
		"  Nothing was stored. The vault already holds a better credential for this seat;\n"+
		"  to replace it anyway:\n"+
		"      cs add %s --force", id, why, id)
}

// addExpectSeat is the seat a credential filed under a name must turn out to
// be: the config's pin if it has one, otherwise whatever is already vaulted
// under that name, otherwise anything (a first add).
func addExpectSeat(configSeat, vaultedSeat string) string {
	return nonEmpty(configSeat, vaultedSeat)
}

// addHeadline is the first line `add` prints.
func addHeadline(id string, refreshed bool) string {
	if refreshed {
		return fmt.Sprintf("refreshed %s in place: same seat, new credential", id)
	}
	return "vaulted " + keychain.VaultService(id)
}

// addError explains a refusal in terms of what `add` does. It saves the
// credential that is live, so meeting an account already vaulted almost always
// means the person is still signed in as it and wanted to add a different one —
// which is a sign-in, and `add` never signs in.
func addError(id string, err error) error {
	var dup *vault.DuplicateSeatError
	var wrong *vault.WrongOrgError
	switch {
	case errors.As(err, &dup):
		return wrapErr(codeAlreadyVaulted, "", fmt.Errorf("the credential live right now is already vaulted as %q (%s).\n"+
			"  Nothing was stored. `add` saves whatever Claude Code is signed in to.\n"+
			"  To add a different account, sign in to it:\n"+
			"      cs login %s", dup.Other, dup.Who, id))
	case errors.As(err, &wrong):
		return wrapErr(codeWrongAccount, "", fmt.Errorf("%q is seat %s, but the credential live right now is %s (%s).\n"+
			"  Nothing was stored. To keep both, add the live one under another name:\n"+
			"      cs add <other-name>\n"+
			"  To renew %s itself, sign in to it:\n"+
			"      cs login %s%w", id, usage.ShortSeat(wrong.WantOrg), usage.ShortSeat(wrong.GotOrg),
			wrong.GotEmail, id, id, quiet{err}))
	}
	return err
}

// quiet wraps an error so %w keeps it reachable through errors.As without
// printing it a second time.
type quiet struct{ error }

func (quiet) Error() string   { return "" }
func (q quiet) Unwrap() error { return q.error }

// priorityLine matches the rotation order when it is written on one line, which
// is how this program writes it. Anything else is left alone rather than
// guessed at. It must not cross a newline: a multi-line list used to match
// from its opening bracket to its closing one and gain a stray separator.
var (
	priorityLine = regexp.MustCompile(`(?m)^priority\s*=\s*\[([^\]\n]*)\]`)
	priorityAny  = regexp.MustCompile(`(?m)^priority\s*=`)
)

// Where an appended account ended up in the rotation order.
type priorityPlace int

const (
	priorityNamed     priorityPlace = iota // added to the end of a one-line list
	priorityNoList                         // no list: config order applies, and it is last
	priorityMultiLine                      // a list we do not edit; the id is not in it
)

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
	_, err := appendAccountPlaced(path, id, block)
	return err
}

func appendAccountPlaced(path, id, block string) (priorityPlace, error) {
	return appendAccountInPool(path, id, block, "")
}

// appendAccountInPool is appendAccountPlaced that also names the account in
// the pool of the declared profile called pool (D20), in the same edit: with
// no profile named "default", an account in no pool would not load at all.
// An empty pool edits no pool.
func appendAccountInPool(path, id, block, pool string) (priorityPlace, error) {
	target, err := configTarget(path)
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return 0, err
	}
	out := strings.TrimRight(string(raw), "\n") + "\n" + block
	if pool != "" {
		if out, err = addToPool(out, pool, id); err != nil {
			return 0, err
		}
	}

	place := priorityNoList
	if m := priorityLine.FindStringSubmatchIndex(out); m != nil {
		inner := strings.TrimSpace(out[m[2]:m[3]])
		sep := ", "
		switch {
		case inner == "":
			sep = ""
		case strings.HasSuffix(inner, ","):
			sep = " "
		}
		out = out[:m[3]] + sep + strconv.Quote(id) + out[m[3]:]
		place = priorityNamed
	} else if priorityAny.MatchString(out) {
		place = priorityMultiLine
	}

	err = writeConfigFile(target, []byte(out), func(cfg *config.Config) error {
		if !hasAccount(cfg, id) {
			return fmt.Errorf("the edit parsed but %q was not in it; discarded", id)
		}
		if pool != "" {
			if owner, _ := cfg.ProfileOf(id); owner != pool {
				return fmt.Errorf("the edit parsed but %q did not land in profile %q's pool; discarded", id, pool)
			}
		}
		return nil
	})
	return place, err
}

// configTarget is the file a config edit must land in: the config itself, or
// what it links to. Renaming over a symlink would replace the link with a plain
// file and leave its target — often a dotfiles repository — stale.
func configTarget(path string) (string, error) {
	t, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return t, nil
}

// writeConfigFile replaces target with data, atomically and durably: a
// temporary file beside it, with the original's mode, synced, parsed back and
// checked, then renamed into place. Anything that fails leaves the original
// untouched and no temporary file behind.
func writeConfigFile(target string, data []byte, check func(*config.Config) error) error {
	return writeConfigFileWith(config.Load, target, data, check)
}

// writeConfigFileWith is writeConfigFile parsing the result with loader:
// rename's lenient one accepts a config whose remaining fault is other bad
// names, since repairing those one at a time is the point.
func writeConfigFileWith(loader func(string) (*config.Config, error), target string, data []byte,
	check func(*config.Config) error) error {
	cleanStaleConfigTemps(target)
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".claudeswitch-new-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// Parse what we are about to install, not what we meant to write.
	cfg, err := loader(name)
	if err != nil {
		os.Remove(name)
		return fmt.Errorf("the edit would not load, so it was discarded: %w", err)
	}
	if check != nil {
		if err := check(cfg); err != nil {
			os.Remove(name)
			return err
		}
	}
	if err := os.Rename(name, target); err != nil {
		os.Remove(name)
		return err
	}
	// The rename is only durable once the directory entry is: without this a
	// crash can bring back the old file, or neither.
	if d, err := os.Open(filepath.Dir(target)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// staleConfigTempAge is how old a leftover temporary config file must be before
// it is removed: old enough that it cannot be another writer's edit in flight.
const staleConfigTempAge = time.Minute

// cleanStaleConfigTemps removes temporary files a crashed config edit left
// beside target.
func cleanStaleConfigTemps(target string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(target),
		globEscape(filepath.Base(target))+".claudeswitch-new-*"))
	for _, m := range matches {
		if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() &&
			time.Since(fi.ModTime()) > staleConfigTempAge {
			_ = os.Remove(m)
		}
	}
}

// globEscape quotes the characters filepath.Glob would treat as a pattern.
func globEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`)
	return r.Replace(s)
}

// pollCadenceLines is doctor's report on the polling cadence. A hot cadence
// faster than the default is kept — it is the person's setting, often written
// out by `cs config` or setup before the default changed — but it is
// warned about, because it empties an account's burst allowance.
func pollCadenceLines(cfg *config.Config) []string {
	used, avail := cfg.CallsPerWindow(), float64(cfg.APIBudget-1)
	mark := "ok  "
	if used > avail {
		mark = "FAIL"
	}
	out := []string{
		fmt.Sprintf("  [%s] poll cadence    active %s · hot %s · idle %s", mark,
			cfg.PollActive.Duration, cfg.PollHot.Duration, cfg.PollIdle.Duration),
		fmt.Sprintf("         └ %.1f of %.0f usage calls per 5 min (api_budget %d, one held for swaps)",
			used, avail, cfg.APIBudget),
	}
	// Per account (GROUND_TRUTH §42): each account's own allowance, which the
	// machine-wide figure above cannot see.
	// A poll_active or poll_idle under the floor runs at the floor and is a
	// warning, never a failure (owner decision 2026-10-07).
	r := cfg.AccountCadence()
	floored := cfg.FlooredPolls()
	mark = "ok  "
	if len(floored) > 0 || r.HotDrains() || r.ActiveStarved() {
		mark = "warn"
	}
	out = append(out,
		fmt.Sprintf("  [%s] account rate    in use %.0f/h · hot %.0f/h for up to %s · idle %.0f/h",
			mark, r.ActivePerHour, r.HotPerHour, fmt.Sprintf("%dm", int(config.HotLookahead.Minutes())), r.IdlePerHour),
		fmt.Sprintf("         └ each account's allowance is ~%d calls and refills ~%.0f/h; a hot spell spends %.1f of the %d held for it (hot_reserve)",
			usage.AccountBurst, r.RefillPerHour, r.HotSpellNet, r.Spare),
		fmt.Sprintf("         └ routine reads leave it alone; the account in use dips into it only once its reading is %s old (stale-decision warning at %s)",
			config.OverdueAfter(r.PollActive), staleDecisionAfter(cfg)),
		fmt.Sprintf("         └ %g/h set aside on a live account (unseen_calls_per_hour) for Claude Code's own reads",
			r.Unseen))
	if r.ActiveStarved() {
		out = append(out,
			fmt.Sprintf("         unseen_calls_per_hour %g leaves %.0f calls/h: the account in use is read about every %s, not %s",
				r.Unseen, r.RefillPerHour-r.Unseen, r.ActiveEvery.Round(time.Second), r.PollActive),
			fmt.Sprintf("         fix: cs config unseen_calls_per_hour %g, or accept it with cs config poll_active %s",
				usage.DefaultLiveUnseenPerHour, r.ActiveEvery.Round(time.Minute)))
	}
	for _, f := range floored {
		out = append(out,
			fmt.Sprintf("         %s %s runs at %s: faster would drain an account's allowance", f.Key, f.Was, f.Now),
			"         fix: "+f.Fix)
	}
	if mins, fast := config.HotDrainMinutes(cfg.PollHot.Duration); fast {
		out = append(out,
			fmt.Sprintf("  [warn] poll cadence  hot %s drains an account's ~%d-call allowance in ~%d min",
				cfg.PollHot.Duration, config.UsageBurstCalls, mins),
			fmt.Sprintf("         fix: cs config poll_hot %s", config.DefaultPollHotSetting))
	}
	return out
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
	profName := fs.String("profile", "", profileFlagHelp)
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return appErr(codeUsage, "", "usage: claudeswitch use <account-id> [--profile NAME] [--json]")
	}
	id := positional[0]

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	// D5 before anything is read: the refusal must not depend on the vault.
	target, err := profileForAccount(cfg, *profName, id, false)
	if err != nil {
		return err
	}
	log := logger(false)
	v := vault.New(log)

	if !v.Has(id) {
		return &appError{Code: codeNotVaulted, Message: fmt.Sprintf(
			"account %q is not in the vault. Log in to it, then run `claudeswitch add %s`", id, id)}
	}
	live, err := cliLiveFor(target)
	if err != nil {
		return fmt.Errorf("profile %q: %w", target.Name, err)
	}
	// §3, as the daemon checks it before every swap: one credential may be
	// live in at most one profile. Unknown counts as live (D18).
	on := ""
	if multiProfile(cfg) {
		on = " in profile " + target.Name
	}
	if other, why := liveConflict(cfg, st, v, target.Name, id); other != "" {
		return &appError{Code: codeLive, Message: fmt.Sprintf("account %q may be live in %s (%s); one credential live in two "+
			"profiles is logged out by whichever refreshes first, so nothing was changed",
			id, whereLiveQ(other), why)}
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
		if *asJSON {
			return emitJSON(map[string]any{"account": id, "profile": target.Name, "dry_run": true,
				"from": orNull(st.Profile(target.Name).Active), "expect_org": orNull(expectOrg)})
		}
		fmt.Printf("\n  would swap%s to %q (expecting org %s)\n", on, id, nonEmpty(expectOrg, "any"))
		fmt.Printf("  mcpOAuth would be carried over from the live item, unchanged\n\n")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	ist := st.Profile(target.Name)
	from := ist.Active
	res, err := swapInto(ctx, v, cfg, target.Name, from, live, id, expectOrg, log)
	if err != nil {
		if res != nil && res.RolledBack {
			return fmt.Errorf("%w\n  (your previous credential was restored and verified)", err)
		}
		if errors.Is(err, vault.ErrLockBusy) {
			return fmt.Errorf("%w\n  Claude Code is refreshing this profile's credential; run the same "+
				"command again in a few seconds", err)
		}
		return err
	}
	ist.SetActive(id)
	// A deliberate manual switch gets the cooldown's protection too, so the
	// daemon does not immediately rotate away from the account you just chose.
	ist.LastSwitch = time.Now()
	// And it belongs in the audit log. Recording only the daemon's switches made
	// the history look stale and wrong: every manual swap was invisible.
	// Only when it actually moved. Re-selecting the account already in use is
	// not a switch, and recording it as one makes the history unreadable.
	if from != id {
		if aud, err := audit.Open(""); err == nil {
			_ = aud.Write(audit.Event{Kind: "switch", Profile: target.Name, From: from, To: id,
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
	if *asJSON {
		m := map[string]any{"account": id, "profile": target.Name, "from": orNull(from), "dry_run": false,
			"org_id": orNull(res.OrgID), "verified": res.Usage != nil, "five_hour": nil, "seven_day": nil}
		if res.Usage != nil {
			m["five_hour"], m["seven_day"] = res.Usage.FiveHour.Pct(), res.Usage.SevenDay.Pct()
		}
		return emitJSON(m)
	}

	fmt.Printf("\n  ✓ now using %s%s\n", id, on)
	if res.Usage != nil {
		fmt.Printf("    5-hour %.1f%%   7-day %.1f%%   org %s\n",
			res.Usage.FiveHour.Pct(), res.Usage.SevenDay.Pct(), shortID(res.OrgID))
	} else {
		fmt.Printf("    installed, but usage could not be read to confirm it (rate limited)\n")
	}
	if from != id {
		useChromeNote(os.Stdout, st, from, id)
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
				"id": a.ID, "vaulted": v.Has(a.ID),
				"active": activeIn(cfg, st, a.ID) != "",
			}
			addProfileJSON(m, cfg, st, a.ID)
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
	t := render.NewTable([]string{"", "ACCOUNT", "SIGNED IN AS", "PLAN", "ORGANIZATION"})
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
		if activeIn(cfg, st, a.ID) != "" {
			mark, name = render.Good("▸"), render.Bold(name)
		}
		if !v.Has(a.ID) {
			name = render.Dim(a.Name())
			who, plan, orgName = render.Dim("not vaulted"), "-", "-"
		}
		t.Add(mark, name, who, plan, nonEmpty(orgName, "-"))
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
	only := fs.String("profile", "", "plan only this Claude Code profile (default: every one)")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if err := checkProfileFlag(cfg, *only); err != nil {
		return err
	}
	log := logger(false)
	if *refresh && !state.DaemonRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		p := poller.New(cfg, st, log)
		if len(cfg.Profiles) == 0 {
			if _, err := p.PollActive(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "note: %v\n", err)
			}
		} else {
			// Each profile's own live credential, as the daemon reads it.
			for _, t := range liveTargets(cfg, cliLiveFor) {
				if *only != "" && t.name != *only {
					continue
				}
				if t.live == nil {
					fmt.Fprintf(os.Stderr, "note: profile %s: %v\n", t.name, t.unresolved)
					continue
				}
				p.SetLive(t.name, t.live)
				if _, err := p.PollActiveIn(ctx, t.name); err != nil {
					fmt.Fprintf(os.Stderr, "note: profile %s: %v\n", t.name, err)
				}
			}
		}
		_ = st.Save()
	}

	now := time.Now()
	views := profileViews(cfg, st, *only)
	if *planJSON {
		if !multiProfile(cfg) {
			d := policy.Decide(views[0].input(st, now))
			return emitJSON(map[string]any{"decision": decisionJSON(d)})
		}
		var list []map[string]any
		for _, v := range views {
			d := policy.Decide(v.input(st, now))
			m := map[string]any{"profile": v.in.Name, "decision": decisionJSON(d)}
			markCurrent(m, cfg, v.in.Name)
			list = append(list, m)
		}
		return emitJSON(map[string]any{"profiles": list})
	}
	fmt.Println()
	for _, v := range views {
		if multiProfile(cfg) {
			v.heading(os.Stdout)
		}
		printPlan(os.Stdout, v.cfg, policy.Decide(v.input(st, now)))
		printPlanModels(os.Stdout, v.cfg, st, v.ist.Active)
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

// printPlan writes one decision as `plan` shows it.
func printPlan(w io.Writer, cfg *config.Config, d policy.Decision) {
	fmt.Fprintf(w, "  decision  %s\n", d.Kind)
	fmt.Fprintf(w, "  because   %s\n", d.Reason)
	if d.Kind == policy.Switch {
		fmt.Fprintf(w, "  target    %s\n", d.Target)
		if d.Forced {
			fmt.Fprintf(w, "  timing    immediately, mid-turn (past the %.0f%% hard floor)\n", cfg.HardFloor)
		} else if d.Failover {
			fmt.Fprintf(w, "  timing    at the next idle gap only; a blind failover never splits a turn\n")
		} else {
			fmt.Fprintf(w, "  timing    at the next idle gap between turns\n")
		}
	}
	if d.Kind == policy.Wait && d.RecoversAccount != "" {
		fmt.Fprintf(w, "  recovers  %s in %s (at %s)\n", d.RecoversAccount,
			time.Until(d.RecoversAt).Round(time.Minute), d.RecoversAt.Local().Format("15:04"))
	}
}

// printPlanModels adds the active account's per-model weekly limits to `plan`
// (IMPROVEMENTS I6), only when it has any: which of them the config counts
// is what decides whether one of them can trigger the plan above.
func printPlanModels(w io.Writer, cfg *config.Config, st *state.State, active string) {
	acct := st.Accounts[active]
	if !acct.HasReading() {
		return
	}
	var parts []string
	for _, l := range acct.Last.ModelWeekly() {
		s := l.ModelName() + " unknown"
		if l.Known() {
			s = fmt.Sprintf("%s %.0f%%", l.ModelName(), l.Pct())
		}
		if cfg.CountsModel(l.ModelName()) {
			s += " (counted)"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(w, "  models    %s weekly on %s\n", strings.Join(parts, ", "), active)
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
		for _, in := range st.Profiles {
			if !known[in.Active] {
				in.Active = ""
			}
		}
	}
	for _, id := range positional {
		if _, ok := st.Accounts[id]; ok {
			st.Drop(id)
			dropped = append(dropped, id)
			for _, in := range st.Profiles {
				if in.Active == id {
					in.Active = ""
				}
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
func warnExpiringRefresh(st *state.State, nt notifier) {
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

// slModels picks the per-model weekly limits worth a place on the status
// line: the one the API marks active, and any at or past the weekly trigger.
// binds says one of them is active and above every window shown, so it is the
// binding limit and carries the ▸. A limit with no figure is left to
// `status`, which says "unknown"; the line has no room to explain it.
func slModels(u *usage.Usage, worst float64, cfg *config.Config) (shown []usage.Limit, binds bool) {
	for _, l := range u.ModelWeekly() {
		if !l.Known() {
			continue
		}
		if l.IsActive || l.Pct() >= cfg.TriggerFor(usage.SevenDayKey) {
			shown = append(shown, l)
			if l.IsActive && l.Pct() > worst {
				binds = true
			}
		}
	}
	return shown, binds
}

// slModel renders one model limit: "Modelname 96% 2d". No bar: the line is
// already two bars long.
func slModel(l usage.Limit, binding bool, now time.Time, cfg *config.Config, colour bool) string {
	mark := ""
	if binding {
		mark = "▸"
	}
	band := slBand(l.Pct(), cfg)
	return fmt.Sprintf("%s%s %s %s", mark, l.ModelName(),
		slPaint(band, fmt.Sprintf("%.0f%%", l.Pct()), colour),
		slPaint(ansiDim, slUntil(l.ResetsAt, now), colour))
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
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		fmt.Print("claudeswitch  no account selected")
		return nil
	}
	err = statuslineBody(cfg, st)
	// After whatever the line said: a daemon older than this binary may be
	// running code that no longer matches what it is shown (owner decision,
	// lane 6). Read-only, like the rest of the line.
	if staleDaemonLine(st, currentBuild(), daemonRunning(), runtime.GOOS) != "" {
		fmt.Print(slPaint(ansiYellow, "  "+outdatedMarker, slColorEnabled()))
	}
	return err
}

// outdatedMarker ends the status line while the running daemon is older than
// this binary.
const outdatedMarker = "⚠ daemon outdated"

// statuslineBody prints the status line itself, for cmdStatusline.
func statuslineBody(cfg *config.Config, st *state.State) error {
	// The status line runs inside a session, with that session's
	// CLAUDE_CONFIG_DIR: it shows that profile, never another's account.
	view, verr := statuslineView(cfg, st)
	if verr != nil {
		fmt.Print("claudeswitch  this session's CLAUDE_CONFIG_DIR is no configured profile")
		return nil
	}
	tag := ""
	if multiProfile(cfg) {
		tag = view.in.Name + ": "
	}
	active := view.ist.Active
	if active == "" {
		fmt.Print("claudeswitch  " + tag + "no account selected")
		return nil
	}
	a, ok := st.Accounts[active]
	if !ok || a.Last == nil {
		fmt.Printf("%s%s  no reading yet", tag, active)
		return nil
	}
	name := active
	for _, c := range cfg.Accounts {
		if c.ID == active {
			name = c.Name()
		}
	}
	name = tag + name
	// The profile's own thresholds (D4) colour the bars and decide.
	cfg = view.cfg
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
	// Per-model weekly limits (IMPROVEMENTS I6) earn a place on the line only
	// when one is the binding limit or at the weekly trigger: the line is
	// glanced at, and a quiet model limit is noise there.
	models, modelBinds := slModels(a.Last, worst, cfg)
	if modelBinds {
		bindKey = "" // the model's segment carries the ▸
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
	for _, l := range models {
		parts = append(parts, slModel(l, modelBinds && l.IsActive, now, cfg, colour))
	}

	// A model the config counts (I6) triggers like the weekly window, so it
	// decides whether the policy is asked, as the weekly figure does.
	if eff, _, _ := a.Last.WithModels(cfg.Models); eff != a.Last {
		if _, w := eff.Worst(); w > worst {
			worst = w
		}
	}

	// What claudeswitch is about to do, in words — decided by the policy engine
	// rather than by the threshold alone.
	//
	// Saying "switching now" because a number crossed a line promises relief
	// that may not be coming: when every account is exhausted there is nowhere
	// to go, and the useful fact is when quota returns instead.
	if worst >= cfg.SwitchAt {
		dec := policy.Decide(view.input(st, now))
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
		if age := now.Sub(a.LastAt); age > staleReadingAfter {
			parts = append(parts, slPaint(ansiYellow, "· read "+slAge(age)+" ago", colour))
		}
	}

	fmt.Print(strings.Join(parts, "  "))
	return nil
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
	profName := fs.String("profile", "", profileFlagHelp)
	parseInterleaved(fs, args)

	cfg, cerr := config.Load(*cfgPath)
	// A config that does not load still answers whoami, for the profile this
	// environment names, exactly as before profiles.
	target := config.Profile{Name: config.DefaultProfile, FromEnv: true}
	if cfg != nil && cerr == nil {
		var err error
		if target, err = pickProfile(cfg, *profName); err != nil {
			return err
		}
	}
	live, err := cliLiveFor(target)
	if err != nil {
		return fmt.Errorf("profile %q: %w", target.Name, err)
	}
	blob, err := live.Read()
	if err != nil {
		return fmt.Errorf("cannot read the live credential: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	u, uerr := vault.New(logger(false)).FetchLive(ctx, blob.ClaudeAIOAuth.AccessToken)

	cachedEmail, cachedOrgName, cachedOrgID := cachedIdentity(target)

	fmt.Println()
	if cfg != nil && multiProfile(cfg) {
		fmt.Printf("  profile      %s\n", target.Name)
	}
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

	seat := ""
	if pr, perr := vault.New(logger(false)).Identify(ctx, blob.ClaudeAIOAuth.AccessToken); perr == nil {
		seat = pr.Seat()
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

	fmt.Println()
	fmt.Print(whoamiVerdict(cfg, seat, u.OrgID))
	fmt.Println()
	return nil
}

// whoamiVerdict says which configured account the live credential is. Only a
// seat — this person in this organization — can say that: a team organization
// has a pool per member, so matching the organization alone named a colleague's
// account as the one signed in. Org-only entries and a missing seat are reported
// as candidates, with what would settle it, never as a match.
func whoamiVerdict(cfg *config.Config, seat, orgID string) string {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if id := cfg.AccountBySeat(seat); id != "" {
		return fmt.Sprintf("  → known to claudeswitch as %q.\n", id)
	}
	var sameOrg []string
	for _, c := range cfg.Accounts {
		if c.OrgID != "" && c.OrgID == orgID {
			sameOrg = append(sameOrg, c.Name())
		}
	}
	quoted := func(ids []string) string {
		q := make([]string, len(ids))
		for i, id := range ids {
			q[i] = strconv.Quote(id)
		}
		return strings.Join(q, ", ")
	}
	var b strings.Builder
	switch {
	case len(sameOrg) == 0:
		b.WriteString("  → this organization is NOT in your config: a new account.\n")
		b.WriteString("    Vault it with:  claudeswitch add <name>\n")
	case seat == "":
		fmt.Fprintf(&b, "  → cannot tell which person this is: the identity lookup failed, and\n"+
			"    %s in this organization may be someone else's seat.\n", quoted(sameOrg))
	default:
		person, _, _ := strings.Cut(seat, "@")
		fmt.Fprintf(&b, "  → this seat (%s) is not in your config.\n", person)
		fmt.Fprintf(&b, "    %s share its organization, but a team organization has a\n", quoted(sameOrg))
		b.WriteString("    separate quota pool per person. If one of them is this seat, pin it with\n")
		fmt.Fprintf(&b, "      account_uuid = %q\n", person)
		b.WriteString("    otherwise vault it with:  claudeswitch add <name>\n")
	}
	return b.String()
}

// cachedIdentity reads what Claude Code believes about the signed-in account.
// It is a label, not a fact: the block is written at login and is not updated
// when the credential underneath it changes.
//
// It asks `claude auth status` rather than reading the file, so Claude Code
// itself resolves which .claude.json (ccdir.GlobalConfig) from the
// CLAUDE_CONFIG_DIR the profile runs with (envForProfile).
// addIdentity is cachedIdentity for `add`'s suggested name and summary. A seam.
var addIdentity = cachedIdentity

func cachedIdentity(in config.Profile) (email, orgName, orgID string) {
	c := exec.Command("claude", "auth", "status")
	c.Env = envForProfile(in)
	out, err := c.Output()
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
// reportUnrenewable handles the one refresh failure that is not transient: the
// credential cannot be renewed at all, and only an interactive login fixes it.
// It reports false for every other error, leaving it to the caller.
//
// Split out from cmdRefresh because that function reads flags, the keychain and
// the config before reaching this point, none of which a test can supply — so
// the branch that matters was the one part with no coverage.
//
// Recording it matters as much as printing it. The daemon's refresher writes
// the same field, and telling the person here while leaving `status` saying
// something else makes the two disagree about the single condition that needs
// acting on, with whichever ran more recently deciding what gets believed. A
// successful poll clears it either way, so a failure that turns out to have
// been someone else rotating the token retracts itself.
func reportUnrenewable(out io.Writer, st *state.State, id string, err error) bool {
	var needsLogin *oauth.NeedsLoginError
	if !errors.As(err, &needsLogin) {
		return false
	}
	fmt.Fprintf(out, "\n  ✗ %s cannot be refreshed.\n", id)
	fmt.Fprintf(out, "    %v\n", err)
	fmt.Fprintf(out, "\n    This is the answer to whether this account can be kept alive:\n")
	fmt.Fprintf(out, "    it cannot, and it will need `claude auth login` each time its\n")
	fmt.Fprintf(out, "    access token expires.\n\n")
	st.Get(id).LastErr = err.Error()
	if serr := st.Save(); serr != nil {
		fmt.Fprintf(out, "    note: could not record that: %v\n", serr)
	}
	return true
}

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

	cfg, st, err := load(*cfgPath)
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
	// Every profile's live item is asked, not one: refreshing revokes the
	// token whichever profile holds it, so no --profile can narrow this.
	holderName, holder, isActive := liveHolder(cliGhostTargets(cfg, st), v, id)
	if isActive && *allowActive && holder == nil {
		return liveHolderUnknown(holderName, id)
	}

	if isActive && !*allowActive {
		if multiProfile(cfg) {
			fmt.Printf("\n  %q is the account currently in use in profile %q.\n", id, holderName)
		} else {
			fmt.Printf("\n  %q is the account currently in use.\n", id)
		}
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
	e, err := v.RefreshIn(ctx, id, cfg.SeatOf(id), holder, *allowActive)
	if err != nil {
		if reportUnrenewable(os.Stdout, st, id, err) {
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
	if err := checkNewAccountID(newID); err != nil {
		return err
	}
	if oldID == newID {
		return fmt.Errorf("those are the same id")
	}
	return renameAccount(*cfgPath, oldID, newID)
}

// renameAccount moves an account to a new id everywhere: the config (its
// [[account]] block, priority and every pool), the vault item and the state
// records. It works on a config that fails only the name rule, which is how
// an id written before the rule is repaired (owner decision, lane 7).
//
// Order, so the credential is never lost: copy the vault item to the new name
// and read it back; edit the config (on failure the new copy is removed and
// nothing else has changed); update state; only then delete the old item. A
// state save that fails stops it there, both vault items kept.
//
// Re-running it finishes a rename a crash or failure interrupted: a step
// already done (the new vault item holding the same credential, the config
// already naming the new id) is skipped, never refused. Two different
// credentials under the two names are refused.
//
// The daemon lock is held throughout, so no daemon can start and write
// state in the middle; a running daemon refuses the rename.
func renameAccount(cfgPath, oldID, newID string) error {
	lock, lerr := state.TryDaemonLock()
	switch {
	case errors.Is(lerr, state.ErrDaemonRunning):
		return appErr(codeDaemonRunning, "stop it first: claudeswitch daemon stop",
			"stop the daemon before renaming (it owns the state file)")
	case lerr != nil:
		return fmt.Errorf("could not take the daemon lock, so nothing was renamed: %w", lerr)
	}
	defer lock.Release()

	cfg, st, err := loadWith(loadIgnoringBadNames, cfgPath)
	if err != nil {
		return err
	}
	vaultOld := keychain.VaultService(oldID)
	vaultNew := keychain.VaultService(newID)
	read := func(name string) (*keychain.Blob, bool, error) {
		b, err := keychain.Read(name)
		switch {
		case err == nil:
			return b, true, nil
		case errors.Is(err, keychain.ErrNotFound):
			return nil, false, nil
		}
		return nil, false, err
	}
	oldBlob, hasOld, err := read(vaultOld)
	if err != nil {
		return fmt.Errorf("could not read the vault entry for %q, so nothing was renamed: %w", oldID, err)
	}
	newBlob, hasNew, err := read(vaultNew)
	if err != nil {
		return fmt.Errorf("could not check whether %q is already vaulted: %w", newID, err)
	}
	configOld, configNew := hasAccount(cfg, oldID), hasAccount(cfg, newID)
	stateOld := stateNames(st, oldID)

	switch {
	case configOld && configNew:
		return fmt.Errorf("%s has both %q and %q; pick another name", cfg.Path, oldID, newID)
	case hasOld && hasNew && (oldBlob.ClaudeAIOAuth == nil || newBlob.ClaudeAIOAuth == nil ||
		oldBlob.ClaudeAIOAuth.AccessToken != newBlob.ClaudeAIOAuth.AccessToken):
		return fmt.Errorf("a vault entry for %q already exists and holds a different credential; "+
			"remove it first", newID)
	case hasNew && !hasOld && !configOld && !stateOld:
		return fmt.Errorf("a vault entry for %q already exists and nothing is named %q any more", newID, oldID)
	case !hasOld && !configOld && !stateOld:
		return fmt.Errorf("%q is neither in the vault, nor in %s, nor in the state", oldID, cfg.Path)
	case hasNew && !hasOld && configOld:
		return fmt.Errorf("a vault entry for %q already exists; remove it first", newID)
	case configNew && !configOld && hasOld && !hasNew:
		// Not a resume: a rename copies the vault entry before it edits
		// the config, so a config already naming newID means the copy was
		// made. This is a different credential headed for an account the
		// config already has.
		return fmt.Errorf("%s already has an account %q, and %q's credential is not its own; "+
			"nothing was renamed", cfg.Path, newID, oldID)
	}

	// The whole annotation moves, the seat included: the identity guards are
	// gated on the seat.
	var meta *keychain.Meta
	created := false
	if hasOld && !hasNew {
		meta = oldBlob.Meta
		if meta == nil {
			meta = &keychain.Meta{}
		}
		meta.AccountID = newID
		moved := &keychain.Blob{ClaudeAIOAuth: oldBlob.ClaudeAIOAuth, Meta: meta}
		if err := keychain.Write(vaultNew, moved); err != nil {
			return fmt.Errorf("could not create the new vault entry (the old one is untouched): %w", err)
		}
		back, err := keychain.Read(vaultNew)
		if err != nil || back.ClaudeAIOAuth == nil || back.ClaudeAIOAuth.AccessToken != oldBlob.ClaudeAIOAuth.AccessToken {
			_ = keychain.Delete(vaultNew)
			return fmt.Errorf("the new vault entry did not read back as written; nothing was renamed")
		}
		created = true
	} else if hasNew {
		meta = newBlob.Meta
	}
	if configOld {
		if err := renameInConfig(cfg, oldID, newID); err != nil {
			if created {
				_ = keychain.Delete(vaultNew)
			}
			return fmt.Errorf("could not rename %q in %s, so nothing was renamed: %w", oldID, cfg.Path, err)
		}
	}

	if a, ok := st.Accounts[oldID]; ok {
		a.ID = newID
		st.Accounts[newID] = a
		st.Drop(oldID)
	}
	for _, v := range st.Vaulted {
		if v == oldID {
			st.DropVaulted(oldID)
			st.AddVaulted(newID)
			break
		}
	}
	// In every profile: the renamed account may be live in any of them.
	for _, in := range st.Profiles {
		if in.Active == oldID {
			// A rename does not change which account is live, so keep the
			// original timestamp rather than claiming a fresh observation.
			in.Active = newID
		}
		if in.Pinned == oldID {
			in.Pinned = newID
		}
	}
	st.RenameGhostAccount(oldID, newID)
	st.RenameChromeAccount(oldID, newID)
	if err := saveRenameState(st); err != nil {
		return fmt.Errorf("renamed %s → %s in the vault and the config, but the state file could not be "+
			"saved (%v). Both vault entries are kept, so nothing is lost; do not start the daemon "+
			"until the rename is finished. Finish it with:\n"+
			"      cs rename %s %s", oldID, newID, err, oldID, newID)
	}
	if hasOld {
		if err := keychain.Delete(vaultOld); err != nil {
			fmt.Fprintf(os.Stderr, "note: created %q but could not remove %q: %v\n", newID, oldID, err)
		}
	}

	fmt.Printf("\n  ✓ renamed %s → %s\n", oldID, newID)
	if meta != nil {
		fmt.Printf("    credential moved, seat annotation kept (%s)\n", shortID(meta.OrgID))
	}
	if configOld {
		fmt.Printf("    %s updated: the account, priority and pools\n", cfg.Path)
	}
	if !created && (hasNew || configNew) {
		fmt.Printf("    (finished an interrupted rename)\n")
	}
	fmt.Println()
	return nil
}

// stateNames reports whether the state still names id anywhere a rename
// changes.
func stateNames(st *state.State, id string) bool {
	if _, ok := st.Accounts[id]; ok {
		return true
	}
	for _, v := range st.Vaulted {
		if v == id {
			return true
		}
	}
	for _, in := range st.Profiles {
		if in.Active == id || in.Pinned == id {
			return true
		}
	}
	for _, g := range st.GhostList() {
		if g.Account == id {
			return true
		}
	}
	return false
}

// loadIgnoringBadNames loads a config whose only fault is names that break
// the rule, as if it had none: the names are what rename is repairing.
func loadIgnoringBadNames(p string) (*config.Config, error) {
	cfg, err := config.LoadAllowingBadNames(p)
	var ne *config.NameError
	if cfg != nil && errors.As(err, &ne) {
		return cfg, nil
	}
	return cfg, err
}

// renameInConfig rewrites oldID as newID in the config's text: the id line of
// its [[account]] block, the top-level priority list, and every
// [[profile]]'s pool, however they are laid out. Comments and everything
// else survive. The result is parsed back (leniently: other ids may still
// break the name rule) and checked before it replaces the file.
func renameInConfig(cfg *config.Config, oldID, newID string) error {
	target, err := configTarget(cfg.Path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	out := renameIDInText(string(raw), oldID, newID)
	owner, inPool := cfg.ProfileOf(oldID)
	declaredPool := false
	for _, in := range cfg.Profiles {
		for _, id := range in.Pool {
			if id == oldID {
				declaredPool = true
			}
		}
	}
	prio := slices.Index(cfg.Priority, oldID)
	return writeConfigFileWith(loadIgnoringBadNames, target, []byte(out), func(c *config.Config) error {
		if !hasAccount(c, newID) || hasAccount(c, oldID) {
			return fmt.Errorf("the edit did not rename the [[account]] block; discarded")
		}
		if prio >= 0 && slices.Index(c.Priority, newID) != prio {
			return fmt.Errorf("the edit did not rename the priority entry; discarded")
		}
		if declaredPool || inPool {
			if got, _ := c.ProfileOf(newID); got != owner {
				return fmt.Errorf("the edit moved the account from profile %q's pool to %q's; discarded", owner, got)
			}
		}
		return nil
	})
}

// renameIDInText replaces the string literal oldID with newID where an
// account id is named: an id line inside [[account]], the top-level priority
// array and a pool array inside [[profile]].
func renameIDInText(text, oldID, newID string) string {
	lines := strings.Split(text, "\n")
	section := "" // "" top level, "account", "profile", "other"
	inArray := false
	for i, line := range lines {
		if !inArray && tableHeader.MatchString(line) {
			switch {
			case accountHdr.MatchString(line):
				section = "account"
			case profileHdr.MatchString(line):
				section = "profile"
			default:
				section = "other"
			}
			continue
		}
		if inArray {
			lines[i], inArray = renameLiterals(line, 0, oldID, newID)
			continue
		}
		switch {
		case section == "account" && idLine.MatchString(line):
			lines[i], _ = renameLiterals(line, strings.Index(line, "="), oldID, newID)
		case section == "" && priorityAny.MatchString(line), section == "profile" && poolLine.MatchString(line):
			lines[i], inArray = renameLiterals(line, strings.Index(line, "["), oldID, newID)
		}
	}
	return strings.Join(lines, "\n")
}

// renameLiterals replaces string literals equal to oldID in line from
// offset from, stopping at a comment, and reports whether an array opened at
// or before from is still open at the end of the line.
func renameLiterals(line string, from int, oldID, newID string) (string, bool) {
	var b strings.Builder
	b.WriteString(line[:from])
	open := true
	for i := from; i < len(line); i++ {
		c := line[i]
		switch c {
		case '#':
			b.WriteString(line[i:])
			return b.String(), open
		case ']':
			open = false
			b.WriteByte(c)
		case '"', '\'':
			j := i + 1
			for j < len(line) && line[j] != c {
				if c == '"' && line[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(line) {
				b.WriteString(line[i:])
				return b.String(), open
			}
			if line[i+1:j] == oldID {
				b.WriteString(strconv.Quote(newID))
			} else {
				b.WriteString(line[i : j+1])
			}
			i = j
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), open
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
	profName := fs.String("profile", "", "the Claude Code profile to sign in through, without --direct "+
		"(default: the one this shell's CLAUDE_CONFIG_DIR belongs to); with --direct, the profile whose "+
		"pool a new account joins")
	asJSON := fs.Bool("json", false, "machine-readable output (with --direct or --code); never prompts")
	noOpen := fs.Bool("no-open", false, "with --direct: do not open a browser, only print the URL")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch login <account-id> [--direct] [--browser <app>] [--sso] [--keep] [--profile NAME]")
	}
	id := positional[0]
	if err := checkNewAccountID(id); err != nil {
		return err
	}

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
	// A name the config has never heard of is how an account gets added: the
	// block is written after the login, pinned to the seat it verified, because
	// the seat is not knowable before then. Refusing here sent people off to
	// write a block by hand that could not be pinned anyway.

	if *code != "" {
		var out map[string]any
		err := humanToStderr(*asJSON, func() error {
			var err error
			out, err = loginComplete(cfg, st, v, *code)
			return err
		})
		if err != nil || !*asJSON {
			return err
		}
		return emitJSON(out)
	}
	if *direct {
		// --direct never touches a live credential. --profile only says whose
		// pool a new account joins when the login completes (D20), and is
		// settled now, before anything is started.
		pool, err := directPool(cfg, id, *profName)
		if err != nil {
			return err
		}
		br := *browser
		if *noOpen {
			br = ""
		}
		err = humanToStderr(*asJSON, func() error {
			return loginDirect(cfg, st, v, id, wantOrg, *pinOrg, oauth.Extra{Prompt: *prompt, LoginHint: *email}, br, pool)
		})
		if err != nil || !*asJSON {
			return err
		}
		return emitJSON(directLoginJSON(cfg, id))
	}
	if *asJSON {
		return appErr(codeUsage, "sign in from an app with: claudeswitch login <id> --direct --json --no-open",
			"login without --direct signs in through Claude Code interactively, which has no JSON form")
	}

	// Without --direct the sign-in goes through a profile's live credential:
	// it must be one whose pool holds the account (D5), and the account must
	// not be live in another profile (§3). A name the config has never heard
	// of is exempt from the first: it has no pool yet, and the block recorded
	// after the login is what gives it one.
	target, err := profileForAccount(cfg, *profName, id, true)
	if err != nil {
		return err
	}
	if other, why := liveConflict(cfg, st, v, target.Name, id); other != "" {
		return fmt.Errorf("account %q may be live in %s (%s); sign in there, or use "+
			"`claudeswitch login %s --direct`, which touches no live credential", id, whereLiveQ(other), why, id)
	}

	// Remember what to come back to. Only an account we can actually restore
	// counts — a vaulted credential, not merely whatever is live now.
	restoreTo := ""
	if prev := st.Profile(target.Name).Active; prev != "" && prev != id && v.Has(prev) {
		restoreTo = prev
	}

	fmt.Println()
	if !known {
		fmt.Print(newAccountNotice(cfg, target, id))
	}
	if wantOrg != "" {
		fmt.Printf("  About to sign in and vault it as %q.\n\n", id)
		// A seat, not an organization: one person in one organization. Calling
		// it an organization made the refusal that follows read as nonsense,
		// since the same person in two organizations differs only in the half
		// the label denied was there.
		fmt.Printf("  That account is seat %s — one person in one organization,\n", usage.ShortSeat(wantOrg))
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
	cmd.Env = envForProfile(target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("`claude auth login` did not complete: %w", err)
	}

	if email, orgName, _ := cachedIdentity(target); orgName != "" {
		fmt.Printf("\n  signed in as  %s\n  organization  %s\n", email, orgName)
	}

	// Resolved after the login: a profile not yet signed in has no item
	// before it (D9), and this login is what creates it.
	live, err := cliLiveFor(target)
	if err != nil {
		return fmt.Errorf("profile %q: %w", target.Name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	var configuredIDs []string
	for _, a := range cfg.Accounts {
		configuredIDs = append(configuredIDs, a.ID)
	}
	// Including what was vaulted outside the config — see cmdAdd.
	e, serr := v.StoreFrom(ctx, live, id, wantOrg, st.KnownAccounts(configuredIDs))
	if serr != nil {
		fmt.Printf("\n  ✗ not vaulted.\n     %v\n", serr)
		restoreActive(v, cfg, st, target.Name, live, restoreTo)
		return nil
	}

	st.AddVaulted(id)
	st.SetEmail(id, e.Email)
	st.SetPlan(id, e.Plan)
	acct := st.Get(id)
	acct.OrgID, acct.RefreshExpiry = e.OrgID, e.RefreshExpiry
	st.Profile(target.Name).SetActive(id)
	fmt.Printf("\n  ✓ vaulted %s (org %s, %s)\n", id, shortID(e.OrgID), nonEmpty(e.Tier, "tier unknown"))
	if e.NoRefreshToken {
		fmt.Printf("    ⚠ no refresh token issued — this account cannot be renewed and will\n")
		fmt.Printf("      need signing in again when its access token expires %s\n", humanUntil(e.Expiry))
	}
	reportSeatRecorded(cfg, id, e, poolToJoin(cfg, target))

	if !*keep && restoreTo != "" {
		restoreActive(v, cfg, st, target.Name, live, restoreTo)
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
func restoreActive(v swapperWith, cfg *config.Config, st *state.State, profName string, live keychain.Live, to string) {
	if to == "" {
		return
	}
	ist := st.Profile(profName)
	from := ist.Active
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	expect := ""
	if a, ok := st.Accounts[to]; ok {
		expect = a.OrgID
	}
	if _, err := v.SwapToWith(ctx, live, to, expect, swapOptions(cfg, profName, from, to)); err != nil {
		fmt.Printf("    ⚠ could not switch back to %s: %v\n", to, err)
		fmt.Printf("      run `claudeswitch use %s --profile %s`\n", to, profName)
		return
	}
	ist.SetActive(to)
	if from != to {
		if aud, err := audit.Open(""); err == nil {
			_ = aud.Write(audit.Event{Kind: "switch", Profile: profName, From: from, To: to,
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
func loginDirect(cfg *config.Config, st *state.State, v *vault.Vault, id, wantOrg string, pinOrg bool, extra oauth.Extra, browser, pool string) error {
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
	if err := flow.Save(oauth.Pending{AccountID: id, OrgID: wantOrg, Pinned: pinOrg,
		Profile: pool}); err != nil {
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
	if !hasAccount(cfg, id) {
		fmt.Printf("  %q is not in %s yet; finishing the login adds it, pinned to the seat\n", id, cfg.Path)
		fmt.Printf("  that comes back.\n")
		if pool != "" {
			fmt.Printf("  It joins profile %q's pool.\n", pool)
		}
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
func loginComplete(cfg *config.Config, st *state.State, v *vault.Vault, code string) (map[string]any, error) {
	flow, pend, err := oauth.LoadPending()
	if err != nil {
		return nil, wrapErr(codeNoPendingLogin, "start one: claudeswitch login <id> --direct", err)
	}
	id, wantOrg, pinned := pend.AccountID, pend.OrgID, pend.Pinned
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tok, err := oauth.NewClient().Exchange(ctx, flow, code)
	if err != nil {
		return nil, fmt.Errorf("%w\n  (nothing was stored; your session is untouched)\n"+
			"  Note: the code must come from the most recent URL — an older one will not match.", err)
	}
	// The code is single-use, so the attempt is spent either way.
	_ = oauth.ClearPending()

	var others []string
	for _, a := range cfg.Accounts {
		others = append(others, a.ID)
	}
	// Including what was vaulted outside the config — see cmdAdd. Without it, a
	// seat vaulted under a name the config never had could be filed again here.
	e, err := v.StoreTokens(ctx, id, wantOrg, tok, st.KnownAccounts(others))
	if err != nil {
		var wrong *vault.WrongOrgError
		var dup *vault.DuplicateSeatError
		switch {
		case errors.As(err, &wrong):
			why := "The browser session decided, as it does when the organization is not pinned. " +
				"(--pin-org does not help: the parameter is accepted but has no effect.)"
			if pinned {
				why = "organization_uuid was sent and the request succeeded, so the parameter is accepted — " +
					"and ignored. The browser session decides which organization a login returns."
			}
			return nil, &appError{Code: codeWrongAccount, Err: err, Hint: why + " Sign in to the right account " +
				"in the browser (or use --browser with another one) and start again.",
				Message: fmt.Sprintf("that login came back as organization %s, not %s; nothing was stored, "+
					"and your session is untouched", wrong.GotOrg, wrong.WantOrg)}
		case errors.As(err, &dup):
			return nil, wrapErr(codeAlreadyVaulted, "", err)
		}
		return nil, err
	}

	st.AddVaulted(id)
	st.SetEmail(id, e.Email)
	st.SetPlan(id, e.Plan)
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
	isNew := !hasAccount(cfg, id)
	reportSeatRecorded(cfg, id, e, completionPool(cfg, pend))
	fmt.Printf("    your live session was never touched\n\n")
	return vaultedAccountJSON(cfg.Path, id, e, isNew), nil
}

// hasAccount reports whether the config has a block for id at all.
func hasAccount(cfg *config.Config, id string) bool {
	for _, a := range cfg.Accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

// reportSeatRecorded writes the verified seat into the config and says what it
// did. The credential is already vaulted by now, so a failure here is reported
// rather than returned: the account works, it just still needs its block.
func reportSeatRecorded(cfg *config.Config, id string, e *vault.Entry, pool string) {
	msg, err := recordSeat(cfg, id, e, pool)
	switch {
	case err != nil:
		fmt.Printf("    ⚠ the credential is vaulted, but the config was not updated:\n")
		fmt.Printf("      %v\n", err)
	case msg != "":
		fmt.Printf("    ✓ %s\n", msg)
		fmt.Printf("      a running daemon picks this up by itself; move it earlier in\n")
		fmt.Printf("      `priority` to spend it sooner\n")
	}
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

	cfg, st, err := load(*cfgPath)
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
			email := v.DescribeOf(id)
			st.SetEmail(id, email) // backfills entries vaulted before emails were recorded
			st.SetPlan(id, v.PlanOf(id))
			fmt.Printf("  %-16s %s  %s\n", id, email, v.PlanOf(id))
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
		// The seat, not the person. `identify` exists to report exactly this
		// distinction and was labelling the account uuid alone as the seat.
		fmt.Printf("  %-16s %s  %s  (seat %s)\n", id, pr.Account.Email, pr.Plan(),
			usage.ShortSeat(pr.Seat()))
		// Recorded so `cs chrome add` can name it without the keychain.
		st.SetEmail(id, pr.Account.Email)
		st.SetPlan(id, pr.Plan())
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
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
	case !st.Default().LastSwitch.IsZero() && st.Default().LastSwitch.After(to.Add(-24*time.Hour)):
		// Default to something meaningful: the span the current rotation covers,
		// widened to catch the work that led up to it.
		if c := st.Default().LastSwitch.Add(-8 * time.Hour); c.After(from) {
			from = c
		}
	}

	s, err := session.Build("", from, to, st.Default().Active)
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
func probeDue(v interface{ VaultedAt(string) time.Time }, accountID string, every time.Duration) bool {
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
						st.SetEmail(id, e.Email)
						st.SetPlan(id, e.Plan)
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
		st.SetEmail(id, e.Email)
		st.SetPlan(id, e.Plan)
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

	if ok, _ := statuslineInstalled(); !ok && askYes("\n  Show quota in Claude Code's status line", true) {
		if err := cmdStatuslineManage("install", nil); err != nil {
			fmt.Printf("    %v\n", err)
		}
	}
	if !pluginInstalled() {
		fmt.Printf("\n  For quota context and /cs skills inside Claude Code:\n")
		fmt.Printf("    /plugin marketplace add https://github.com/bogdan-alexandrescu/claudeswitch\n")
		fmt.Printf("    /plugin install cs@claudeswitch\n")
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
//
// It only ever proposes an id the config would load (config.ValidName): the
// organization's name, else the email's local part, lower-cased with every
// other character mapped to '-', runs of '-' collapsed, '-' and '.' trimmed
// from the ends, ".." collapsed, cut to the length limit; "account" when
// nothing is left. A taken id gets -2, -3, … until it is free.
func suggestName(pr *usage.Profile, taken map[string]string) string {
	base := sanitizeID(pr.Organization.Name)
	if base == "" {
		base = sanitizeID(strings.SplitN(pr.Account.Email, "@", 2)[0])
	}
	if base == "" {
		base = "account"
	}
	used := map[string]bool{}
	for k, id := range taken {
		used[k], used[id] = true, true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		cand := strings.TrimRight(truncateID(base, config.MaxNameLen-len(suffix)), "-.") + suffix
		if !used[cand] {
			return cand
		}
	}
}

// sanitizeID turns free text into a valid account id, or "".
func sanitizeID(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, strings.ToLower(s))
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.Trim(truncateID(strings.Trim(s, "-."), config.MaxNameLen), "-.")
	if config.ValidName("account id", s) != nil {
		return ""
	}
	return s
}

func truncateID(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// isTerminal reports whether we can prompt: stdin must be a terminal, and not
// merely a character device — /dev/null is one, which let `cs top` draw escape
// codes into a pipe.
func isTerminal() bool {
	if promptsOff {
		return false
	}
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

// staleDecisionAfter is when a reading is too old to decide on: three poll
// intervals, so a single missed poll is unremarkable but a pattern is not.
func staleDecisionAfter(cfg *config.Config) time.Duration {
	iv := cfg.PollActive.Duration
	if iv <= 0 {
		iv = 4 * time.Minute
	}
	// min(3 × poll_active, 4m). Capped so a slower poll_active cannot make a
	// stuck poller take longer to be called out (while the account in use is
	// moving it is read every poll_hot anyway); 4m, not 3m, so that routine
	// operation at the 3m default never warns — a read at 3m plus its tick,
	// or an overdue read at 3m15s after a hot spell (config.OverdueAfter),
	// stays under it (owner decision 2026-10-07).
	return min(3*iv, 4*time.Minute)
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
// treating itself as wedged: ten poll_active intervals, but never more than
// ten minutes nor less than two. Long enough that ordinary timeouts pass
// without a restart (a 429 backoff never counts: see Poller.Blind), short
// enough that a genuinely stuck daemon is replaced within minutes. The cap
// keeps the ceiling where it was at the old 60s default: a slower poll_active
// (2m since lane 11) must not leave a wedged daemon running for twenty.
func blindLimit(cfg *config.Config) time.Duration {
	iv := cfg.PollActive.Duration
	if iv <= 0 {
		iv = time.Minute
	}
	d := min(10*iv, 10*time.Minute)
	if d > 2*time.Minute {
		return d
	}
	return 2 * time.Minute
}

// readingsStale reports whether the active account's figure is old enough that
// the daemon cannot be keeping up.
// With more than one profile, any profile's active account counts; an
// profile with nothing active (not logged in) does not.
func readingsStale(cfg *config.Config, st *state.State, limit time.Duration) bool {
	if !multiProfile(cfg) {
		a, ok := st.Accounts[st.Default().Active]
		if !ok || a == nil || a.Last == nil {
			return true
		}
		return time.Since(a.LastAt) > limit
	}
	for _, v := range profileViews(cfg, st, "") {
		if v.ist.Active == "" {
			continue
		}
		a, ok := st.Accounts[v.ist.Active]
		if !ok || a == nil || a.Last == nil || time.Since(a.LastAt) > limit {
			return true
		}
	}
	return false
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
	only := fs.String("profile", "", "explain only this Claude Code profile (default: every one)")
	parseInterleaved(fs, args)

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	if err := checkProfileFlag(cfg, *only); err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(whyJSON(cfg, st, time.Now(), *only))
	}
	renderWhy(os.Stdout, cfg, st, time.Now(), *only)
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
	if d.Failover {
		m["failover"] = true
	}
	if !d.RecoversAt.IsZero() {
		m["recovers_account"] = d.RecoversAccount
		m["recovers_at"] = d.RecoversAt
	}
	return m
}

func verdictsJSON(vs []policy.Verdict, cfg *config.Config, st *state.State, now time.Time) []map[string]any {
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
		addWeeklyJSON(m, cfg, st.Accounts[v.ID], now)
		out = append(out, m)
	}
	return out
}

// addWeeklyJSON adds an account's per-model weekly limits (IMPROVEMENTS I6)
// and weekly pace (I8). Additive: each key appears only when there is data
// for it, so output for an account without any is unchanged.
//
//	"model_limits":   [{"model", "percent" (null: unknown), "severity",
//	                    "resets_at", "is_active", "counted"}]
//	"unknown_limits": [{"kind", "group", "describe", "percent", "severity"}]
//	"weekly_pace":    {"expected", "actual", "resets_at",
//	                   "at_reset", "unused_at_reset" (only once estimable)}
func addWeeklyJSON(m map[string]any, cfg *config.Config, acct *state.Account, now time.Time) {
	if !acct.HasReading() {
		return
	}
	pct := func(l usage.Limit) any {
		if !l.Known() {
			return nil
		}
		return l.Pct()
	}
	var models []map[string]any
	for _, l := range acct.Last.ModelWeekly() {
		e := map[string]any{
			"model": l.ModelName(), "percent": pct(l), "severity": l.Severity,
			"is_active": l.IsActive, "counted": cfg.CountsModel(l.ModelName()),
		}
		if l.ResetsAt != nil {
			e["resets_at"] = *l.ResetsAt
		}
		models = append(models, e)
	}
	if len(models) > 0 {
		m["model_limits"] = models
	}
	var unknown []map[string]any
	for _, l := range acct.Last.UnknownLimits() {
		unknown = append(unknown, map[string]any{
			"kind": l.Kind, "group": l.Group, "describe": l.Describe(),
			"percent": pct(l), "severity": l.Severity,
		})
	}
	if len(unknown) > 0 {
		m["unknown_limits"] = unknown
	}
	if p, ok := acct.WeeklyPace(now); ok {
		pace := map[string]any{"expected": p.Expected, "actual": p.Actual, "resets_at": p.ResetsAt}
		if p.Estimated {
			pace["at_reset"] = p.AtReset
			pace["unused_at_reset"] = p.Unused
		}
		m["weekly_pace"] = pace
	}
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
			"id": a.ID, "vaulted": v.Has(a.ID),
			"active": activeIn(cfg, st, a.ID) != "",
		}
		addProfileJSON(m, cfg, st, a.ID)
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
			// "counted" follows the models list of the profile whose pool
			// holds the account.
			pcfg := cfg
			if owner, ok := cfg.ProfileOf(a.ID); ok && len(cfg.Profiles) > 0 {
				pcfg = cfg.ForProfile(owner)
			}
			addWeeklyJSON(m, pcfg, acct, time.Now())
		} else {
			m["state"] = "unknown"
			m["usable"] = false
		}
		accounts = append(accounts, m)
	}
	degraded, why := p.Degraded()
	out := map[string]any{
		"active":   st.Default().Active,
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
	if !st.Default().LastSwitch.IsZero() {
		out["last_switch"] = st.Default().LastSwitch
	}
	// With more than one profile, each one's active account, pool and
	// effective thresholds (D4). The fields above stay as they were, the
	// default profile's, so a script written for one profile keeps working.
	if multiProfile(cfg) {
		var list []map[string]any
		for _, v := range profileViews(cfg, st, "") {
			m := map[string]any{
				"profile": v.in.Name, "active": v.ist.Active, "pool": v.pool,
				"thresholds": thresholdsJSON(v.cfg),
			}
			markCurrent(m, cfg, v.in.Name)
			if v.ist.Pinned != "" {
				m["pinned"] = v.ist.Pinned
			}
			if !v.ist.LastSwitch.IsZero() {
				m["last_switch"] = v.ist.LastSwitch
			}
			list = append(list, m)
		}
		out["profiles"] = list
	}
	return out
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
	only := fs.String("profile", "", "show only this Claude Code profile (default: every one)")
	parseInterleaved(fs, args)
	if cfg, err := config.Load(*cfgPath); err == nil {
		if err := checkProfileFlag(cfg, *only); err != nil {
			return err
		}
	}

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
		vlt := vault.New(logger(false))
		vaulted, plans := map[string]bool{}, map[string]string{}
		for _, a := range cfg.Accounts {
			vaulted[a.ID], plans[a.ID] = vlt.Has(a.ID), vlt.PlanOf(a.ID)
		}
		degraded, why := p.Degraded()
		renderStatus(&buf, cfg, st, render.Options{
			Budget: p.Budget(), Degraded: degraded, DegradedWhy: why,
			DaemonOwns: state.DaemonRunning(), Vaulted: vaulted, Plans: plans,
			Known: knownAccounts(cfg),
		}, recentSwitches(5), time.Now(), *only)
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
	if serviceSeams.goos == "darwin" {
		_, _ = serviceSeams.run(launchctlPath, "unload", plist)
		if err := os.Remove(plist); err == nil {
			fmt.Printf("  removed %s\n", plist)
		}
	} else {
		_, _ = serviceSeams.run(systemctlPath(), "--user", "disable", "--now", serviceUnit)
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
				id, usage.ShortSeat(b.Meta.Seat()), usage.ShortSeat(want)))
		}
	}
	return strings.Join(problems, "; ")
}
