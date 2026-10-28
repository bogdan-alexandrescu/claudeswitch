package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
func cleanGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// cleanRepo is a repository at root/proj with one commit on main, and the
// options a scan of it uses, judged far enough ahead that nothing is recent.
func cleanRepo(t *testing.T) (string, clean.Options) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "proj")
	cleanGit(t, root, "init", "-q", "-b", "main", repo)
	os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644)
	cleanGit(t, repo, "add", "a")
	cleanGit(t, repo, "commit", "-q", "-m", "a")
	return repo, clean.Options{Now: time.Now().Add(200 * time.Hour), After: time.Hour, Projects: []string{repo},
		Worktrees: true, StateDir: filepath.Join(root, "state"), TrashDir: filepath.Join(root, "Trash")}
}

// mergedWorktree adds a worktree on lane/<name> with one commit and merges
// it into main.
func mergedWorktree(t *testing.T, repo, name string) string {
	wt := filepath.Join(repo, ".claude", "worktrees", "agent-"+name)
	cleanGit(t, repo, "worktree", "add", "-q", "-b", "lane/"+name, wt)
	os.WriteFile(filepath.Join(wt, name), []byte(name), 0o644)
	cleanGit(t, wt, "add", name)
	cleanGit(t, wt, "commit", "-q", "-m", name)
	cleanGit(t, repo, "merge", "-q", "--no-ff", "-m", "m", "lane/"+name)
	return wt
}

func TestCleanPlanApplyAndUndoAsJSON(t *testing.T) {
	repo, opt := cleanRepo(t)
	wt := mergedWorktree(t, repo, "x")
	var out bytes.Buffer
	if err := runClean(context.Background(), &out, opt, "", false, false, true); err != nil {
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
	if err := runClean(context.Background(), &out, opt, "", true, false, true); err != nil {
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

// --plan shows and applies the plan that was saved, not a new one: what
// became removable after it was made stays (owner decision 2026-10-25: the
// first run removes what Alice read, nothing more).
func TestCleanApplyActsOnTheSavedPlanOnly(t *testing.T) {
	repo, opt := cleanRepo(t)
	x := mergedWorktree(t, repo, "x")
	var out bytes.Buffer
	if err := runClean(context.Background(), &out, opt, "", false, false, true); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(saved, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	y := mergedWorktree(t, repo, "y")

	out.Reset()
	if err := runClean(context.Background(), &out, opt, saved, false, false, false); err != nil {
		t.Fatal(err)
	}
	if text := out.String(); !strings.Contains(text, "agent-x") || strings.Contains(text, "agent-y") ||
		!strings.Contains(text, "claudeswitch clean --apply --plan "+saved) {
		t.Fatalf("the saved plan printed as:\n%s", text)
	}

	out.Reset()
	if err := runClean(context.Background(), &out, opt, saved, true, false, true); err != nil {
		t.Fatal(err)
	}
	var applied struct {
		Run clean.Run `json:"run"`
	}
	if err := json.Unmarshal(out.Bytes(), &applied); err != nil || len(applied.Run.Done) != 1 {
		t.Fatalf("apply %s (%v)", out.String(), err)
	}
	if _, err := os.Stat(x); err == nil {
		t.Fatal("the planned worktree is still there")
	}
	if _, err := os.Stat(y); err != nil {
		t.Fatal("removed a worktree the saved plan did not list")
	}
}

// A file that holds no plan is refused before anything is judged.
func TestCleanRefusesAPlanFileThatIsNotOne(t *testing.T) {
	_, opt := cleanRepo(t)
	bad := filepath.Join(t.TempDir(), "plan.json")
	os.WriteFile(bad, []byte(`{"runs": []}`), 0o600)
	for _, path := range []string{bad, filepath.Join(t.TempDir(), "missing.json")} {
		err := runClean(context.Background(), io.Discard, opt, path, true, false, true)
		var ae *appError
		if !errors.As(err, &ae) || ae.Code != codeUsage {
			t.Fatalf("%s: %v, want a usage error", path, err)
		}
	}
}

// On a Mac the scan never looks in the folders macOS guards, and the plan
// says which projects it left alone.
func TestCleanLeavesTheFoldersMacOSGuards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setCCDir(t, "")
	cfg, err := config.Load(writeConfig(t, "priority = [\"a\"]\n\n[[account]]\nid = \"a\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	opt, err := cleanOptions(cfg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		if len(opt.Guarded) == 0 || opt.Guarded[0] != filepath.Join(home, "Desktop") {
			t.Fatalf("guarded %v", opt.Guarded)
		}
	} else if len(opt.Guarded) != 0 {
		t.Fatalf("guarded %v off macOS", opt.Guarded)
	}
	var out bytes.Buffer
	printCleanPlan(&out, &clean.Plan{Guarded: []string{filepath.Join(home, "Desktop", "taxes")}}, opt, "", false)
	if !strings.Contains(out.String(), "not looked in (macOS asks first): ~/Desktop/taxes") {
		t.Fatalf("plan said:\n%s", out.String())
	}
}
