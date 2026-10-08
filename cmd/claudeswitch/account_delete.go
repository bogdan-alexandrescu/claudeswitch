package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Seams for `account delete`.
var (
	tryDaemonLock = state.TryDaemonLock
	// A delete with a daemon running waits up to deleteWait for it to load
	// the config without the account, looking every deletePoll.
	deleteWait = 10 * time.Second
	deletePoll = 100 * time.Millisecond
	// waitDaemonConfig reports whether the running daemon now runs the
	// config whose content hash is hash (or no daemon runs any more); false
	// on timeout. A marker from an earlier run or an earlier config never
	// matches.
	waitDaemonConfig = func(hash string) bool {
		deadline := time.Now().Add(deleteWait)
		for {
			if st, err := state.Load(""); err == nil && hash != "" && st.DaemonConfigHash == hash {
				return true
			}
			if !daemonRunning() {
				return true
			}
			if !time.Now().Before(deadline) {
				return false
			}
			time.Sleep(min(deletePoll, time.Until(deadline)))
		}
	}
)

// accountDelete removes an account everywhere (owner decision M5): its
// vault credential, its [[account]] block, its pool and priority entries
// and its state records. Refused while the account is or may be live in
// any profile or ghost (§3, D18) — unless another vaulted name holds the
// very same credential, the one repair for that corrupt state.
//
// Liveness is checked before the confirmation and again after it, since a
// prompt can sit for any length of time (lane 12 security review), and
// again just before the credential goes. With no daemon running the daemon
// lock is held throughout, as rename holds it, so none can start midway.
// With one running (owner decision: delete works while it runs), the
// config edit comes first; the credential is deleted only once the daemon
// has reloaded a config without the account — from then on it neither
// polls it nor swaps to it, and its reload drops the state record — and the
// account is still live nowhere. A timeout, or the account turning live
// meanwhile, puts the config back and keeps the credential.
func accountDelete(w io.Writer, cfgPath, id string, yes, asJSON bool) error {
	cfg, st, err := load(cfgPath)
	if err != nil {
		return wrapErr(codeConfigInvalid, "", err)
	}
	daemon := false
	lock, lerr := tryDaemonLock()
	switch {
	case errors.Is(lerr, state.ErrDaemonRunning):
		daemon = true
	case lerr != nil:
		return fmt.Errorf("could not take the daemon lock, so nothing was deleted: %w", lerr)
	default:
		defer lock.Release()
	}

	inConfig := hasAccount(cfg, id)
	vaulted := acctSeams.has(id)
	inState := stateNames(st, id) || slices.Contains(st.Vaulted, id)
	if !inConfig && !vaulted && !inState {
		return appErr(codeNotFound, "", "no account %q in the config, the vault or the state", id)
	}
	seat, org, email := "", "", ""
	if vaulted {
		seat, org = acctSeams.identity(id)
		email = acctSeams.describe(id)
	}
	if seat == "" {
		seat = cfg.SeatOf(id)
	}
	twin := ""
	if vaulted {
		twin = acctSeams.twin(cfg, st, id)
	}
	// check is the §3 test against state read afresh.
	check := func() error {
		fresh, err := state.Load("", cfg.ProfileNames()...)
		if err != nil {
			return fmt.Errorf("could not read the state, so nothing more was deleted: %w", err)
		}
		if holder, live := accountLiveIn(cfg, fresh, id, func(string, bool) bool { return true }); live && twin == "" {
			return liveRefusal(id, holder, "deleted")
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}

	who := id
	if email != "" {
		who += " (" + email + ")"
	}
	if seat != "" {
		who += ", seat " + usage.ShortSeat(seat)
	}
	if !yes {
		if !isTerminal() {
			return appErr(codeConfirm, "pass --yes to delete it",
				"deleting %s removes its credential, its config block, its pool and priority entries and "+
					"its records; signing in again is the only way back", who)
		}
		if !askYes(fmt.Sprintf("\n  delete %s? Its credential is gone for good", who), false) {
			return appErr(codeConfirm, "", "not deleted")
		}
		if err := check(); err != nil {
			return err
		}
	}

	pool := ""
	for _, in := range cfg.Profiles {
		if slices.Contains(in.Pool, id) {
			pool = in.Name
		}
	}
	inPriority := slices.Contains(cfg.Priority, id)

	putBack := func(err error) error { return err }
	waitFor := "" // the content hash of the config the daemon must run
	if inConfig {
		target, err := configTarget(cfg.Path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		var wrote []byte
		putBack = func(cause error) error {
			// Compare-and-swap: only the edit this delete made is undone. A
			// config someone changed since is theirs, and stays.
			if cur, err := os.ReadFile(target); err != nil || !bytes.Equal(cur, wrote) {
				return &appError{Code: codeConfigChanged, Err: cause,
					Hint: fmt.Sprintf("the credential was kept; add the [[account]] block for %q back by hand "+
						"if you want it, or delete it again", id),
					Message: cause.Error() + "; and the config was changed by something else meanwhile, so " +
						"this delete's edit was not undone"}
			}
			if rerr := writeConfigFile(target, raw, nil); rerr != nil {
				return fmt.Errorf("%w; and putting the config back failed too: %v", cause, rerr)
			}
			return cause
		}
		err = editConfigText(cfg.Path, func(text string) (string, error) {
			return dropAccountText(text, cfg, id)
		}, func(back *config.Config) error {
			if hasAccount(back, id) || slices.Contains(back.Priority, id) {
				return fmt.Errorf("the edit parsed but still names %q; discarded", id)
			}
			for _, in := range back.Profiles {
				if slices.Contains(in.Pool, id) {
					return fmt.Errorf("the edit parsed but profile %q's pool still names %q; discarded", in.Name, id)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if wrote, err = os.ReadFile(target); err != nil {
			return putBack(err)
		}
		waitFor = config.ContentHash(wrote)
	} else if raw, err := os.ReadFile(cfg.Path); err == nil {
		// Not in the config: still wait for the daemon to run the config as
		// it is, so no older one of its own can name the account.
		waitFor = config.ContentHash(raw)
	}
	if daemon && waitFor != "" && !waitDaemonConfig(waitFor) {
		return putBack(appErr(codeDaemonNotLoaded, "check `claudeswitch daemon status`, then try again",
			"the running daemon did not load the config without %q within %s, so its credential was "+
				"kept and the config put back", id, deleteWait))
	}
	// Last look before the credential goes: it may have turned live while
	// the config was edited or the daemon reloaded.
	if err := check(); err != nil {
		return putBack(err)
	}
	if vaulted {
		if err := acctSeams.deleteVault(id); err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return putBack(fmt.Errorf("could not delete the credential, so the config was put back: %w", err))
		}
	}

	fresh, err := state.Load("", cfg.ProfileNames()...)
	if err != nil {
		fresh = st
	}
	fresh.Drop(id)
	fresh.DropVaulted(id)
	fresh.ForgetIdentity(id)
	for _, ps := range fresh.Profiles {
		if ps == nil {
			continue
		}
		if ps.Pinned == id {
			ps.Pinned = ""
		}
		if ps.Active == id {
			// Only reachable through the twin: the credential live there is
			// filed under the twin's name now.
			ps.Active, ps.ActiveAt = "", time.Now()
		}
	}
	stateErr := fresh.Save()

	if asJSON {
		return emitTo(w, map[string]any{
			"account": id, "seat": orNull(seat), "org_id": orNull(org), "email": orNull(email),
			"removed": map[string]any{
				"credential": vaulted, "config": inConfig, "priority": inPriority, "pool": orNull(pool),
				"state": stateErr == nil,
			},
			"twin": orNull(twin), "daemon_running": daemon,
		})
	}
	if twin != "" {
		fmt.Fprintf(w, "\n  note: %s holds this same credential, so it stays vaulted under that name.\n", twin)
	}
	fmt.Fprintf(w, "\n  ✓ removed %s\n", who)
	if vaulted {
		fmt.Fprintf(w, "    the credential is gone; signing in to that account again is the only way back\n")
	}
	if inConfig {
		fmt.Fprintf(w, "    its [[account]] block")
		if inPriority {
			fmt.Fprintf(w, ", priority entry")
		}
		if pool != "" {
			fmt.Fprintf(w, " and profile %s's pool entry", pool)
		}
		fmt.Fprintf(w, " are gone from %s\n", cfg.Path)
	}
	if stateErr != nil {
		fmt.Fprintf(w, "    note: the state was not saved: %v\n", stateErr)
	}
	fmt.Fprintln(w)
	return nil
}
