package clean

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// scanClones judges every second clone of a project's repository found
// beside one: in the folders that hold the repositories scanned.
func (s *scanner) scanClones(repos []string) []Item {
	byURL := map[string][]string{}
	seen := map[string]bool{}
	for _, r := range repos {
		parent := filepath.Dir(r)
		if seen[parent] {
			continue
		}
		seen[parent] = true
		ents, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range ents {
			d := filepath.Join(parent, e.Name())
			if fi, err := os.Stat(filepath.Join(d, ".git")); err != nil || !fi.IsDir() {
				continue // not a clone (a worktree has a .git file)
			}
			if u := originURL(s.ctx, d); u != "" {
				byURL[u] = append(byURL[u], d)
			}
		}
	}
	urls := make([]string, 0, len(byURL))
	for u := range byURL {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	var items []Item
	for _, u := range urls {
		clones := byURL[u]
		if len(clones) < 2 {
			continue
		}
		var primary string
		for _, c := range clones {
			if filepath.Base(c) == repoName(u) {
				primary = c
			}
		}
		var others []string
		for _, c := range clones {
			if c != primary {
				others = append(others, c)
			}
		}
		judged := make([]Item, len(others))
		parallel(len(others), 4, func(i int) { judged[i] = s.cloneItem(others[i], primary, repoName(u)) })
		items = append(items, judged...)
	}
	return items
}

// allowedRefs are the refs a lane clone may hold and still be removed:
// branches (judged one by one), origin's, tags (compared with the primary)
// and cs clean's own archive of merged branches.
var allowedRefs = []string{"refs/heads/", "refs/remotes/", "refs/tags/", "refs/archive/"}

func (s *scanner) cloneItem(dir, primary, name string) Item {
	it := Item{Kind: KindClone, Path: dir}
	keep := func(why string) Item { it.Why = why; return it }
	if primary == "" {
		return keep("no primary clone named " + name + " beside it")
	}
	if g := s.opt.kept(dir, ""); g != "" {
		return keep("matches clean_keep " + strconv.Quote(g))
	}
	if why := s.inUse(dir); why != "" {
		return keep(why)
	}
	common := filepath.Join(dir, ".git")
	if op := inProgress(common); op != "" {
		return keep(op)
	}
	if wts, err := listWorktrees(s.ctx, dir); err != nil {
		return keep("git cannot list its worktrees")
	} else if n := len(wts) - 1; n > 0 {
		return keep(fmt.Sprintf("it has %d worktree%s of its own; they are judged first", n, plural(n)))
	}
	if out, _ := git(s.ctx, dir, "stash", "list"); out != "" {
		n := len(strings.Split(out, "\n"))
		return keep(fmt.Sprintf("%d stash entr%s", n, map[bool]string{true: "y", false: "ies"}[n == 1]))
	}
	refs, err := git(s.ctx, dir, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return keep("git cannot list its refs")
	}
	def := defaultBranch(s.ctx, dir)
	touched := latest(mtime(filepath.Join(common, "logs", "HEAD")), mtime(filepath.Join(common, "index")))
	var branches int
	for _, line := range strings.Split(refs, "\n") {
		ref, sha, ok := strings.Cut(line, " ")
		if !ok || ref == "refs/stash" {
			continue
		}
		allowed := false
		for _, p := range allowedRefs {
			allowed = allowed || strings.HasPrefix(ref, p)
		}
		if !allowed {
			return keep("it has " + ref)
		}
		switch {
		case strings.HasPrefix(ref, "refs/tags/"):
			if theirs, _ := git(s.ctx, primary, "rev-parse", "-q", "--verify", ref); theirs != sha {
				return keep("tag " + strings.TrimPrefix(ref, "refs/tags/") + " is not in " + filepath.Base(primary))
			}
		case strings.HasPrefix(ref, "refs/heads/"):
			b := strings.TrimPrefix(ref, "refs/heads/")
			branches++
			merged, _, mergedAt := s.merged(dir, def, b, sha)
			if !merged {
				return keep("branch " + b + ": " + s.unmergedWhy(dir, def, sha))
			}
			touched = latest(touched, commitTime(s.ctx, dir, sha), mergedAt)
		}
	}
	d := s.disk(dir, def)
	if d.keep != "" {
		return keep(d.keep)
	}
	touched = latest(touched, d.newest)
	if why := s.opt.recent(touched, s.opt.After); why != "" {
		return keep(why)
	}
	it.Remove, it.Archive = true, d.archive
	it.Why = fmt.Sprintf("a second clone of %s; its %d branch%s merged", name, branches, map[bool]string{true: "", false: "es"}[branches == 1])
	if d.note != "" {
		it.Why += "; " + d.note
	}
	it.State = refs + " " + d.state
	it.Bytes = treeSize(dir)
	return it
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
