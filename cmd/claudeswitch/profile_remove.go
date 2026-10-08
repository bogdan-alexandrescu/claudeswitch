package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// `cs profile remove <name> [--to <profile>]` (lane 15): removes a
// [[profile]] block. Its pool's accounts move to --to, or to `default` (D6);
// with neither, an enabled account would be in no pool, which is refused
// (would_orphan). The last profile cannot be removed (last_profile), and
// `default` needs --to while other profiles exist, since accounts in no
// pool join it.
//
// Nothing on disk but the config changes: the profile's folder and its
// keychain item (or credential file) stay. The account last live there may
// still be — a Claude Code session can keep running on the old folder — so
// it becomes a ghost (D22, D25) and is installed nowhere else until that
// item no longer holds it or `cs profile forget <name>`:
//
//   - with a daemon running, its hot reload stops the profile's loop and
//     records the ghost (makeGhost, D21); the command waits up to 10 s for
//     it to run the edited config and says whether it did. Until then, and
//     whatever happens, the CLI's checks derive the ghost from the state's
//     record (offlineGhosts).
//   - with none, the daemon lock is held from the first check to the end
//     (no daemon starts midway) and the command records the ghost itself,
//     as a starting daemon would (adoptOfflineGhosts).

const profileRemoveUsage = "usage: claudeswitch profile remove <name> [--to <profile>] [--yes] [--json]"

// profileRemove is `cs profile remove`.
func profileRemove(w io.Writer, cfgPath, name, to string, yes, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if declaredIndex(cfg, name) < 0 {
		if _, ok := cfg.ProfileNamed(name); ok {
			return appErr(codeLastProfile, "",
				"profile %q is the only profile (the config declares no [[profile]] blocks), so there is none to remove",
				name)
		}
		return noSuchProfile(cfg, name)
	}
	if len(cfg.Profiles) == 1 {
		return appErr(codeLastProfile, "every account needs a profile; create another first: claudeswitch profile create <name>",
			"profile %q is the only profile, so it cannot be removed", name)
	}

	dst := to
	switch {
	case to == name:
		return appErr(codeUsage, profileRemoveUsage, "--to names the profile being removed")
	case to != "":
		if declaredIndex(cfg, to) < 0 {
			return noSuchProfile(cfg, to)
		}
	case name == config.DefaultProfile:
		return appErr(codeUsage, "say where its accounts go: claudeswitch profile remove default --to <profile>",
			"accounts in no pool join profile %q (D6), so removing it needs --to to say where its accounts go",
			config.DefaultProfile)
	case declaredIndex(cfg, config.DefaultProfile) >= 0:
		dst = config.DefaultProfile
	}
	eff, _ := cfg.ProfileNamed(name)
	moving := append([]string{}, eff.Pool...)
	if dst == "" {
		for _, id := range moving {
			if accountByID(cfg, id).IsEnabled() {
				return appErr(codeWouldOrphan,
					fmt.Sprintf("say where its accounts go: claudeswitch profile remove %s --to <profile>", name),
					"removing profile %q would leave %q in no pool (there is no %q profile), which the config refuses",
					name, id, config.DefaultProfile)
			}
		}
	}

	daemon := false
	lock, lerr := tryDaemonLock()
	switch {
	case errors.Is(lerr, state.ErrDaemonRunning):
		daemon = true
	case lerr != nil:
		return fmt.Errorf("could not take the daemon lock, so nothing was removed: %w", lerr)
	default:
		defer lock.Release()
	}

	live := ""
	if ps := st.Profiles[name]; ps != nil && ps.Active != state.Unattributed {
		live = ps.Active
	}
	if !yes {
		what := removeSummary(name, eff, moving, dst, live)
		if !isTerminal() {
			return appErr(codeConfirm, "pass --yes to remove it", "%s", what)
		}
		fmt.Fprintf(os.Stderr, "\n  %s\n", what)
		if !askYes(fmt.Sprintf("  remove profile %s", name), false) {
			return appErr(codeConfirm, "", "not removed")
		}
	}

	err = editConfigText(cfg.Path, func(text string) (string, error) {
		out, err := deleteBlock(text, blockRef{"profile", name})
		if err != nil {
			return "", err
		}
		if dst == "" || len(moving) == 0 {
			return out, nil
		}
		next, found, err := mapArray(out, blockRef{"profile", dst}, "pool", func(v []string) []string {
			for _, id := range moving {
				if !slices.Contains(v, id) {
					v = append(v, id)
				}
			}
			return v
		})
		if err != nil {
			return "", err
		}
		if !found {
			next, err = setKey(out, blockRef{"profile", dst}, "pool", tomlList(moving))
		}
		return next, err
	}, func(back *config.Config) error {
		if declaredIndex(back, name) >= 0 {
			return fmt.Errorf("the edit parsed but profile %q is still declared; discarded", name)
		}
		if dst == "" {
			return nil
		}
		for _, id := range moving {
			if got, _ := back.ProfileOf(id); got != dst {
				return fmt.Errorf("the edit parsed but %q landed in the pool of profile %q, not of %q; discarded", id, got, dst)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	back, err := config.Load(cfg.Path)
	if err != nil {
		return err
	}

	var loaded any // null with no daemon
	stateErr := error(nil)
	if daemon {
		// Confirmed only when the daemon is seen running the edited config.
		ok := false
		if target, err := configTarget(cfg.Path); err == nil {
			if raw, err := os.ReadFile(target); err == nil {
				ok = waitDaemonConfig(config.ContentHash(raw))
			}
		}
		loaded = ok
	} else {
		fresh, err := state.Load("", back.ProfileNames()...)
		if err != nil {
			fresh = st
		}
		recorded := false
		for _, g := range offlineGhosts(back, fresh) {
			if g.Profile == name {
				fresh.AddGhost(g)
				recorded = true
			}
		}
		if recorded {
			forgetProfileRecord(fresh, name)
			stateErr = fresh.Save()
		}
	}

	fresh, err := state.Load("", back.ProfileNames()...)
	if err != nil {
		fresh = st
	}
	var ghost map[string]any
	for _, g := range allGhosts(back, fresh) {
		if g.Profile == name {
			ghost = map[string]any{"profile": g.Profile, "account": g.Account, "why": g.Why, "since": g.Since.UTC()}
			break
		}
	}
	credential := ""
	if ps := st.Profiles[name]; ps != nil && ps.Item != nil {
		credential = ps.Item.Service
	}

	if asJSON {
		pools := map[string][]string{}
		for _, in := range back.EffectiveProfiles() {
			pools[in.Name] = append([]string{}, in.Pool...)
		}
		var g any
		if ghost != nil {
			g = ghost
		}
		return emitTo(w, map[string]any{
			"profile": name, "to": orNull(dst), "moved": moving, "ghost": g,
			"kept":           map[string]any{"dir": orNull(eff.Dir), "credential": orNull(credential)},
			"daemon_running": daemon, "daemon_loaded": loaded, "pools": pools,
		})
	}
	fmt.Fprintf(w, "\n  ✓ removed profile %s from %s\n", name, cfg.Path)
	if len(moving) > 0 {
		if dst != "" {
			fmt.Fprintf(w, "    its accounts (%s) are now in profile %s's pool\n", strings.Join(moving, ", "), dst)
		} else {
			fmt.Fprintf(w, "    its accounts (%s) are disabled and now in no pool\n", strings.Join(moving, ", "))
		}
	}
	fmt.Fprintf(w, "    its folder %s and its saved login were not deleted\n", nonEmpty(eff.Dir, "~/.claude"))
	if ghost != nil {
		fmt.Fprintf(w, "    %s may still be signed in there, so it stays guarded and is used in no other profile\n"+
			"    until that login is gone, or: claudeswitch profile forget %s\n", ghost["account"], name)
	}
	switch {
	case daemon && loaded == true:
		fmt.Fprintln(w, "    the running daemon has stopped that profile")
	case daemon:
		fmt.Fprintln(w, "    the running daemon has not loaded the change yet; it stops that profile when it does")
	}
	if stateErr != nil {
		fmt.Fprintf(w, "    note: the state was not saved: %v\n", stateErr)
	}
	fmt.Fprintln(w)
	return nil
}

// removeSummary is what removing a profile does, for the confirmation: the
// CLI's own refusal message, which the app shows as its confirmation text.
func removeSummary(name string, eff config.Profile, moving []string, dst, live string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "removing profile %q deletes its [[profile]] block", name)
	switch {
	case len(moving) == 0:
		b.WriteString("; its pool is empty")
	case dst != "":
		fmt.Fprintf(&b, "; its accounts (%s) move to profile %q", strings.Join(moving, ", "), dst)
	default:
		fmt.Fprintf(&b, "; its disabled accounts (%s) are left in no pool", strings.Join(moving, ", "))
	}
	fmt.Fprintf(&b, ". Its folder %s and its saved login stay on disk: nothing is deleted. ",
		nonEmpty(eff.Dir, "~/.claude"))
	if live != "" {
		fmt.Fprintf(&b, "%s was last live there and may still be signed in, so it stays guarded (a ghost): "+
			"it is installed in no other profile until that login is gone, or `claudeswitch profile forget %s`.",
			live, name)
	} else {
		b.WriteString("Nothing was recorded live there, so nothing stays guarded.")
	}
	return b.String()
}
