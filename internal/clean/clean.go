// Package clean finds what Claude Code sessions leave behind once their
// work is merged, and removes it reversibly (owner decisions 2026-10-25,
// IMPROVEMENTS "Cleaning up after Claude").
//
// Scan only reads. It makes a Plan: every candidate it found, each marked
// Remove or kept with the reason. Apply looks at each removable item again
// just before acting, moves folders to the Trash, keeps each deleted
// branch's tip as refs/archive/<branch>, copies files that are neither
// tracked nor rebuildable to an archive, and writes a journal. Undo reads
// the journal and puts things back.
//
// An item is removable only when every rule holds: no commit outside a
// merge (the default branch, or a pull request merged with exactly this
// tip); no file that is not committed, rebuildable or archived; not in use
// (a live Claude Code session, a process's working directory, a lock, a git
// operation in progress, a change within Options.After); and not protected
// (a main worktree, the default branch, a primary clone, Options.Keep).
package clean

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kind is what an item is.
type Kind string

const (
	// KindWorktree is a linked git worktree; removing it also archives and
	// deletes its branch.
	KindWorktree Kind = "worktree"
	// KindBranch is a local branch no worktree has checked out.
	KindBranch Kind = "branch"
	// KindPrune is git's record of a worktree whose folder is gone.
	KindPrune Kind = "prune"
	// KindClone is a second clone of a project's repository beside it.
	KindClone Kind = "clone"
	// KindScratch is an ended session's folder in Claude Code's temp folder.
	KindScratch Kind = "scratch"
	// KindPlugin is a plugin version folder in Claude Code's plugin cache.
	KindPlugin Kind = "plugin"
	// KindTranscript is an ended session's subagents/ transcripts folder.
	KindTranscript Kind = "transcript"
	// KindRemote is a branch on the repository's origin.
	KindRemote Kind = "remote"
)

// kindOrder is the order a plan lists, and Apply acts in: a worktree goes
// before its branch, and a remote branch before nothing local depends on it.
var kindOrder = map[Kind]int{KindWorktree: 0, KindPrune: 1, KindBranch: 2, KindRemote: 3, KindClone: 4,
	KindScratch: 5, KindPlugin: 6, KindTranscript: 7}

// Options is what a scan looks at, and how it judges.
type Options struct {
	// Now is the time the rules are judged at.
	Now time.Time
	// After is how long something must have gone unchanged: no commit,
	// merge or file write.
	After time.Duration
	// Keep are globs; an item whose path, folder name or branch matches one
	// is never removed.
	Keep []string
	// Projects are folders Claude Code has run in; Roots are more folders
	// to look for repositories in, three levels down.
	Projects []string
	Roots    []string
	// ConfigDirs are Claude Code's config folders, one per profile: where
	// its live sessions, plugin cache and transcripts are.
	ConfigDirs []string
	// TempRoot is Claude Code's temp folder for this user
	// (/tmp/claude-<uid>), "" for none.
	TempRoot string
	// StateDir holds the journal, the archive and worktrees' git records.
	StateDir string
	// TrashDir is where removed folders go; "" is the platform's Trash.
	TrashDir string

	// The kinds, each its own setting.
	Worktrees bool // worktrees, local branches, and records of missing worktrees
	Clones    bool
	Temp      bool
	Remote    bool
	// TranscriptsAfter is how old an ended session's subagent transcripts
	// must be to go, with Temp; 0 keeps them.
	TranscriptsAfter time.Duration
	// Guarded are folders never looked in: macOS asks the person before a
	// process reads them, and the daemon has no one to ask (MacGuarded).
	Guarded []string
}

// MacGuarded are the folders macOS asks about before a process may read
// them (Files and Folders): Desktop, Documents, Downloads and iCloud Drive.
func MacGuarded(home string) []string {
	var out []string
	for _, d := range []string{"Desktop", "Documents", "Downloads", "Library/Mobile Documents"} {
		out = append(out, filepath.Join(home, d))
	}
	return out
}

// guarded reports whether path is in a folder never looked in.
func (o Options) guarded(path string) bool {
	for _, g := range o.Guarded {
		if within(path, g) {
			return true
		}
	}
	return false
}

// on reports whether a kind is looked at.
func (o Options) on(k Kind) bool {
	switch k {
	case KindWorktree, KindBranch, KindPrune:
		return o.Worktrees
	case KindClone:
		return o.Clones
	case KindScratch, KindPlugin, KindTranscript:
		return o.Temp
	case KindRemote:
		return o.Remote
	}
	return false
}

// Item is one candidate.
type Item struct {
	Kind   Kind   `json:"kind"`
	Path   string `json:"path"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Remove bool   `json:"remove"`
	// Why is what makes it removable, or what keeps it.
	Why   string `json:"why"`
	Bytes int64  `json:"bytes,omitempty"`
	// Archive are paths inside Path, neither tracked nor rebuildable, that
	// Apply copies to the archive before moving Path to the Trash.
	Archive []string `json:"archive,omitempty"`
	// Admin is a worktree's record in its repository (.git/worktrees/<id>).
	Admin string `json:"admin,omitempty"`
	// State is what Apply compares, to act only on what the plan saw.
	State string `json:"state,omitempty"`
}

// Plan is a scan's result.
type Plan struct {
	At    time.Time `json:"at"`
	Items []Item    `json:"items"`
	// Guarded are the projects, and folders under a root, left unread
	// because macOS guards them (Options.Guarded).
	Guarded []string `json:"guarded,omitempty"`
}

// Removable is the items Apply would act on.
func (p *Plan) Removable() []Item {
	var out []Item
	for _, it := range p.Items {
		if it.Remove {
			out = append(out, it)
		}
	}
	return out
}

// Scan finds every candidate and judges it. It changes nothing.
func Scan(ctx context.Context, opt Options) (*Plan, error) {
	s := newScanner(ctx, opt)
	p := &Plan{At: opt.Now}
	repos := s.repos()
	p.Guarded = s.guarded
	if opt.Worktrees || opt.Remote {
		byRepo := make([][]Item, len(repos))
		parallel(len(repos), 3, func(i int) { byRepo[i] = s.scanRepo(repos[i]) })
		for _, items := range byRepo {
			p.Items = append(p.Items, items...)
		}
	}
	if opt.Clones {
		p.Items = append(p.Items, s.scanClones(repos)...)
	}
	if opt.Temp {
		p.Items = append(p.Items, s.scanTemp()...)
	}
	sortItems(p.Items)
	return p, nil
}

func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Branch < b.Branch
	})
}

// kept reports the Keep glob an item matches, "" for none.
func (o Options) kept(path, branch string) string {
	for _, g := range o.Keep {
		for _, s := range []string{path, filepath.Base(path), branch} {
			if s == "" {
				continue
			}
			if ok, _ := filepath.Match(g, s); ok {
				return g
			}
		}
	}
	return ""
}

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// recent is the keep reason for something changed within After, "" when
// it has been quiet long enough.
func (o Options) recent(touched time.Time, window time.Duration) string {
	if touched.IsZero() {
		return ""
	}
	if d := o.Now.Sub(touched); d < window {
		return fmt.Sprintf("changed %s ago (waits %s)", ago(d), ago(window))
	}
	return ""
}

func latest(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

func mtime(path string) time.Time {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// reflogTime is when a reflog's newest entry was written, read from the
// entry: git gc rewrites every reflog (reflog expire), which moves the
// file's time while nothing changed. An empty reflog is no change; one
// whose last entry cannot be read counts by its file's time.
func reflogTime(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return time.Time{}
	}
	off := max(fi.Size()-8<<10, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return fi.ModTime()
	}
	tail := strings.TrimRight(string(buf), "\n")
	if strings.TrimSpace(tail) == "" {
		return time.Time{}
	}
	// <old> <new> <name> <<email>> <unix time> <zone>\t<message>
	head, _, _ := strings.Cut(tail[strings.LastIndexByte(tail, '\n')+1:], "\t")
	if fields := strings.Fields(head); len(fields) >= 2 {
		if n, err := strconv.ParseInt(fields[len(fields)-2], 10, 64); err == nil {
			return time.Unix(n, 0)
		}
	}
	return fi.ModTime()
}

// newestFile is the latest modification time of any file under path (not
// of folders, whose times change when something inside is removed).
func newestFile(path string) time.Time {
	var m time.Time
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(m) {
			m = fi.ModTime()
		}
		return nil
	})
	return m
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
