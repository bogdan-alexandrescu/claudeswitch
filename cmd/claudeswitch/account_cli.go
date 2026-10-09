package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// `cs account …` (IMPROVEMENTS M3, M5): list (lane 15), rename, delete,
// pin, unpin and priority, each with --json. (`scope` was removed in lane 16.)

// acctDeps are the account commands' reaches into the vault. A seam.
type acctDeps struct {
	has         func(id string) bool
	identity    func(id string) (seat, org string)
	describe    func(id string) string
	plan        func(id string) string
	deleteVault func(id string) error
	twin        func(cfg *config.Config, st *state.State, id string) string
}

func realAcctDeps() acctDeps {
	v := func() *vault.Vault { return vault.New(logger(false)) }
	return acctDeps{
		has:         func(id string) bool { return v().Has(id) },
		identity:    func(id string) (string, string) { return v().IdentityOf(id) },
		describe:    func(id string) string { return v().DescribeOf(id) },
		plan:        func(id string) string { return v().PlanOf(id) },
		deleteVault: func(id string) error { return keychain.Delete(keychain.VaultService(id)) },
		twin:        otherHoldingSameCredential,
	}
}

var acctSeams = realAcctDeps()

const accountUsage = "usage: claudeswitch account list | rename <old> <new> | delete <id> [--yes] | " +
	"pin <id> [--hard] | unpin [<id>] [--profile P] | priority <id>...   (each takes --json)"

// cmdAccount dispatches `cs account <verb>`.
func cmdAccount(args []string) error {
	if len(args) == 0 {
		return appErr(codeUsage, accountUsage, "name an account command")
	}
	verb, rest := args[0], args[1:]
	fs := appFlags("account " + verb)
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	profName := fs.String("profile", "", "unpin: the profile to unpin")
	hard := fs.Bool("hard", false, "pin: stay even when the account is refused, out of quota or needs a sign-in")
	p, err := parseApp(fs, rest, accountUsage)
	if err != nil {
		return err
	}
	w := os.Stdout
	switch verb {
	case "list":
		if len(p) != 0 {
			return appErr(codeUsage, accountUsage, "list takes no arguments")
		}
		return accountList(w, *cfgPath, *asJSON)
	case "rename":
		if len(p) != 2 {
			return appErr(codeUsage, accountUsage, "rename takes the old and the new id")
		}
		return accountRename(w, *cfgPath, p[0], p[1], *asJSON)
	case "delete", "remove":
		if len(p) != 1 {
			return appErr(codeUsage, accountUsage, "%s takes one account id", verb)
		}
		return accountDelete(w, *cfgPath, p[0], *yes, *asJSON)
	case "pin":
		if len(p) != 1 {
			return appErr(codeUsage, accountUsage, "pin takes one account id")
		}
		return accountPin(w, *cfgPath, p[0], *hard, *asJSON)
	case "unpin":
		if len(p) > 1 {
			return appErr(codeUsage, accountUsage, "unpin takes at most one account id")
		}
		id := ""
		if len(p) == 1 {
			id = p[0]
		}
		return accountUnpin(w, *cfgPath, id, *profName, *asJSON)
	case "priority":
		return setPriority(w, *cfgPath, p, *asJSON)
	}
	return appErr(codeUsage, accountUsage, "unknown account command %q", verb)
}

// cmdPriority is `cs priority <id...>`.
func cmdPriority(args []string) error {
	fs := appFlags("priority")
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	p, err := parseApp(fs, args, "usage: claudeswitch priority <id>... [--json]")
	if err != nil {
		return err
	}
	return setPriority(os.Stdout, *cfgPath, p, *asJSON)
}

// cmdRemove is `cs remove <id>`, the same as `cs account delete <id>`.
func cmdRemove(args []string) error {
	fs := appFlags("remove")
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	p, err := parseApp(fs, args, "usage: claudeswitch remove <account-id> [--yes] [--json]")
	if err != nil {
		return err
	}
	if len(p) != 1 {
		return appErr(codeUsage, "usage: claudeswitch remove <account-id> [--yes] [--json]", "name one account")
	}
	return accountDelete(os.Stdout, *cfgPath, p[0], *yes, *asJSON)
}

func accountRename(w io.Writer, cfgPath, oldID, newID string, asJSON bool) error {
	if err := checkNewAccountID(newID); err != nil {
		return wrapErr(codeInvalidValue, "", err)
	}
	if oldID == newID {
		return appErr(codeUsage, "", "those are the same id")
	}
	if err := humanToStderr(asJSON, func() error { return renameAccount(cfgPath, oldID, newID) }); err != nil {
		return err
	}
	if asJSON {
		return emitTo(w, map[string]any{"account": newID, "previous": oldID})
	}
	return nil
}

// dropAccountText removes id's [[account]] block and every list entry
// naming it: priority, and each profile's pool.
func dropAccountText(text string, cfg *config.Config, id string) (string, error) {
	out, err := deleteBlock(text, blockRef{"account", id})
	if err != nil {
		return "", err
	}
	drop := func(v []string) []string { return slices.DeleteFunc(v, func(s string) bool { return s == id }) }
	if slices.Contains(cfg.Priority, id) {
		if out, _, err = mapArray(out, blockRef{}, "priority", drop); err != nil {
			return "", err
		}
	}
	for _, in := range cfg.Profiles {
		if slices.Contains(in.Pool, id) {
			if out, _, err = mapArray(out, blockRef{"profile", in.Name}, "pool", drop); err != nil {
				return "", err
			}
		}
	}
	return out, nil
}

// accountPin suspends automatic rotation in the profile id is live in
// (policy: "pinned to X"). Only the live account can be pinned: a pin on
// another would hold the profile on whatever it is using now.
//
// The daemon lifts a pin on an account that is refused, out of quota or
// needs a sign-in (IMPROVEMENTS F3, policy.PinLifted); hard keeps it even
// then, as every pin did before.
func accountPin(w io.Writer, cfgPath, id string, hard, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if !hasAccount(cfg, id) {
		return appErr(codeNotFound, "", "no account %q in the config", id)
	}
	prof, ok := cfg.ProfileOf(id)
	if !ok {
		// Profiles declared, no default, a disabled account no pool lists:
		// it can be live in no profile, so there is nothing to pin.
		return appErr(codeNotActive, "", "%q is in no profile's pool, so it is live in no profile", id)
	}
	ps := st.Profile(prof)
	if ps.Active != id {
		return appErr(codeNotActive, fmt.Sprintf("switch to it first: claudeswitch use %s --profile %s", id, prof),
			"%q is not the account live in profile %s, so pinning it would hold that profile on %s", id, prof,
			nonEmpty(ps.Active, "no recorded account"))
	}
	ps.Pinned, ps.PinHard = id, hard
	if err := st.Save(); err != nil {
		return err
	}
	if asJSON {
		return emitTo(w, map[string]any{"profile": prof, "pinned": id, "pin_hard": hard})
	}
	fmt.Fprintf(w, "  pinned %s in profile %s: automatic rotation is off there until `cs account unpin %s`\n",
		id, prof, id)
	if hard {
		fmt.Fprintf(w, "  a hard pin: it stays even if %s is refused, runs out or needs a sign-in\n", id)
	} else {
		fmt.Fprintf(w, "  if %s is refused, runs out or needs a sign-in, the daemon lifts the pin and rotates "+
			"(`--hard` keeps it)\n", id)
	}
	return nil
}

// accountUnpin turns automatic rotation back on: in the profile named, or
// in every profile pinned to id, or (neither given) in every profile.
func accountUnpin(w io.Writer, cfgPath, id, profName string, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	if profName != "" {
		if _, ok := cfg.ProfileNamed(profName); !ok {
			return noSuchProfile(cfg, profName)
		}
	}
	unpinned := []string{}
	for _, name := range cfg.ProfileNames() {
		ps := st.Profile(name)
		if ps.Pinned == "" || (profName != "" && name != profName) || (id != "" && ps.Pinned != id) {
			continue
		}
		ps.Pinned, ps.PinHard = "", false
		unpinned = append(unpinned, name)
	}
	if len(unpinned) > 0 {
		if err := st.Save(); err != nil {
			return err
		}
	}
	if asJSON {
		return emitTo(w, map[string]any{"unpinned": unpinned})
	}
	if len(unpinned) == 0 {
		fmt.Fprintln(w, "  nothing was pinned")
		return nil
	}
	fmt.Fprintf(w, "  unpinned %s: automatic rotation is on again\n", strings.Join(unpinned, ", "))
	return nil
}

// setPriority writes the rotation order: the ids given, in that order.
// Accounts left out follow in config order, as they always have.
func setPriority(w io.Writer, cfgPath string, ids []string, asJSON bool) error {
	if len(ids) == 0 {
		return appErr(codeUsage, "usage: claudeswitch priority <id>...", "name the accounts in order")
	}
	cfg, err := loadForEdit(cfgPath)
	if err != nil {
		return err
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
	}
	err = editConfigText(cfg.Path, func(text string) (string, error) {
		return setKey(text, blockRef{}, "priority", tomlList(ids))
	}, func(back *config.Config) error {
		if !slices.Equal(back.Priority, ids) {
			return fmt.Errorf("the edit parsed but the priority came out as %v; discarded", back.Priority)
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
	order := []string{}
	for _, a := range back.Ordered() {
		order = append(order, a.ID)
	}
	if asJSON {
		return emitTo(w, map[string]any{"priority": ids, "order": order})
	}
	fmt.Fprintf(w, "  rotation order: %s\n", strings.Join(order, ", "))
	return nil
}
