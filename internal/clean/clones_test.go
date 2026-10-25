package clean

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A lane clone is a second clone of a project's repository beside it. It is
// removed whole, to the Trash, only when nothing in it is unique: every
// branch merged, no stash, no tag or other ref the primary clone lacks, and
// no worktree of its own. The primary, the clone named after its
// repository, is never removed.

func (w *world) clone(name string) string {
	w.t.Helper()
	path := filepath.Join(w.root, name)
	w.git(w.root, "clone", "-q", w.origin, path)
	w.git(path, "remote", "set-head", "origin", "main")
	return path
}

func cloneWorld(t *testing.T) *world {
	w := newWorld(t)
	// The primary clone is named after the repository: rename proj to
	// origin's name.
	primary := filepath.Join(w.root, "origin")
	if err := os.Rename(w.repo, primary); err != nil {
		t.Fatal(err)
	}
	w.repo = primary
	w.opt.Projects = []string{primary}
	w.opt.Clones = true
	return w
}

func TestALaneCloneWithEverythingMergedIsRemoved(t *testing.T) {
	w := cloneWorld(t)
	lane := w.clone("origin-integrate-a")
	w.git(lane, "checkout", "-q", "-b", "integrate/a")
	w.commit(lane, "a.txt", "a\n", "a")
	w.git(lane, "push", "-q", "origin", "integrate/a")
	w.git(w.repo, "fetch", "-q", "origin")
	w.git(w.repo, "merge", "-q", "--no-ff", "-m", "merge a", "origin/integrate/a")
	w.git(w.repo, "push", "-q", "origin", "main")
	w.git(lane, "fetch", "-q", "origin")
	p := w.scan()
	mustRemove(t, w.item(p, KindClone, lane))
	for _, it := range p.Items {
		if it.Kind == KindClone && it.Path == w.repo {
			t.Fatalf("the primary clone is a candidate: %+v", it)
		}
	}
	if _, err := Apply(context.Background(), p, w.opt); err != nil {
		t.Fatal(err)
	}
	if exists(lane) {
		t.Fatal("the clone is still there")
	}
}

func TestALaneCloneWithAnythingUniqueIsKept(t *testing.T) {
	w := cloneWorld(t)
	unmerged := w.clone("origin-wt")
	w.git(unmerged, "checkout", "-q", "-b", "fix/only-here")
	w.commit(unmerged, "f.txt", "f\n", "only here")

	stash := w.clone("origin-stash")
	w.write(filepath.Join(stash, "README.md"), "changed\n")
	w.git(stash, "stash", "-q")

	tagged := w.clone("origin-tag")
	w.git(tagged, "tag", "v-local")

	rescued := w.clone("origin-rescue")
	w.git(rescued, "update-ref", "refs/rescue/x", w.git(rescued, "rev-parse", "HEAD"))

	withWT := w.clone("origin-withwt")
	w.git(withWT, "worktree", "add", "-q", "-b", "side", filepath.Join(w.root, "side-wt"))

	p := w.scan()
	mustKeep(t, w.item(p, KindClone, unmerged), "not in main")
	mustKeep(t, w.item(p, KindClone, stash), "stash")
	mustKeep(t, w.item(p, KindClone, tagged), "tag")
	mustKeep(t, w.item(p, KindClone, rescued), "refs/rescue/x")
	mustKeep(t, w.item(p, KindClone, withWT), "worktree")
}

func TestClonesAreLeftAloneWithTheKindOff(t *testing.T) {
	w := cloneWorld(t)
	lane := w.clone("origin-integrate-b")
	w.opt.Clones = false
	for _, it := range w.scan().Items {
		if it.Kind == KindClone && it.Path == lane && it.Remove {
			t.Fatalf("removed with clones off: %+v", it)
		}
	}
}

// Claude's temp files: an ended session's scratch folder, plugin versions no
// session can be using, and subagent transcripts past their age.
func TestTempFilesOfEndedSessionsAreRemovedAndALiveOnesAreKept(t *testing.T) {
	w := newWorld(t)
	w.opt.Worktrees = false
	w.opt.Temp = true
	w.opt.TranscriptsAfter = 30 * 24 * time.Hour
	w.opt.TempRoot = filepath.Join(w.root, "tmp-claude")
	old := w.opt.Now.Add(-40 * 24 * time.Hour)

	ended := filepath.Join(w.opt.TempRoot, "-proj", "s-ended")
	live := filepath.Join(w.opt.TempRoot, "-proj", "s-live")
	for _, d := range []string{ended, live} {
		w.write(filepath.Join(d, "scratchpad", "f"), "x\n")
		touch(t, filepath.Join(d, "scratchpad", "f"), old)
	}
	sess, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "s-live", "cwd": w.repo,
		"startedAt": w.opt.Now.Add(-2 * time.Hour).UnixMilli()})
	w.write(filepath.Join(w.cfg, "sessions", fmt.Sprint(os.Getpid())+".json"), string(sess))

	// Plugin versions: 1.0 is old, 1.1 was current when the live session
	// started, 1.2 is installed now.
	cache := filepath.Join(w.cfg, "plugins", "cache", "mkt", "p")
	for v, ago := range map[string]time.Duration{"1.0": 5 * 24 * time.Hour, "1.1": 4 * 24 * time.Hour,
		"1.2": time.Hour} { // 1.2 came after the session started
		w.write(filepath.Join(cache, v, "plugin.json"), "{}\n")
		touch(t, filepath.Join(cache, v), w.opt.Now.Add(-ago))
	}
	installed, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{
		"p@mkt": []map[string]any{{"scope": "user", "installPath": filepath.Join(cache, "1.2"), "version": "1.2"}}}})
	w.write(filepath.Join(w.cfg, "plugins", "installed_plugins.json"), string(installed))

	subOld := filepath.Join(w.cfg, "projects", "-proj", "s-old", "subagents")
	subNew := filepath.Join(w.cfg, "projects", "-proj", "s-new", "subagents")
	w.write(filepath.Join(subOld, "agent-1.jsonl"), "{}\n")
	touch(t, filepath.Join(subOld, "agent-1.jsonl"), old)
	w.write(filepath.Join(subNew, "agent-2.jsonl"), "{}\n")
	touch(t, filepath.Join(subNew, "agent-2.jsonl"), w.opt.Now.Add(-24*time.Hour))

	p := w.scan()
	mustRemove(t, w.item(p, KindScratch, ended))
	mustKeep(t, w.item(p, KindScratch, live), "session")
	mustRemove(t, w.item(p, KindPlugin, filepath.Join(cache, "1.0")))
	mustKeep(t, w.item(p, KindPlugin, filepath.Join(cache, "1.1")), "session")
	mustKeep(t, w.item(p, KindPlugin, filepath.Join(cache, "1.2")), "installed")
	mustRemove(t, w.item(p, KindTranscript, subOld))
	mustKeep(t, w.item(p, KindTranscript, subNew), "changed")
	if _, err := Apply(context.Background(), p, w.opt); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{ended, filepath.Join(cache, "1.0"), subOld} {
		if exists(gone) {
			t.Fatalf("%s is still there", gone)
		}
	}
	if !exists(filepath.Join(w.cfg, "projects", "-proj", "s-old")) {
		t.Fatal("the session folder went too; only its subagents/ should")
	}
}

// Merged branches on GitHub, only with their own setting on: a branch whose
// pull request merged with exactly its tip, that no open pull request
// targets.
func TestARemoteBranchIsDeletedOnlyWhenOnAndMergedExactly(t *testing.T) {
	w := newWorld(t)
	for _, b := range []string{"lane/r1", "lane/r2", "lane/r3"} {
		w.git(w.repo, "checkout", "-q", "-b", b, "main")
		w.commit(w.repo, b[5:]+".txt", b+"\n", b)
		w.git(w.repo, "push", "-q", "origin", b)
		w.git(w.repo, "checkout", "-q", "main")
		w.squash(b, len(w.prs)+10)
	}
	// r2 got a commit pushed after its merge; r3 is the base of an open PR.
	w.git(w.repo, "checkout", "-q", "lane/r2")
	w.commit(w.repo, "r2-later.txt", "later\n", "after the merge")
	w.git(w.repo, "push", "-q", "origin", "lane/r2")
	w.git(w.repo, "checkout", "-q", "main")
	w.prs = append(w.prs, PR{Number: 99, Head: "lane/r4", Base: "lane/r3", State: "OPEN"})
	w.git(w.repo, "fetch", "-q", "origin")

	ref := func(p *Plan, b string) (Item, bool) {
		for _, it := range p.Items {
			if it.Kind == KindRemote && it.Branch == b {
				return it, true
			}
		}
		return Item{}, false
	}
	if it, ok := ref(w.scan(), "lane/r1"); ok && it.Remove {
		t.Fatal("a remote branch would be deleted with the setting off")
	}
	w.opt.Remote = true
	p := w.scan()
	r1, ok := ref(p, "lane/r1")
	if !ok {
		t.Fatal("no remote item for lane/r1")
	}
	mustRemove(t, r1)
	if r2, _ := ref(p, "lane/r2"); r2.Remove {
		t.Fatal("lane/r2 has a commit after its merge")
	}
	if r3, _ := ref(p, "lane/r3"); r3.Remove || !strings.Contains(r3.Why, "#99") {
		t.Fatalf("lane/r3 is the base of #99: %+v", r3)
	}
	run, err := Apply(context.Background(), &Plan{Items: []Item{r1}}, w.opt)
	if err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin, "branch", "--list", "lane/r1"); out != "" {
		t.Fatalf("still on origin: %q", out)
	}
	if _, err := Undo(context.Background(), w.opt, run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if out := w.git(w.origin, "branch", "--list", "lane/r1"); out == "" {
		t.Fatal("undo did not push it back")
	}
}
