// Package keychain is the macOS-era name for the credential store. It now
// forwards to credstore, which does the same job on Linux too.
//
// Kept as aliases rather than renamed everywhere at once: the call sites are
// spread across the vault, the poller and the CLI, and a mechanical rename of
// working credential-handling code is exactly the sort of change that introduces
// a subtle fault for no behavioural gain.
package keychain

import "github.com/bogdan-alexandrescu/claudeswitch/internal/credstore"

type (
	OAuth = credstore.OAuth
	Meta  = credstore.Meta
	Blob  = credstore.Blob
)

const LiveService = credstore.LiveService

var (
	Read         = credstore.Read
	Write        = credstore.Write
	Delete       = credstore.Delete
	VaultService = credstore.VaultService
	Redact       = credstore.Redact
	MergeForSwap = credstore.MergeForSwap
)

// ReadLive returns the credential Claude Code is currently using.
func ReadLive() (*Blob, error) { return credstore.Read(credstore.LiveService) }

// Backend describes where credentials live on this platform.
const Backend = credstore.Backend

// CheckWritable proves the credential store can be written before a caller does
// something irreversible that depends on it. It writes a sentinel item, reads it
// back and deletes it, so a store that accepts writes but loses them fails here
// rather than at the moment an account is on the line.
func CheckWritable() error {
	return credstore.CheckWritable()
}
