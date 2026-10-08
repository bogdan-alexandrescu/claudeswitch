// Package ccdir locates Claude Code's own files the way Claude Code does.
//
// Every claudeswitch path into Claude Code's directory goes through here, so a
// process run with CLAUDE_CONFIG_DIR set sees one profile throughout rather
// than its settings in one place and its transcripts in another.
//
// Read from Claude Code 2.1.293:
//
//	configDir    = (CLAUDE_CONFIG_DIR ?? join(homedir, ".claude")).normalize("NFC")
//	globalConfig = exists(join(configDir, ".config.json")) ? that
//	             : join(CLAUDE_CONFIG_DIR || homedir, ".claude.json")
//	storageDir   = CLAUDE_SECURESTORAGE_CONFIG_DIR set ? (it || join(homedir, ".claude"))
//	             : configDir
//
// Two deliberate differences, both for values Claude Code would resolve
// against its cwd: an empty CLAUDE_CONFIG_DIR counts as unset, and a leading
// ~ is expanded. The daemon's cwd is not the person's, so taking either
// literally would point somewhere they never meant. Neither applies to the
// keychain service name, which hashes the string as given (see credstore).
package ccdir

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvConfigDir        = "CLAUDE_CONFIG_DIR"
	EnvSecureStorageDir = "CLAUDE_SECURESTORAGE_CONFIG_DIR"
)

// ExpandHome expands a leading ~ to the home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, p[1:]), nil
}

func defaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// Dir is Claude Code's config directory: $CLAUDE_CONFIG_DIR, or ~/.claude.
func Dir() (string, error) {
	return dirFor(os.Getenv(EnvConfigDir))
}

// dirFor is the config directory for a CLAUDE_CONFIG_DIR value, "" meaning
// unset.
func dirFor(configDir string) (string, error) {
	if configDir != "" {
		return ExpandHome(configDir)
	}
	return defaultDir()
}

// Projects is where Claude Code writes session transcripts.
func Projects() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects"), nil
}

// GlobalConfig is Claude Code's .claude.json, which holds the cached
// oauthAccount. Note it is in the home directory, not ~/.claude, when
// CLAUDE_CONFIG_DIR is unset.
func GlobalConfig() (string, error) { return globalConfigFor(os.Getenv(EnvConfigDir)) }

func globalConfigFor(configDir string) (string, error) {
	d, err := dirFor(configDir)
	if err != nil {
		return "", err
	}
	legacy := filepath.Join(d, ".config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy, nil
	}
	base := configDir
	if base != "" {
		if base, err = ExpandHome(base); err != nil {
			return "", err
		}
	} else if base, err = os.UserHomeDir(); err != nil {
		return "", err
	}
	return filepath.Join(base, ".claude.json"), nil
}

// SecureStorageDir is where Claude Code keeps a plaintext .credentials.json
// (Linux) and the directory whose name it hashes into the keychain service.
func SecureStorageDir() (string, error) {
	if d, ok := os.LookupEnv(EnvSecureStorageDir); ok {
		if d == "" {
			return defaultDir()
		}
		return ExpandHome(d)
	}
	return Dir()
}

// Paths is every file location of one Claude Code profile.
type Paths struct {
	Dir           string // the config dir, ~ expanded
	Projects      string // session transcripts
	GlobalConfig  string // .claude.json, holding oauthAccount
	SecureStorage string // where Linux keeps .credentials.json
}

// For resolves a configured profile's paths from its dir as the person
// launches Claude Code with it, "" meaning CLAUDE_CONFIG_DIR unset. This
// process's own environment plays no part: the daemon serves every profile,
// and its environment belongs to none of them.
//
// CLAUDE_SECURESTORAGE_CONFIG_DIR is not modelled for a configured profile:
// its secure storage is its config dir.
func For(configDir string) (Paths, error) {
	var p Paths
	var err error
	if p.Dir, err = dirFor(configDir); err != nil {
		return Paths{}, err
	}
	if p.GlobalConfig, err = globalConfigFor(configDir); err != nil {
		return Paths{}, err
	}
	p.Projects = filepath.Join(p.Dir, "projects")
	p.SecureStorage = p.Dir
	return p, nil
}

// FromEnv is the paths of the profile this process's environment names: the
// same answers Dir, Projects, GlobalConfig and SecureStorageDir give.
func FromEnv() (Paths, error) {
	p, err := For(os.Getenv(EnvConfigDir))
	if err != nil {
		return Paths{}, err
	}
	if p.SecureStorage, err = SecureStorageDir(); err != nil {
		return Paths{}, err
	}
	return p, nil
}
