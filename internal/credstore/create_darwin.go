//go:build darwin

package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
)

// LiveExistsFor reports whether the profile with CLAUDE_CONFIG_DIR=dir has a
// live item under any spelling of dir, from metadata alone. A lookup that
// fails is an error, never "absent".
func LiveExistsFor(dir string) (bool, error) {
	_, err := LiveServiceFor(dir)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, err
}

// CreateLiveItem stores b as a new profile's live credential: the keychain
// item service, which must be a suffixed live name (LiveServiceName of a
// dir). It only ever creates. An item already there is ErrExists, and the
// add carries no -U, so even a race ends in security refusing a duplicate
// rather than in an overwrite. No -T either: the item's access list is
// Claude Code's, and changing a live item's access list is the prompt
// GROUND_TRUTH §41 describes. The line keeps to SecurityLineMax (§43). The
// file argument is the Linux credential file, unused here.
func CreateLiveItem(service, _ string, b *Blob) error {
	if !suffixedLive.MatchString(service) {
		return fmt.Errorf("not creating %q: only a profile's own suffixed live item is created", service)
	}
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	found, err := liveLookup(service)
	if err != nil {
		return fmt.Errorf("not creating %q: could not tell whether it exists: %w", service, err)
	}
	if found {
		return fmt.Errorf("keychain item %q: %w", service, ErrExists)
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	user := currentUser()
	if err := refuseControl(service, service, user); err != nil {
		return err
	}
	line := fmt.Sprintf("add-generic-password -s %s -a %s -w %s\n",
		escapeForSecurity(service), escapeForSecurity(user), escapeForSecurity(string(payload)))
	if err := lineFits(service, line, len(payload)); err != nil {
		return err
	}
	timedOut, stderr, err := runSecurity(line)
	if err != nil {
		// As in Write: a timed-out command may still have stored it.
		if timedOut && readBack(service, b) == nil {
			return nil
		}
		// Never the payload in an error.
		return fmt.Errorf("creating keychain item %q: %w (%s)", service, err, stderr)
	}
	return readBack(service, b)
}
