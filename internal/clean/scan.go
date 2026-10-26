package clean

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// session is a live Claude Code session, from its config folder's
// sessions/<pid>.json.
type session struct {
	configDir string
	pid       int
	id        string
	name      string
	cwd       string
	started   time.Time
}

type scanner struct {
	ctx  context.Context
	opt  Options
	live []session
	cwds []string

	mu  sync.Mutex
	prs map[string]*prList // by repository

	// guarded are the folders repos() left unread (Options.Guarded).
	guarded []string
}

// prList is one repository's pull requests, fetched once however many
// workers ask, and the head commits of its merged ones that this clone has.
type prList struct {
	once      sync.Once
	prs       []PR
	err       error // why the pull requests could not be read
	headsOnce sync.Once
	heads     []string
	merged    []PR
}

func newScanner(ctx context.Context, opt Options) *scanner {
	return &scanner{ctx: ctx, opt: opt, live: liveSessions(opt.ConfigDirs), cwds: processCwds(ctx),
		prs: map[string]*prList{}}
}

// parallel runs f for 0..n-1 on up to workers goroutines. A scan's git
// commands only read, so worktrees of a large repository are judged side by
// side.
func parallel(n, workers int, f func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers && w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				f(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// processCwds is every process's working directory; tests replace it.
var processCwds = func(ctx context.Context) []string {
	if runtime.GOOS == "linux" {
		return procCwds()
	}
	cmd := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-Fn", "-u", fmt.Sprint(os.Getuid()))
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run() // lsof exits 1 when some process could not be read
	var dirs []string
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "n") {
			dirs = append(dirs, line[1:])
		}
	}
	return dirs
}

func procCwds() []string {
	ents, _ := os.ReadDir("/proc")
	var dirs []string
	for _, e := range ents {
		if d, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd")); err == nil {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// alive reports whether a process exists; tests replace it.
// processStart is when a process started, as ps prints it ("Fri Sep  4
// 06:39:38 2026"); "" when it cannot be read.
var processStart = func(pid int) string {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// claudeLock is the reason Claude Code gives its lock on an agent's
// worktree: the agent, and the process holding it with when it started (so
// that a pid used again is told apart).
var claudeLock = regexp.MustCompile(`^claude agent \S+ \(pid (\d+) start (.+)\)$`)

// staleLock says why a worktree's lock no longer means it is in use, "" when
// it still does. Only Claude Code's own lock can be stale: its process has
// ended, or its pid now belongs to a process that started at another time.
// Any other lock is someone's, and a start time that cannot be read or
// compared counts as the same process.
func staleLock(reason string) string {
	m := claudeLock.FindStringSubmatch(strings.TrimSpace(reason))
	if m == nil {
		return ""
	}
	pid, _ := strconv.Atoi(m[1])
	if !alive(pid) {
		return fmt.Sprintf("its lock is a Claude Code session's that ended (pid %d)", pid)
	}
	const layout = "Mon Jan _2 15:04:05 2006"
	locked, err1 := time.ParseInLocation(layout, m[2], time.Local)
	now, err2 := time.ParseInLocation(layout, processStart(pid), time.Local)
	if err1 != nil || err2 != nil || now.Equal(locked) {
		return ""
	}
	return fmt.Sprintf("its lock is a Claude Code session's that ended (pid %d is another process now)", pid)
}

var alive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func liveSessions(configDirs []string) []session {
	var out []session
	for _, dir := range configDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var s struct {
				PID       int    `json:"pid"`
				SessionID string `json:"sessionId"`
				Name      string `json:"name"`
				CWD       string `json:"cwd"`
				StartedAt int64  `json:"startedAt"`
			}
			if json.Unmarshal(b, &s) != nil || !alive(s.PID) {
				continue
			}
			out = append(out, session{configDir: dir, pid: s.PID, id: s.SessionID, name: s.Name, cwd: s.CWD,
				started: time.UnixMilli(s.StartedAt)})
		}
	}
	return out
}

// inUse is why a folder counts as in use, "" when nothing uses it: a live
// session working inside it, or a process whose working directory is there.
func (s *scanner) inUse(path string) string {
	for _, l := range s.live {
		if within(l.cwd, path) {
			name := l.name
			if name == "" {
				name = fmt.Sprintf("pid %d", l.pid)
			}
			return fmt.Sprintf("a live Claude Code session (%s) works in it", name)
		}
	}
	for _, c := range s.cwds {
		if within(c, path) {
			return "a process works in it (" + c + ")"
		}
	}
	return ""
}

// repos is the main worktree of every repository to look at: those
// Claude Code ran in, and those under Roots.
func (s *scanner) repos() []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		common := commonDir(s.ctx, dir)
		if common == "" || filepath.Base(common) != ".git" {
			return // not in a repository, or a bare one
		}
		root := filepath.Dir(common)
		if !seen[root] {
			seen[root] = true
			out = append(out, root)
		}
	}
	skip := func(dir string) bool {
		if !s.opt.guarded(dir) {
			return false
		}
		if !slices.Contains(s.guarded, dir) {
			s.guarded = append(s.guarded, dir)
		}
		return true
	}
	for _, p := range s.opt.Projects {
		if skip(p) {
			continue // not even a stat: it is what makes macOS ask
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			add(p)
		}
	}
	for _, r := range s.opt.Roots {
		walkRepos(r, 3, add, skip)
	}
	sort.Strings(s.guarded)
	return out
}

// walkRepos calls add for every folder holding a .git folder, depth levels
// down from root.
func walkRepos(root string, depth int, add func(string), skip func(string) bool) {
	if skip(root) {
		return
	}
	if fi, err := os.Stat(filepath.Join(root, ".git")); err == nil && fi.IsDir() {
		add(root)
	}
	if depth == 0 {
		return
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" {
			walkRepos(filepath.Join(root, e.Name()), depth-1, add, skip)
		}
	}
}

func (s *scanner) prList(repo string) *prList {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.prs[repo]
	if l == nil {
		l = &prList{}
		s.prs[repo] = l
	}
	return l
}

func (s *scanner) pullRequests(repo string) []PR {
	l := s.prList(repo)
	l.once.Do(func() {
		l.prs, l.err = forge.PRs(s.ctx, repo)
	})
	return l.prs
}

// mergedHeads is the head commit of every merged pull request that the
// repository has the object for (one git call filters them), and those
// pull requests.
func (s *scanner) mergedHeads(repo string) ([]string, []PR) {
	prs := s.pullRequests(repo)
	l := s.prList(repo)
	l.headsOnce.Do(func() {
		var want strings.Builder
		for _, pr := range prs {
			if pr.State == "MERGED" && pr.HeadSHA != "" {
				l.merged = append(l.merged, pr)
				want.WriteString(pr.HeadSHA + "\n")
			}
		}
		if want.Len() == 0 {
			return
		}
		out, err := gitIn(s.ctx, repo, want.String(), "cat-file", "--batch-check=%(objectname) %(objecttype)")
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			if sha, typ, ok := strings.Cut(line, " "); ok && typ == "commit" && !seen[sha] {
				seen[sha] = true
				l.heads = append(l.heads, sha)
			}
		}
	})
	return l.heads, l.merged
}

// merged reports whether every commit of tip is already merged: in the
// default branch, or in the head of a pull request that merged. how says
// which; count is the commits not in the default branch.
func (s *scanner) merged(repo string, def defaultRef, branch, tip string) (ok bool, how string, when time.Time) {
	if tip == "" {
		return false, "", time.Time{}
	}
	if def.ref != "" && isAncestor(s.ctx, repo, tip, def.ref) {
		return true, "merged into " + def.short, time.Time{}
	}
	return s.mergedByPR(repo, branch, tip)
}

// mergedByPR reports whether tip's commits were all in the heads of pull
// requests that merged: a merged pull request's head is tip, or the
// branch's own merged pull request contains it, or, in one git call, no
// commit of tip is outside every merged head.
func (s *scanner) mergedByPR(repo, branch, tip string) (ok bool, how string, when time.Time) {
	heads, prs := s.mergedHeads(repo)
	if pr, ok := namedPR(prs, branch, tip); ok {
		return true, fmt.Sprintf("PR #%d merged", pr.Number), pr.MergedAt
	}
	for _, pr := range prs {
		if branch != "" && pr.Head == branch && isAncestor(s.ctx, repo, tip, pr.HeadSHA) {
			return true, fmt.Sprintf("PR #%d merged", pr.Number), pr.MergedAt
		}
	}
	if len(heads) == 0 {
		return false, "", time.Time{}
	}
	out, err := git(s.ctx, repo, append([]string{"rev-list", "--count", tip, "--not"}, heads...)...)
	if err != nil || out != "0" {
		return false, "", time.Time{}
	}
	return true, "its commits are in merged pull requests", time.Time{}
}

// namedPR is the merged pull request whose head is exactly tip.
func namedPR(prs []PR, branch, tip string) (PR, bool) {
	for _, pr := range prs {
		if pr.HeadSHA == tip {
			return pr, true
		}
	}
	return PR{}, false
}

// unmergedWhy is the keep reason for a tip with commits in no merge.
func (s *scanner) unmergedWhy(repo string, def defaultRef, tip string) string {
	n := "commits"
	if def.ref != "" {
		if out, err := git(s.ctx, repo, "rev-list", "--count", tip, "--not", def.ref); err == nil {
			if out == "1" {
				n = "1 commit"
			} else {
				n = out + " commits"
			}
		}
	}
	return s.noMergeWhy(repo, n)
}

// noMergeWhy keeps n commits that are in no merge; when the repository's
// pull requests could not be read, it says that instead of "in none".
func (s *scanner) noMergeWhy(repo, n string) string {
	if err := s.prList(repo).err; err != nil {
		return n + " not in main; pull requests not checked: " + err.Error()
	}
	return n + " not in main and in no merged pull request"
}
