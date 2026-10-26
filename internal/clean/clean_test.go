package clean

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Owner decisions 2026-10-25 (IMPROVEMENTS "Cleaning up after Claude"): a
// worktree, branch, clone or temp folder is removed only when it holds no
// commit outside a merge, no file that is not rebuildable or archived, is not
// in use, has not changed within the window, and is not protected. Removal
// goes to the Trash with a journal, branch tips are kept as
// refs/archive/<branch>, and a run can be undone.
//
// The worlds here are real git repositories: an "origin" bare repository and
// a clone of it, so merges, pushes and the default branch behave as on a
// person's machine. Pull requests come from a fake forge.

type world struct {
	t      *testing.T
	root   string
	origin string
	repo   string
	cfg    string
	opt    Options
	prs    []PR
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, root: root, origin: filepath.Join(root, "origin.git"), repo: filepath.Join(root, "proj"),
		cfg: filepath.Join(root, "cc")}
	w.git(root, "init", "-q", "--bare", "-b", "main", w.origin)
	w.git(root, "clone", "-q", w.origin, w.repo)
	w.commit(w.repo, "README.md", "hello\n", "first")
	w.git(w.repo, "push", "-q", "origin", "main")
	w.git(w.repo, "remote", "set-head", "origin", "main")
	if err := os.MkdirAll(filepath.Join(w.cfg, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	w.opt = Options{
		Now: time.Now().Add(100 * time.Hour), After: 72 * time.Hour,
		Projects: []string{w.repo}, ConfigDirs: []string{w.cfg},
		StateDir: filepath.Join(root, "state"), TrashDir: filepath.Join(root, "Trash"),
		Worktrees: true,
	}
	oldForge, oldCwds, oldEnv := forge, processCwds, gitEnv
	t.Cleanup(func() { forge, processCwds, gitEnv = oldForge, oldCwds, oldEnv })
	forge = fakeForge{w}
	gitEnv = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}
	processCwds = func(context.Context) []string { return nil }
	return w
}

type fakeForge struct{ w *world }

func (f fakeForge) PRs(_ context.Context, _ string) ([]PR, error) { return f.w.prs, nil }

func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *world) commit(dir, file, content, msg string) string {
	w.t.Helper()
	w.write(filepath.Join(dir, file), content)
	w.git(dir, "add", file)
	w.git(dir, "commit", "-q", "-m", msg)
	return w.git(dir, "rev-parse", "HEAD")
}

func (w *world) write(path, content string) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// worktree adds a linked worktree on a new branch with one commit, where
// Claude Code puts an agent's.
func (w *world) worktree(name, branch string) string {
	w.t.Helper()
	path := filepath.Join(w.repo, ".claude", "worktrees", name)
	w.git(w.repo, "worktree", "add", "-q", "-b", branch, path)
	w.commit(path, name+".txt", name+"\n", "work on "+name)
	return path
}

// mergeNoFF merges a branch into main with a merge commit and pushes main.
func (w *world) mergeNoFF(branch string) {
	w.t.Helper()
	w.git(w.repo, "merge", "-q", "--no-ff", "-m", "merge "+branch, branch)
	w.git(w.repo, "push", "-q", "origin", "main")
}

// squash lands a branch's changes on main as one new commit, as a squash
// merge on GitHub does, and records the merged pull request.
func (w *world) squash(branch string, number int) {
	w.t.Helper()
	head := w.git(w.repo, "rev-parse", branch)
	w.git(w.repo, "merge", "-q", "--squash", branch)
	w.git(w.repo, "commit", "-q", "-m", fmt.Sprintf("%s (#%d)", branch, number))
	w.git(w.repo, "push", "-q", "origin", "main")
	w.prs = append(w.prs, PR{Number: number, Head: branch, HeadSHA: head, Base: "main", State: "MERGED",
		MergedAt: time.Now()})
}

func (w *world) scan() *Plan {
	w.t.Helper()
	p, err := Scan(context.Background(), w.opt)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (w *world) item(p *Plan, kind Kind, path string) Item {
	w.t.Helper()
	for _, it := range p.Items {
		if it.Kind == kind && it.Path == path {
			return it
		}
	}
	var got []string
	for _, it := range p.Items {
		got = append(got, fmt.Sprintf("%s %s", it.Kind, it.Path))
	}
	w.t.Fatalf("no %s item for %s in the plan; it has:\n%s", kind, path, strings.Join(got, "\n"))
	return Item{}
}

func (w *world) branchItem(p *Plan, branch string) Item {
	w.t.Helper()
	for _, it := range p.Items {
		if it.Kind == KindBranch && it.Branch == branch {
			return it
		}
	}
	w.t.Fatalf("no branch item for %s", branch)
	return Item{}
}

func mustRemove(t *testing.T, it Item) {
	t.Helper()
	if !it.Remove {
		t.Fatalf("%s %s kept: %s", it.Kind, it.Path, it.Why)
	}
}

func mustKeep(t *testing.T, it Item, why string) {
	t.Helper()
	if it.Remove {
		t.Fatalf("%s %s would be removed (%s); it must be kept", it.Kind, it.Path, it.Why)
	}
	if !strings.Contains(it.Why, why) {
		t.Fatalf("%s %s kept for %q, want it to say %q", it.Kind, it.Path, it.Why, why)
	}
}

func TestAMergedWorktreeIsMovedToTheTrashAndComesBackOnUndo(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-a1", "lane/a1")
	tip := w.git(wt, "rev-parse", "HEAD")
	w.mergeNoFF("lane/a1")
	p := w.scan()
	mustRemove(t, w.item(p, KindWorktree, wt))

	run, err := Apply(context.Background(), p, w.opt)
	if err != nil {
		t.Fatal(err)
	}
	if exists(wt) {
		t.Fatal("the worktree is still there")
	}
	if list := w.git(w.repo, "worktree", "list"); strings.Contains(list, "agent-a1") {
		t.Fatalf("git still lists it:\n%s", list)
	}
	if got := w.git(w.repo, "rev-parse", "refs/archive/lane/a1"); got != tip {
		t.Fatalf("refs/archive/lane/a1 = %s, want the tip %s", got, tip)
	}
	if out := w.git(w.repo, "branch", "--list", "lane/a1"); out != "" {
		t.Fatalf("the branch is still there: %q", out)
	}
	var trashed string
	for _, e := range run.Done {
		if e.Kind == KindWorktree {
			trashed = e.Trash
		}
	}
	if trashed == "" || !strings.HasPrefix(trashed, w.opt.TrashDir) || !exists(trashed) {
		t.Fatalf("not in the Trash: %q", trashed)
	}
	// The journal is on disk, and undo puts it all back.
	runs, err := Runs(w.opt)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs %v (%v)", runs, err)
	}
	if _, err := Undo(context.Background(), w.opt, run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(wt, "agent-a1.txt")) {
		t.Fatal("undo did not bring the worktree back")
	}
	if got := w.git(wt, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("restored worktree at %s, want %s", got, tip)
	}
	if out := w.git(wt, "status", "--porcelain"); out != "" {
		t.Fatalf("restored worktree is not clean: %q", out)
	}
	if !strings.Contains(w.git(w.repo, "worktree", "list"), "agent-a1") {
		t.Fatal("git does not list the restored worktree")
	}
}

func TestAWorktreeWithCommitsInNoMergeIsKept(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-b1", "lane/b1")
	mustKeep(t, w.item(w.scan(), KindWorktree, wt), "not in main")
}

func TestASquashMergedWorktreeIsRemovedButNotOneWithACommitAfterTheMerge(t *testing.T) {
	w := newWorld(t)
	done := w.worktree("agent-c1", "lane/c1")
	w.squash("lane/c1", 7)
	later := w.worktree("agent-c2", "lane/c2")
	w.squash("lane/c2", 8)
	w.commit(later, "after.txt", "after the merge\n", "a commit after the merge")
	p := w.scan()
	mustRemove(t, w.item(p, KindWorktree, done))
	if why := w.item(p, KindWorktree, done).Why; !strings.Contains(why, "#7") {
		t.Fatalf("why does not name the pull request: %q", why)
	}
	mustKeep(t, w.item(p, KindWorktree, later), "not in main")
}

// A branch moved on under its checked-out folder leaves the folder showing
// the old commit's files as changes; nothing in them is new.
func TestAStaleCheckoutCountsAsCleanAndItsScratchIsArchived(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-d1", "lane/d1")
	w.mergeNoFF("lane/d1")
	// Main moves on, and the branch ref is moved to it from the main
	// worktree, under the checked-out folder.
	w.commit(w.repo, "more.txt", "more\n", "more on main")
	w.git(w.repo, "push", "-q", "origin", "main")
	w.git(w.repo, "update-ref", "refs/heads/lane/d1", w.git(w.repo, "rev-parse", "main"))
	if w.git(wt, "status", "--porcelain") == "" {
		t.Fatal("setup: the stale checkout should show changes")
	}
	w.write(filepath.Join(wt, "scratchpad", "notes.md"), "agent notes\n")
	w.write(filepath.Join(wt, "node_modules", "x", "index.js"), "module.exports = 1\n")
	w.write(filepath.Join(wt, ".gitignore"), "node_modules/\n")
	it := w.item(w.scan(), KindWorktree, wt)
	// .gitignore is a new untracked file too: archived like the notes.
	mustRemove(t, it)
	if !strings.Contains(it.Why, "stale") {
		t.Fatalf("why: %q", it.Why)
	}
	joined := strings.Join(it.Archive, " ")
	if !strings.Contains(joined, "scratchpad") || strings.Contains(joined, "node_modules") {
		t.Fatalf("archive %v: want scratchpad, not node_modules", it.Archive)
	}
	run, err := Apply(context.Background(), &Plan{Items: []Item{it}}, w.opt)
	if err != nil {
		t.Fatal(err)
	}
	var archived string
	for _, e := range run.Done {
		archived = e.ArchiveTo
	}
	b, err := os.ReadFile(filepath.Join(archived, "scratchpad", "notes.md"))
	if err != nil || string(b) != "agent notes\n" {
		t.Fatalf("notes not archived at %s: %q %v", archived, b, err)
	}
}

func TestAWorktreeWithRealChangesIsKept(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-e1", "lane/e1")
	w.mergeNoFF("lane/e1")
	w.write(filepath.Join(wt, "agent-e1.txt"), "edited and never committed\n")
	mustKeep(t, w.item(w.scan(), KindWorktree, wt), "match no commit")
}

func TestAWorktreeInUseIsKept(t *testing.T) {
	w := newWorld(t)
	live := w.worktree("agent-f1", "lane/f1")
	cwd := w.worktree("agent-f2", "lane/f2")
	locked := w.worktree("agent-f3", "lane/f3")
	recent := w.worktree("agent-f4", "lane/f4")
	merging := w.worktree("agent-f5", "lane/f5")
	for _, b := range []string{"lane/f1", "lane/f2", "lane/f3", "lane/f4", "lane/f5"} {
		w.mergeNoFF(b)
	}
	// A live Claude Code session works in one.
	sess, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "s-live", "cwd": live,
		"startedAt": time.Now().UnixMilli()})
	w.write(filepath.Join(w.cfg, "sessions", fmt.Sprint(os.Getpid())+".json"), string(sess))
	// A process has its working directory in another.
	processCwds = func(context.Context) []string { return []string{filepath.Join(cwd, "sub")} }
	w.git(w.repo, "worktree", "lock", locked)
	w.write(filepath.Join(recent, "scratchpad", "x.md"), "just now\n")
	touch(t, filepath.Join(recent, "scratchpad", "x.md"), w.opt.Now.Add(-time.Hour))
	gitdir := w.git(merging, "rev-parse", "--absolute-git-dir")
	w.write(filepath.Join(gitdir, "MERGE_HEAD"), w.git(merging, "rev-parse", "HEAD")+"\n")
	p := w.scan()
	mustKeep(t, w.item(p, KindWorktree, live), "session")
	mustKeep(t, w.item(p, KindWorktree, cwd), "process")
	mustKeep(t, w.item(p, KindWorktree, locked), "locked")
	mustKeep(t, w.item(p, KindWorktree, recent), "changed")
	mustKeep(t, w.item(p, KindWorktree, merging), "in progress")
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// Claude Code locks an agent's worktree while the agent runs, naming its
// process ("claude agent <name> (pid N start <time>)"), and a session that
// ends without cleaning up leaves the lock behind. A lock whose process is
// gone, or whose pid now belongs to a later process, is not a use (found
// 2026-10-25: eight such locks from September and October on this Mac). Any
// other lock still keeps a worktree.
func TestALockLeftByAnEndedClaudeSessionIsNotAUse(t *testing.T) {
	w := newWorld(t)
	ended := w.worktree("agent-k1", "lane/k1")
	reused := w.worktree("agent-k2", "lane/k2")
	running := w.worktree("agent-k3", "lane/k3")
	theirs := w.worktree("agent-k4", "lane/k4")
	for _, b := range []string{"lane/k1", "lane/k2", "lane/k3", "lane/k4"} {
		w.mergeNoFF(b)
	}
	const start = "Fri Sep  4 06:39:38 2026"
	w.git(w.repo, "worktree", "lock", "--reason", "claude agent agent-k1 (pid 4001 start "+start+")", ended)
	w.git(w.repo, "worktree", "lock", "--reason", "claude agent agent-k2 (pid 4002 start "+start+")", reused)
	w.git(w.repo, "worktree", "lock", "--reason", "claude agent agent-k3 (pid 4003 start "+start+")", running)
	w.git(w.repo, "worktree", "lock", "--reason", "pinned for the demo", theirs)
	oldAlive, oldStart := alive, processStart
	t.Cleanup(func() { alive, processStart = oldAlive, oldStart })
	alive = func(pid int) bool { return pid == 4002 || pid == 4003 }
	processStart = func(pid int) string {
		if pid == 4003 {
			return "Fri Sep 4 06:39:38 2026" // the same start, spaced as ps prints it
		}
		return "Sun Oct 25 09:00:00 2026"
	}
	p := w.scan()
	mustRemove(t, w.item(p, KindWorktree, ended))
	mustRemove(t, w.item(p, KindWorktree, reused))
	mustKeep(t, w.item(p, KindWorktree, running), "locked")
	mustKeep(t, w.item(p, KindWorktree, theirs), "locked")
	if why := w.item(p, KindWorktree, ended).Why; !strings.Contains(why, "ended") {
		t.Fatalf("the plan does not say the lock was left by an ended session: %q", why)
	}
}

func TestProtectedWorktreesAreNeverRemoved(t *testing.T) {
	w := newWorld(t)
	kept := w.worktree("release-rel", "lane/rel")
	w.mergeNoFF("lane/rel")
	w.opt.Keep = []string{"*-rel"}
	// A worktree on the default branch itself.
	w.git(w.repo, "checkout", "-q", "--detach")
	onMain := filepath.Join(w.root, "proj-main")
	w.git(w.repo, "worktree", "add", "-q", onMain, "main")
	p := w.scan()
	mustKeep(t, w.item(p, KindWorktree, kept), "keep")
	mustKeep(t, w.item(p, KindWorktree, onMain), "default branch")
	for _, it := range p.Items {
		if it.Path == w.repo && it.Remove {
			t.Fatalf("the main worktree would be removed: %+v", it)
		}
	}
}

func TestAWorktreeWhoseFolderIsGoneIsPruned(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-g1", "lane/g1")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	it := w.item(w.scan(), KindPrune, wt)
	mustRemove(t, it)
	if _, err := Apply(context.Background(), &Plan{Items: []Item{it}}, w.opt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.git(w.repo, "worktree", "list"), "agent-g1") {
		t.Fatal("still listed")
	}
}

func TestAMergedLocalBranchIsArchivedAndDeleted(t *testing.T) {
	w := newWorld(t)
	w.git(w.repo, "checkout", "-q", "-b", "feat/x")
	tip := w.commit(w.repo, "x.txt", "x\n", "x")
	w.git(w.repo, "checkout", "-q", "main")
	w.mergeNoFF("feat/x")
	w.git(w.repo, "checkout", "-q", "-b", "feat/open")
	w.commit(w.repo, "y.txt", "y\n", "y")
	w.git(w.repo, "checkout", "-q", "main")
	p := w.scan()
	it := w.branchItem(p, "feat/x")
	mustRemove(t, it)
	mustKeep(t, w.branchItem(p, "feat/open"), "not in main")
	if _, err := Apply(context.Background(), &Plan{Items: []Item{it}}, w.opt); err != nil {
		t.Fatal(err)
	}
	if w.git(w.repo, "branch", "--list", "feat/x") != "" {
		t.Fatal("not deleted")
	}
	if got := w.git(w.repo, "rev-parse", "refs/archive/feat/x"); got != tip {
		t.Fatalf("archive %s, want %s", got, tip)
	}
}

func TestNothingIsRemovedWithItsKindOff(t *testing.T) {
	w := newWorld(t)
	w.worktree("agent-h1", "lane/h1")
	w.mergeNoFF("lane/h1")
	w.opt.Worktrees = false
	for _, it := range w.scan().Items {
		if it.Remove {
			t.Fatalf("removed with worktrees off: %+v", it)
		}
	}
}

// Apply looks again just before it moves anything: a worktree that changed
// since the plan was made is left alone.
func TestApplyJudgesEachItemOnceMoreBeforeRemoving(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-i1", "lane/i1")
	w.mergeNoFF("lane/i1")
	p := w.scan()
	mustRemove(t, w.item(p, KindWorktree, wt))
	w.write(filepath.Join(wt, "agent-i1.txt"), "edited after the plan\n")
	run, err := Apply(context.Background(), p, w.opt)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(wt) {
		t.Fatal("removed although it changed after the plan")
	}
	if len(run.Skipped) != 1 {
		t.Fatalf("skipped %v", run.Skipped)
	}
}

// git gc rewrites every reflog (reflog expire), which moves the files' times
// but not the entries' own: a branch or worktree it touched has not changed
// (measured 2026-10-25: 619 ai-studio branches waited three more days after
// one gc).
func TestAReflogRewrittenByGCIsNotAChange(t *testing.T) {
	w := newWorld(t)
	w.git(w.repo, "checkout", "-q", "-b", "feat/gc")
	w.commit(w.repo, "gc.txt", "gc\n", "gc")
	w.git(w.repo, "checkout", "-q", "main")
	w.mergeNoFF("feat/gc")
	wt := w.worktree("agent-gc", "lane/gc")
	w.mergeNoFF("lane/gc")
	gcAt := w.opt.Now.Add(-time.Hour)
	common := filepath.Join(w.repo, ".git")
	for _, f := range []string{filepath.Join(common, "logs", "refs", "heads", "feat", "gc"),
		filepath.Join(common, "worktrees", "agent-gc", "logs", "HEAD"), filepath.Join(common, "logs", "HEAD")} {
		if err := os.Chtimes(f, gcAt, gcAt); err != nil {
			t.Fatal(err)
		}
	}
	p := w.scan()
	mustRemove(t, w.branchItem(p, "feat/gc"))
	mustRemove(t, w.item(p, KindWorktree, wt))
}

// A reflog entry is a change: a branch moved within the window (git branch
// -f, a reset) waits, even when its commits are old.
func TestABranchMovedRecentlyWaits(t *testing.T) {
	w := newWorld(t)
	w.git(w.repo, "checkout", "-q", "-b", "feat/moved")
	w.commit(w.repo, "m.txt", "m\n", "m")
	w.git(w.repo, "checkout", "-q", "main")
	w.mergeNoFF("feat/moved")
	w.opt.Now = time.Now().Add(time.Hour)
	mustKeep(t, w.branchItem(w.scan(), "feat/moved"), "changed")
}

// macOS asks the person before a process reads Desktop, Documents,
// Downloads or iCloud Drive, and the daemon's scan has no one to ask: four
// projects on the Desktop held its first run on a prompt (2026-10-26). A
// project or a root in a guarded folder is never looked in; the plan names
// it instead.
func TestFoldersMacOSGuardsAreNeverLookedIn(t *testing.T) {
	w := newWorld(t)
	desktop := filepath.Join(w.root, "Desktop")
	inside := filepath.Join(desktop, "proj2")
	w.git(w.root, "clone", "-q", w.origin, inside)
	w.git(inside, "worktree", "add", "-q", "-b", "lane/g1", filepath.Join(inside, ".claude", "worktrees", "agent-g1"))
	w.opt.Projects = append(w.opt.Projects, inside)
	w.opt.Roots = []string{w.root}
	w.opt.Guarded = []string{desktop}
	p := w.scan()
	for _, it := range p.Items {
		if within(it.Path, desktop) || within(it.Repo, desktop) {
			t.Fatalf("looked inside a guarded folder: %+v", it)
		}
	}
	if strings.Join(p.Guarded, ",") != desktop+","+inside {
		t.Fatalf("guarded %v, want the folder the walk met and the project", p.Guarded)
	}
	if got := MacGuarded("/Users/me"); strings.Join(got, ",") != "/Users/me/Desktop,/Users/me/Documents,"+
		"/Users/me/Downloads,/Users/me/Library/Mobile Documents" {
		t.Fatalf("MacGuarded = %v", got)
	}
}

// The daemon runs under launchd's PATH (/usr/bin:/bin:/usr/sbin:/sbin),
// where gh is not, so its first run checked no pull request and kept every
// squash-merged branch (2026-10-26). gh is looked for where installers put
// it too.
func TestGHIsFoundOffThePath(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	w := newWorld(t)
	w.write(filepath.Join(b, "gh"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(b, "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findGH(a, []string{a, b}); got != filepath.Join(b, "gh") {
		t.Fatalf("found %q", got)
	}
	if got := findGH(a, []string{a}); got != "" {
		t.Fatalf("found %q where there is none", got)
	}
	if !slices.Contains(ghDirs("/Users/me"), "/opt/homebrew/bin") ||
		!slices.Contains(ghDirs("/Users/me"), "/Users/me/.local/bin") {
		t.Fatalf("dirs %v", ghDirs("/Users/me"))
	}
}

type failingForge struct{}

func (failingForge) PRs(context.Context, string) ([]PR, error) { return nil, errNoGH }

// When pull requests could not be read, a branch outside the default
// branch is kept and the plan says why, rather than "in no merged pull
// request".
func TestABranchSaysWhenPullRequestsWereNotChecked(t *testing.T) {
	w := newWorld(t)
	w.git(w.repo, "checkout", "-q", "-b", "feat/sq")
	w.commit(w.repo, "sq.txt", "sq\n", "sq")
	w.git(w.repo, "checkout", "-q", "main")
	w.squash("feat/sq", 7)
	forge = failingForge{}
	mustKeep(t, w.branchItem(w.scan(), "feat/sq"), "pull requests not checked: gh not found")
}

// A saved plan is applied with the settings of now: an item whose kind was
// turned off after the plan was made is left alone.
func TestApplyLeavesAKindTurnedOffSinceThePlan(t *testing.T) {
	w := newWorld(t)
	wt := w.worktree("agent-j1", "lane/j1")
	w.mergeNoFF("lane/j1")
	p := w.scan()
	mustRemove(t, w.item(p, KindWorktree, wt))
	w.opt.Worktrees = false
	run, err := Apply(context.Background(), p, w.opt)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(wt) {
		t.Fatal("removed with worktrees turned off")
	}
	if len(run.Done) != 0 || len(run.Skipped) == 0 {
		t.Fatalf("done %v skipped %v", run.Done, run.Skipped)
	}
	for _, it := range run.Skipped {
		if !strings.Contains(it.Why, "turned off") {
			t.Fatalf("skipped for %q", it.Why)
		}
	}
}

// A branch made at, or behind, a merged pull request's head under another
// name (verify-899 at #899's head) holds nothing that did not merge.
func TestABranchInsideAMergedPullRequestUnderAnotherNameIsRemoved(t *testing.T) {
	w := newWorld(t)
	w.git(w.repo, "checkout", "-q", "-b", "feat/pr", "main")
	early := w.commit(w.repo, "p1.txt", "1\n", "one")
	w.commit(w.repo, "p2.txt", "2\n", "two")
	w.git(w.repo, "branch", "verify-at-head")
	w.git(w.repo, "branch", "verify-behind", early)
	w.git(w.repo, "checkout", "-q", "main")
	w.squash("feat/pr", 899)
	w.git(w.repo, "checkout", "-q", "-b", "verify-past", "verify-at-head")
	w.commit(w.repo, "p3.txt", "3\n", "past the head")
	w.git(w.repo, "checkout", "-q", "main")
	p := w.scan()
	at := w.branchItem(p, "verify-at-head")
	mustRemove(t, at)
	if !strings.Contains(at.Why, "#899") {
		t.Fatalf("why: %q", at.Why)
	}
	mustRemove(t, w.branchItem(p, "verify-behind"))
	mustKeep(t, w.branchItem(p, "verify-past"), "not in main")
}
