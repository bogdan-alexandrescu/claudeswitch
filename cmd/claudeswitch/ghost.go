package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Ghosts (lane 7 security review, owner decision): a profile removed from
// the config, or re-pointed to another dir, leaves its old live item behind,
// and the account last live there may still be — Claude Code keeps running on
// the old dir. Without it, the §3 checks no longer saw that item, so the
// account could be installed in a second profile. The old item is kept as a
// state.Ghost, persisted, and consulted by every §3 check (the daemon's
// liveElsewhere and liveAnywhere, and the CLI's use, login, refresh and
// remove) until the daemon finds the item no longer holds the account, or
// `cs profile forget <name>`.

// ghostName describes a ghost where a refusal names it.
func ghostName(g *state.Ghost) string {
	if g.Why == state.GhostRepointed {
		return fmt.Sprintf("profile %s's previous credential (before its dir changed)", g.Profile)
	}
	return "removed profile " + g.Profile
}

// itemID is a live item's concrete identity, for comparing a ghost with the
// items in use.
type itemID struct{ service, file string }

func (a itemID) same(b itemID) bool {
	return a.service == b.service && (a.file == "" || b.file == "" || a.file == b.file)
}

func itemOf(l keychain.Live, ref func(keychain.Live) (string, string, bool)) itemID {
	if ref != nil {
		if s, f, ok := ref(l); ok {
			return itemID{s, f}
		}
	}
	if r, ok := l.(keychain.ItemRefer); ok {
		if s, f, ok := r.ItemRef(); ok {
			return itemID{s, f}
		}
	}
	return itemID{service: l.Name()}
}

// withGhosts adds every ghost to a §3 check's targets, except one whose item
// is an item in use by a current profile: that profile's own check covers
// it, and for the profile itself it is its own item.
func withGhosts(st *state.State, targets []liveTarget, mk func(service, file string) keychain.Live) []liveTarget {
	return withGhostsRef(st, targets, mk, nil)
}

func withGhostsRef(st *state.State, targets []liveTarget, mk func(service, file string) keychain.Live,
	ref func(keychain.Live) (string, string, bool)) []liveTarget {
	if st == nil {
		return targets
	}
	return withGhostList(st.GhostList(), targets, mk, ref)
}

func withGhostList(gs []*state.Ghost, targets []liveTarget, mk func(service, file string) keychain.Live,
	ref func(keychain.Live) (string, string, bool)) []liveTarget {
	var inUse []itemID
	for _, t := range targets {
		if t.live != nil && t.ghost == nil {
			inUse = append(inUse, itemOf(t.live, ref))
		}
	}
	out := append([]liveTarget(nil), targets...)
next:
	for _, g := range gs {
		if g.Service == "" {
			// No item was ever recorded: the account is guarded by name.
			out = append(out, liveTarget{name: ghostName(g), ghost: g})
			continue
		}
		id := itemID{g.Service, g.File}
		for _, u := range inUse {
			if u.same(id) {
				continue next
			}
		}
		out = append(out, liveTarget{name: ghostName(g), live: mk(g.Service, g.File), ghost: g})
	}
	return out
}

// offlineGhosts are the ghosts state implies but no daemon made: a profile
// state records with a real active account that the config no longer
// declares, or declares on another dir than the item state recorded was
// resolved from (an edit made while no daemon ran). Each guards the recorded
// item, or, when none was ever recorded, the account by name.
func offlineGhosts(cfg *config.Config, st *state.State) []*state.Ghost {
	if cfg == nil || st == nil {
		return nil
	}
	declared := map[string]config.Profile{}
	for _, in := range cfg.EffectiveProfiles() {
		declared[in.Name] = in
	}
	names := make([]string, 0, len(st.Profiles))
	for name := range st.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []*state.Ghost
	for _, name := range names {
		in := st.Profiles[name]
		if in == nil || in.Active == "" || in.Active == state.Unattributed {
			continue
		}
		conf, ok := declared[name]
		why := state.GhostRemoved
		switch {
		case !ok:
		case in.Item != nil && (in.Item.Dir != conf.Dir || in.Item.FromEnv != conf.FromEnv):
			why = state.GhostRepointed
		default:
			continue
		}
		g := &state.Ghost{Profile: name, Why: why, Account: in.Active, Seat: cfg.SeatOf(in.Active),
			Since: in.ActiveAt}
		if g.Since.IsZero() {
			g.Since = time.Now()
		}
		switch {
		case in.Item != nil:
			g.Service, g.File, g.Dir = in.Item.Service, in.Item.File, in.Item.Dir
		case name == state.DefaultProfile:
			// The implicit profile, or a record migrated from a state file
			// older than profiles: it can only ever have used the item this
			// environment names. Guard that item, so the profile now using
			// it is not refused and the guard can be released.
			if s, f, ok := envItemRef(); ok && s != "" {
				g.Service, g.File = s, f
			}
		}
		out = append(out, g)
	}
	return out
}

// envItemRef names the item this process's environment resolves to (the
// implicit profile's). A seam: it looks the item up in the keychain.
var envItemRef = func() (string, string, bool) {
	if r, ok := keychain.EnvLive().(keychain.ItemRefer); ok {
		return r.ItemRef()
	}
	return "", "", false
}

// liveHolderUnknown is refresh's refusal when the holder's item cannot be
// written back: a profile whose item could not be looked up, or a ghost
// that guards the account by name.
func liveHolderUnknown(holderName, id string) error {
	if isGhostName(holderName) {
		return fmt.Errorf("%q may be live in %s, whose credential was never recorded, so the renewed token "+
			"could not be written back to it; not refreshing (`cs profile forget` releases that guard)",
			id, holderName)
	}
	return fmt.Errorf("profile %q's live credential could not be looked up, so whether it holds %q "+
		"is unknown and the renewed token could not be written back to it; not refreshing", holderName, id)
}

// isGhostName reports whether a §3 target name describes a ghost.
func isGhostName(name string) bool {
	return strings.HasPrefix(name, "removed profile ") || strings.Contains(name, "'s previous credential")
}

// allGhosts is every ghost the CLI must honour: the recorded ones and those
// an offline edit implies. A by-name ghost of a profile the config
// declares again is not: that profile's own record covers it.
func allGhosts(cfg *config.Config, st *state.State) []*state.Ghost {
	if st == nil {
		return nil
	}
	declared := map[string]config.Profile{}
	if cfg != nil {
		for _, in := range cfg.EffectiveProfiles() {
			declared[in.Name] = in
		}
	}
	var gs []*state.Ghost
	have := map[string]bool{}
	for _, g := range st.GhostList() {
		// A by-name ghost of a profile declared again is covered by that
		// profile's record — unless it guards a re-pointed profile's old dir.
		if in, ok := declared[g.Profile]; ok && g.Service == "" &&
			!(g.Why == state.GhostRepointed && g.Dir != in.Dir) {
			continue
		}
		gs = append(gs, g)
		have[g.Key()] = true
	}
	for _, g := range offlineGhosts(cfg, st) {
		if !have[g.Key()] {
			gs = append(gs, g)
		}
	}
	return gs
}

// cliGhostTargets is every profile's live item plus every ghost, for the
// CLI's §3 and live-holder checks.
func cliGhostTargets(cfg *config.Config, st *state.State) []liveTarget {
	return withGhostList(allGhosts(cfg, st), liveTargets(cfg, cliLiveFor), cliGhostItem, nil)
}

// forgetProfileRecord clears what state records as live in a profile, so
// an offline-implied ghost is not derived again.
func forgetProfileRecord(st *state.State, name string) bool {
	in := st.Profiles[name]
	if in == nil || (in.Active == "" && in.Item == nil) {
		return false
	}
	in.Active, in.ActiveAt, in.Item = "", time.Now(), nil
	return true
}

// adoptOfflineGhosts turns every ghost an offline edit implies into a
// recorded one, at daemon startup, before any profile is resolved again.
// The profile's record is cleared (a re-pointed one holds until attributed),
// so a release or a forget sticks.
func (d *daemon) adoptOfflineGhosts() {
	for _, il := range d.profs {
		d.dropGhostsOfItem(il) // a by-name ghost of a profile declared again
	}
	have := map[string]bool{}
	for _, g := range d.st.GhostList() {
		have[g.Key()] = true
	}
	for _, g := range offlineGhosts(d.cfg, d.st) {
		if !have[g.Key()] {
			d.st.AddGhost(g)
		}
		prev := d.st.Profiles[g.Profile].Active
		forgetProfileRecord(d.st, g.Profile)
		for _, il := range d.profs {
			if il.name == g.Profile {
				il.hold.afterReload(prev, "")
			}
		}
		how := "its old credential " + g.Service
		if g.Service == "" {
			how = "the account by name (no credential was recorded for it); `cs profile forget " +
				g.Profile + "` releases it"
		}
		d.log.Warn("a profile was "+g.Why+" while no daemon ran; guarding "+how,
			"profile", g.Profile, "account", g.Account)
	}
}

// recordItem stores the item a profile resolved to, with its dir, so an
// edit made while no daemon runs can still be guarded.
func (d *daemon) recordItem(il *profileLoop) {
	if il.live == nil {
		return
	}
	id := itemOf(il.live, d.itemRef)
	if id.service == "" {
		return
	}
	d.profState(il).Item = &state.ItemRef{Service: id.service, File: id.file, Dir: il.conf.Dir, FromEnv: il.conf.FromEnv}
}

// liveConflict is the §3 check `use` and `login` run before putting accountID
// into profile self's live item. It runs whenever there is anything else to
// check against: other profiles, or ghosts — with one profile left after a
// consolidation, the removed one's Claude Code may still hold the account.
func liveConflict(cfg *config.Config, st *state.State, v holdsChecker, self, accountID string) (string, string, time.Time) {
	if !multiProfile(cfg) && len(allGhosts(cfg, st)) == 0 {
		return "", "", time.Time{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return liveElsewhereOf(ctx, v, cfg, st, self, cliGhostTargets(cfg, st), accountID)
}

// saveRenameState saves state for rename. A seam, so a test can make it fail.
var saveRenameState = func(st *state.State) error { return st.Save() }

// cliGhostItem reopens a ghost's item for the CLI.
func cliGhostItem(service, file string) keychain.Live { return keychain.LiveItem(service, file) }

// ghostTargets is the daemon's current profiles plus its ghosts.
func (d *daemon) ghostTargets() []liveTarget {
	targets := make([]liveTarget, 0, len(d.profs))
	for _, o := range d.profs {
		targets = append(targets, liveTarget{name: o.name, live: o.live, unresolved: o.unresolved})
	}
	return withGhostsRef(d.st, targets, d.ghostItemFn(), d.itemRef)
}

func (d *daemon) ghostItemFn() func(service, file string) keychain.Live {
	if d.ghostItem != nil {
		return d.ghostItem
	}
	return keychain.LiveItem
}

// makeGhost records a stopping profile's old item as a ghost when an
// account was last live there. It runs before the profile's state changes.
//
// An item that never resolved, or cannot be named, leaves no item to watch,
// but the account recorded live there may still be: it is guarded by name,
// as an offline edit's is (offlineGhosts), until `cs profile forget`. The
// reload clears the profile's record next, so without this the account
// would lose its only guard (lane 15 review).
func (d *daemon) makeGhost(il *profileLoop, why string) {
	active := d.profState(il).Active
	if active == "" && il.hold.holding() {
		// Started by a reload and not attributed yet (re-pointed twice in
		// one interval, say): the account it held before is what may be live
		// in this item, and the hold still carries it.
		active = il.hold.lost
	}
	if active == "" || active == state.Unattributed {
		return // nothing of ours was recorded live there
	}
	var id itemID
	if il.live != nil {
		id = itemOf(il.live, d.itemRef)
	}
	g := &state.Ghost{Profile: il.name, Why: why, Dir: il.conf.Dir, Service: id.service, File: id.file,
		Account: active, Seat: d.cfg.SeatOf(active), Since: time.Now()}
	d.st.AddGhost(g)
	if id.service == "" {
		il.log.Warn("this profile's old credential could not be named, so its account is guarded by name",
			"account", active, "why", why, "released", "`cs profile forget "+il.name+"`")
		return
	}
	il.log.Warn("guarding this profile's old credential as a ghost: its account may still be live there",
		"account", active, "item", il.live.Name(), "why", why,
		"released", "when the item no longer holds it, or `cs profile forget "+il.name+"`")
}

// dropGhostsOfItem releases the ghosts of a live item a profile has just
// started on (one declared again on its old dir): that profile's own §3
// check covers the item from now on.
//
// A ghost that guards its account by name (no item recorded) is released
// when its own profile is declared again: that profile's record covers it.
func (d *daemon) dropGhostsOfItem(il *profileLoop) {
	var id itemID
	if il.live != nil {
		id = itemOf(il.live, d.itemRef)
	}
	for _, g := range d.st.GhostList() {
		// A by-name ghost is released when its profile is declared again —
		// but not by the restart of a re-pointing: that ghost guards the old
		// dir's item, which the profile no longer reads.
		byName := g.Service == "" && g.Profile == il.name &&
			!(g.Why == state.GhostRepointed && g.Dir != il.conf.Dir)
		if byName || (id.service != "" && g.Service != "" && id.same(itemID{g.Service, g.File})) {
			d.st.DropGhost(g.Key())
			d.log.Info("ghost released: a profile runs on its item again", "profile", il.name,
				"ghost_of", g.Profile, "account", g.Account, "item", g.Service)
		}
	}
}

// releaseGhosts drops every ghost whose item is known no longer to hold its
// account: the token compare is free, the seat compare cached (HoldsAccount).
// Unknown keeps it (D18).
func (d *daemon) releaseGhosts(ctx context.Context) {
	mk := d.ghostItemFn()
	for _, g := range d.st.GhostList() {
		if g.Service == "" {
			continue // guarded by name: only `cs profile forget` releases it
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		holds, known := d.v.HoldsAccount(cctx, mk(g.Service, g.File), g.Account, g.Seat)
		cancel()
		if !known || holds {
			continue
		}
		d.st.DropGhost(g.Key())
		d.log.Info("ghost released: the old credential no longer holds its account",
			"profile", g.Profile, "account", g.Account, "item", g.Service)
	}
}

// ghostLines is what status prints about ghosts, "" with none.
func ghostLines(gs []*state.Ghost) string {
	if len(gs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  Guarded old credentials (an account may still be live there, so it is installed nowhere else):\n")
	for _, g := range gs {
		fmt.Fprintf(&b, "    %s: %s, since %s ago — `cs profile forget %s` releases it\n",
			ghostName(g), g.Account, ageShort(time.Since(g.Since)), g.Profile)
	}
	return b.String()
}

// doctorGhostLine is doctor's warning about ghosts, "" with none.
func doctorGhostLine(gs []*state.Ghost) string {
	if len(gs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  [warn] ghosts          %d old credential(s) still guarded\n", len(gs))
	for _, g := range gs {
		fmt.Fprintf(&b, "         └ %s may still hold %s; once it is signed out or closed this clears by itself, "+
			"or `cs profile forget %s`\n", ghostName(g), g.Account, g.Profile)
	}
	return b.String()
}

// profileUsage lists the profile subcommands.
const profileUsage = "usage: claudeswitch profile create <name> [--dir PATH] [--pool a,b] [--seed <account>]\n" +
	"       claudeswitch profile seed <name> <account>\n" +
	"       claudeswitch profile list\n" +
	"       claudeswitch profile remove <name> [--to <profile>] [--yes]\n" +
	"       claudeswitch profile forget <name>\n" +
	"       claudeswitch profile pool <name> add|remove <account> [--to <profile>]\n" +
	"       claudeswitch profile set <name> <key> <value|inherit>\n" +
	"       claudeswitch profile which [--dir PATH]\n" +
	"       claudeswitch profile hook zsh|bash|fish\n" +
	"  each takes --json; forget releases the guard on a removed or re-pointed profile's old credential"

// cmdProfile is `cs profile create|seed|list|remove|forget|pool|set|which|hook`.
func cmdProfile(args []string) error {
	if len(args) == 0 {
		return appErr(codeUsage, profileUsage, "name a profile command")
	}
	if args[0] == "create" {
		return cmdProfileCreate(args[1:])
	}
	fs := appFlags("profile " + args[0])
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	to := fs.String("to", "", "pool remove, remove: the profile whose pool the accounts join instead")
	yes := fs.Bool("yes", false, "remove: do not ask for confirmation")
	dir := fs.String("dir", "", "which: the folder to ask about (default: this one)")
	hook := fs.Bool("hook", false, "which: the one line the shell hook reads")
	p, err := parseApp(fs, args[1:], profileUsage)
	if err != nil {
		return err
	}
	if args[0] != "which" && (*dir != "" || *hook) {
		return appErr(codeUsage, profileUsage, "--dir and --hook are flags of profile which")
	}
	w := io.Writer(os.Stdout)
	switch args[0] {
	case "seed":
		if len(p) != 2 {
			return appErr(codeUsage, profileUsage, "seed takes a profile and an account")
		}
		if err := humanToStderr(*asJSON, func() error { return profileSeed(os.Stdout, *cfgPath, p[0], p[1]) }); err != nil {
			return err
		}
		if *asJSON {
			return emitTo(w, map[string]any{"profile": p[0], "seeded": p[1]})
		}
		return nil
	case "list":
		if len(p) != 0 {
			return appErr(codeUsage, profileUsage, "list takes no arguments")
		}
		if *asJSON {
			return profileListJSON(w, *cfgPath)
		}
		return profileList(w, *cfgPath)
	case "pool":
		if len(p) != 3 {
			return appErr(codeUsage, poolUsage, "pool takes a profile, add or remove, and an account")
		}
		return profilePool(w, *cfgPath, p[0], p[1], p[2], *to, *asJSON)
	case "set":
		if len(p) != 3 {
			return appErr(codeUsage, profileUsage, "set takes a profile, a key and a value")
		}
		return profileSet(w, *cfgPath, p[0], p[1], p[2], *asJSON)
	case "remove":
		if len(p) != 1 {
			return appErr(codeUsage, profileRemoveUsage, "remove takes one profile name")
		}
		return profileRemove(w, *cfgPath, p[0], *to, *yes, *asJSON)
	case "which":
		if len(p) != 0 {
			return appErr(codeUsage, profileUsage, "which takes no arguments (--dir names a folder)")
		}
		return profileWhich(w, *cfgPath, *dir, *asJSON, *hook)
	case "hook":
		if len(p) != 1 {
			return appErr(codeUsage, profileUsage, "hook takes a shell: zsh, bash or fish")
		}
		return profileHook(w, p[0])
	case "forget":
		if len(p) != 1 {
			return appErr(codeUsage, profileUsage, "forget takes one profile name")
		}
		return profileForget(w, *cfgPath, p[0], *asJSON)
	}
	return appErr(codeUsage, profileUsage, "unknown profile command %q", args[0])
}

// profileForget is `cs profile forget <name>`.
func profileForget(w io.Writer, cfgPath, name string, asJSON bool) error {
	// The state alone: the guard can be released whatever the config says,
	// and nothing here should reconcile observations against it.
	st, err := state.Load("")
	if err != nil {
		return err
	}
	// The config only to see which guards an offline edit implies; an
	// unreadable one implies none, and the recorded ghosts are still released.
	cfg, _ := loadIgnoringBadNames(cfgPath)
	gs := allGhosts(cfg, st)
	n := st.DropGhostsOf(name)
	for _, g := range offlineGhosts(cfg, st) {
		if g.Profile == name && forgetProfileRecord(st, name) {
			n++
		}
	}
	if n == 0 {
		var names []string
		for _, g := range gs {
			names = append(names, g.Profile)
		}
		if len(names) == 0 {
			return appErr(codeNotFound, "", "no old credential of profile %q is guarded; nothing is", name)
		}
		return appErr(codeNotFound, "guarded: "+strings.Join(names, ", "),
			"no old credential of profile %q is guarded; guarded: %s", name, strings.Join(names, ", "))
	}
	if err := st.Save(); err != nil {
		return errors.Join(errors.New("could not save the state"), err)
	}
	if asJSON {
		return emitTo(w, map[string]any{"profile": name, "released": n})
	}
	fmt.Fprintf(w, "  released %d guarded credential(s) of profile %s; its account(s) may be used elsewhere again\n",
		n, name)
	return nil
}
