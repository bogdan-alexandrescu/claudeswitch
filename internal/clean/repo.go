package clean

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// scanRepo judges one repository's linked worktrees, the records of those
// whose folder is gone, its local branches and, with Remote, its branches on
// origin.
func (s *scanner) scanRepo(repo string) []Item {
	wts, err := listWorktrees(s.ctx, repo)
	if err != nil || len(wts) == 0 {
		return nil
	}
	def := defaultBranch(s.ctx, repo)
	var items []Item
	checkedOut := map[string]bool{}
	for _, wt := range wts {
		if wt.branch != "" {
			checkedOut[wt.branch] = true
		}
	}
	if s.opt.Worktrees {
		linked := wts[1:] // the first is the main worktree
		judged := make([]*Item, len(linked))
		parallel(len(linked), 4, func(i int) {
			wt := linked[i]
			switch {
			case wt.bare:
			case wt.prunable || !exists(wt.path):
				it := s.pruneItem(repo, wt)
				judged[i] = &it
			default:
				it := s.worktreeItem(repo, def, wt)
				judged[i] = &it
			}
		})
		for _, it := range judged {
			if it != nil {
				items = append(items, *it)
			}
		}
		items = append(items, s.branchItems(repo, def, checkedOut)...)
	}
	if s.opt.Remote {
		items = append(items, s.remoteItems(repo, def)...)
	}
	return items
}

func (s *scanner) pruneItem(repo string, wt worktree) Item {
	it := Item{Kind: KindPrune, Path: wt.path, Repo: repo, Branch: wt.branch, SHA: wt.head}
	if wt.locked {
		it.Why = "locked (git worktree lock)"
		return it
	}
	it.Remove, it.Why = true, "its folder is gone; git still lists it"
	return it
}

func (s *scanner) worktreeItem(repo string, def defaultRef, wt worktree) Item {
	it := Item{Kind: KindWorktree, Path: wt.path, Repo: repo, Branch: wt.branch, SHA: wt.head}
	gd := gitDir(s.ctx, wt.path)
	it.Admin = gd
	keep := func(why string) Item { it.Why = why; return it }
	switch {
	case def.short != "" && wt.branch == def.short:
		return keep("on the default branch, " + def.short)
	case s.opt.kept(wt.path, wt.branch) != "":
		return keep("matches clean_keep " + strconv.Quote(s.opt.kept(wt.path, wt.branch)))
	case wt.locked:
		return keep("locked (git worktree lock)")
	case gd == "":
		return keep("git cannot read it")
	}
	if why := s.inUse(wt.path); why != "" {
		return keep(why)
	}
	if op := inProgress(gd); op != "" {
		return keep(op)
	}
	ok, how, mergedAt := s.merged(repo, def, wt.branch, wt.head)
	if !ok {
		return keep(s.unmergedWhy(repo, def, wt.head))
	}
	d := s.disk(wt.path, def)
	if d.keep != "" {
		return keep(d.keep)
	}
	touched := latest(commitTime(s.ctx, repo, wt.head), mergedAt, d.newest,
		mtime(filepath.Join(gd, "HEAD")), mtime(filepath.Join(gd, "index")), mtime(filepath.Join(gd, "logs", "HEAD")))
	if why := s.opt.recent(touched, s.opt.After); why != "" {
		return keep(why)
	}
	it.Remove, it.Archive, it.State = true, d.archive, wt.head+" "+d.state
	it.Why = how
	if d.note != "" {
		it.Why += "; " + d.note
	}
	it.Bytes = treeSize(wt.path)
	return it
}

// branchItems judges the local branches no worktree has checked out. Two
// git calls cover every branch: their tips and dates, and which are merged
// into the default branch; only the rest are looked up among pull requests.
func (s *scanner) branchItems(repo string, def defaultRef, checkedOut map[string]bool) []Item {
	out, err := git(s.ctx, repo, "for-each-ref", "--format=%(refname:strip=2) %(objectname) %(committerdate:unix)",
		"refs/heads")
	if err != nil || out == "" {
		return nil
	}
	inMain := map[string]bool{}
	if def.ref != "" {
		if m, err := git(s.ctx, repo, "for-each-ref", "--merged="+def.ref, "--format=%(refname:strip=2)",
			"refs/heads"); err == nil {
			for _, b := range strings.Split(m, "\n") {
				inMain[b] = true
			}
		}
	}
	// Which branches some merged pull request's head contains, in one call
	// (several --merged list what any one of them reaches).
	inPR := map[string]bool{}
	if heads, _ := s.mergedHeads(repo); len(heads) > 0 {
		args := []string{"for-each-ref", "--format=%(refname:strip=2)"}
		for _, h := range heads {
			args = append(args, "--merged="+h)
		}
		if m, err := git(s.ctx, repo, append(args, "refs/heads")...); err == nil {
			for _, b := range strings.Split(m, "\n") {
				inPR[b] = true
			}
		}
	}
	common := filepath.Join(repo, ".git")
	var items []Item
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] == def.short || checkedOut[f[0]] {
			continue
		}
		name, sha := f[0], f[1]
		it := Item{Kind: KindBranch, Path: repo, Repo: repo, Branch: name, SHA: sha}
		if g := s.opt.kept(repo, name); g != "" {
			it.Why = "matches clean_keep " + strconv.Quote(g)
			items = append(items, it)
			continue
		}
		merged, how, mergedAt := inMain[name], "merged into "+def.short, time.Time{}
		if !merged && inPR[name] {
			merged, how, mergedAt = s.mergedByPR(repo, name, sha)
		}
		if !merged {
			it.Why = "commits not in main and in no merged pull request"
			items = append(items, it)
			continue
		}
		var committed time.Time
		if n, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			committed = time.Unix(n, 0)
		}
		touched := latest(committed, mergedAt, mtime(filepath.Join(common, "logs", "refs", "heads", name)))
		if why := s.opt.recent(touched, s.opt.After); why != "" {
			it.Why = why
			items = append(items, it)
			continue
		}
		it.Remove, it.Why, it.State = true, how, sha
		items = append(items, it)
	}
	return items
}

// remoteItems judges origin's branches that a merged pull request had as
// its head: deleted only when the branch is still exactly the merged tip and
// no open pull request targets it.
func (s *scanner) remoteItems(repo string, def defaultRef) []Item {
	out, err := git(s.ctx, repo, "for-each-ref", "--format=%(refname:strip=3) %(objectname)", "refs/remotes/origin")
	if err != nil {
		return nil
	}
	prs := s.pullRequests(repo)
	var items []Item
	for _, line := range strings.Split(out, "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if !ok || name == "HEAD" || name == def.short {
			continue
		}
		var mergedPR *PR
		for i := range prs {
			if prs[i].State == "MERGED" && prs[i].Head == name {
				if mergedPR == nil || prs[i].MergedAt.After(mergedPR.MergedAt) {
					mergedPR = &prs[i]
				}
			}
		}
		if mergedPR == nil {
			continue // not a merged pull request's branch: nothing to judge
		}
		it := Item{Kind: KindRemote, Path: repo, Repo: repo, Branch: name, SHA: sha}
		items = append(items, it)
		last := &items[len(items)-1]
		switch {
		case s.opt.kept(repo, name) != "":
			last.Why = "matches clean_keep " + strconv.Quote(s.opt.kept(repo, name))
			continue
		case mergedPR.HeadSHA != sha:
			last.Why = fmt.Sprintf("a commit was pushed after #%d merged", mergedPR.Number)
			continue
		}
		if base := openBasedOn(prs, name); base != 0 {
			last.Why = fmt.Sprintf("the base of open pull request #%d", base)
			continue
		}
		if why := s.opt.recent(mergedPR.MergedAt, s.opt.After); why != "" {
			last.Why = why
			continue
		}
		last.Remove, last.Why, last.State = true, fmt.Sprintf("PR #%d merged with this tip", mergedPR.Number), sha
	}
	return items
}

func openBasedOn(prs []PR, branch string) int {
	for _, pr := range prs {
		if pr.State == "OPEN" && pr.Base == branch {
			return pr.Number
		}
	}
	return 0
}
