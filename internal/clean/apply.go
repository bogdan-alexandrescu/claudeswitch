package clean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Entry is one thing a run did, with what Undo needs to put it back.
type Entry struct {
	Item
	// Trash is where the folder went.
	Trash string `json:"trash,omitempty"`
	// AdminTo is where a worktree's git record was kept.
	AdminTo string `json:"admin_to,omitempty"`
	// ArchiveTo holds the copies of Item.Archive.
	ArchiveTo string `json:"archive_to,omitempty"`
	// Ref is refs/archive/<branch>, the deleted branch's tip.
	Ref    string `json:"ref,omitempty"`
	Undone bool   `json:"undone,omitempty"`
}

// Failure is an item a run could not act on.
type Failure struct {
	Item
	Err string `json:"error"`
}

// Run is one Apply, as its journal records it.
type Run struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Done    []Entry   `json:"done"`
	Skipped []Item    `json:"skipped,omitempty"`
	Failed  []Failure `json:"failed,omitempty"`
}

// Bytes is what a run moved to the Trash.
func (r *Run) Bytes() int64 {
	var n int64
	for _, e := range r.Done {
		n += e.Bytes
	}
	return n
}

func runsDir(opt Options) string { return filepath.Join(opt.StateDir, "runs") }

func (r *Run) save(opt Options) error {
	if err := os.MkdirAll(runsDir(opt), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(runsDir(opt), r.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Runs is every journal, the newest first.
func Runs(opt Options) ([]Run, error) {
	files, err := filepath.Glob(filepath.Join(runsDir(opt), "*.json"))
	if err != nil {
		return nil, err
	}
	var runs []Run
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var r Run
		if json.Unmarshal(b, &r) == nil {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })
	return runs, nil
}

// ErrBusy is lock's refusal while another run holds it.
var ErrBusy = errors.New("another cleanup is running")

// lock holds clean's lock for a run: one at a time, from the CLI, the
// daemon or the app.
func lock(opt Options) (func(), error) {
	if err := os.MkdirAll(opt.StateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(opt.StateDir, "clean.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// Apply acts on a plan's removable items. Each is judged again first, and
// one that changed since the plan, or no longer passes, is skipped.
func Apply(ctx context.Context, p *Plan, opt Options) (*Run, error) {
	unlock, err := lock(opt)
	if err != nil {
		return nil, err
	}
	defer unlock()
	run := &Run{ID: newRunID(opt), At: opt.Now}
	s := newScanner(ctx, opt)
	items := p.Removable()
	sortItems(items)
	pruned := map[string]bool{}
	for _, it := range items {
		now, still := s.recheck(it)
		if !still {
			now.Remove = false
			run.Skipped = append(run.Skipped, now)
			continue
		}
		e := Entry{Item: now}
		var err error
		switch it.Kind {
		case KindWorktree:
			err = s.removeWorktree(run, &e)
		case KindBranch:
			err = s.removeBranch(&e)
		case KindPrune:
			if !pruned[it.Repo] {
				_, err = git(ctx, it.Repo, "worktree", "prune")
				pruned[it.Repo] = true
			}
		case KindClone:
			if err = s.archive(run, &e); err == nil {
				e.Trash, err = toTrash(opt, run.ID, it.Path)
			}
		case KindScratch, KindPlugin, KindTranscript:
			e.Trash, err = toTrash(opt, run.ID, it.Path)
		case KindRemote:
			err = s.removeRemote(&e)
		}
		if err != nil {
			run.Failed = append(run.Failed, Failure{Item: now, Err: err.Error()})
		} else {
			run.Done = append(run.Done, e)
		}
		if err := run.save(opt); err != nil {
			return run, err
		}
	}
	return run, run.save(opt)
}

func newRunID(opt Options) string {
	base := opt.Now.Format("20060102-150405")
	id := base
	for i := 2; exists(filepath.Join(runsDir(opt), id+".json")); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

// recheck judges an item again as a fresh scan would; still reports it is
// still removable and unchanged since the plan.
func (s *scanner) recheck(it Item) (Item, bool) {
	if !s.opt.on(it.Kind) {
		it.Why = "its kind was turned off since the plan"
		return it, false
	}
	var now Item
	switch it.Kind {
	case KindWorktree:
		wts, err := listWorktrees(s.ctx, it.Repo)
		if err != nil {
			return it, false
		}
		for _, wt := range wts[1:] {
			if wt.path == it.Path {
				now = s.worktreeItem(it.Repo, defaultBranch(s.ctx, it.Repo), wt)
			}
		}
	case KindPrune:
		return it, !exists(it.Path)
	case KindBranch:
		def := defaultBranch(s.ctx, it.Repo)
		out := map[string]bool{}
		if wts, err := listWorktrees(s.ctx, it.Repo); err == nil {
			for _, wt := range wts {
				out[wt.branch] = true
			}
		}
		for _, b := range s.branchItems(it.Repo, def, out) {
			if b.Branch == it.Branch {
				now = b
			}
		}
	case KindRemote:
		for _, r := range s.remoteItems(it.Repo, defaultBranch(s.ctx, it.Repo)) {
			if r.Branch == it.Branch {
				now = r
			}
		}
	case KindClone:
		for _, c := range s.scanClones([]string{it.Path}) {
			if c.Path == it.Path {
				now = c
			}
		}
	case KindScratch, KindPlugin, KindTranscript:
		for _, t := range s.scanTemp() {
			if t.Path == it.Path {
				now = t
			}
		}
	}
	if now.Path == "" {
		it.Why = "gone since the plan"
		return it, false
	}
	if !now.Remove {
		return now, false
	}
	if it.State != "" && now.State != it.State {
		now.Why = "changed since the plan"
		return now, false
	}
	return now, true
}

func (s *scanner) removeWorktree(run *Run, e *Entry) error {
	if err := s.archive(run, e); err != nil {
		return err
	}
	if e.Admin != "" {
		to := filepath.Join(s.opt.StateDir, "admin", run.ID, fmt.Sprintf("%d-%s", len(run.Done), filepath.Base(e.Admin)))
		if err := moveDir(e.Admin, to); err != nil {
			return fmt.Errorf("keeping its git record: %w", err)
		}
		e.AdminTo = to
	}
	trash, err := toTrash(s.opt, run.ID, e.Path)
	if err != nil {
		if e.AdminTo != "" {
			_ = moveDir(e.AdminTo, e.Admin) // leave the worktree as it was
		}
		return err
	}
	e.Trash = trash
	if e.Branch != "" && e.SHA != "" {
		if err := s.archiveBranch(e); err != nil {
			return fmt.Errorf("moved to the Trash, but its branch %s stays: %w", e.Branch, err)
		}
	}
	return nil
}

func (s *scanner) removeBranch(e *Entry) error { return s.archiveBranch(e) }

// archiveBranch keeps a branch's tip as refs/archive/<branch>, then deletes
// the branch, only if it still points at that tip.
func (s *scanner) archiveBranch(e *Entry) error {
	ref := "refs/archive/" + e.Branch
	if _, err := git(s.ctx, e.Repo, "update-ref", ref, e.SHA); err != nil {
		return err
	}
	e.Ref = ref
	_, err := git(s.ctx, e.Repo, "update-ref", "-d", "refs/heads/"+e.Branch, e.SHA)
	return err
}

// removeRemote deletes a branch on origin, only if origin still has it at
// the tip the plan judged.
func (s *scanner) removeRemote(e *Entry) error {
	out, err := git(s.ctx, e.Repo, "ls-remote", "--heads", "origin", "refs/heads/"+e.Branch)
	if err != nil {
		return err
	}
	if sha, _, _ := strings.Cut(out, "\t"); sha != e.SHA {
		return fmt.Errorf("origin has %s at %s now, not %s", e.Branch, short(sha), short(e.SHA))
	}
	if _, err := git(s.ctx, e.Repo, "update-ref", "refs/archive/"+e.Branch, e.SHA); err == nil {
		e.Ref = "refs/archive/" + e.Branch
	}
	_, err = git(s.ctx, e.Repo, "push", "--quiet", "origin", "--delete", "refs/heads/"+e.Branch)
	return err
}

// archive copies an item's untracked, unrebuildable paths out before its
// folder goes.
func (s *scanner) archive(run *Run, e *Entry) error {
	if len(e.Archive) == 0 {
		return nil
	}
	to := filepath.Join(s.opt.StateDir, "archive", run.ID, fmt.Sprintf("%d-%s", len(run.Done), filepath.Base(e.Path)))
	for _, rel := range e.Archive {
		if err := copyTree(filepath.Join(e.Path, rel), filepath.Join(to, rel)); err != nil {
			return fmt.Errorf("archiving %s: %w", rel, err)
		}
	}
	e.ArchiveTo = to
	return nil
}

// Undo puts back what a run did, newest first; only, when set, limits it to
// entries whose path or branch contains it.
func Undo(ctx context.Context, opt Options, id, only string) (*Run, error) {
	unlock, err := lock(opt)
	if err != nil {
		return nil, err
	}
	defer unlock()
	b, err := os.ReadFile(filepath.Join(runsDir(opt), id+".json"))
	if err != nil {
		return nil, fmt.Errorf("no cleanup run %s", id)
	}
	var run Run
	if err := json.Unmarshal(b, &run); err != nil {
		return nil, err
	}
	var errs []string
	for i := len(run.Done) - 1; i >= 0; i-- {
		e := &run.Done[i]
		if e.Undone || (only != "" && !strings.Contains(e.Path, only) && !strings.Contains(e.Branch, only)) {
			continue
		}
		if err := undoEntry(ctx, e); err != nil {
			errs = append(errs, fmt.Sprintf("%s %s: %v", e.Kind, e.Path, err))
			continue
		}
		e.Undone = true
	}
	if err := run.save(opt); err != nil {
		return &run, err
	}
	if len(errs) > 0 {
		return &run, errors.New(strings.Join(errs, "; "))
	}
	return &run, nil
}

func undoEntry(ctx context.Context, e *Entry) error {
	restoreBranch := func() error {
		if e.Ref == "" || e.Branch == "" {
			return nil
		}
		if gitOK(ctx, e.Repo, "rev-parse", "-q", "--verify", "refs/heads/"+e.Branch) {
			return nil
		}
		_, err := git(ctx, e.Repo, "update-ref", "refs/heads/"+e.Branch, e.SHA)
		return err
	}
	switch e.Kind {
	case KindBranch:
		return restoreBranch()
	case KindRemote:
		_, err := git(ctx, e.Repo, "push", "--quiet", "origin", e.SHA+":refs/heads/"+e.Branch)
		return err
	case KindPrune:
		return nil // its folder was already gone
	case KindWorktree:
		if err := restoreBranch(); err != nil {
			return err
		}
		if err := fromTrash(e.Trash, e.Path); err != nil {
			return err
		}
		if e.AdminTo != "" {
			return moveDir(e.AdminTo, e.Admin)
		}
		return nil
	default:
		return fromTrash(e.Trash, e.Path)
	}
}

func fromTrash(trash, path string) error {
	if trash == "" {
		return errors.New("nothing was moved to the Trash")
	}
	if !exists(trash) {
		return errors.New("no longer in the Trash (" + trash + ")")
	}
	if exists(path) {
		return errors.New("something is at " + path + " again")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Rename(trash, path)
}

// moveDir renames a folder, copying it across file systems.
func moveDir(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		return err
	}
	return os.RemoveAll(from)
}

// copyTree copies a file or folder, symlinks as symlinks.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, 0o700)
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			return os.Symlink(target, dst)
		case !info.Mode().IsRegular():
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
