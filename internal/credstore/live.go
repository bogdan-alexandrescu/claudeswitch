package credstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"golang.org/x/text/unicode/norm"
)

// liveServiceBase is the item Claude Code reads when CLAUDE_CONFIG_DIR is
// unset. With it set the name carries a suffix, so callers never use this
// directly: they resolve the live name with LiveService.
const liveServiceBase = "Claude Code-credentials"

// liveServiceFor is the item name Claude Code uses for a config dir, read from
// Claude Code 2.1.293:
//
//	"Claude Code-credentials-" + sha256(dir.normalize("NFC")).hex[:8]
//
// The hash is of the string exactly as Claude Code saw it, after NFC
// normalisation, so ~/x, /home/me/x and /home/me/x/ are three different items
// while a decomposed and a composed é are one.
func liveServiceFor(dir string) string {
	sum := sha256.Sum256([]byte(norm.NFC.String(dir)))
	return liveServiceBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// liveLookup reports whether an item exists, reading metadata only. A seam:
// tests replace it so they never reach the keychain.
var liveLookup = lookupItem

var (
	liveMu    sync.Mutex
	liveCache = map[string]string{}
)

func resetLiveCache() {
	liveMu.Lock()
	defer liveMu.Unlock()
	liveCache = map[string]string{}
}

// hashedDir returns the directory string Claude Code hashes into the service
// name, or "" when the name carries no suffix.
func hashedDir() string {
	if ss, ok := os.LookupEnv(ccdir.EnvSecureStorageDir); ok {
		return ss // set but empty means no suffix
	}
	return os.Getenv(ccdir.EnvConfigDir)
}

// spellings lists the ways the same directory may have been written when
// Claude Code was launched, the string as given first.
func spellings(dir string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, v := range []string{s, strings.TrimRight(s, "/")} {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	add(dir)
	exp, err := ccdir.ExpandHome(dir)
	if err == nil {
		add(exp)
		if abs, err := filepath.Abs(exp); err == nil {
			add(abs)
		}
	}
	return out
}

// LiveService resolves the name of the credential item Claude Code reads in
// this environment.
//
// With CLAUDE_CONFIG_DIR unset it is the bare name and nothing is looked up.
// With it set, the name is computed for each plausible spelling of the dir and
// confirmed by a metadata-only lookup, which reads no secret and raises no
// prompt; the first that exists wins. None existing is an error naming every
// candidate, never a guess: writing a credential to an item Claude Code does
// not read would look like a swap and do nothing.
func LiveService() (string, error) { return LiveServiceFor(hashedDir()) }

// LiveServiceFor resolves the credential item of the profile Claude Code runs
// with CLAUDE_CONFIG_DIR=dir, "" meaning unset, independent of this process's
// environment. It is how a configured profile names its item.
//
// "" and "~/.claude" are different profiles to the keychain: the first reads
// the bare item, the second a suffixed one, even though both keep their files
// in ~/.claude.
func LiveServiceFor(dir string) (string, error) {
	if dir == "" {
		return liveServiceBase, nil
	}
	liveMu.Lock()
	if s, ok := liveCache[dir]; ok {
		liveMu.Unlock()
		return s, nil
	}
	liveMu.Unlock()

	var tried []string
	var lookupErr error
	for _, sp := range spellings(dir) {
		svc := liveServiceFor(sp)
		found, err := liveLookup(svc)
		if err != nil {
			lookupErr = errors.Join(lookupErr, fmt.Errorf("looking up %q: %w", svc, err))
			continue
		}
		if found {
			liveMu.Lock()
			liveCache[dir] = svc
			liveMu.Unlock()
			return svc, nil
		}
		tried = append(tried, fmt.Sprintf("%q (%s)", svc, sp))
	}
	if lookupErr != nil {
		return "", fmt.Errorf("resolving Claude Code's credential for %s: %w", dir, lookupErr)
	}
	msg := fmt.Sprintf("no Claude Code credential found for config dir %q; tried %s.",
		dir, strings.Join(tried, ", "))
	// The plain item belongs to the profile with CLAUDE_CONFIG_DIR unset, so
	// it is never used here, only mentioned: its presence says this profile
	// most likely has not been logged in. A failed lookup just omits the hint.
	if found, err := liveLookup(liveServiceBase); err == nil && found {
		msg += fmt.Sprintf(" %q (the plain item exists: this profile may not be "+
			"logged in yet — run `CLAUDE_CONFIG_DIR=%s claude` and /login)", liveServiceBase, dir)
	} else {
		msg += fmt.Sprintf(" Has Claude Code been signed in with CLAUDE_CONFIG_DIR=%s?", dir)
	}
	return "", notFoundError(msg)
}

// ReadLive returns the credential Claude Code is currently using.
func ReadLive() (*Blob, error) {
	svc, err := LiveService()
	if err != nil {
		return nil, err
	}
	return Read(svc)
}

// WriteLive replaces the credential Claude Code is currently using.
func WriteLive(b *Blob) error {
	svc, err := LiveService()
	if err != nil {
		return err
	}
	return Write(svc, b)
}
