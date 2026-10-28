package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/ccdir"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/clean"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// `cs clean` (owner decisions 2026-10-25, IMPROVEMENTS "Cleaning up after
// Claude"): a plan of what Claude left behind that is merged, unused and
// unchanged; --apply moves it to the Trash with a journal; undo puts a run
// back. The kinds and the window are the clean_* settings; clean itself
// only turns on the daemon's runs.

const cleanUsage = "usage: claudeswitch clean [--apply] [--plan <file>] [--all] [--json] | " +
	"clean undo [<run>] [--only <text>] [--json] | clean runs [--json]"

func cmdClean(args []string) error {
	fs := appFlags("clean")
	cfgPath := fs.String("config", "", "path to config.toml")
	asJSON := fs.Bool("json", false, "machine-readable output")
	apply := fs.Bool("apply", false, "move what the plan marks to the Trash")
	all := fs.Bool("all", false, "also list what is kept, and why")
	from := fs.String("plan", "", "a plan saved from clean --json (- for stdin): show it, or with --apply act on it alone")
	only := fs.String("only", "", "undo: only entries whose path or branch contains this")
	positional, err := parseApp(fs, args, cleanUsage)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil && cfg == nil {
		return err
	}
	opt, err := cleanOptions(cfg, time.Now())
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch {
	case len(positional) == 0:
		return runClean(ctx, os.Stdout, opt, *from, *apply, *all, *asJSON)
	case positional[0] == "runs" && len(positional) == 1:
		return cleanRuns(os.Stdout, opt, *asJSON)
	case positional[0] == "undo" && len(positional) <= 2:
		id := ""
		if len(positional) == 2 {
			id = positional[1]
		}
		return cleanUndo(ctx, os.Stdout, opt, id, *only, *asJSON)
	}
	return appErr(codeUsage, cleanUsage, "unknown clean command %q", strings.Join(positional, " "))
}

// cleanOptions is what a scan looks at, from the config: every profile's
// Claude Code folders and the projects its .claude.json lists, Claude
// Code's temp folder, and the clean_* settings.
func cleanOptions(cfg *config.Config, now time.Time) (clean.Options, error) {
	opt := clean.Options{Now: now, After: cfg.CleanWindow(), Keep: cfg.CleanKeep,
		TranscriptsAfter: cfg.CleanTranscripts(),
		StateDir:         filepath.Join(filepath.Dir(state.DefaultPath()), "clean")}
	opt.Worktrees, opt.Clones, opt.Temp, opt.Remote = cfg.CleanKinds()
	for _, r := range cfg.CleanRoots {
		if d, err := ccdir.ExpandHome(r); err == nil {
			opt.Roots = append(opt.Roots, d)
		}
	}
	seen := map[string]bool{}
	for _, p := range cfg.EffectiveProfiles() {
		paths, err := ccdir.For(p.Dir)
		if err != nil || seen[paths.Dir] {
			continue
		}
		seen[paths.Dir] = true
		opt.ConfigDirs = append(opt.ConfigDirs, paths.Dir)
		opt.Projects = append(opt.Projects, claudeProjects(paths.GlobalConfig)...)
	}
	if d, err := filepath.EvalSymlinks(filepath.Join("/tmp", fmt.Sprintf("claude-%d", os.Getuid()))); err == nil {
		opt.TempRoot = d
	}
	// macOS asks before anything reads these, and the daemon cannot answer
	// (owner decision 2026-10-26: a prompt held its first run).
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS == "darwin" {
		opt.Guarded = clean.MacGuarded(home)
	}
	return opt, nil
}

// claudeProjects are the folders a .claude.json says Claude Code has run in.
func claudeProjects(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	out := make([]string, 0, len(f.Projects))
	for p := range f.Projects {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func cleanErr(err error) error {
	if errors.Is(err, clean.ErrBusy) {
		return appErr(codeCleanRunning, "wait for it to finish, then run this again", "another cleanup is running")
	}
	return err
}

// runClean makes a plan, or reads the one saved at from, and shows it or
// applies it. A saved plan is applied as it was read: each item judged once
// more, and nothing it does not list, so what goes is what was looked at.
func runClean(ctx context.Context, w io.Writer, opt clean.Options, from string, apply, all, asJSON bool) error {
	var plan *clean.Plan
	var err error
	if from != "" {
		plan, err = readCleanPlan(from)
	} else {
		plan, err = clean.Scan(ctx, opt)
	}
	if err != nil {
		return err
	}
	if !apply {
		if asJSON {
			return emitTo(w, map[string]any{"plan": plan, "kinds": cleanKinds(opt)})
		}
		printCleanPlan(w, plan, opt, from, all)
		return nil
	}
	run, err := clean.Apply(ctx, plan, opt)
	if err != nil {
		return cleanErr(err)
	}
	if asJSON {
		return emitTo(w, map[string]any{"run": run, "bytes": run.Bytes()})
	}
	printCleanRun(w, run)
	return nil
}

// readCleanPlan reads a plan as clean --json prints it ({"plan": …}), or a
// bare one; "-" is stdin.
func readCleanPlan(path string) (*clean.Plan, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, appErr(codeUsage, "save one with: claudeswitch clean --json > <file>", "cannot read the plan: %v", err)
	}
	var f struct {
		Plan  *clean.Plan  `json:"plan"`
		At    time.Time    `json:"at"`
		Items []clean.Item `json:"items"`
	}
	if err := json.Unmarshal(b, &f); err != nil || (f.Plan == nil && f.At.IsZero()) {
		return nil, appErr(codeUsage, "save one with: claudeswitch clean --json > <file>", "%s holds no cleanup plan", path)
	}
	if f.Plan != nil {
		return f.Plan, nil
	}
	return &clean.Plan{At: f.At, Items: f.Items}, nil
}

func cleanKinds(opt clean.Options) map[string]bool {
	return map[string]bool{"worktrees": opt.Worktrees, "clones": opt.Clones, "temp": opt.Temp,
		"remote_branches": opt.Remote}
}

// tilde shows a path under the home folder as ~/….
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

var kindTitle = map[clean.Kind]string{
	clean.KindWorktree: "worktrees", clean.KindPrune: "records of worktrees whose folder is gone",
	clean.KindBranch: "local branches", clean.KindRemote: "branches on GitHub", clean.KindClone: "lane clones",
	clean.KindScratch: "ended sessions' scratch", clean.KindPlugin: "old plugin versions",
	clean.KindTranscript: "old subagent transcripts",
}

// kindNoun is a kind's noun for a count, singular then plural: "1
// worktree", "3 worktrees".
var kindNoun = map[clean.Kind][2]string{
	clean.KindWorktree: {"worktree", "worktrees"},
	clean.KindPrune:    {"record of a worktree whose folder is gone", "records of worktrees whose folder is gone"},
	clean.KindBranch:   {"local branch", "local branches"}, clean.KindRemote: {"branch on GitHub", "branches on GitHub"},
	clean.KindClone: {"lane clone", "lane clones"}, clean.KindScratch: {"ended session's scratch", "ended sessions' scratch"},
	clean.KindPlugin:     {"old plugin version", "old plugin versions"},
	clean.KindTranscript: {"old subagent transcript", "old subagent transcripts"},
}

// kindCount is n of a kind in words.
func kindCount(k clean.Kind, n int) string {
	return render.Plural(n, kindNoun[k][0], kindNoun[k][1])
}

var kindOrderCLI = []clean.Kind{clean.KindWorktree, clean.KindPrune, clean.KindBranch, clean.KindRemote,
	clean.KindClone, clean.KindScratch, clean.KindPlugin, clean.KindTranscript}

// grouped kinds list a line per repository or project unless --all.
var grouped = map[clean.Kind]bool{clean.KindBranch: true, clean.KindRemote: true, clean.KindTranscript: true}

func printCleanPlan(w io.Writer, p *clean.Plan, opt clean.Options, from string, all bool) {
	if from == "" {
		fmt.Fprintln(w, "claudeswitch clean: what would go to the Trash (nothing moves without --apply)")
	} else {
		fmt.Fprintf(w, "claudeswitch clean: the plan in %s, made %s (nothing moves without --apply)\n", tilde(from),
			render.When(p.At, time.Now()))
	}
	var total int64
	var kept []clean.Item
	byKind := map[clean.Kind][]clean.Item{}
	for _, it := range p.Items {
		if it.Remove {
			byKind[it.Kind] = append(byKind[it.Kind], it)
			total += it.Bytes
		} else {
			kept = append(kept, it)
		}
	}
	any := false
	for _, k := range kindOrderCLI {
		items := byKind[k]
		if len(items) == 0 {
			continue
		}
		any = true
		var bytes int64
		for _, it := range items {
			bytes += it.Bytes
		}
		head := fmt.Sprintf("\n%s (%d", kindTitle[k], len(items))
		if bytes > 0 {
			head += ", " + clean.Human(bytes)
		}
		fmt.Fprintln(w, head+")")
		if grouped[k] && !all {
			counts := map[string]int{}
			var keys []string
			for _, it := range items {
				key := it.Repo
				if key == "" {
					key = filepath.Dir(filepath.Dir(it.Path))
				}
				if counts[key] == 0 {
					keys = append(keys, key)
				}
				counts[key]++
			}
			for _, key := range keys {
				fmt.Fprintf(w, "  %-60s %d\n", tilde(key), counts[key])
			}
			continue
		}
		for _, it := range items {
			fmt.Fprintf(w, "  %s\n", itemLine(it))
		}
	}
	if !any {
		fmt.Fprintln(w, "\nnothing to remove")
	}
	var off []string
	for name, on := range cleanKinds(opt) {
		if !on {
			off = append(off, "clean_"+name)
		}
	}
	sort.Strings(off)
	fmt.Fprintf(w, "\n%d kept", len(kept))
	if !all && len(kept) > 0 {
		fmt.Fprint(w, " (claudeswitch clean --all says why, one by one)")
	}
	fmt.Fprintln(w)
	if all {
		for _, it := range kept {
			fmt.Fprintf(w, "  keep %s\n", itemLine(it))
		}
	}
	if len(off) > 0 {
		fmt.Fprintf(w, "off: %s\n", strings.Join(off, ", "))
	}
	if len(p.Guarded) > 0 {
		names := make([]string, len(p.Guarded))
		for i, g := range p.Guarded {
			names[i] = tilde(g)
		}
		fmt.Fprintf(w, "not looked in (macOS asks first): %s\n", strings.Join(names, ", "))
	}
	switch {
	case any && from != "":
		fmt.Fprintf(w, "claudeswitch clean --apply --plan %s moves %s to the Trash, each item judged once more; "+
			"claudeswitch clean undo puts a run back\n", from, clean.Human(total))
	case any:
		fmt.Fprintf(w, "claudeswitch clean --apply moves %s to the Trash; claudeswitch clean undo puts a run back\n",
			clean.Human(total))
	}
}

func itemLine(it clean.Item) string {
	where := tilde(it.Path)
	if it.Kind == clean.KindBranch || it.Kind == clean.KindRemote {
		where = tilde(it.Repo) + ": " + it.Branch
	} else if it.Branch != "" {
		where += " [" + it.Branch + "]"
	}
	line := fmt.Sprintf("%-8s %s — %s", it.Kind, where, it.Why)
	if it.Remove && it.Bytes > 0 {
		line += " (" + clean.Human(it.Bytes) + ")"
	}
	return line
}

func printCleanRun(w io.Writer, run *clean.Run) {
	counts := map[clean.Kind]int{}
	for _, e := range run.Done {
		counts[e.Kind]++
	}
	var parts []string
	for _, k := range kindOrderCLI {
		if n := counts[k]; n > 0 {
			parts = append(parts, kindCount(k, n))
		}
	}
	if len(parts) == 0 {
		fmt.Fprintf(w, "claudeswitch clean: run %s moved nothing\n", run.ID)
	} else {
		fmt.Fprintf(w, "claudeswitch clean: run %s moved %s to the Trash: %s\n", run.ID, clean.Human(run.Bytes()),
			strings.Join(parts, ", "))
		fmt.Fprintln(w, "  deleted branches' tips are kept as refs/archive/<branch>")
	}
	for _, it := range run.Skipped {
		fmt.Fprintf(w, "  skipped %s\n", itemLine(it))
	}
	for _, f := range run.Failed {
		fmt.Fprintf(w, "  failed  %s %s: %s\n", f.Kind, tilde(f.Path), f.Err)
	}
	if len(run.Done) > 0 {
		fmt.Fprintf(w, "claudeswitch clean undo %s puts it back\n", run.ID)
	}
}

func cleanRuns(w io.Writer, opt clean.Options, asJSON bool) error {
	runs, err := clean.Runs(opt)
	if err != nil {
		return err
	}
	if asJSON {
		type summary struct {
			ID      string    `json:"id"`
			At      time.Time `json:"at"`
			Removed int       `json:"removed"`
			Undone  int       `json:"undone"`
			Bytes   int64     `json:"bytes"`
		}
		out := []summary{}
		for _, r := range runs {
			s := summary{ID: r.ID, At: r.At, Bytes: r.Bytes()}
			for _, e := range r.Done {
				s.Removed++
				if e.Undone {
					s.Undone++
				}
			}
			out = append(out, s)
		}
		return emitTo(w, map[string]any{"runs": out})
	}
	if len(runs) == 0 {
		fmt.Fprint(w, "\n  no cleanup has run\n\n")
		return nil
	}
	// The newest first, each with when it ran; the id is what undo takes.
	now := time.Now()
	t := render.NewTable([]string{"WHEN", "RUN", "MOVED TO THE TRASH", "PUT BACK"})
	for _, r := range runs {
		undone := 0
		for _, e := range r.Done {
			if e.Undone {
				undone++
			}
		}
		moved := render.Plural(len(r.Done), "item", "items") + ", " + clean.Human(r.Bytes())
		back := "-"
		if undone > 0 {
			back = fmt.Sprintf("%d", undone)
		}
		t.Add(render.Grey(render.When(r.At, now)), r.ID, moved, back)
	}
	fmt.Fprintf(w, "\n%s\n  claudeswitch clean undo <run> puts a run back (with no run named, the newest)\n\n",
		t.Render("  "))
	return nil
}

func cleanUndo(ctx context.Context, w io.Writer, opt clean.Options, id, only string, asJSON bool) error {
	if id == "" {
		runs, err := clean.Runs(opt)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return appErr(codeNotFound, "", "no cleanup has run")
		}
		id = runs[0].ID
	}
	run, err := clean.Undo(ctx, opt, id, only)
	if run == nil {
		if errors.Is(err, clean.ErrBusy) {
			return cleanErr(err)
		}
		return appErr(codeNotFound, "claudeswitch clean runs lists them", "%v", err)
	}
	if asJSON {
		if err != nil {
			return appErr(codeFailed, "", "%v", err)
		}
		return emitTo(w, map[string]any{"run": run})
	}
	n := 0
	for _, e := range run.Done {
		if e.Undone {
			n++
		}
	}
	fmt.Fprintf(w, "claudeswitch clean: run %s: %d of %d put back\n", run.ID, n, len(run.Done))
	return err
}
