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
	// Live is one profile's live credential; see credstore.Live.
	Live = credstore.Live
	// LockDirer is a Live that names the dir Claude Code locks; see
	// credstore.LockDirer.
	LockDirer = credstore.LockDirer
	// WriteChecker is a Live that can check a write without making it.
	WriteChecker = credstore.WriteChecker
	// ItemRefer is a Live that can name its concrete item.
	ItemRefer = credstore.ItemRefer
)

// RecoveryService names a profile's recovery item; see credstore.
var (
	RecoveryService     = credstore.RecoveryService
	RecoverySlotID      = credstore.RecoverySlotID
	ParseRecoverySlotID = credstore.ParseRecoverySlotID
)

var (
	Read         = credstore.Read
	Write        = credstore.Write
	Delete       = credstore.Delete
	VaultService = credstore.VaultService
	Redact       = credstore.Redact
	MergeForSwap = credstore.MergeForSwap

	// LiveService resolves the item Claude Code reads under this process's
	// CLAUDE_CONFIG_DIR; ReadLive and WriteLive go through it.
	LiveService = credstore.LiveService
	ReadLive    = credstore.ReadLive
	WriteLive   = credstore.WriteLive
	EnvLive     = credstore.EnvLive
	LiveItem    = credstore.LiveItem

	// ErrNotFound: the item is not stored at all, so it holds nothing.
	ErrNotFound = credstore.ErrNotFound
)

// Backend describes where credentials live on this platform.
const Backend = credstore.Backend
