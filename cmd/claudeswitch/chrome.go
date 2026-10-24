package main

// `cs chrome` (IMPROVEMENTS C1): one Chrome profile per Claude account, so
// Claude in Chrome can be signed in to the account Claude Code is on.
//
// The extension keeps its own claude.ai login, pinned to one account, and
// meets Claude Code on a bridge channel keyed by account uuid. After a
// rotation Claude Code looks for a browser on the new account. claudeswitch
// cannot move the extension's login (writing its storage or Chrome's cookies
// is ruled out), so it gives each account a Chrome profile of its own and
// says which one to use.
//
// The profiles live inside Chrome's default user-data dir, chosen with
// --profile-directory: the native-messaging host manifests Claude in Chrome
// needs are installed there, so a separate --user-data-dir would not see
// them. claudeswitch records only the account → directory mapping, in its
// own state. It never writes Chrome's files and reads only Local State's
// profile list (C2, chrome_local.go): never Preferences, cookies or
// extension storage, and it never decrypts anything. No `cs
// chrome` command reads the keychain either: the email `add` names is the one
// state.json recorded when the account was vaulted or identified.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

const (
	// chromeExtensionID is Claude in Chrome's Web Store id.
	chromeExtensionID = "fcoeoabgfenejglbffodgkkbkcdhcgfn"
	chromeLoginURL    = "https://claude.ai/login"
	chromeStoreURL    = "https://chromewebstore.google.com/detail/" + chromeExtensionID
	// chromeDirPrefix starts every profile directory claudeswitch names.
	chromeDirPrefix = "claudeswitch-"
)

// Seams, so tests pick the platform and get a fixed clock.
var (
	chromeGOOS = runtime.GOOS
	chromeNow  = time.Now
)

// validChromeDir checks a profile directory before it reaches Chrome's argv,
// including one read back from state.json, which is an editable file: a name
// starting with "-" would be a flag, and ".." would leave the user-data dir.
func validChromeDir(dir string) *chromeError {
	if err := config.ValidChromeFolder(dir); err != nil {
		return chromeFail("invalid_value", "forget it and make it again: cs chrome forget <account>, "+
			"then cs chrome add <account>", "%v", err)
	}
	return nil
}

// chromeLinuxBinaries are tried in order on Linux.
var chromeLinuxBinaries = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}

// chromeError is lane 12's appError: the same code, message and hint, and the
// same error object (appcli.go) as every other app action.
type chromeError = appError

func chromeFail(code, hint, format string, a ...any) *chromeError {
	return appErr(code, hint, format, a...)
}

// writeChromeError prints the error object `--json` callers branch on.
func writeChromeError(w io.Writer, e *chromeError) { writeJSONError(w, e) }

func cmdChrome(args []string) error {
	if len(args) > 0 && args[0] == "hint" {
		fs := flag.NewFlagSet("chrome hint", flag.ExitOnError)
		cfgPath := fs.String("config", "", "path to config.toml")
		parseInterleaved(fs, args[1:])
		os.Exit(runChromeHint(os.Stdin, os.Stdout, os.Stderr, *cfgPath))
	}
	asJSON := slices.Contains(args, "--json") || slices.Contains(args, "-json")
	err := runChrome(os.Stdout, os.Stderr, args)
	var ce *chromeError
	if err != nil && asJSON {
		if !errors.As(err, &ce) {
			ce = &chromeError{Code: "failed", Message: err.Error()}
		}
		writeChromeError(os.Stdout, ce)
		os.Exit(1)
	}
	return err
}

const chromeUsage = "usage: claudeswitch chrome add <account> [--existing <folder|name>] | " +
	"chrome forget <account> | chrome [open|signin] [<account>] | chrome list|profiles [--json] | " +
	"chrome check [--profile NAME] [--json]"

// runChrome is `cs chrome` without the exits: add, open, list, forget, and
// a bare account (or nothing) to open.
func runChrome(stdout, stderr io.Writer, args []string) error {
	// Before the flags are parsed: a help flag fails the parse, which left no
	// command, and no command means `open`, so --help opened Chrome.
	if len(args) > 0 && args[0] == "help" || slices.ContainsFunc(args, func(a string) bool {
		return a == "-h" || a == "-help" || a == "--help"
	}) {
		fmt.Fprintln(stdout, chromeUsage)
		return nil
	}
	fs := flag.NewFlagSet("chrome", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "answer with one JSON object")
	existing := fs.String("existing", "", "add: map the account to a Chrome profile you already have (folder or name)")
	profName := fs.String("profile", "", "check: the Claude Code profile to check (default: this shell's)")
	pos := parseInterleaved(fs, args)

	sub := "open"
	if len(pos) > 0 {
		switch pos[0] {
		case "add", "open", "list", "forget", "profiles", "signin", "check":
			sub, pos = pos[0], pos[1:]
		}
	}
	human := stdout
	if *asJSON {
		human = stderr
	}
	usageErr := func() error { return chromeFail("usage", "", chromeUsage) }
	if *existing != "" && sub != "add" || *profName != "" && sub != "check" {
		return usageErr()
	}

	cfg, st, err := load(*cfgPath)
	if err != nil {
		return chromeFail("config_invalid", "", "%v", err)
	}

	switch sub {
	case "check":
		if len(pos) != 0 {
			return usageErr()
		}
		return chromeCheck(stdout, human, cfg, st, *profName, *asJSON)
	case "profiles":
		if len(pos) != 0 {
			return usageErr()
		}
		return chromeProfiles(stdout, cfg, st, *asJSON)
	case "list":
		if len(pos) != 0 {
			return usageErr()
		}
		return chromeList(stdout, cfg, st, *asJSON)
	case "forget":
		if len(pos) != 1 {
			return usageErr()
		}
		id := pos[0]
		cp := st.ChromeOf(id)
		if cp == nil {
			return chromeFail("not_found", "", "account %q has no Chrome profile recorded", id)
		}
		st.DropChrome(id)
		if err := st.Save(); err != nil {
			return chromeFail("failed", "", "could not save the state: %v", err)
		}
		if *asJSON {
			return emitJSONTo(stdout, map[string]any{"account": id, "profile_dir": cp.Dir, "forgotten": true})
		}
		fmt.Fprintf(human, "\n  forgot the Chrome profile %q for %s\n", cp.Dir, id)
		fmt.Fprintf(human, "  the profile itself is still in Chrome; remove it there if you no longer want it\n\n")
		return nil
	case "add":
		if len(pos) != 1 {
			return usageErr()
		}
		if *existing != "" {
			return chromeAddExisting(stdout, human, cfg, st, pos[0], *existing, *asJSON)
		}
		return chromeAdd(stdout, human, cfg, st, pos[0], *asJSON)
	}

	// open and signin
	if len(pos) > 1 {
		return usageErr()
	}
	id, via := "", ""
	if len(pos) == 1 {
		id = pos[0]
	} else {
		in, err := pickProfile(cfg, "")
		if err != nil {
			return chromeFail("not_found", "name the account: cs chrome <account>", "%v", err)
		}
		if ps := st.Profiles[in.Name]; ps != nil {
			id = ps.Active
		}
		if id == "" {
			return chromeFail("not_found", "name the account: cs chrome <account>",
				"no account is known to be live in profile %q", in.Name)
		}
		via = in.Name
	}
	if sub == "signin" {
		return chromeSignin(stdout, human, cfg, st, id, via, *asJSON)
	}
	// IMPROVEMENTS C2: the account's own profile, its profile's, or
	// Chrome's last used.
	r := resolveChrome(cfg, st, readChromeLocal(), via, id)
	if r.Rule == "none" {
		return chromeNothingToOpen(r)
	}
	if err := launchChrome(r.Dir, nil); err != nil {
		return err
	}
	if *asJSON {
		m := r.json()
		m["opened"] = true
		return emitJSONTo(stdout, m)
	}
	if via != "" {
		fmt.Fprintf(human, "\n  opened Chrome profile %q (%s): %s is live in profile %s\n\n", r.label(), r.why(), id, via)
	} else {
		fmt.Fprintf(human, "\n  opened Chrome profile %q for %s (%s)\n\n", r.label(), id, r.why())
	}
	return nil
}

func chromeAdd(stdout, human io.Writer, cfg *config.Config, st *state.State, id string, asJSON bool) error {
	if !hasAccount(cfg, id) && !slices.Contains(st.Vaulted, id) {
		return chromeFail("not_found", "the accounts are listed by: cs accounts",
			"no account %q in the config or the vault", id)
	}
	dir := chromeDirPrefix + id
	created := true
	// A profile add made before is opened again; one the person had
	// (--existing) is replaced by a new one, as asked.
	if cp := st.ChromeOf(id); cp != nil && !cp.Existing {
		dir, created = cp.Dir, false
	}
	if created {
		if err := config.ValidName("Chrome profile directory", dir); err != nil {
			return chromeFail("invalid_value", "rename the account to a shorter id (cs rename), then try again", "%v", err)
		}
	}
	if err := launchChrome(dir, []string{chromeLoginURL, chromeStoreURL}); err != nil {
		return err
	}
	if created {
		st.SetChrome(id, dir, chromeNow())
		if err := st.Save(); err != nil {
			return chromeFail("failed", "", "Chrome opened, but the mapping could not be saved: %v", err)
		}
	}
	email := st.EmailOf(id)

	if asJSON {
		var e any
		if email != "" {
			e = email
		}
		return emitJSONTo(stdout, map[string]any{"account": id, "profile_dir": dir, "created": created,
			"opened": true, "email": e, "urls": []string{chromeLoginURL, chromeStoreURL}})
	}
	as := id
	if email != "" {
		as = email + " (" + id + ")"
	}
	if created {
		fmt.Fprintf(human, "\n  opened a new Chrome profile for %s (directory %q)\n", id, dir)
	} else {
		fmt.Fprintf(human, "\n  opened the Chrome profile for %s again (directory %q)\n", id, dir)
	}
	fmt.Fprintf(human, "\n  In the new Chrome window:\n")
	fmt.Fprintf(human, "    1. sign in to claude.ai as %s\n", as)
	fmt.Fprintf(human, "    2. in the Web Store tab, add (or enable) Claude in Chrome\n")
	fmt.Fprintf(human, "    3. open the extension and sign it in, as %s too\n", as)
	if others := sameEmailAccounts(st, id); email != "" && len(others) > 0 {
		// The extension stays bound to the organization it is first opened
		// in (seen 2026-10-23 with alice@example.com, whose extension never
		// left its own organization; anthropics/claude-code issue 82455).
		fmt.Fprintf(human, "\n  %s is also %s (%s), in another organization. Before the extension is first\n",
			email, strings.Join(others, ", "), map[bool]string{true: "its other seat", false: "its other seats"}[len(others) == 1])
		fmt.Fprintf(human, "  opened, switch claude.ai to %s's organization: it stays bound to the one it is\n", id)
		fmt.Fprintf(human, "  first opened in.\n")
	}
	fmt.Fprintf(human, "\n  Chrome names the profile itself; rename it there if you like.\n")
	fmt.Fprintf(human, "  From now on, `cs chrome %s` opens this profile.\n\n", id)
	return nil
}

func chromeList(stdout io.Writer, cfg *config.Config, st *state.State, asJSON bool) error {
	entries := st.ChromeList()
	local := readChromeLocal()
	liveIn := func(id string) []string {
		out := []string{}
		for _, in := range cfg.EffectiveProfiles() {
			if ps := st.Profiles[in.Name]; ps != nil && ps.Active == id {
				out = append(out, in.Name)
			}
		}
		return out
	}
	if asJSON {
		list := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			var added any
			if !e.Added.IsZero() {
				added = e.Added.UTC().Format(time.RFC3339)
			}
			list = append(list, map[string]any{"account": e.Account, "profile_dir": e.Dir,
				"added": added, "live_in": liveIn(e.Account), "existing": e.Existing,
				"name": orNull(local.nameOf(e.Dir))})
		}
		return emitJSONTo(stdout, map[string]any{"chrome_profiles": list,
			"supported": chromeSupported(chromeGOOS), "live": chromeLiveJSON(cfg, st, local)})
	}
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "\n  no Chrome profiles yet; make one with: cs chrome add <account>\n\n")
		return nil
	}
	fmt.Fprintln(stdout)
	for _, e := range entries {
		live := ""
		if in := liveIn(e.Account); len(in) > 0 {
			live = "  live in " + strings.Join(in, ", ")
		}
		fmt.Fprintf(stdout, "  %-16s %s%s\n", e.Account, chromeShow(local, e.Dir), live)
	}
	fmt.Fprintln(stdout)
	return nil
}

func emitJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func chromeSupported(goos string) bool { return goos == "darwin" || goos == "linux" }

// chromeArgv is the command that opens Chrome on that profile directory.
// Always an argv, never a shell line.
func chromeArgv(goos, dir string, urls []string) ([]string, error) {
	flagDir := "--profile-directory=" + dir
	switch goos {
	case "darwin":
		// -n: a new invocation, so --args reach Chrome even when it is
		// already running (it hands them to the running instance).
		return append([]string{"open", "-na", "Google Chrome", "--args", flagDir}, urls...), nil
	case "linux":
		for _, b := range chromeLinuxBinaries {
			if _, err := exec.LookPath(b); err == nil {
				return append([]string{b, flagDir}, urls...), nil
			}
		}
		return nil, chromeFail("not_found", "install Google Chrome, or put it on PATH as one of: "+
			strings.Join(chromeLinuxBinaries, ", "), "no Chrome or Chromium found on PATH")
	}
	return nil, chromeFail("unsupported_platform", "",
		"cs chrome supports macOS and Linux; this is %s", goos)
}

// chromeArgvBackground is the command that opens Chrome on that profile
// directory without taking focus (IMPROVEMENTS C4): after a switch the window
// comes up behind whatever the person is doing. Linux has no such switch;
// Chrome opens the window as it does.
func chromeArgvBackground(goos, dir string) ([]string, error) {
	argv, err := chromeArgv(goos, dir, nil)
	if err != nil || goos != "darwin" {
		return argv, err
	}
	return append([]string{"open", "-g"}, argv[1:]...), nil
}

// launchChrome runs the launcher. On macOS `open` returns once Chrome has the
// request. On Linux the binary may itself become the browser, so it is
// started in its own session and waited for only briefly: an early exit is
// an error to report, a process still running is Chrome.
func launchChrome(dir string, urls []string) error {
	if e := validChromeDir(dir); e != nil {
		return e
	}
	argv, err := chromeArgv(chromeGOOS, dir, urls)
	if err != nil {
		return err
	}
	return runChromeLauncher(argv)
}

// launchChromeBackground opens a profile behind the current app, with no
// pages: its extension starts and connects (C4).
func launchChromeBackground(dir string) error {
	if e := validChromeDir(dir); e != nil {
		return e
	}
	argv, err := chromeArgvBackground(chromeGOOS, dir)
	if err != nil {
		return err
	}
	return runChromeLauncher(argv)
}

func runChromeLauncher(argv []string) error {
	c := exec.Command(argv[0], argv[1:]...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	if chromeGOOS == "darwin" {
		if err := c.Run(); err != nil {
			return chromeFail("failed", strings.TrimSpace(out.String()), "could not open Chrome: %v", err)
		}
		return nil
	}
	c.Stdout, c.Stderr = nil, nil
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return chromeFail("failed", "", "could not start %s: %v", argv[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return chromeFail("failed", "", "%s exited: %v", argv[0], err)
		}
	case <-time.After(3 * time.Second):
	}
	return nil
}

// chromeHintPhrases are the failures that mean the extension and Claude Code
// are not on one account (or no browser answers on this account's channel).
var chromeHintPhrases = []string{"same claude.ai account", "not connected"}

// runChromeHint is the plugin hook after a Claude in Chrome tool call. When
// the tool failed with the same-account or not-connected error, it opens the
// live account's own Chrome profile (C4) and tells Claude which account
// Claude Code is on and to retry, once per rotation of the caller's profile;
// it never asks for a sign-in. It never fails the tool call: every other
// case exits 0 silently.
//
// Only PostToolUseFailure counts, and only its `error` field is read. A
// successful call's response is page content: a page saying "not connected"
// (a router, a VPN, a hostile page) must neither produce a hint nor use up
// the rotation's one hint (lane 13 review). The hint goes to stderr with
// exit 2, which Claude Code shows to Claude; stdout is unused.
func runChromeHint(stdin io.Reader, stdout, stderr io.Writer, cfgPath string) int {
	_ = stdout
	var in struct {
		Event string `json:"hook_event_name"`
		Tool  string `json:"tool_name"`
		Error string `json:"error"`
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 4<<20))
	if err != nil || json.Unmarshal(b, &in) != nil {
		return 0
	}
	if in.Event != "PostToolUseFailure" || !strings.HasPrefix(in.Tool, "mcp__claude-in-chrome__") {
		return 0
	}
	text := strings.ToLower(in.Error)
	if !slices.ContainsFunc(chromeHintPhrases, func(p string) bool { return strings.Contains(text, p) }) {
		return 0
	}
	cfg, err := config.Load(cfgPath)
	if err != nil && cfg == nil {
		return 0
	}
	prof, err := pickProfile(cfg, "")
	if err != nil {
		return 0
	}
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		return 0
	}
	ps := st.Profiles[prof.Name]
	if ps == nil || ps.Active == "" {
		return 0
	}
	id := ps.Active
	key := id + "@" + ps.LastSwitch.UTC().Format(time.RFC3339Nano)
	if st.ChromeHinted(prof.Name) == key {
		return 0
	}
	st.SetChromeHinted(prof.Name, key)
	if err := st.Save(); err != nil {
		return 0 // unsaved, it would repeat on every call; say nothing
	}

	on := ""
	if multiProfile(cfg) {
		on = " (profile " + prof.Name + ")"
	}
	msg := fmt.Sprintf("[claudeswitch] Claude in Chrome reaches only a browser signed in to the account Claude Code "+
		"is on, %s%s. ", id, on)
	if cp := st.ChromeOf(id); cp != nil {
		// C4: open it rather than tell anyone how to (owner decision
		// 2026-10-23), once per rotation like the hint itself.
		name := readChromeLocal().nameOf(cp.Dir)
		if name == "" {
			name = cp.Dir
		}
		if err := launchChromeBackground(cp.Dir); err != nil {
			msg += fmt.Sprintf("claudeswitch could not open %s's Chrome profile %q (%v). ", id, name, err)
		} else {
			msg += fmt.Sprintf("claudeswitch has just opened %s's Chrome profile %q, whose extension is signed in "+
				"as %s: retry the browser call in a few seconds. ", id, name, id)
		}
		msg += fmt.Sprintf("Do not ask the person to sign in to Claude in Chrome. If the call still says not "+
			"connected, this session started on another account and keeps that connection (GROUND_TRUTH §56): "+
			"ask the person to run /mcp, choose claude-in-chrome and Reconnect, which moves this session to %s's "+
			"browser (if several browsers are then listed, select_browser the one on this computer); a new "+
			"session, such as `claude --resume`, reaches it too. Without the person, run the browser step in a "+
			"fresh process, which always reaches the live account's browser: echo '<the whole browser task>' | "+
			"claude -p --chrome --allowedTools mcp__claude-in-chrome (`cs chrome check` shows what a fresh "+
			"connection finds).", id)
	} else {
		msg += fmt.Sprintf("%s has no Chrome profile of its own, so no browser follows it there. Do not ask the "+
			"person to sign in to Claude in Chrome; the lasting fix is a Chrome profile for %s, set up once "+
			"with `cs chrome add %s`.", id, id, id)
	}
	fmt.Fprintln(stderr, msg)
	return 2
}

// sameEmailAccounts are the other accounts recorded with id's email: the
// same person's seats in other organizations.
func sameEmailAccounts(st *state.State, id string) []string {
	email := st.EmailOf(id)
	if email == "" {
		return nil
	}
	var out []string
	for other, e := range st.Emails {
		if other != id && strings.EqualFold(e, email) {
			out = append(out, other)
		}
	}
	slices.Sort(out)
	return out
}

// offerChromePerAccount is setup's offer of a Chrome profile per account
// (P2): the setup in which Claude in Chrome follows rotation by itself,
// because the bridge finds the browser signed in as the live account. It
// is offered, not assumed: each new profile opens Chrome and needs one
// sign-in. Accounts that have a Chrome profile already are left as they are.
func offerChromePerAccount(w io.Writer, cfg *config.Config, st *state.State,
	ask func(prompt string, def bool) bool, add func(id string) error) {
	if len(cfg.Accounts) < 2 {
		return
	}
	fmt.Fprintf(w, "\n  Claude in Chrome follows a rotation by itself when each account has a\n")
	fmt.Fprintf(w, "  Chrome profile of its own, signed in once; otherwise the shared one needs\n")
	fmt.Fprintf(w, "  signing in again after each rotation.\n")
	if !ask("  Give each account its own Chrome profile now", false) {
		fmt.Fprintf(w, "    later, one at a time:  claudeswitch chrome add <account>\n")
		return
	}
	for _, a := range cfg.Accounts {
		if st.ChromeOf(a.ID) != nil {
			continue
		}
		if err := add(a.ID); err != nil {
			fmt.Fprintf(w, "    %s: %v\n", a.ID, err)
		}
	}
	fmt.Fprintf(w, "    in each window, sign in to claude.ai and to Claude in Chrome as its account\n")
}
