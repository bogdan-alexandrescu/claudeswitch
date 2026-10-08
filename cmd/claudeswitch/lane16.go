package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// appContract is the version of the app's CLI contract (docs/APP_CLI.md).
// The app reads it from `version --json` and refuses a binary below the one
// it was built for. 2 (lane 16): account scope removed (S1), `add --from`,
// `config clean`, why's best account.
const appContract = 2

// cmdVersion is `claudeswitch version [--json]`.
func cmdVersion(w io.Writer, args []string) error {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return emitTo(w, map[string]any{"version": version, "contract": appContract})
		}
	}
	fmt.Fprintln(w, "claudeswitch "+version)
	return nil
}

// warnLegacy prints the one warning about scope lines and [project] tables
// left in the config (lane 16, S1), nothing when there are none.
func warnLegacy(w io.Writer, cfg *config.Config) {
	if cfg == nil || legacyWarned || legacyQuiet {
		return
	}
	if msg := cfg.LegacyWarning(); msg != "" {
		fmt.Fprintln(w, "warning: "+msg)
		legacyWarned = true
	}
}

// legacyWarned: the warning is printed once per process, however many
// times a command loads the config (review). legacyQuiet: never, for the
// invocations whose stderr belongs to Claude Code (quietCommand).
var legacyWarned, legacyQuiet bool

// quietCommand reports the invocations Claude Code runs on its own — the
// status line, `context`, the plugin's `chrome hint` hook — where a
// warning on stderr is noise in someone else's output.
func quietCommand(cmd string, args []string) bool {
	switch cmd {
	case "statusline", "context":
		return true
	case "chrome":
		return len(args) > 0 && args[0] == "hint"
	}
	return false
}

var projectHdr = func(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[project.") || strings.HasPrefix(t, "[project ") ||
		strings.HasPrefix(t, "[[project]]") || strings.HasPrefix(t, "[[ project ]]") || t == "[project]"
}

// removeLegacy deletes every scope line in an [[account]] block and every
// [project…] table (its sub-keys, and the comment lines directly above its
// header, as deleteBlock does), leaving everything else as written.
func removeLegacy(text string) string {
	es := scanTOML(text)
	drop := make([]bool, len(es))
	for i, e := range es {
		if e.key == "scope" && e.section >= 0 && es[e.section].header == "account" {
			drop[i] = true
		}
		if e.header == "" || !projectHdr(text[e.start:e.end]) {
			continue
		}
		last := i
		for j := i + 1; j < len(es) && es[j].section == i; j++ {
			last = j
		}
		for last > i && isTrivia(text, es[last]) {
			last--
		}
		for j := i; j <= last; j++ {
			drop[j] = true
		}
		// The comment lines directly above the header are the table's only
		// when a blank line (or the start of the file) sets them off: a
		// comment run touching the block above may be that block's own
		// (a commented-out `# switch_at = 70`), and is kept (review).
		k := i - 1
		for ; k >= 0; k-- {
			t := strings.TrimSpace(text[es[k].start:es[k].end])
			if !strings.HasPrefix(t, "#") {
				break
			}
		}
		blankAbove := k >= 0 && es[k].header == "" && es[k].key == "" &&
			strings.TrimSpace(text[es[k].start:es[k].end]) == ""
		if k < 0 || blankAbove {
			for c := k + 1; c < i; c++ {
				drop[c] = true
			}
		}
		// The blank line that set the table off goes with it.
		if blankAbove {
			drop[k] = true
		}
	}
	var b strings.Builder
	for i, e := range es {
		if drop[i] {
			continue
		}
		b.WriteString(text[e.start:min(e.end+1, len(text))])
	}
	return b.String()
}

// configClean is `claudeswitch config clean [--yes] [--json]`: it removes
// what the config still carries of account scope and project rules.
func configClean(w io.Writer, cfgPath string, yes, asJSON bool) error {
	cfg, err := loadForEdit(cfgPath)
	if err != nil {
		return err
	}
	lg := cfg.Legacy()
	scope := append([]string{}, lg.Scope...)
	answer := func() error {
		if asJSON {
			return emitTo(w, map[string]any{
				"removed": map[string]any{"scope": scope, "projects": lg.Projects}, "path": cfg.Path})
		}
		if lg.Empty() {
			fmt.Fprintf(w, "  %s has no scope lines or project rules; nothing to remove\n", cfg.Path)
			return nil
		}
		fmt.Fprintf(w, "  removed %s from %s\n", legacyWhat(lg), cfg.Path)
		return nil
	}
	if lg.Empty() {
		return answer()
	}
	if !yes {
		if asJSON || !isTerminal() || !askYes(fmt.Sprintf("\n  Remove %s from %s", legacyWhat(lg), cfg.Path), true) {
			return appErr(codeConfirm, "pass --yes to remove them",
				"this removes %s from %s; nothing else changes", legacyWhat(lg), cfg.Path)
		}
	}
	err = editConfigText(cfg.Path, func(text string) (string, error) {
		return removeLegacy(text), nil
	}, func(back *config.Config) error {
		if !back.Legacy().Empty() {
			return fmt.Errorf("the edit parsed but still has %s; discarded", legacyWhat(back.Legacy()))
		}
		if len(back.Accounts) != len(cfg.Accounts) || len(back.Profiles) != len(cfg.Profiles) {
			return fmt.Errorf("the edit lost an account or a profile; discarded")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return answer()
}

// legacyWhat names what is left, for a confirmation or a note.
func legacyWhat(lg config.Legacy) string {
	var parts []string
	if len(lg.Scope) > 0 {
		parts = append(parts, fmt.Sprintf("the scope line of %s", strings.Join(lg.Scope, ", ")))
	}
	switch {
	case lg.Projects == 1:
		parts = append(parts, "1 [project] table")
	case lg.Projects > 1:
		parts = append(parts, fmt.Sprintf("%d [project] tables", lg.Projects))
	}
	return strings.Join(parts, " and ")
}
