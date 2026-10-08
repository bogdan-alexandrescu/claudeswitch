package credstore

import (
	"path/filepath"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
)

// Live is one Claude Code profile's live credential: the item that profile
// reads. Every write the program makes to a live credential goes through one,
// so a swap, its rollback and its verification all reach the same item
// (docs/PROFILES.md §5).
type Live interface {
	// Name identifies the item in logs and errors.
	Name() string
	Read() (*Blob, error)
	Write(*Blob) error
}

// EnvLive is the live credential this process's environment names, resolved
// on every call exactly as ReadLive and WriteLive do. It is the implicit
// profile's item when no profiles are configured, which keeps that case
// identical to the single-profile program.
func EnvLive() Live { return envLive{} }

type envLive struct{}

func (envLive) Name() string {
	if svc, err := LiveService(); err == nil {
		return svc
	}
	return liveServiceBase
}
func (envLive) Read() (*Blob, error) { return ReadLive() }
func (envLive) Write(b *Blob) error  { return WriteLive(b) }

// LiveItem is a configured profile's live credential: the keychain item
// service on macOS, the credential file on Linux, where the item's name
// carries no information (see pathFor). Both come from profile.Resolve.
func LiveItem(service, file string) Live { return liveItem{service: service, file: file} }

type liveItem struct{ service, file string }

func (l liveItem) Name() string         { return liveItemName(l.service, l.file) }
func (l liveItem) Read() (*Blob, error) { return readLiveItem(l.service, l.file) }
func (l liveItem) Write(b *Blob) error  { return writeLiveItem(l.service, l.file, b) }

// ItemRefer is a live credential that can name the concrete item it reads
// now: the keychain service and, on Linux, the credential file. LiveItem of
// the two reaches the same item later, whatever the environment then; it is
// how a removed profile's old item is remembered (state.Ghost).
type ItemRefer interface {
	ItemRef() (service, file string, ok bool)
}

func (l liveItem) ItemRef() (string, string, bool) { return l.service, l.file, true }

func (envLive) ItemRef() (string, string, bool) {
	svc, err := LiveService()
	if err != nil {
		return "", "", false
	}
	file := ""
	if d, err := ccdir.SecureStorageDir(); err == nil {
		file = filepath.Join(d, ".credentials.json")
	}
	return svc, file, true
}

// WriteChecker is a live credential that can say, without writing, whether a
// blob could be written to it. A swap asks for both the blob it installs and
// the one it would roll back to, before writing either.
type WriteChecker interface {
	CheckWrite(b *Blob) error
}

func (envLive) CheckWrite(b *Blob) error {
	svc, err := LiveService()
	if err != nil {
		return err
	}
	return checkLiveWrite(svc, "", b)
}

func (l liveItem) CheckWrite(b *Blob) error { return checkLiveWrite(l.service, l.file, b) }

// LockDirer is a live credential that knows the secure-storage directory
// Claude Code locks before writing it (GROUND_TRUTH §43). Writers take the
// same locks there; "" means unknown, and no lock is taken.
type LockDirer interface {
	LockDir() string
}

// LockDir is this environment's secure-storage dir, as Claude Code resolves it.
func (envLive) LockDir() string {
	d, err := ccdir.SecureStorageDir()
	if err != nil {
		return ""
	}
	return d
}

// LockDir is the dir holding the profile's credential file, which is its
// secure-storage dir (profile.Resolve).
func (l liveItem) LockDir() string {
	if l.file == "" {
		return ""
	}
	return filepath.Dir(l.file)
}
