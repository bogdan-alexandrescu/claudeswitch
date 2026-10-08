package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/cclock"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// I7: `cs profile create` makes a Claude Code profile in one command, `cs run`
// launches Claude Code in one, and `cs profile list` shows them.

// createOptions is `cs profile create <name> [--dir] [--pool] [--seed]`.
type createOptions struct {
	cfgPath string
	name    string
	dir     string // "" is ~/.claude-<name>
	pool    []string
	seed    string // an account to install as the new profile's live credential
}

// seedVault is the part of the vault seeding needs: the vaulted credential,
// and the §3 check's view of every other profile's item.
type seedVault interface {
	Load(accountID string) (*keychain.OAuth, error)
	holdsChecker
}

// Seams, so tests reach neither the keychain nor a real Claude Code.
var (
	newSeedVault       = func() seedVault { return vault.New(logger(false)) }
	liveExistsFor      = keychain.LiveExistsFor
	createLiveItem     = keychain.CreateLiveItem
	resolveLiveService = keychain.LiveServiceFor
	// execClaude replaces this process with Claude Code.
	execClaude = func(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
	// runNotes is where `run` says a profile is not signed in yet.
	runNotes io.Writer = os.Stderr
	// A seed waits up to seedWait for a running daemon (daemonRunning) to
	// load the new profile, looking every seedPoll.
	seedWait = 10 * time.Second
	seedPoll = 100 * time.Millisecond
)

// sharedBase is what a new profile shares with ~/.claude, by symlink: the
// person's own setup, which they would otherwise copy by hand and then keep
// in step. Never shared: projects/ (transcripts, which is how the daemon
// tells profiles' activity apart), history, plugins, and the credential.
var sharedBase = []string{"settings.json", "CLAUDE.md", "skills", "commands", "agents"}

// profileCreate makes a profile: its dir, the shared setup, a copy of the
// user's MCP servers, its [[profile]] block and, with a seed, its live
// credential. Every check is made before anything is written, so a refusal
// leaves no dir and no config edit behind.
func profileCreate(w io.Writer, o createOptions) error {
	if err := config.ValidName("profile name", o.name); err != nil {
		return wrapErr(codeInvalidValue, "", err)
	}
	cfg, st, err := load(o.cfgPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.Path); err != nil {
		return appErr(codeNoConfig, "", "no config at %s to add the profile to; run `claudeswitch setup` first", cfg.Path)
	}
	if _, ok := cfg.ProfileNamed(o.name); ok {
		return appErr(codeInvalidValue, "", "profile %q already exists", o.name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, ".claude")
	dir, err := newProfileDir(o.dir, home, o.name)
	if err != nil {
		return err
	}
	if err := checkNewDir(cfg, base, dir); err != nil {
		return err
	}
	if err := checkNewPool(cfg, o.pool); err != nil {
		return err
	}

	var v seedVault
	if o.seed != "" {
		v = newSeedVault()
		if err := checkSeed(cfg, st, v, o, dir); err != nil {
			return err
		}
	}

	// Checks done; from here on things are made.
	made := false
	var warnings []string
	if fi, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		made = true
	} else if err == nil && fi.Mode().Perm() != 0o700 {
		warnings = append(warnings, fmt.Sprintf("%s is mode %04o, not 0700: others may read the MCP "+
			"server settings copied into it, which can carry secrets; chmod 700 %s", dir,
			fi.Mode().Perm(), shellQuote(dir)))
	}
	if root := gitWorkTree(dir); root != "" {
		warnings = append(warnings, fmt.Sprintf("%s is inside the git work tree %s: keep its "+
			".claude.json (MCP server settings, which can carry secrets) and .credentials.json out of "+
			"commits", dir, root))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if made {
		_ = os.Chmod(dir, 0o700) // MkdirAll's mode is subject to the umask
	}
	linked, kept, err := shareBase(base, dir)
	if err != nil {
		return fmt.Errorf("made %s, but sharing ~/.claude into it failed: %w", dir, err)
	}
	mcpNote, err := mirrorMCP(filepath.Join(home, ".claude.json"), filepath.Join(dir, ".claude.json"))
	if err != nil {
		return fmt.Errorf("made %s, but copying your MCP servers failed: %w", dir, err)
	}
	addedDefault, defaultDir, err := writeProfileBlock(cfg, o.name, dir, o.pool)
	if err != nil {
		return fmt.Errorf("made %s, but the config was not changed: %w", dir, err)
	}

	fmt.Fprintf(w, "\n  ✓ profile %s\n", o.name)
	if made {
		fmt.Fprintf(w, "    dir     %s (new)\n", dir)
	} else {
		fmt.Fprintf(w, "    dir     %s (existing; nothing in it was replaced)\n", dir)
	}
	fmt.Fprintf(w, "    pool    %s\n", nonEmpty(strings.Join(o.pool, ", "), "(empty)"))
	if len(linked) > 0 {
		fmt.Fprintf(w, "    linked  %s, from ~/.claude\n", strings.Join(linked, ", "))
	}
	if len(kept) > 0 {
		fmt.Fprintf(w, "    kept    %s, already in the dir\n", strings.Join(kept, ", "))
	}
	fmt.Fprintf(w, "    not shared: transcripts, history, plugins, credentials\n")
	fmt.Fprintf(w, "    %s\n", mcpNote)
	switch {
	case addedDefault && defaultDir != "":
		fmt.Fprintf(w, "    the config had no [[profile]] blocks, so a %q one was declared too, on this\n"+
			"    shell's CLAUDE_CONFIG_DIR (%s); the accounts in no pool stay in it\n",
			config.DefaultProfile, defaultDir)
	case addedDefault:
		fmt.Fprintf(w, "    the config had no [[profile]] blocks, so a %q one (CLAUDE_CONFIG_DIR unset) was\n"+
			"    declared too; the accounts in no pool stay in it\n", config.DefaultProfile)
	}
	fmt.Fprintf(w, "    written to %s; a running daemon picks it up without a restart\n", cfg.Path)
	for _, warn := range warnings {
		fmt.Fprintf(w, "    warning: %s\n", warn)
	}

	if o.seed != "" {
		if err := seedWhenLoaded(w, cfg.Path, v, o.name, dir, dir, o.seed); err != nil {
			return fmt.Errorf("the profile was made, but not seeded: %w", err)
		}
	} else {
		fmt.Fprintf(w, "\n    not signed in yet: `cs run %s` and /login there, or from the vault:\n"+
			"    claudeswitch profile seed %s <account>\n", o.name, o.name)
	}
	fmt.Fprintf(w, "\n    start it: cs run %s\n", o.name)
	fmt.Fprintf(w, "    or yourself: CLAUDE_CONFIG_DIR=%s claude\n\n", shellQuote(dir))
	return nil
}

// seedWhenLoaded seeds profile name once a running daemon has loaded it
// (owner decision, lane 10 security review): until then the daemon's §3
// checks do not know the profile, and could swap the seeded account into
// another one meanwhile. With no daemon running it seeds at once. Either way
// the config and state are read afresh for the §3 check.
func seedWhenLoaded(w io.Writer, cfgPath string, v seedVault, name, hashDir, fsDir, id string) error {
	if daemonRunning() {
		fmt.Fprintf(w, "\n    waiting for the daemon to load profile %s…\n", name)
		hash := ""
		if c, err := config.Load(cfgPath); err == nil {
			hash = c.Hash
		}
		if !waitDaemonLoaded(name, hashDir, hash) {
			return appErr(codeDaemonNotLoaded, "", "the running daemon did not load profile %q within %s (it may be older than "+
				"this cs, or the config did not reload), and seeding before it has would let it swap %s "+
				"in elsewhere meanwhile, so no credential was written. Once `claudeswitch status` shows the "+
				"profile, run: claudeswitch profile seed %s %s", name, seedWait, id, name, id)
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("the config would not load back: %w", err)
	}
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		return err
	}
	return seedProfile(w, cfg, st, v, name, hashDir, fsDir, id)
}

// waitDaemonLoaded polls state until the daemon records profile name on
// dir, running the config whose content hash is hash: the profile alone
// could be a marker from an earlier run (lane 12 re-review).
func waitDaemonLoaded(name, dir, hash string) bool {
	deadline := time.Now().Add(seedWait)
	for {
		if st, err := state.Load(""); err == nil && hash != "" && st.DaemonConfigHash == hash {
			if d, ok := st.DaemonProfiles[name]; ok && d == dir {
				return true
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(min(seedPoll, time.Until(deadline)))
	}
}

// profileSeed is `profile seed <name> <account>`: the seed of an existing
// profile, with every check `profile create --seed` makes.
func profileSeed(w io.Writer, cfgPath, name, id string) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return err
	}
	in, ok := cfg.ProfileNamed(name)
	if !ok {
		return appErr(codeNotFound, "", "no profile named %q; the profiles are %s", name,
			strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	if in.Dir == "" || in.FromEnv {
		return fmt.Errorf("profile %q has no dir; only a profile's own suffixed credential is seeded, "+
			"never the one Claude Code uses with CLAUDE_CONFIG_DIR unset", name)
	}
	fsDir, err := ccdir.ExpandHome(in.Dir)
	if err != nil {
		return err
	}
	v := newSeedVault()
	o := createOptions{name: name, pool: in.Pool, seed: id}
	if err := checkSeed(cfg, st, v, o, in.Dir); err != nil {
		return err
	}
	if err := os.MkdirAll(fsDir, 0o700); err != nil {
		return err
	}
	return seedWhenLoaded(w, cfg.Path, v, name, in.Dir, fsDir, id)
}

// shellQuote quotes s for a POSIX shell, always: a printed command must
// paste back as one word whatever the path holds.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// gitWorkTree is the root of the git work tree holding dir, or "".
func gitWorkTree(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// newProfileDir is the new profile's dir, absolute. It is written to the
// config absolute too: the keychain item is hashed from CLAUDE_CONFIG_DIR as
// Claude Code sees it, and `cs run` sets exactly the configured string, so
// an absolute one names the same item however the person later launches it
// from a shell (which expands ~ before Claude Code sees it).
func newProfileDir(flagDir, home, name string) (string, error) {
	if flagDir == "" {
		return filepath.Join(home, ".claude-"+name), nil
	}
	if strings.HasPrefix(flagDir, "~") && flagDir != "~" && !strings.HasPrefix(flagDir, "~/") {
		// ~user is the shell's to expand, not ours: read here it would be a
		// relative path named "~user".
		return "", fmt.Errorf("--dir %s: write another user's home out in full, or use ~/ for your own", flagDir)
	}
	d, err := ccdir.ExpandHome(flagDir)
	if err != nil {
		return "", err
	}
	if d, err = filepath.Abs(d); err != nil {
		return "", err
	}
	return filepath.Clean(d), nil
}

// checkNewDir refuses a dir that is, holds or sits inside the base
// ~/.claude (inside it, the links made from it would loop: skills/ inside
// skills/), another profile's dir, or this shell's CLAUDE_CONFIG_DIR, which
// is the implicit profile's — compared as real paths, through links and,
// on macOS, case (owner decision, lane 10 security review). Also a path
// that is not a directory.
func checkNewDir(cfg *config.Config, base, dir string) error {
	type taken struct{ what, dir string }
	all := []taken{{"the base setup the new profile shares from", base}}
	if env := os.Getenv(ccdir.EnvConfigDir); env != "" {
		all = append(all, taken{"this shell's CLAUDE_CONFIG_DIR", env})
	}
	for _, in := range cfg.EffectiveProfiles() {
		if in.FromEnv {
			continue // the base and this shell's dir, both above
		}
		d := in.Dir
		if d == "" {
			d = base
		}
		all = append(all, taken{fmt.Sprintf("profile %q's dir", in.Name), d})
	}
	nf := config.RealFolder(dir)
	for _, t := range all {
		tf := config.RealFolder(t.dir)
		switch nf.Overlap(tf) {
		case "same":
			return appErr(codeInvalidValue, "", "%s is the same folder as %s (%s → %s); give the profile its own dir",
				dir, t.what, t.dir, tf.Real)
		case "inside":
			return appErr(codeInvalidValue, "", "%s is inside %s (%s); give the profile a dir of its own, outside it",
				dir, t.what, tf.Real)
		case "contains":
			return appErr(codeInvalidValue, "", "%s holds %s (%s); give the profile a dir of its own, outside it",
				dir, t.what, tf.Real)
		}
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return appErr(codeInvalidValue, "", "%s exists and is not a directory", dir)
	}
	return nil
}

// checkNewPool is D1 for the new pool: configured accounts, each once, none
// listed in another profile's pool. An account no pool lists joins default
// only by rule (D6), and the new pool may take it; naming it in a pool is
// how an account leaves default.
func checkNewPool(cfg *config.Config, pool []string) error {
	seen := map[string]bool{}
	for _, id := range pool {
		if seen[id] {
			return appErr(codeInvalidValue, "", "--pool names %q twice", id)
		}
		seen[id] = true
		if !configured(cfg, id) {
			return appErr(codeNotFound, "", "--pool names %q, which the config does not have; sign it in first "+
				"(`claudeswitch login %s --direct`)", id, id)
		}
		for _, in := range cfg.Profiles {
			for _, p := range in.Pool {
				if p == id {
					return appErr(codeInOtherPool, "", "account %q is in the pool of profile %q; pools must not overlap, since "+
						"one credential live in two profiles is logged out by whichever refreshes first",
						id, in.Name)
				}
			}
		}
	}
	return nil
}

// checkSeed is every check a seed must pass before anything is made: the
// account is in the new pool, vaulted and renewable; the new profile has no
// credential yet; and the account is live nowhere else (§3), unknown
// counting as live (D18), ghosts included (D22, D25).
func checkSeed(cfg *config.Config, st *state.State, v seedVault, o createOptions, dir string) error {
	inPool := false
	for _, id := range o.pool {
		inPool = inPool || id == o.seed
	}
	if !inPool {
		return appErr(codeOutsidePool, "", "--seed %s: the account must be in the new profile's pool (--pool %s)", o.seed, o.seed)
	}
	cred, err := v.Load(o.seed)
	if err != nil {
		return fmt.Errorf("--seed %s: %w", o.seed, err)
	}
	if cred.RefreshDead() {
		return fmt.Errorf("--seed %s: its vaulted credential has expired and needs a login "+
			"(`claudeswitch login %s --direct`)", o.seed, o.seed)
	}
	exists, err := liveExistsFor(dir)
	if err != nil {
		return fmt.Errorf("--seed %s: could not tell whether %s already has a credential, so none is "+
			"written: %w", o.seed, dir, err)
	}
	if exists {
		return fmt.Errorf("--seed %s: %s already has a Claude Code credential; seeding only ever "+
			"creates one. To put %s there, use `claudeswitch use %s --profile %s` once it exists",
			o.seed, dir, o.seed, o.seed, o.name)
	}
	return seedConflict(cfg, st, v, o.name, o.seed)
}

// seedConflict is the §3 check for a seed, the one `use` and the daemon
// make: is the account, or may it be, live in any profile but self?
func seedConflict(cfg *config.Config, st *state.State, v holdsChecker, self, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if other, why, at := liveElsewhereOf(ctx, v, cfg, st, self, cliGhostTargets(cfg, st), id); other != "" {
		if !at.IsZero() {
			return rateLimitedRefusal(id, other, at)
		}
		return appErr(codeLive, "", "--seed %s: the account may be live in %s (%s); one credential live in two "+
			"profiles is logged out by whichever refreshes first, so nothing was made",
			id, whereLiveQ(other), why)
	}
	return nil
}

// seedProfile installs id's vaulted credential as the new profile's live
// credential: the item Claude Code reads for hashDir (the dir as
// configured, which Claude Code hashes), created (never updated) while
// holding Claude Code's credential locks for fsDir (the same dir on disk,
// GROUND_TRUTH §43), then confirmed by resolving the item the way every
// later command will. cfg and st are fresh from disk (seedWhenLoaded).
func seedProfile(w io.Writer, cfg *config.Config, st *state.State, v seedVault, name, hashDir, fsDir, id string) error {
	// Again, against the config and state now on disk: time has passed
	// since the first check, and a daemon may have swapped meanwhile.
	if err := seedConflict(cfg, st, v, name, id); err != nil {
		return err
	}
	cred, err := v.Load(id)
	if err != nil {
		return err
	}
	service := keychain.LiveServiceName(hashDir)
	file := filepath.Join(fsDir, ".credentials.json")
	cp := *cred
	blob := &keychain.Blob{ClaudeAIOAuth: &cp} // no vault metadata, no MCP logins

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	held, err := cclock.Acquire(ctx, fsDir, cclock.DefaultWait)
	if err != nil {
		return err
	}
	werr := createLiveItem(service, file, blob)
	_ = held.Release()
	if werr != nil {
		return werr
	}
	got, err := resolveLiveService(hashDir)
	if err != nil {
		return fmt.Errorf("wrote %s, but resolving the profile's credential afterwards failed: %w", service, err)
	}
	if got != service {
		return fmt.Errorf("wrote %s, but resolving the profile's credential finds %q; Claude Code may "+
			"not read what was written", service, got)
	}

	ps := st.Profile(name)
	ps.SetActive(id)
	ps.LastSwitch = time.Now()
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	if aud, err := audit.Open(""); err == nil {
		_ = aud.Write(audit.Event{Kind: "switch", Profile: name, To: id,
			Reason: "seeded from the vault (`cs profile create --seed` or `cs profile seed`)"})
	}
	fmt.Fprintf(w, "\n    ✓ signed in as %s: its vaulted credential is now this profile's (%s)\n",
		id, keychain.LiveItem(service, file).Name())
	return nil
}

// shareBase links each of sharedBase present in base into dir, leaving
// anything already in dir alone. It reports what it linked and kept.
func shareBase(base, dir string) (linked, kept []string, err error) {
	for _, name := range sharedBase {
		src := filepath.Join(base, name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			kept = append(kept, name)
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return linked, kept, err
		}
		linked = append(linked, name)
	}
	return linked, kept, nil
}

// mirrorMCP copies the user-scope MCP servers from src (~/.claude.json) into
// a new dst holding that key alone. Nothing else is copied: src also holds
// the signed-in account and per-project state, which belong to the base
// profile. An existing dst is left as it is.
func mirrorMCP(src, dst string) (string, error) {
	raw, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return "MCP     none to copy (no ~/.claude.json)", nil
	}
	if err != nil {
		return "", err
	}
	var g map[string]json.RawMessage
	if err := json.Unmarshal(raw, &g); err != nil {
		return "", fmt.Errorf("reading %s: %w", src, err)
	}
	var servers map[string]json.RawMessage
	if s, ok := g["mcpServers"]; ok {
		if err := json.Unmarshal(s, &servers); err != nil {
			return "", fmt.Errorf("reading mcpServers in %s: %w", src, err)
		}
	}
	if len(servers) == 0 {
		return "MCP     none to copy (no user-scope servers in ~/.claude.json)", nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Sprintf("MCP     kept the existing %s; your %d user-scope server(s) were not copied", dst,
			len(servers)), nil
	}
	out, err := json.MarshalIndent(map[string]json.RawMessage{"mcpServers": g["mcpServers"]}, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(append(out, '\n')); err != nil {
		f.Close()
		os.Remove(dst)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(dst)
		return "", err
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	return fmt.Sprintf("MCP     copied %d user-scope server(s) (a copy: later changes are per profile); "+
		"servers that sign in with OAuth need you to sign in again in this profile (/mcp)", len(names)), nil
}

// writeProfileBlock appends the new [[profile]] block to the config, as a
// text edit parsed back before it replaces the file (writeConfigFile: mode
// and symlink kept). A config with no [[profile]] blocks gets a "default"
// one first: without it, every account left out of the new pool would join
// no profile and the config would not load (D6). That block keeps the
// profile the implicit one was (owner decision, lane 10 security review):
// with this shell's CLAUDE_CONFIG_DIR set, its dir is that value as
// written, which it also returns; unset, it has no dir.
func writeProfileBlock(cfg *config.Config, name, dir string, pool []string) (bool, string, error) {
	target, err := configTarget(cfg.Path)
	if err != nil {
		return false, "", err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return false, "", err
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(string(raw), "\n"))
	b.WriteString("\n")
	addDefault := len(cfg.Profiles) == 0
	defaultDir := ""
	if addDefault {
		b.WriteString("\n[[profile]]\nname = \"" + config.DefaultProfile + "\"\n")
		if defaultDir = os.Getenv(ccdir.EnvConfigDir); defaultDir != "" {
			b.WriteString("# this shell's CLAUDE_CONFIG_DIR when the profile was made; accounts in no pool join it\n")
			fmt.Fprintf(&b, "dir  = %s\n", strconv.Quote(defaultDir))
		} else {
			b.WriteString("# no dir: Claude Code runs with CLAUDE_CONFIG_DIR unset; accounts in no pool join it\n")
		}
	}
	b.WriteString("\n[[profile]]\n")
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(name))
	fmt.Fprintf(&b, "dir  = %s\n", strconv.Quote(dir))
	q := make([]string, len(pool))
	for i, id := range pool {
		q[i] = strconv.Quote(id)
	}
	fmt.Fprintf(&b, "pool = [%s]\n", strings.Join(q, ", "))

	return addDefault, defaultDir, writeConfigFile(target, []byte(b.String()), func(c *config.Config) error {
		in, ok := c.ProfileNamed(name)
		if !ok || in.Dir != dir || strings.Join(in.Pool, "\x00") != strings.Join(pool, "\x00") {
			return fmt.Errorf("the edit parsed but profile %q did not come out as written; discarded", name)
		}
		if addDefault {
			if def, ok := c.ProfileNamed(config.DefaultProfile); !ok || def.Dir != defaultDir {
				return fmt.Errorf("the edit parsed but profile %q did not come out as written; discarded",
					config.DefaultProfile)
			}
		}
		return nil
	})
}

// cmdProfileCreate parses `profile create`.
func cmdProfileCreate(args []string) error {
	const usage = "usage: claudeswitch profile create <name> [--dir PATH] [--pool a,b] [--seed <account>] [--json]"
	fs := appFlags("profile create")
	cfgPath := fs.String("config", "", "path to config.toml")
	dir := fs.String("dir", "", "its CLAUDE_CONFIG_DIR (default ~/.claude-<name>)")
	pool := fs.String("pool", "", "comma-separated accounts it rotates within")
	seed := fs.String("seed", "", "a vaulted account from the pool to sign it in with")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, err := parseApp(fs, args, usage)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return appErr(codeUsage, "", usage)
	}
	var ids []string
	for _, id := range strings.Split(*pool, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	o := createOptions{cfgPath: *cfgPath, name: positional[0], dir: *dir, pool: ids, seed: *seed}
	if !*asJSON {
		return profileCreate(os.Stdout, o)
	}
	if err := profileCreate(os.Stderr, o); err != nil {
		return err
	}
	cfg, st, err := load(*cfgPath)
	if err != nil {
		return err
	}
	in, _ := cfg.ProfileNamed(o.name)
	m := profileJSON(cfg, st, in)
	m["seeded"] = orNull(o.seed)
	return emitJSON(m)
}

// profileList prints each profile: its dir, pool and the account recorded
// live in it.
func profileList(w io.Writer, cfgPath string) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return err
	}
	fmt.Fprintln(w)
	for _, in := range cfg.EffectiveProfiles() {
		dir := in.Dir
		switch {
		case in.FromEnv:
			dir = "this shell's CLAUDE_CONFIG_DIR (no [[profile]] blocks)"
		case dir == "":
			dir = "~/.claude (CLAUDE_CONFIG_DIR unset)"
		}
		live := "none recorded"
		if ps := st.Profiles[in.Name]; ps != nil && ps.Active != "" {
			live = ps.Active
		}
		if _, err := cliLiveFor(in); err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				live += "; not signed in"
			} else {
				live += "; its credential could not be looked up"
			}
		}
		fmt.Fprintf(w, "  %s\n", in.Name)
		fmt.Fprintf(w, "    dir   %s\n", dir)
		fmt.Fprintf(w, "    pool  %s\n", nonEmpty(strings.Join(in.Pool, ", "), "(empty)"))
		fmt.Fprintf(w, "    live  %s\n", live)
	}
	fmt.Fprintln(w)
	return nil
}

// cmdRun is `cs run <profile> [-- claude args]`: Claude Code in that
// profile, with CLAUDE_CONFIG_DIR set to its dir (unset for the profile with
// none, D11). The daemon needs telling nothing; it watches the config.
func cmdRun(args []string) error {
	var claudeArgs []string
	for i, a := range args {
		if a == "--" {
			claudeArgs = args[i+1:]
			args = args[:i]
			break
		}
	}
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	positional := parseInterleaved(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: claudeswitch run <profile> [-- claude arguments]")
	}
	cfg, _, err := load(*cfgPath)
	if err != nil {
		return err
	}
	in, ok := cfg.ProfileNamed(positional[0])
	if !ok {
		return fmt.Errorf("no profile named %q; the profiles are %s", positional[0],
			strings.Join(quoteAll(cfg.ProfileNames()), ", "))
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude is not on PATH: %w", err)
	}
	env := envForProfile(in)
	if env == nil {
		env = os.Environ()
	}
	if note := runNote(in); note != "" {
		fmt.Fprintln(runNotes, note)
	}
	return execClaude(path, append([]string{"claude"}, claudeArgs...), env)
}

// runNote is what `run` says before starting a profile whose live
// credential cannot be found. It starts it anyway (owner decision, lane 10
// security review): /login inside it is how a new profile is signed in, and
// the daemon picks the credential up from there.
func runNote(in config.Profile) string {
	live, err := cliLiveFor(in)
	if err == nil && runtime.GOOS == "linux" {
		// On Linux resolution always succeeds; the file is the credential.
		if r, ok := live.(keychain.ItemRefer); ok {
			if _, file, ok := r.ItemRef(); ok && file != "" {
				if _, serr := os.Stat(file); errors.Is(serr, os.ErrNotExist) {
					err = fmt.Errorf("no credential at %s: %w", file, keychain.ErrNotFound)
				}
			}
		}
	}
	switch {
	case err == nil:
		return ""
	case errors.Is(err, keychain.ErrNotFound):
		return fmt.Sprintf("profile %q is not signed in yet — use /login, then claudeswitch takes over", in.Name)
	}
	return fmt.Sprintf("could not tell whether profile %q is signed in (%v); if it is not, use /login, "+
		"then claudeswitch takes over", in.Name, err)
}
