package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/clean"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// cs clean finds the repositories each profile's Claude Code has run in
// from its .claude.json, and reads the kinds and the window from the
// clean_* settings.
func TestCleanOptionsComeFromTheProfilesAndTheSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setCCDir(t, "")
	b, _ := json.Marshal(map[string]any{"projects": map[string]any{"/w/b": map[string]any{}, "/w/a": map[string]any{}}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfig(t, "priority = [\"a\"]\nclean_after = \"12h\"\nclean_clones = false\n"+
		"clean_remote_branches = true\nclean_keep = [\"*-rel\"]\nclean_roots = [\"~/src\"]\n\n[[account]]\nid = \"a\"\n")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := cleanOptions(cfg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(opt.Projects, ",") != "/w/a,/w/b" {
		t.Fatalf("projects %v", opt.Projects)
	}
	if len(opt.ConfigDirs) != 1 || opt.ConfigDirs[0] != filepath.Join(home, ".claude") {
		t.Fatalf("config dirs %v", opt.ConfigDirs)
	}
	if opt.After != 12*time.Hour || !opt.Worktrees || opt.Clones || !opt.Temp || !opt.Remote {
		t.Fatalf("settings not read: %+v", opt)
	}
	if opt.Roots[0] != filepath.Join(home, "src") || opt.Keep[0] != "*-rel" {
		t.Fatalf("roots %v keep %v", opt.Roots, opt.Keep)
	}
	if opt.StateDir != filepath.Join(home, ".local", "state", "claudeswitch", "clean") {
		t.Fatalf("state dir %s", opt.StateDir)
	}
}

// The plan, --apply and undo, end to end on a repository with one merged
// worktree, as JSON for the app.
func TestCleanPlanApplyAndUndoAsJSON(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	repo := filepath.Join(root, "proj")
	run(root, "init", "-q", "-b", "main", repo)
	os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644)
	run(repo, "add", "a")
	run(repo, "commit", "-q", "-m", "a")
	wt := filepath.Join(repo, ".claude", "worktrees", "agent-x")
	run(repo, "worktree", "add", "-q", "-b", "lane/x", wt)
	os.WriteFile(filepath.Join(wt, "x"), []byte("x"), 0o644)
	run(wt, "add", "x")
	run(wt, "commit", "-q", "-m", "x")
	run(repo, "merge", "-q", "--no-ff", "-m", "m", "lane/x")

	opt := clean.Options{Now: time.Now().Add(200 * time.Hour), After: time.Hour, Projects: []string{repo},
		Worktrees: true, StateDir: filepath.Join(root, "state"), TrashDir: filepath.Join(root, "Trash")}
	var out bytes.Buffer
	if err := runClean(context.Background(), &out, opt, false, false, true); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Plan  clean.Plan      `json:"plan"`
		Kinds map[string]bool `json:"kinds"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(plan.Plan.Removable()) != 1 || !plan.Kinds["worktrees"] {
		t.Fatalf("plan %s", out.String())
	}
	out.Reset()
	if err := runClean(context.Background(), &out, opt, true, false, true); err != nil {
		t.Fatal(err)
	}
	var applied struct {
		Run clean.Run `json:"run"`
	}
	if err := json.Unmarshal(out.Bytes(), &applied); err != nil || len(applied.Run.Done) != 1 {
		t.Fatalf("apply %s (%v)", out.String(), err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("still there")
	}
	out.Reset()
	if err := cleanUndo(context.Background(), &out, opt, "", "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 of 1 put back") {
		t.Fatalf("undo said %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(wt, "x")); err != nil {
		t.Fatal("not put back")
	}
}
