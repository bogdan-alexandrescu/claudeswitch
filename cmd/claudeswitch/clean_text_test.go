package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/clean"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// clean runs printed "20261025-160212  572 removed, 1.4 GB": no header, no
// indent, "removed" for what went to the Trash, and no word of undo.
func TestCleanRunsSaysWhatWentWhereAndHowToUndoIt(t *testing.T) {
	repo, opt := cleanRepo(t)
	mergedWorktree(t, repo, "x")
	t.Setenv("NO_COLOR", "1")
	render.SetColor(false)
	var out bytes.Buffer
	if err := runClean(context.Background(), &out, opt, "", true, false, true); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cleanRuns(&out, opt, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "removed") || !strings.Contains(strings.ToLower(text), "moved to the trash") {
		t.Errorf("a run moves things to the Trash:\n%s", text)
	}
	runs, err := clean.Runs(opt)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %v, %v", runs, err)
	}
	if !strings.Contains(text, "WHEN") || !strings.Contains(text, "  "+runs[0].ID) ||
		!strings.Contains(text, render.When(runs[0].At, time.Now())) {
		t.Errorf("a run is listed under a header, indented, with when it ran:\n%s", text)
	}
	if !strings.Contains(text, "claudeswitch clean undo") {
		t.Errorf("the list must say how to put a run back:\n%s", text)
	}
	if strings.Contains(text, "1 items") || !strings.Contains(text, "1 item") {
		t.Errorf("one item is one item:\n%s", text)
	}
}

// One worktree moved is "1 worktree", not "1 worktrees", in the run's line
// and in the audit's.
func TestCleanCountsInWords(t *testing.T) {
	run := &clean.Run{ID: "20261025-160212", At: time.Now(), Done: []clean.Entry{
		{Item: clean.Item{Kind: clean.KindWorktree, Path: "/w/a", Bytes: 2 << 20, Remove: true}},
		{Item: clean.Item{Kind: clean.KindPlugin, Path: "/p/a", Remove: true}},
	}}
	var out bytes.Buffer
	printCleanRun(&out, run)
	for _, s := range []string{out.String(), cleanSummary(run)} {
		if strings.Contains(s, "1 worktrees") || strings.Contains(s, "1 old plugin versions") ||
			!strings.Contains(s, "1 worktree") || !strings.Contains(s, "1 old plugin version") {
			t.Errorf("counts in words:\n%s", s)
		}
	}
}

// Sizes are counted in powers of 1024 and were labelled GB and MB, which
// are powers of 1000: the labels say what the numbers are.
func TestCleanSizesAreLabelledTruthfully(t *testing.T) {
	for n, want := range map[int64]string{1536 << 20: "1.5 GiB", 300 << 20: "300 MiB", 4 << 10: "4 KiB", 12: "12 B"} {
		if got := clean.Human(n); got != want {
			t.Errorf("Human(%d) = %q, want %q", n, got, want)
		}
	}
}

// A saved plan said when it was made as "Jan 2 15:04", a format of its own.
func TestCleanPlanDatesAsTheRestDo(t *testing.T) {
	made := time.Now().AddDate(0, 0, -10)
	var out bytes.Buffer
	printCleanPlan(&out, &clean.Plan{At: made}, clean.Options{}, "/tmp/plan.json", false)
	if !strings.Contains(out.String(), made.Format("2 Jan 15:04")) {
		t.Errorf("the plan's date reads as the rest of the CLI's:\n%s", out.String())
	}
}
