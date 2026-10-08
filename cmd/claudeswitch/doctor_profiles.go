package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
)

// doctorWarnings prints the config's warnings: settings that load and run but
// do not do what they suggest, such as two profiles sharing a directory.
func doctorWarnings(w io.Writer, cfg *config.Config) {
	for _, warning := range cfg.Warnings() {
		fmt.Fprintf(w, "  [warn] config          %s\n", warning)
	}
}

// doctorExit turns the number of failed checks into doctor's result, so a
// failure shows in the exit status and not only in the text.
func doctorExit(failures int) error {
	if failures == 0 {
		return nil
	}
	return fmt.Errorf("%d doctor check(s) failed", failures)
}

// doctorProfiles checks each Claude Code profile: its dir exists, its live
// credential item resolves, and which accounts it rotates within. It reads
// metadata only — never a secret — so it raises no keychain prompt. It
// reports whether anything failed: a missing dir or an unresolved live item.
// An empty pool is a warning and does not count.
func doctorProfiles(w io.Writer, cfg *config.Config,
	resolve func(config.Profile) (profile.Resolved, error)) bool {
	failed := false
	mark := func(ok bool) string {
		if ok {
			return "ok  "
		}
		failed = true
		return "FAIL"
	}
	for _, in := range cfg.EffectiveProfiles() {
		r, rerr := resolve(in)

		how := "CLAUDE_CONFIG_DIR unset"
		switch {
		case in.FromEnv:
			how = "no [[profile]] blocks: as this environment's CLAUDE_CONFIG_DIR says"
		case in.Dir != "":
			how = "CLAUDE_CONFIG_DIR=" + in.Dir
		}
		_, derr := os.Stat(r.Dir)
		dirOK := r.Dir != "" && derr == nil

		// The keychain item is the live credential on macOS; on Linux it is a
		// file, whose name the lookup cannot confirm, so check the file.
		itemOK, itemNote := rerr == nil, ""
		if rerr != nil {
			itemNote = rerr.Error()
		} else if runtime.GOOS == "linux" {
			if _, err := os.Stat(r.CredentialFile); err != nil {
				itemOK = false
				itemNote = fmt.Sprintf("no credential file at %s; this profile may not be logged in yet "+
					"(run Claude Code with %s and /login)", r.CredentialFile, how)
			} else {
				itemNote = r.CredentialFile
			}
		} else {
			itemNote = fmt.Sprintf("%q", r.Service)
		}

		fmt.Fprintf(w, "  [%s] profile        %s (%s)\n", mark(dirOK && itemOK), in.Name, how)
		if dirOK {
			fmt.Fprintf(w, "         └ dir         %s\n", r.Dir)
		} else {
			fmt.Fprintf(w, "         └ dir         %s does not exist — has Claude Code run with %s?\n", r.Dir, how)
		}
		state := "live item  "
		if !itemOK {
			state = "live item  FAILS — "
		}
		fmt.Fprintf(w, "         └ %s%s\n", state, itemNote)
		if len(r.Pool) == 0 {
			fmt.Fprintf(w, "         └ pool        empty — this profile has no account to rotate to\n")
		} else {
			fmt.Fprintf(w, "         └ pool        %s\n", strings.Join(r.Pool, ", "))
		}
		if in.SwitchAt > 0 || in.SwitchAtWeekly > 0 || in.HardFloor > 0 {
			fmt.Fprintf(w, "         └ thresholds  switch_at %g · switch_at_weekly %g · hard_floor %g\n",
				cfg.SwitchAtFor(in.Name), cfg.SwitchAtWeeklyFor(in.Name), cfg.HardFloorFor(in.Name))
		}
	}
	return failed
}
