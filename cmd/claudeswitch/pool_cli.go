package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// `cs pool` and `cs profile pools` (docs/PROFILES.md §10, D31-D35): named
// pools of accounts that profiles list, as parse-checked text edits.
//
// Every edit is made first on a copy of the config's layout (its [[pool]]
// blocks, and each declared profile's own pool and the pools it lists). The
// copy is validated as a whole config, checked against §3 account by account,
// and only then written, as the text edits that turn one layout into the
// other; the result is parsed back and must have exactly the new layout.

const poolCmdUsage = "usage: claudeswitch pool list\n" +
	"       claudeswitch pool create <name> [<account>...]\n" +
	"       claudeswitch pool add <pool> <account>\n" +
	"       claudeswitch pool remove <pool> <account>\n" +
	"       claudeswitch pool move <account> --to <pool>\n" +
	"       claudeswitch pool rename <pool> <new-name>\n" +
	"       claudeswitch pool delete <name> [--to <pool> | --no-pool]\n" +
	"  each takes --json; a profile's own pool is named after the profile"

// cmdPool is `cs pool list|create|add|remove|move|rename|delete`.
func cmdPool(args []string) error {
	if len(args) == 0 {
		return appErr(codeUsage, poolCmdUsage, "name a pool command")
	}
	verb := args[0]
	fs := appFlags("pool " + verb)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	to := fs.String("to", "", "move: the pool the account goes to; delete: the pool its accounts go to")
	noPool := fs.Bool("no-pool", false, "delete: its accounts go to no pool (the default)")
	p, err := parseApp(fs, args[1:], poolCmdUsage)
	if err != nil {
		return err
	}
	if verb != "move" && verb != "delete" && *to != "" {
		return appErr(codeUsage, poolCmdUsage, "--to is a flag of pool move and pool delete")
	}
	if verb != "delete" && *noPool {
		return appErr(codeUsage, poolCmdUsage, "--no-pool is a flag of pool delete")
	}
	w := io.Writer(os.Stdout)
	switch verb {
	case "list":
		if len(p) != 0 {
			return appErr(codeUsage, poolCmdUsage, "list takes no arguments")
		}
		return poolList(w, *cfgPath, *asJSON)
	case "create":
		if len(p) < 1 {
			return appErr(codeUsage, poolCmdUsage, "create takes a name, then any accounts")
		}
		return poolCreate(w, *cfgPath, p[0], p[1:], *asJSON)
	case "add", "remove":
		if len(p) != 2 {
			return appErr(codeUsage, poolCmdUsage, "%s takes a pool and an account", verb)
		}
		if verb == "add" {
			return poolAdd(w, *cfgPath, p[0], p[1], *asJSON)
		}
		return poolRemove(w, *cfgPath, p[0], p[1], *asJSON)
	case "move":
		if len(p) != 1 || *to == "" {
			return appErr(codeUsage, poolCmdUsage, "move takes an account and --to <pool>")
		}
		return poolMove(w, *cfgPath, p[0], *to, *asJSON)
	case "rename":
		if len(p) != 2 {
			return appErr(codeUsage, poolCmdUsage, "rename takes a pool and its new name")
		}
		return poolRename(w, *cfgPath, p[0], p[1], *asJSON)
	case "delete":
		if len(p) != 1 {
			return appErr(codeUsage, poolCmdUsage, "delete takes one pool name")
		}
		if *to != "" && *noPool {
			return appErr(codeUsage, poolCmdUsage, "--to and --no-pool say different things; give one")
		}
		return poolDeleteTo(w, *cfgPath, p[0], joinTarget{Pool: *to, None: *noPool}, *asJSON)
	}
	return appErr(codeUsage, poolCmdUsage, "unknown pool command %q", verb)
}

// poolRef is a pool as the pool commands name it: a [[pool]] block, or a
// declared profile's own pool (D35), under its profile's name. Every
// declared profile that lists no pools has one, empty or not.
type poolRef struct {
	name string
	own  bool
}

func findPool(cfg *config.Config, name string) (poolRef, bool) {
	if _, ok := cfg.PoolNamed(name); ok {
		return poolRef{name: name}, true
	}
	if i := declaredIndex(cfg, name); i >= 0 && hasOwnPool(cfg, cfg.Profiles[i]) {
		return poolRef{name: name, own: true}, true
	}
	return poolRef{}, false
}

// hasOwnPool reports whether a declared profile has an own pool the pool
// commands can name: it lists no pools, and no [[pool]] has its name (which
// the config allows only while that own pool is empty).
func hasOwnPool(cfg *config.Config, in config.Profile) bool {
	if len(in.Pools) > 0 {
		return false
	}
	_, block := cfg.PoolNamed(in.Name)
	return !block
}

// poolNames lists every pool findPool knows, for a not_found hint.
func poolNames(cfg *config.Config) []string {
	var out []string
	for _, p := range cfg.Pools {
		out = append(out, p.Name)
	}
	for _, in := range cfg.Profiles {
		// An empty own pool is no pool to show: its profile uses what it lists,
		// or, for default, the accounts in no pool.
		if hasOwnPool(cfg, in) && len(in.Pool) > 0 {
			out = append(out, in.Name)
		}
	}
	return out
}

func noSuchPool(cfg *config.Config, name string) error {
	hint := "make it: claudeswitch pool create " + name
	if names := poolNames(cfg); len(names) > 0 {
		hint = "the pools are " + strings.Join(quoteAll(names), ", ") + "; or " + hint
	}
	return appErr(codeNotFound, hint, "no pool named %q", name)
}

// poolListJSON is `cs pool list --json`, and what every pool edit answers:
// each [[pool]] block with the profiles that list it, then each declared
// profile's own pool (D35, "own": true), and the accounts no pool lists
// (D6: the profile named default uses them, after its pools).
func poolListJSON(cfg *config.Config) map[string]any {
	pools := []map[string]any{}
	for _, p := range cfg.Pools {
		pools = append(pools, map[string]any{"name": p.Name, "accounts": nonNilList(p.Accounts),
			"profiles": nonNilList(cfg.SubscribersOf(p.Name)), "own": false})
	}
	for _, in := range cfg.Profiles {
		// An empty own pool is no pool to show: its profile uses what it lists,
		// or, for default, the accounts in no pool.
		if hasOwnPool(cfg, in) && len(in.Pool) > 0 {
			pools = append(pools, map[string]any{"name": in.Name, "accounts": nonNilList(in.Pool),
				"profiles": []string{in.Name}, "own": true})
		}
	}
	return map[string]any{"pools": pools, "unlisted": unlistedAccounts(cfg)}
}

// unlistedAccounts are the accounts no pool lists, in config order.
func unlistedAccounts(cfg *config.Config) []string {
	out := []string{}
	for _, a := range cfg.Accounts {
		if _, ok := cfg.PoolOf(a.ID); !ok {
			out = append(out, a.ID)
		}
	}
	return out
}

func nonNilList(v []string) []string {
	return append([]string{}, v...)
}

// poolList is `cs pool list [--json]`.
func poolList(w io.Writer, cfgPath string, asJSON bool) error {
	cfg, _, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if asJSON {
		return emitTo(w, poolListJSON(cfg))
	}
	fmt.Fprintln(w)
	for _, p := range cfg.Pools {
		users := "listed by no profile, so its accounts rotate nowhere"
		if subs := cfg.SubscribersOf(p.Name); len(subs) > 0 {
			users = "used by " + strings.Join(subs, ", ")
		}
		fmt.Fprintf(w, "  %s\n    accounts  %s\n    %s\n", p.Name,
			nonEmpty(strings.Join(p.Accounts, ", "), "(empty)"), users)
	}
	for _, in := range cfg.Profiles {
		// An empty own pool is no pool to show: its profile uses what it lists,
		// or, for default, the accounts in no pool.
		if hasOwnPool(cfg, in) && len(in.Pool) > 0 {
			fmt.Fprintf(w, "  %s (profile %s's own)\n    accounts  %s\n    used by %s\n", in.Name, in.Name,
				nonEmpty(strings.Join(in.Pool, ", "), "(empty)"), in.Name)
		}
	}
	if un := unlistedAccounts(cfg); len(un) > 0 {
		who := fmt.Sprintf("profile %s uses them, after its pools", config.DefaultProfile)
		switch {
		case len(cfg.Profiles) == 0:
			who = "with no [[profile]] blocks, every account is in the one profile"
		case declaredIndex(cfg, config.DefaultProfile) < 0:
			who = fmt.Sprintf("no profile uses them: there is no %q profile", config.DefaultProfile)
		}
		fmt.Fprintf(w, "  in no pool\n    accounts  %s\n    %s\n", strings.Join(un, ", "), who)
	}
	fmt.Fprintln(w)
	return nil
}

// loadPoolEdit loads the config a pool edit changes, and the state its §3
// check reads.
func loadPoolEdit(cfgPath string) (*config.Config, *state.State, error) {
	cfg, err := loadForEdit(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		return nil, nil, err
	}
	return cfg, st, nil
}

// withLayout is a copy of cfg whose pools and profiles' pool lists can be
// changed without changing cfg's (ForProfile's copy shares them).
func withLayout(cfg *config.Config) *config.Config {
	cp := *cfg
	cp.Pools = make([]config.Pool, len(cfg.Pools))
	for i, p := range cfg.Pools {
		cp.Pools[i] = config.Pool{Name: p.Name, Accounts: slices.Clone(p.Accounts)}
	}
	cp.Profiles = make([]config.Profile, len(cfg.Profiles))
	for i, in := range cfg.Profiles {
		in.Pool, in.Pools = slices.Clone(in.Pool), slices.Clone(in.Pools)
		cp.Profiles[i] = in
	}
	return &cp
}

// poolAccounts points at pool ref's account list in c, a withLayout copy.
func poolAccounts(c *config.Config, ref poolRef) *[]string {
	if ref.own {
		return &c.Profiles[declaredIndex(c, ref.name)].Pool
	}
	for i := range c.Pools {
		if c.Pools[i].Name == ref.name {
			return &c.Pools[i].Accounts
		}
	}
	return nil
}

// dropFromPools takes id out of whichever pool lists it in c.
func dropFromPools(c *config.Config, id string) {
	drop := func(s string) bool { return s == id }
	for i := range c.Pools {
		c.Pools[i].Accounts = slices.DeleteFunc(c.Pools[i].Accounts, drop)
	}
	for i := range c.Profiles {
		if c.Profiles[i].Pool != nil {
			c.Profiles[i].Pool = slices.DeleteFunc(c.Profiles[i].Pool, drop)
		}
	}
}

// checkLayoutLive is §3 for a pool edit, account by account: an account a
// profile can no longer use must not be live (or maybe live, D18) there, and
// one a profile may newly use must not be live anywhere outside the profiles
// that may use it after the edit, a guarded old credential included (D22).
// Sharing an account with the profile it is live in is fine: one profile
// uses it at a time (D34).
func checkLayoutLive(before, after *config.Config, st *state.State) error {
	for _, a := range before.Accounts {
		was, now := before.ProfilesOf(a.ID), after.ProfilesOf(a.ID)
		var leaving, joining []string
		for _, p := range was {
			if !slices.Contains(now, p) {
				leaving = append(leaving, p)
			}
		}
		for _, p := range now {
			if !slices.Contains(was, p) {
				joining = append(joining, p)
			}
		}
		if len(leaving) > 0 {
			if holder, live := accountLiveIn(before, st, a.ID, func(n string, ghost bool) bool {
				return !ghost && slices.Contains(leaving, n)
			}); live {
				return liveRefusal(a.ID, holder, "moved")
			}
		}
		if len(joining) > 0 {
			if holder, live := accountLiveIn(before, st, a.ID, func(n string, ghost bool) bool {
				return ghost || !slices.Contains(now, n)
			}); live {
				return liveRefusal(a.ID, holder, "given to profile "+strings.Join(joining, ", "))
			}
		}
	}
	return nil
}

// setList sets key to a one-line list in ref, where it is written or as a
// new line.
func setList(text string, ref blockRef, key string, v []string) (string, error) {
	out, found, err := mapArray(text, ref, key, func([]string) []string { return v })
	if err != nil || found {
		return out, err
	}
	return setKey(text, ref, key, tomlList(v))
}

// layoutEdit turns text from before's layout into after's: a changed list
// rewritten on one line where it is written, a profile's pool or pools key
// removed when after has none, a [[pool]] block deleted (with the comments
// directly above it) or appended. Pool edits never add or remove profiles.
func layoutEdit(text string, before, after *config.Config) (string, error) {
	var err error
	for i, in := range after.Profiles {
		was, ref := before.Profiles[i], blockRef{"profile", in.Name}
		if was.Pool != nil && was.Pools == nil && in.Pool == nil && in.Pools != nil {
			// An own pool converted (D35): pools takes pool's line.
			out, found, err := replaceKey(text, ref, "pool", "pools", tomlList(in.Pools))
			if err != nil {
				return "", err
			}
			if found {
				text = out
				continue
			}
		}
		for _, k := range []struct {
			key      string
			was, now []string
		}{{"pool", was.Pool, in.Pool}, {"pools", was.Pools, in.Pools}} {
			if slices.Equal(k.was, k.now) && (k.was == nil) == (k.now == nil) {
				continue
			}
			if k.now == nil {
				text, err = deleteKey(text, ref, k.key)
			} else {
				text, err = setList(text, ref, k.key, k.now)
			}
			if err != nil {
				return "", err
			}
		}
	}
	for _, p := range before.Pools {
		q, ok := after.PoolNamed(p.Name)
		switch {
		case !ok:
			text, err = deleteBlock(text, blockRef{"pool", p.Name})
		case !slices.Equal(p.Accounts, q.Accounts):
			text, err = setList(text, blockRef{"pool", p.Name}, "accounts", nonNilList(q.Accounts))
		}
		if err != nil {
			return "", err
		}
	}
	for _, q := range after.Pools {
		if _, ok := before.PoolNamed(q.Name); !ok {
			text = strings.TrimRight(text, "\n") + "\n\n[[pool]]\nname     = " + strconv.Quote(q.Name) +
				"\naccounts = " + tomlList(q.Accounts) + "\n"
		}
	}
	return text, nil
}

// sameLayout checks that a config parsed back from an edit has after's
// layout exactly.
func sameLayout(back, after *config.Config) error {
	if len(back.Pools) != len(after.Pools) {
		return fmt.Errorf("the edit parsed but has %d [[pool]] blocks, not %d; discarded", len(back.Pools), len(after.Pools))
	}
	for _, q := range after.Pools {
		if p, ok := back.PoolNamed(q.Name); !ok || !slices.Equal(p.Accounts, q.Accounts) {
			return fmt.Errorf("the edit parsed but pool %q did not come out as asked; discarded", q.Name)
		}
	}
	if len(back.Profiles) != len(after.Profiles) {
		return fmt.Errorf("the edit parsed but changed the profiles; discarded")
	}
	for i, in := range after.Profiles {
		got := back.Profiles[i]
		if got.Name != in.Name || !slices.Equal(got.Pool, in.Pool) || !slices.Equal(got.Pools, in.Pools) {
			return fmt.Errorf("the edit parsed but profile %q's pools did not come out as asked; discarded", in.Name)
		}
	}
	return nil
}

// applyLayout writes after's layout over cfg's: validated, checked against
// §3, edited in place and parsed back. It returns the config as written.
func applyLayout(cfg *config.Config, st *state.State, after *config.Config) (*config.Config, error) {
	if err := after.Validate(); err != nil {
		return nil, appErr(codeInvalidValue, "", "that would make the config invalid: %v", err)
	}
	if err := checkLayoutLive(cfg, after, st); err != nil {
		return nil, err
	}
	err := editConfigText(cfg.Path, func(text string) (string, error) {
		return layoutEdit(text, cfg, after)
	}, func(back *config.Config) error { return sameLayout(back, after) })
	if err != nil {
		return nil, err
	}
	back, err := config.Load(cfg.Path)
	if err != nil {
		return nil, wrapErr(codeConfigInvalid, "", err)
	}
	return back, nil
}

// answerPools is a pool edit's answer: the pool list, or one line.
func answerPools(w io.Writer, back *config.Config, asJSON bool, line string) error {
	if asJSON {
		return emitTo(w, poolListJSON(back))
	}
	fmt.Fprintf(w, "  %s; a running daemon picks this up by itself\n", line)
	return nil
}

// inOtherPool is D32's refusal: id is in pool cur already.
func inOtherPool(id, cur, want string) error {
	return appErr(codeInOtherPool, fmt.Sprintf("move it: claudeswitch pool move %s --to %s", id, want),
		"account %q is in pool %q; an account is in one pool (profiles share a pool by listing it)", id, cur)
}

// wouldOrphan reports whether taking id out of every pool leaves it in none
// a profile uses: with profiles declared and no default, an enabled account
// in no pool is a config error (D6).
func wouldOrphan(cfg *config.Config, id string) bool {
	return len(cfg.Profiles) > 0 && declaredIndex(cfg, config.DefaultProfile) < 0 && accountByID(cfg, id).IsEnabled()
}

// poolCreate is `cs pool create <name> [<account>...]`.
func poolCreate(w io.Writer, cfgPath, name string, ids []string, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	if err := config.ValidName("pool name", name); err != nil {
		return wrapErr(codeInvalidValue, "", err)
	}
	if _, ok := cfg.PoolNamed(name); ok {
		return appErr(codeInvalidValue, "", "a pool named %q exists already", name)
	}
	if i := declaredIndex(cfg, name); i >= 0 && hasOwnPool(cfg, cfg.Profiles[i]) {
		return appErr(codeInvalidValue, "to share it, list it: claudeswitch profile pools <profile> "+name,
			"profile %q's own pool is named %q already", name, name)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !hasAccount(cfg, id) {
			return appErr(codeNotFound, "", "no account %q in the config", id)
		}
		if seen[id] {
			return appErr(codeInvalidValue, "", "%q is named twice", id)
		}
		seen[id] = true
		if cur, ok := cfg.PoolOf(id); ok {
			return appErr(codeInOtherPool,
				fmt.Sprintf("create it without %s, then: claudeswitch pool move %s --to %s", id, id, name),
				"account %q is in pool %q; an account is in one pool (profiles share a pool by listing it)", id, cur)
		}
	}
	after := withLayout(cfg)
	after.Pools = append(after.Pools, config.Pool{Name: name, Accounts: nonNilList(ids)})
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pool %s made, holding %s", name, nonEmpty(strings.Join(ids, ", "), "no accounts"))
	if len(back.SubscribersOf(name)) == 0 && len(cfg.Profiles) > 0 {
		line += "; no profile lists it yet, so its accounts rotate nowhere until one does " +
			"(claudeswitch profile pools <profile> <pool>...)"
	}
	return answerPools(w, back, asJSON, line)
}

// poolAdd is `cs pool add <pool> <account>`: an account in no pool joins
// one. One already in another pool is refused (D32): move it instead.
func poolAdd(w io.Writer, cfgPath, pool, id string, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	ref, ok := findPool(cfg, pool)
	if !ok {
		return noSuchPool(cfg, pool)
	}
	if !hasAccount(cfg, id) {
		return appErr(codeNotFound, "", "no account %q in the config", id)
	}
	if cur, ok := cfg.PoolOf(id); ok {
		if cur == pool {
			return answerPools(w, cfg, asJSON, fmt.Sprintf("%s is already in pool %s", id, pool))
		}
		return inOtherPool(id, cur, pool)
	}
	after := withLayout(cfg)
	list := poolAccounts(after, ref)
	*list = append(*list, id)
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	return answerPools(w, back, asJSON, fmt.Sprintf("%s is now in pool %s", id, pool))
}

// poolRemove is `cs pool remove <pool> <account>`: the account is then in
// no pool, so the profile named default uses it (D6).
func poolRemove(w io.Writer, cfgPath, pool, id string, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	if _, ok := findPool(cfg, pool); !ok {
		return noSuchPool(cfg, pool)
	}
	if cur, ok := cfg.PoolOf(id); !ok || cur != pool {
		return appErr(codeNotFound, "", "pool %q does not list %q", pool, id)
	}
	if wouldOrphan(cfg, id) {
		return appErr(codeWouldOrphan,
			fmt.Sprintf("move it to another pool in the same step: claudeswitch pool move %s --to <pool>", id),
			"removing %q from pool %q would leave it in no pool, and with no %q profile the config refuses that",
			id, pool, config.DefaultProfile)
	}
	after := withLayout(cfg)
	dropFromPools(after, id)
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	return answerPools(w, back, asJSON, fmt.Sprintf("%s is in no pool now, so profile %s uses it (after its pools)",
		id, config.DefaultProfile))
}

// poolMove is `cs pool move <account> --to <pool>`: out of its pool, if
// any, and into another, in one edit.
func poolMove(w io.Writer, cfgPath, id, to string, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	ref, ok := findPool(cfg, to)
	if !ok {
		return noSuchPool(cfg, to)
	}
	if !hasAccount(cfg, id) {
		return appErr(codeNotFound, "", "no account %q in the config", id)
	}
	if cur, ok := cfg.PoolOf(id); ok && cur == to {
		return answerPools(w, cfg, asJSON, fmt.Sprintf("%s is already in pool %s", id, to))
	}
	after := withLayout(cfg)
	dropFromPools(after, id)
	list := poolAccounts(after, ref)
	*list = append(*list, id)
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	return answerPools(w, back, asJSON, fmt.Sprintf("%s is now in pool %s", id, to))
}

// poolDelete is `cs pool delete <name>`: its accounts go to no pool (D6).
func poolDelete(w io.Writer, cfgPath, name string, asJSON bool) error {
	return poolDeleteTo(w, cfgPath, name, joinTarget{}, asJSON)
}

// poolDeleteTo is `cs pool delete <name> [--to <pool> | --no-pool]` (D38): any
// pool, a profile's own included. Its accounts go to the pool named, or to
// no pool, and every profile that listed it stops listing it. §3 refuses an
// account in use in a profile that could not use it where it lands.
func poolDeleteTo(w io.Writer, cfgPath, name string, j joinTarget, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	ref, ok := findPool(cfg, name)
	if !ok {
		return noSuchPool(cfg, name)
	}
	var dest *poolRef
	if j.Pool != "" {
		if j.Pool == name {
			return appErr(codeUsage, "choose another pool, or --no-pool",
				"pool %q cannot take its own accounts as it goes", name)
		}
		d, ok := findPool(cfg, j.Pool)
		if !ok {
			return noSuchPool(cfg, j.Pool)
		}
		dest = &d
	}
	after := withLayout(cfg)
	ids := slices.Clone(*poolAccounts(after, ref))
	if ref.own {
		after.Profiles[declaredIndex(after, name)].Pool = nil
	} else {
		after.Pools = slices.DeleteFunc(after.Pools, func(q config.Pool) bool { return q.Name == name })
	}
	subs := cfg.SubscribersOf(name)
	for i := range after.Profiles {
		after.Profiles[i].Pools = slices.DeleteFunc(after.Profiles[i].Pools, func(n string) bool { return n == name })
		if len(after.Profiles[i].Pools) == 0 {
			after.Profiles[i].Pools = nil
		}
	}
	if dest != nil {
		list := poolAccounts(after, *dest)
		*list = append(*list, ids...)
	} else {
		for _, id := range ids {
			if wouldOrphan(cfg, id) {
				return appErr(codeWouldOrphan,
					fmt.Sprintf("give its accounts a pool: claudeswitch pool delete %s --to <pool>", name),
					"deleting pool %q would leave %q in no pool, and with no %q profile the config refuses that",
					name, id, config.DefaultProfile)
			}
		}
	}
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("pool %s deleted", name)
	switch {
	case len(ids) > 0 && dest != nil:
		line += fmt.Sprintf("; %s now in pool %s", strings.Join(ids, ", "), dest.name)
	case len(ids) > 0:
		line += fmt.Sprintf("; %s in no pool now", strings.Join(ids, ", "))
	}
	if len(subs) > 0 {
		line += fmt.Sprintf("; %s no longer use it", strings.Join(subs, ", "))
	}
	return answerPools(w, back, asJSON, line)
}

// poolRename is `cs pool rename <pool> <new-name>` (D39). A [[pool]] is
// renamed where it is written, with every profile's listing of it, so its
// comments and its place stay; a profile's own pool becomes a [[pool]] of
// the new name, which that profile lists in its place.
func poolRename(w io.Writer, cfgPath, from, to string, asJSON bool) error {
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	ref, ok := findPool(cfg, from)
	if !ok {
		return noSuchPool(cfg, from)
	}
	if err := config.ValidName("pool name", to); err != nil {
		return wrapErr(codeInvalidValue, "", err)
	}
	if to == from {
		return answerPools(w, cfg, asJSON, fmt.Sprintf("pool %s is named %s already", from, to))
	}
	if _, taken := findPool(cfg, to); taken {
		return appErr(codeInvalidValue, "", "a pool named %q exists already", to)
	}
	if declaredIndex(cfg, to) >= 0 && hasOwnPool(cfg, cfg.Profiles[declaredIndex(cfg, to)]) {
		return appErr(codeInvalidValue, "", "%q is profile %q's own pool's name", to, to)
	}
	if ref.own {
		after := withLayout(cfg)
		in := &after.Profiles[declaredIndex(after, from)]
		after.Pools = append(after.Pools, config.Pool{Name: to, Accounts: in.Pool})
		in.Pool, in.Pools = nil, []string{to}
		back, err := applyLayout(cfg, st, after)
		if err != nil {
			return err
		}
		return answerPools(w, back, asJSON, fmt.Sprintf("profile %s's own pool is now pool %s, which it uses", from, to))
	}
	p, _ := cfg.PoolNamed(from)
	subs := cfg.SubscribersOf(from)
	err = editConfigText(cfg.Path, func(text string) (string, error) {
		text, err := setKey(text, blockRef{"pool", from}, "name", strconv.Quote(to))
		if err != nil {
			return "", err
		}
		for _, in := range cfg.Profiles {
			if !slices.Contains(in.Pools, from) {
				continue
			}
			renamed := slices.Clone(in.Pools)
			renamed[slices.Index(renamed, from)] = to
			if text, err = setList(text, blockRef{"profile", in.Name}, "pools", renamed); err != nil {
				return "", err
			}
		}
		return text, nil
	}, func(back *config.Config) error {
		q, ok := back.PoolNamed(to)
		if !ok || !slices.Equal(q.Accounts, p.Accounts) || !slices.Equal(back.SubscribersOf(to), subs) {
			return fmt.Errorf("the edit parsed but pool %q did not come out as %q with its accounts and profiles; discarded", from, to)
		}
		return nil
	})
	if err != nil {
		return err
	}
	back, err := config.Load(cfg.Path)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	return answerPools(w, back, asJSON, fmt.Sprintf("pool %s is now named %s", from, to))
}

const profilePoolsUsage = "usage: claudeswitch profile pools <profile> <pool>..."

// profilePools is `cs profile pools <profile> <pool>...`: the pools a
// profile uses, in the order it uses them (D33). A profile's own pool (D35)
// is converted first into a [[pool]] named after the profile, which leads
// the list unless the list names it. Naming another profile's own pool
// converts that one the same way, and that profile then lists it, so both
// share it.
func profilePools(w io.Writer, cfgPath, name string, pools []string, asJSON bool) error {
	if len(pools) == 0 {
		return appErr(codeUsage, profilePoolsUsage, "name the pools the profile uses, in order")
	}
	cfg, st, err := loadPoolEdit(cfgPath)
	if err != nil {
		return err
	}
	i := declaredIndex(cfg, name)
	if i < 0 {
		if _, ok := cfg.ProfileNamed(name); ok {
			return appErr(codeNotFound, "declare profiles first: claudeswitch profile create <name>",
				"profile %q is not declared in the config, so it lists no pools", name)
		}
		return noSuchProfile(cfg, name)
	}
	seen := map[string]bool{}
	for _, p := range pools {
		if seen[p] {
			return appErr(codeInvalidValue, profilePoolsUsage, "pool %q is named twice", p)
		}
		seen[p] = true
		if _, ok := findPool(cfg, p); !ok {
			return noSuchPool(cfg, p)
		}
	}
	after := withLayout(cfg)
	var converted []string
	convert := func(j int) {
		in := &after.Profiles[j]
		after.Pools = append(after.Pools, config.Pool{Name: in.Name, Accounts: nonNilList(in.Pool)})
		in.Pool = nil
		if j != i {
			in.Pools = []string{in.Name}
		}
		converted = append(converted, in.Name)
	}
	list := append([]string{}, pools...)
	if len(cfg.Profiles[i].Pools) == 0 {
		// Its own pool: converted when the list names it, and when it holds
		// accounts, leading the list unless the list says where it goes. (A
		// [[pool]] with its name means that own pool is empty, D35.)
		switch {
		case !hasOwnPool(cfg, cfg.Profiles[i]), !seen[name] && len(cfg.Profiles[i].Pool) == 0:
			after.Profiles[i].Pool = nil
		case seen[name]:
			convert(i)
		default:
			convert(i)
			list = append([]string{name}, list...)
		}
	}
	for _, p := range pools {
		if j := declaredIndex(cfg, p); j >= 0 && j != i && hasOwnPool(cfg, cfg.Profiles[j]) {
			convert(j)
		}
	}
	after.Profiles[i].Pools = list
	back, err := applyLayout(cfg, st, after)
	if err != nil {
		return err
	}
	in, _ := back.ProfileNamed(name)
	if asJSON {
		bst, err := state.Load("", back.ProfileNames()...)
		if err != nil {
			bst = st
		}
		return emitTo(w, profileJSON(back, bst, in))
	}
	for _, c := range converted {
		fmt.Fprintf(w, "  profile %s's own pool is now the [[pool]] %s\n", c, c)
	}
	fmt.Fprintf(w, "  profile %s uses %s, in that order; a running daemon picks this up by itself\n",
		name, strings.Join(list, ", "))
	return nil
}
