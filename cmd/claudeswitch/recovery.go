package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/vault"
)

// `cs recovery` (IMPROVEMENTS I1a): the credentials a swap kept because it
// could not tell whose they were, and the way back into the vault.

// recoveryDeps are the command's reaches into the store and the network.
type recoveryDeps struct {
	list     func(profiles []string) []vault.RecoveryItem
	identify func(ctx context.Context, it vault.RecoveryItem) (*usage.Profile, error)
	restore  func(ctx context.Context, slot, account, seat string, force bool) (*vault.Entry, error)
	clear    func(slot string) error
	// confirm asks a yes/no question; nil when nobody is at a terminal.
	confirm func(prompt string) bool
}

type recoveryOpts struct{ identify, force, yes bool }

func defaultRecoveryDeps() recoveryDeps {
	v := vault.New(logger(false))
	d := recoveryDeps{
		list:     vault.ListRecovery,
		identify: v.IdentifyRecovery,
		restore:  v.RestoreRecovery,
		clear:    vault.ClearRecovery,
	}
	if isTerminal() {
		d.confirm = func(prompt string) bool {
			a := strings.ToLower(strings.TrimSpace(ask(prompt+" [y/N]", "")))
			return a == "y" || a == "yes"
		}
	}
	return d
}

func cmdRecovery(args []string) error {
	fs := flag.NewFlagSet("recovery", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	identify := fs.Bool("identify", false,
		"ask whose each kept credential is (one identity lookup each, through the shared call budget)")
	force := fs.Bool("force", false, "restore: replace the vaulted credential even when it looks better")
	yes := fs.Bool("yes", false, "clear: do not ask for confirmation")
	positional := parseInterleaved(fs, args)
	cfg, err := config.Load(*cfgPath)
	if err != nil && cfg == nil {
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: %v\n", err)
	}
	return runRecovery(os.Stdout, cfg, positional, recoveryOpts{identify: *identify, force: *force, yes: *yes},
		defaultRecoveryDeps())
}

// runRecovery is `cs recovery [restore <slot> <account> | clear <slot>]`.
func runRecovery(w io.Writer, cfg *config.Config, args []string, o recoveryOpts, deps recoveryDeps) error {
	if len(args) == 0 {
		return listRecovery(w, cfg, o, deps)
	}
	switch args[0] {
	case "list":
		return listRecovery(w, cfg, o, deps)
	case "restore":
		if len(args) != 3 {
			return fmt.Errorf("usage: claudeswitch recovery restore <slot> <account> [--force]")
		}
		return restoreRecovery(w, cfg, args[1], args[2], o, deps)
	case "clear":
		if len(args) != 2 {
			return fmt.Errorf("usage: claudeswitch recovery clear <slot> [--yes]")
		}
		return clearRecovery(w, cfg, args[1], o, deps)
	}
	return fmt.Errorf("usage: claudeswitch recovery [--identify] | restore <slot> <account> [--force] | clear <slot> [--yes]")
}

func listRecovery(w io.Writer, cfg *config.Config, o recoveryOpts, deps recoveryDeps) error {
	items := deps.list(cfg.ProfileNames())
	fmt.Fprintln(w)
	if len(items) == 0 {
		fmt.Fprintln(w, "  no recovery copies kept")
		fmt.Fprintln(w)
		return nil
	}
	fmt.Fprintln(w, "  Recovery copies: live credentials a swap overwrote and could not file under an account.")
	fmt.Fprintln(w, "  Each may be the only copy of a working login.")
	fmt.Fprintln(w)
	now := time.Now()
	for _, it := range items {
		prof := it.Profile
		if prof == "" {
			prof = "(no profile)"
		}
		fmt.Fprintf(w, "  %-14s profile %s\n", it.Slot, prof)
		if it.Err != nil {
			fmt.Fprintf(w, "  %-14s unreadable: %v\n", "", it.Err)
			continue
		}
		kept := "kept at an unrecorded time"
		if !it.KeptAt.IsZero() {
			kept = fmt.Sprintf("kept %s ago (%s)", ageShort(now.Sub(it.KeptAt)), it.KeptAt.Local().Format("01-02 15:04"))
		}
		fmt.Fprintf(w, "  %-14s %s\n", "", kept)
		seat := it.Seat
		who := ""
		if o.identify {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			pr, err := deps.identify(ctx, it)
			cancel()
			switch {
			case err != nil:
				who = fmt.Sprintf("could not identify: %v", err)
			default:
				seat = pr.Seat()
				who = pr.Describe()
			}
		}
		line := "seat not identified when kept"
		if seat != "" {
			line = "seat " + usage.ShortSeat(seat)
			if acct := cfg.AccountBySeat(seat); acct != "" {
				line += ", account " + acct
			} else {
				line += ", no configured account"
			}
		}
		if who != "" {
			line += " (" + who + ")"
		}
		fmt.Fprintf(w, "  %-14s %s\n", "", line)
		fmt.Fprintf(w, "  %-14s %s\n", "", recoveryValidity(it, now))
	}
	fmt.Fprintln(w)
	if !o.identify {
		fmt.Fprintln(w, "  `cs recovery --identify` asks whose each one is (one identity lookup each).")
	}
	fmt.Fprintln(w, "  `cs recovery restore <slot> <account>` vaults one under its account, after checking its seat;")
	fmt.Fprintln(w, "  `cs recovery clear <slot>` deletes one.")
	fmt.Fprintln(w)
	return nil
}

// recoveryValidity says whether a kept credential still works or can be
// renewed, from its recorded expiries.
func recoveryValidity(it vault.RecoveryItem, now time.Time) string {
	access := "access token expiry unknown"
	if !it.Expiry.IsZero() {
		if it.Expiry.After(now) {
			access = "access token valid " + ageShort(it.Expiry.Sub(now)) + " more"
		} else {
			access = "access token expired " + ageShort(now.Sub(it.Expiry)) + " ago"
		}
	}
	switch {
	case !it.HasRefresh:
		return access + "; no refresh token, cannot be renewed"
	case !it.RefreshExpiry.IsZero() && !it.RefreshExpiry.After(now):
		return access + "; refresh token expired, cannot be renewed"
	case !it.RefreshExpiry.IsZero():
		return access + "; renewable for " + ageShort(it.RefreshExpiry.Sub(now))
	}
	return access + "; renewable"
}

func restoreRecovery(w io.Writer, cfg *config.Config, slot, account string, o recoveryOpts, deps recoveryDeps) error {
	if !hasAccount(cfg, account) {
		return fmt.Errorf("%q is not a configured account; restore files a credential under an account the "+
			"config already has, after checking it is that account's seat", account)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	e, err := deps.restore(ctx, slot, account, cfg.SeatOf(account), o.force)
	if err != nil {
		if e != nil {
			fmt.Fprintf(w, "\n  ✓ vaulted slot %s as %s\n", slot, account)
		}
		return err
	}
	fmt.Fprintf(w, "\n  ✓ vaulted slot %s as %s (seat %s) and cleared the slot\n", slot, account,
		usage.ShortSeat(e.AccountUUID+"@"+e.OrgID))
	if e.NoRefreshToken {
		fmt.Fprintf(w, "    ⚠ no refresh token: it works until its access token expires %s\n", humanUntil(e.Expiry))
	}
	fmt.Fprintln(w)
	return nil
}

func clearRecovery(w io.Writer, cfg *config.Config, slot string, o recoveryOpts, deps recoveryDeps) error {
	if !o.yes {
		if deps.confirm == nil {
			return fmt.Errorf("clearing slot %s deletes what may be the only copy of a login; "+
				"run it at a terminal to confirm, or pass --yes", slot)
		}
		what := ""
		for _, it := range deps.list(cfg.ProfileNames()) {
			if it.Slot == slot && it.Seat != "" {
				what = " (seat " + usage.ShortSeat(it.Seat) + ")"
			}
		}
		if !deps.confirm(fmt.Sprintf("  delete recovery slot %s%s? It may be the only copy of a login", slot, what)) {
			return errors.New("not cleared")
		}
	}
	if err := deps.clear(slot); err != nil {
		return err
	}
	fmt.Fprintf(w, "  cleared recovery slot %s\n", slot)
	return nil
}

// ageShort renders a duration as "45m", "26h" or "3d".
func ageShort(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// doctorRecoveryLine is doctor's mention of kept recovery copies: a warning
// with the count and the oldest, or "" when there are none.
func doctorRecoveryLine(items []vault.RecoveryItem, now time.Time) string {
	if len(items) == 0 {
		return ""
	}
	oldest := time.Time{}
	for _, it := range items {
		if !it.KeptAt.IsZero() && (oldest.IsZero() || it.KeptAt.Before(oldest)) {
			oldest = it.KeptAt
		}
	}
	s := fmt.Sprintf("  [warn] recovery        %d kept credential(s) a swap could not file", len(items))
	if !oldest.IsZero() {
		s += fmt.Sprintf(", oldest %s ago", ageShort(now.Sub(oldest)))
	}
	return s + "\n         └ `cs recovery` lists them; restore or clear each\n"
}
