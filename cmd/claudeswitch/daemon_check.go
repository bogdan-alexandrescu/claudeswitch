package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// daemonCheck is `cs daemon check`: it reads every profile's live credential
// once, as the daemon will unattended, so that macOS asks for Keychain access
// while someone is at the keyboard to answer. A daemon under launchd cannot
// answer that prompt, and security(1) waits for an answer forever.
//
// Each profile is read through its own item, never through this shell's
// CLAUDE_CONFIG_DIR: the shell running install.sh need not match any profile,
// and with several profiles, reading one would leave the others unasked. A
// profile nobody has signed in to yet has nothing to approve and is skipped.
// Any other failure is reported as it happened, under the profile's name.
//
// --json counts the failures rather than returning an error, as doctor --json
// does; the text form fails, which is what install.sh stops on.
func daemonCheck(w io.Writer, cfgPath string, asJSON bool) error {
	cfg, err := config.Load(cfgPath)
	// No config yet (install.sh runs before setup): the defaults' one profile
	// is the credential Claude Code uses in this environment.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var rows []map[string]any
	var failed []string
	for _, in := range cfg.EffectiveProfiles() {
		item, err := readLiveOnce(in)
		status, why := "ok", ""
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			status, why = "skip", err.Error()
		case err != nil:
			status, why = "fail", err.Error()
			failed = append(failed, in.Name)
		}
		rows = append(rows, map[string]any{"profile": in.Name, "item": orNull(item),
			"status": status, "error": orNull(why)})
		if asJSON {
			continue
		}
		switch status {
		case "ok":
			fmt.Fprintf(w, "  ok    %-16s %s\n", in.Name, item)
		case "skip":
			fmt.Fprintf(w, "  skip  %-16s not signed in yet\n", in.Name)
		default:
			fmt.Fprintf(w, "  FAIL  %-16s %s\n", in.Name, why)
		}
	}
	if asJSON {
		return emitJSONTo(w, map[string]any{"profiles": rows, "failed": len(failed)})
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not read the credential of %s", strings.Join(quoteAll(failed), ", "))
	}
	return nil
}

// readLiveOnce reads a profile's live item and returns the item's name.
func readLiveOnce(in config.Profile) (string, error) {
	item, err := cliLiveFor(in)
	if err != nil {
		return "", err
	}
	if _, err := item.Read(); err != nil {
		return item.Name(), err
	}
	return item.Name(), nil
}
