// Package profile resolves a configured Claude Code profile to the places
// it keeps things: its config dir, transcripts, identity file and live
// credential (docs/PROFILES.md §2, §4).
//
// Resolution reads metadata only. The keychain item is confirmed by an
// attribute lookup that reads no secret and raises no prompt.
package profile

import (
	"path/filepath"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/credstore"
)

// Seams, so tests never reach the keychain.
var (
	serviceForDir  = credstore.LiveServiceFor
	serviceFromEnv = credstore.LiveService
)

// Resolved is a profile with every location worked out.
type Resolved struct {
	Name     string
	Dir      string // config dir, ~ expanded (D8)
	Projects string // session transcripts
	Identity string // .claude.json, holding oauthAccount
	// CredentialFile is where Claude Code keeps the live credential on Linux.
	CredentialFile string
	// Service is the live keychain item. Empty when it could not be resolved,
	// in which case Resolve also returns the reason.
	Service string
	Pool    []string
}

// Resolve works out a profile's locations. A declared profile resolves from
// its dir alone, "" meaning CLAUDE_CONFIG_DIR unset; the implicit one
// (FromEnv) resolves from this process's environment, exactly as claudeswitch
// did before profiles.
//
// When the paths resolve but the keychain item does not, the paths are still
// returned with the error, so a caller can report each separately. The item's
// name is hashed from dir as written, not as expanded (D8).
func Resolve(in config.Profile) (Resolved, error) {
	var p ccdir.Paths
	var err error
	if in.FromEnv {
		p, err = ccdir.FromEnv()
	} else {
		p, err = ccdir.For(in.Dir)
	}
	if err != nil {
		return Resolved{Name: in.Name, Pool: in.Pool}, err
	}
	r := Resolved{
		Name:           in.Name,
		Dir:            p.Dir,
		Projects:       p.Projects,
		Identity:       p.GlobalConfig,
		CredentialFile: filepath.Join(p.SecureStorage, ".credentials.json"),
		Pool:           in.Pool,
	}
	if in.FromEnv {
		r.Service, err = serviceFromEnv()
	} else {
		r.Service, err = serviceForDir(in.Dir)
	}
	return r, err
}
