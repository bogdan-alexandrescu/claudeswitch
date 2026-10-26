package clean

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitEnv is added to every git command's environment; tests use it to keep
// the person's own git configuration (hooks, signing) out.
var gitEnv []string

// git runs git in dir and returns its trimmed standard output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

func gitRaw(ctx context.Context, dir string, args ...string) (string, error) {
	return gitIn(ctx, dir, "", args...)
}

// gitIn is gitRaw with input on stdin.
func gitIn(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Dir = dir
	// GIT_OPTIONAL_LOCKS=0: git status would otherwise refresh the index,
	// taking index.lock in a worktree a session may be working in.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), gitEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New("git " + strings.Join(args, " ") + ": " + msg)
	}
	return out.String(), nil
}

// gitOK runs a git command whose exit status is the answer.
func gitOK(ctx context.Context, dir string, args ...string) bool {
	_, err := gitRaw(ctx, dir, args...)
	return err == nil
}

// worktree is one entry of `git worktree list --porcelain`.
type worktree struct {
	path   string
	head   string
	branch string // short name; "" when detached
	bare   bool
	locked bool
	// lockReason is what `git worktree lock --reason` recorded, if anything.
	lockReason string
	prunable   bool
}

func listWorktrees(ctx context.Context, repo string) ([]worktree, error) {
	out, err := gitRaw(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var all []worktree
	var cur *worktree
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			all = append(all, worktree{path: val})
			cur = &all[len(all)-1]
		case "HEAD":
			if cur != nil {
				cur.head = val
			}
		case "branch":
			if cur != nil {
				cur.branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.bare = true
			}
		case "locked":
			if cur != nil {
				cur.locked, cur.lockReason = true, val
			}
		case "prunable":
			if cur != nil {
				cur.prunable = true
			}
		}
	}
	return all, nil
}

// defaultRef is a repository's default branch: its short name ("main") and
// the ref merges are judged against, origin's copy when there is one.
type defaultRef struct {
	short string
	ref   string
}

func defaultBranch(ctx context.Context, repo string) defaultRef {
	if out, err := git(ctx, repo, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return defaultRef{short: strings.TrimPrefix(out, "origin/"), ref: "refs/remotes/" + out}
	}
	for _, b := range []string{"main", "master"} {
		if gitOK(ctx, repo, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+b) {
			return defaultRef{short: b, ref: "refs/remotes/origin/" + b}
		}
	}
	for _, b := range []string{"main", "master"} {
		if gitOK(ctx, repo, "rev-parse", "-q", "--verify", "refs/heads/"+b) {
			return defaultRef{short: b, ref: "refs/heads/" + b}
		}
	}
	return defaultRef{}
}

// commonDir is the repository's shared .git folder for any folder inside
// one of its worktrees, "" when dir is in no repository.
func commonDir(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return filepath.Clean(out)
}

// gitDir is a worktree's own git folder (.git/worktrees/<id> for a linked
// one).
func gitDir(ctx context.Context, dir string) string {
	out, err := git(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	return filepath.Clean(out)
}

func commitTime(ctx context.Context, repo, rev string) time.Time {
	out, err := git(ctx, repo, "log", "-1", "--format=%ct", rev, "--")
	if err != nil {
		return time.Time{}
	}
	n, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func isAncestor(ctx context.Context, repo, a, b string) bool {
	return gitOK(ctx, repo, "merge-base", "--is-ancestor", a, b)
}

// inProgress names a git operation underway in a worktree's git folder, ""
// for none.
func inProgress(gitdir string) string {
	for _, f := range []struct{ name, what string }{
		{"MERGE_HEAD", "a merge"}, {"rebase-merge", "a rebase"}, {"rebase-apply", "a rebase"},
		{"CHERRY_PICK_HEAD", "a cherry-pick"}, {"REVERT_HEAD", "a revert"}, {"BISECT_LOG", "a bisect"},
		{"index.lock", "a git command (index.lock)"},
	} {
		if exists(filepath.Join(gitdir, f.name)) {
			return f.what + " in progress"
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// originURL is a repository's origin, normalized so clones made over https
// and ssh compare equal.
func originURL(ctx context.Context, repo string) string {
	out, err := git(ctx, repo, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return normalizeURL(out)
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		u = strings.TrimPrefix(u, p)
	}
	u = strings.TrimPrefix(u, "git@")
	if i := strings.Index(u, "@"); i >= 0 && !strings.HasPrefix(u, "/") {
		u = u[i+1:]
	}
	if host, path, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") {
		u = host + "/" + path
	}
	return strings.ToLower(u)
}

// repoName is the last part of an origin: what a primary clone is named.
func repoName(url string) string {
	return filepath.Base(strings.TrimSuffix(url, ".git"))
}

// githubSlug is owner/name for a GitHub origin, "" otherwise.
func githubSlug(url string) string {
	rest, ok := strings.CutPrefix(url, "github.com/")
	if !ok {
		return ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
