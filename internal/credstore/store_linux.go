//go:build linux

package credstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
)

// Backend names where credentials live on this platform, for doctor output.
const Backend = "~/.claude (files, 0600)"

// pathFor resolves a service name to a file.
//
// Claude Code keeps the live credential at <secure-storage dir>/.credentials.json
// on Linux, which is its config dir unless CLAUDE_SECURESTORAGE_CONFIG_DIR says
// otherwise. Any non-vault name means that file: the keychain-style suffix
// LiveService computes carries no information here.
//
// Vault entries live in ~/.claude/claudeswitch, a directory of our own, so that
// a stray claudeswitch file can never be mistaken for the credential Claude
// Code reads. The vault deliberately does not follow CLAUDE_CONFIG_DIR: it is
// shared by every profile, and claudeswitch run from inside one must see the
// same accounts as the daemon.
func pathFor(service string) (string, error) {
	// A recovery item first: as a non-vault name it would otherwise mean the
	// live file, and a recovery copy would overwrite a live credential.
	if IsRecoveryService(service) {
		return recoveryPath(service)
	}
	if !IsVaultService(service) {
		dir, err := ccdir.SecureStorageDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, ".credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	id := service[len("claudeswitch:"):]
	// The id becomes part of a path, so it must be a single ordinary name.
	// filepath.Base is not sufficient on its own: Base("..") is "..", which
	// passed the earlier check. Not exploitable as written, since an extension
	// is appended, but the check was wrong and a future change would make it so.
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || filepath.Base(id) != id {
		return "", fmt.Errorf("refusing a credential name that is not a plain id: %q", service)
	}
	return filepath.Join(home, ".claude", "claudeswitch", id+".json"), nil
}

// lookupItem: on Linux every live name is the one file, which a first login
// may not have created yet, so resolution needs no lookup and Read reports a
// missing file with its path.
func lookupItem(string) (bool, error) { return true, nil }

// Read returns the parsed blob for a service.
func Read(service string) (*Blob, error) {
	p, err := pathFor(service)
	if err != nil {
		return nil, err
	}
	return readAt(service, p)
}

// readAt reads the credential file p, naming it service in errors.
func readAt(service, p string) (*Blob, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no credential stored at %s: %w", p, ErrNotFound)
		}
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	return parse(service, raw)
}

// Write stores a blob atomically, 0600 inside a 0700 directory, then verifies it
// by reading it back.
func Write(service string, b *Blob) error {
	p, err := pathFor(service)
	if err != nil {
		return err
	}
	return writeAt(service, p, b)
}

// writeAt is Write to the file p.
func writeAt(service, p string, b *Blob) error {
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// Compact, matching the macOS path exactly. Pretty-printing would re-indent
	// the raw mcpOAuth section, and "mcpOAuth survives byte-for-byte" is a
	// guarantee this package makes rather than an approximation.
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	// 0600 from creation: a credential must never exist world-readable, even
	// briefly between the write and a chmod.
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	// Read back from the same file, not through the service name: a configured
	// profile's file is not the one pathFor would pick.
	got, err := readAt(service, p)
	if err != nil {
		return fmt.Errorf("wrote credential %q but could not read it back: %w", service, err)
	}
	if got.ClaudeAIOAuth.AccessToken != b.ClaudeAIOAuth.AccessToken {
		return fmt.Errorf("credential %q read back different from what was written; not trusting it", service)
	}
	return nil
}

// On Linux a configured profile's live credential is its file; the keychain
// style name carries no information here (see pathFor). An empty file falls
// back to the service's own path.
func liveItemName(service, file string) string {
	if file == "" {
		return service
	}
	return file
}

// checkLiveWrite: a file takes any size.
func checkLiveWrite(_, _ string, b *Blob) error {
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	return nil
}

func readLiveItem(service, file string) (*Blob, error) {
	if file == "" {
		return Read(service)
	}
	return readAt(service, file)
}

func writeLiveItem(service, file string, b *Blob) error {
	if file == "" {
		return Write(service, b)
	}
	return writeAt(service, file, b)
}

// Delete removes an item. Missing is not an error.
func Delete(service string) error {
	p, err := pathFor(service)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting %s: %w", p, err)
	}
	return nil
}
