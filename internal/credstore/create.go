package credstore

import (
	"errors"
	"regexp"
)

// ErrExists means a live item that was to be created is already there.
// Nothing was written.
var ErrExists = errors.New("the credential already exists")

// LiveServiceName is the item Claude Code reads for CLAUDE_CONFIG_DIR=dir,
// "" meaning unset: computed from the string exactly as given, not confirmed
// by a lookup (that is LiveServiceFor). It names an item about to be created,
// so that Claude Code, launched with the same string, adopts it.
func LiveServiceName(dir string) string {
	if dir == "" {
		return liveServiceBase
	}
	return liveServiceFor(dir)
}

// suffixedLive matches a profile's own live item. The bare item belongs to
// the profile with CLAUDE_CONFIG_DIR unset, and is never created here.
var suffixedLive = regexp.MustCompile(`^Claude Code-credentials-[0-9a-f]{8}$`)
