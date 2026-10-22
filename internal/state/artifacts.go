package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// An Artifact is one the plugin saw published (IMPROVEMENTS phase 1 G, owner
// decision 2026-10-21). claude.ai keeps an artifact for the account that
// published it, and only that account can read, change or delete it
// (GROUND_TRUTH §55); a call on it made as another account fails with
// "There is no Artifact at …". Recording the owner lets the plugin refuse
// such a call first, saying whose it is.
type Artifact struct {
	// ID is the last part of its URL (ArtifactID).
	ID  string `json:"id"`
	URL string `json:"url,omitempty"`
	// Path and Session are the file it was published from and the Claude
	// Code session that did it: within that session a publish of the same
	// path updates this artifact, and in another it makes a new one.
	Path    string `json:"path,omitempty"`
	Session string `json:"session,omitempty"`
	// Account is the owner as it was called then; Seat (account@org) is
	// what still names it after a rename.
	Account string    `json:"account"`
	Seat    string    `json:"seat,omitempty"`
	At      time.Time `json:"at"`
}

// ArtifactKeep is how many artifacts the book remembers, the newest first:
// enough for weeks of publishing, and a file the hook reads in a moment.
const ArtifactKeep = 500

const artifactsLockName = "artifacts.lock"

// ArtifactsPath is the book's file, beside state.json. It is a file of its
// own because the plugin's hook writes it from every session, and state.json
// belongs to the daemon and the CLI.
func ArtifactsPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "artifacts.json")
}

type artifactBook struct {
	Version   int        `json:"version"`
	Artifacts []Artifact `json:"artifacts"`
}

// ArtifactID is the id in an artifact's URL, the part after /artifact/
// (claude.ai/artifact/{id} or claude.ai/code/artifact/{uuid}); "" when the
// URL is not an artifact's.
func ArtifactID(url string) string {
	_, rest, ok := strings.Cut(url, "/artifact/")
	if !ok {
		return ""
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func readArtifacts() ([]Artifact, error) {
	b, err := os.ReadFile(ArtifactsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var book artifactBook
	if err := json.Unmarshal(b, &book); err != nil {
		return nil, err
	}
	return book.Artifacts, nil
}

// RecordArtifact adds a publish to the book, replacing an earlier record of
// the same artifact, and drops the oldest past ArtifactKeep.
func RecordArtifact(a Artifact) error {
	lk, err := blockingLock(lockPath(artifactsLockName))
	if err != nil {
		return err
	}
	defer lk.Release()
	all, err := readArtifacts()
	if err != nil {
		all = nil // an unreadable book is started again rather than blocking every publish
	}
	all = slices.DeleteFunc(all, func(x Artifact) bool { return x.ID == a.ID })
	all = append([]Artifact{a}, all...)
	slices.SortStableFunc(all, func(x, y Artifact) int { return y.At.Compare(x.At) })
	if len(all) > ArtifactKeep {
		all = all[:ArtifactKeep]
	}
	b, err := json.MarshalIndent(artifactBook{Version: 1, Artifacts: all}, "", "  ")
	if err != nil {
		return err
	}
	path := ArtifactsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ArtifactOwner is the record of the artifact with this id.
func ArtifactOwner(id string) (Artifact, bool, error) {
	return findArtifact(func(a Artifact) bool { return id != "" && a.ID == id })
}

// ArtifactAt is the record of the artifact this session published from this
// file.
func ArtifactAt(session, path string) (Artifact, bool, error) {
	return findArtifact(func(a Artifact) bool {
		return session != "" && path != "" && a.Session == session && a.Path == path
	})
}

func findArtifact(match func(Artifact) bool) (Artifact, bool, error) {
	all, err := readArtifacts()
	if err != nil {
		return Artifact{}, false, err
	}
	for _, a := range all {
		if match(a) {
			return a, true, nil
		}
	}
	return Artifact{}, false, nil
}
