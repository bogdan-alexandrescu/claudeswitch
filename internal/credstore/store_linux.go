//go:build linux

package credstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Backend names where credentials live on this platform, for doctor output.
const Backend = "~/.claude (files, 0600)"

// pathFor resolves a service name to a file.
//
// Claude Code keeps the live credential at ~/.claude/.credentials.json on Linux.
// Vault entries live beside it in a directory of our own, so that a stray
// claudeswitch file can never be mistaken for the credential Claude Code reads.
func pathFor(service string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !IsVaultService(service) {
		return filepath.Join(home, ".claude", ".credentials.json"), nil
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

// Read returns the parsed blob for a service.
func Read(service string) (*Blob, error) {
	p, err := pathFor(service)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no credential stored at %s", p)
		}
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	return parse(service, raw)
}

// Write stores a blob atomically, 0600 inside a 0700 directory, then verifies it
// by reading it back.
func Write(service string, b *Blob) error {
	if b == nil || b.ClaudeAIOAuth == nil {
		return fmt.Errorf("refusing to write a credential with no claudeAiOauth section")
	}
	p, err := pathFor(service)
	if err != nil {
		return err
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
	return verifyWrite(service, b)
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
