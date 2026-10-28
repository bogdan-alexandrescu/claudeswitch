package clean

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// archiveCap is the most a worktree's untracked, unrebuildable files may
// hold for it to be removed with them archived; more keeps it, since that
// much is not an agent's notes.
const archiveCap = 50 << 20

// rebuildable are folder and file names a build or a package manager makes
// again: dropped with a worktree, never archived.
var rebuildable = map[string]bool{
	"node_modules": true, ".venv": true, "venv": true, ".tox": true, "dist": true, "build": true,
	".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true, ".parcel-cache": true, ".cache": true,
	"__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true, "coverage": true,
	"htmlcov": true, ".coverage": true, ".gradle": true, "target": true, ".build": true, "DerivedData": true,
	".terraform": true, ".eggs": true, ".DS_Store": true, "test-results": true, "playwright-report": true,
	"blob-report": true, ".eslintcache": true, ".nyc_output": true,
}

func isRebuildable(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(strings.TrimSuffix(rel, "/")), "/") {
		if rebuildable[part] || strings.HasSuffix(part, ".egg-info") || strings.HasSuffix(part, ".pyc") ||
			strings.HasSuffix(part, ".tsbuildinfo") {
			return true
		}
	}
	return false
}

// diskVerdict is what a worktree's folder holds beyond its commit.
type diskVerdict struct {
	keep    string    // why it must stay; "" when nothing would be lost
	note    string    // what the plan says about it
	archive []string  // paths to copy before removal
	newest  time.Time // the last write among its changed and untracked files
	state   string    // fingerprint of its status
}

// disk judges a worktree's folder: clean, or a stale checkout (index and
// tracked files exactly a commit that stays reachable), with untracked and
// ignored paths either rebuildable or small enough to archive.
func (s *scanner) disk(dir string, def defaultRef) diskVerdict {
	out, err := gitRaw(s.ctx, dir, "status", "--porcelain=v1", "-z", "--ignored=traditional",
		"--untracked-files=normal")
	if err != nil {
		return diskVerdict{keep: "git status failed: " + err.Error()}
	}
	v := diskVerdict{state: fmt.Sprintf("%x", sha1.Sum([]byte(out)))}
	var tracked, loose []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 4 {
			continue
		}
		xy, path := e[:2], e[3:]
		switch {
		case xy == "??" || xy == "!!":
			loose = append(loose, path)
		default:
			tracked = append(tracked, path)
			if xy[0] == 'R' || xy[0] == 'C' {
				i++ // the rename's source follows
			}
		}
	}
	for _, p := range tracked {
		v.newest = latest(v.newest, mtime(filepath.Join(dir, p)))
	}
	if len(tracked) > 0 {
		c := staleOf(s.ctx, dir, def)
		if c == "" {
			return diskVerdict{keep: fmt.Sprintf("%d changed files match no commit", len(tracked)), state: v.state}
		}
		v.note = "a stale checkout of " + short(c)
	}
	var size int64
	for _, p := range loose {
		if isRebuildable(p) {
			continue
		}
		full := filepath.Join(dir, p)
		v.archive = append(v.archive, strings.TrimSuffix(p, "/"))
		size += treeSize(full)
		v.newest = latest(v.newest, newestFile(full))
	}
	if size > archiveCap {
		return diskVerdict{keep: fmt.Sprintf("%s of untracked files (the archive takes up to %s)", human(size),
			human(archiveCap)), state: v.state}
	}
	if len(v.archive) > 0 {
		sort.Strings(v.archive)
		if v.note != "" {
			v.note += "; "
		}
		v.note += strings.Join(v.archive, ", ") + " archived"
	}
	return v
}

// staleOf is the commit a worktree's index and tracked files exactly are,
// among the recent history of its HEAD, its upstream and the default
// branch; "" when none. The index's tree is hashed here rather than written
// with git write-tree, so a scan writes nothing into the repository.
func staleOf(ctx context.Context, dir string, def defaultRef) string {
	if format, _ := git(ctx, dir, "rev-parse", "--show-object-format"); format != "" && format != "sha1" {
		return ""
	}
	if !gitOK(ctx, dir, "diff", "--quiet") { // the files differ from the index
		return ""
	}
	tree, ok := indexTree(ctx, dir)
	if !ok {
		return ""
	}
	revs := []string{"HEAD"}
	if def.ref != "" {
		revs = append(revs, def.ref)
	}
	if gitOK(ctx, dir, "rev-parse", "-q", "--verify", "@{upstream}") {
		revs = append(revs, "@{upstream}")
	}
	out, err := git(ctx, dir, append([]string{"log", "--format=%H %T", "--max-count=600"}, revs...)...)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if c, t, ok := strings.Cut(line, " "); ok && t == tree {
			return c
		}
	}
	return ""
}

// indexTree is the tree object id of a worktree's index, computed from
// `git ls-files -s` the way git builds trees. An index with conflicts has
// none.
func indexTree(ctx context.Context, dir string) (string, bool) {
	out, err := gitRaw(ctx, dir, "ls-files", "-s", "-z")
	if err != nil {
		return "", false
	}
	root := &treeNode{kids: map[string]*treeNode{}}
	for _, e := range strings.Split(out, "\x00") {
		if e == "" {
			continue
		}
		meta, path, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[2] != "0" {
			return "", false
		}
		id, err := hex.DecodeString(f[1])
		if err != nil {
			return "", false
		}
		n := root
		parts := strings.Split(path, "/")
		for _, d := range parts[:len(parts)-1] {
			k := n.kids[d]
			if k == nil {
				k = &treeNode{kids: map[string]*treeNode{}}
				n.kids[d] = k
			}
			n = k
		}
		n.kids[parts[len(parts)-1]] = &treeNode{mode: strings.TrimLeft(f[0], "0"), id: id}
	}
	return hex.EncodeToString(root.hash()), true
}

type treeNode struct {
	mode string
	id   []byte
	kids map[string]*treeNode
}

func (n *treeNode) hash() []byte {
	if n.kids == nil {
		return n.id
	}
	names := make([]string, 0, len(n.kids))
	for k := range n.kids {
		names = append(names, k)
	}
	// git orders tree entries by name, a subtree's name as if it ended in
	// "/".
	key := func(k string) string {
		if n.kids[k].kids != nil {
			return k + "/"
		}
		return k
	}
	sort.Slice(names, func(i, j int) bool { return key(names[i]) < key(names[j]) })
	var body bytes.Buffer
	for _, k := range names {
		kid := n.kids[k]
		mode := kid.mode
		if kid.kids != nil {
			mode = "40000"
		}
		body.WriteString(mode + " " + k + "\x00")
		body.Write(kid.hash())
	}
	h := sha1.New()
	fmt.Fprintf(h, "tree %d\x00", body.Len())
	h.Write(body.Bytes())
	return h.Sum(nil)
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

// treeSize is the bytes of every file under path.
func treeSize(path string) int64 {
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// human is a size in powers of 1024, labelled as such: GiB, not GB, which
// is a power of 1000 and what Finder counts in.
func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Human is a size as the plan prints it.
func Human(n int64) string { return human(n) }
