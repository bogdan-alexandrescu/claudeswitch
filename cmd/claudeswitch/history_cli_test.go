package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// refusalLine is a transcript record of a refusal, as Claude Code writes
// one per retry: its own time, the window, and when the window resets.
func refusalLine(at time.Time, kind string, resets time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"<synthetic>"},`+
		`"quotaLimits":{"status":"rejected","resetsAt":%d,"rateLimitType":%q}}`,
		at.UTC().Format(time.RFC3339Nano), resets.Unix(), kind)
}

func writeTranscript(t *testing.T, dir string, lines ...string) {
	t.Helper()
	proj := filepath.Join(dir, "projects", "-Users-someone-src")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// history's rows: WHEN is the refusal's own time (it was the reset's day),
// the window is named for each kind (anything not seven_day read 5-hour),
// the account comes from the daemon's record of the refusal, and every
// profile's transcripts are read, not only this shell's, saying which.
func TestHistoryRowsSayWhenWhatAndWhose(t *testing.T) {
	path := appWorld(t, twoProfileTOML)
	t.Setenv("COLUMNS", "120")
	t.Setenv("NO_COLOR", "1")
	render.SetColor(false)
	home := os.Getenv("HOME")
	now := time.Now().Truncate(time.Second)
	t1, r1 := now.Add(-26*time.Hour), now.Add(-23*time.Hour)
	t2, r2 := now.Add(-3*time.Hour), now.Add(40*time.Hour)
	t3, r3 := now.Add(-2*time.Hour), now.Add(50*time.Hour)
	writeTranscript(t, filepath.Join(home, ".claude"),
		refusalLine(t1, "five_hour", r1), refusalLine(t1.Add(time.Minute), "five_hour", r1))
	writeTranscript(t, filepath.Join(home, ".claude-work"),
		refusalLine(t2, "seven_day", r2), refusalLine(t3, "seven_day_opus", r3))
	log, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Write(audit.Event{At: t2, Kind: "rejection", Profile: "work", From: "w1", Window: "seven_day", ResetsAt: r2})

	out := captureStdout(t, func() error { return cmdHistory([]string{"--config", path, "--days", "3"}) })
	rows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for k, at := range map[string]time.Time{"t1": t1, "t2": t2, "t3": t3} {
			if strings.Contains(line, render.When(at, now)) {
				rows[k] = line
			}
		}
	}
	if len(rows) != 3 {
		t.Fatalf("each refusal's row must be dated by the refusal itself:\n%s", out)
	}
	if !strings.Contains(rows["t1"], "session") || !strings.Contains(rows["t1"], render.When(r1, now)) {
		t.Errorf("the session refusal's row, with when it cleared:\n%s", out)
	}
	if !strings.Contains(rows["t2"], "week") || !strings.Contains(rows["t2"], "w1") {
		t.Errorf("the weekly refusal's row must name the week and the account refused:\n%s", out)
	}
	if !strings.Contains(rows["t3"], "seven_day_opus") || strings.Contains(rows["t3"], "session") {
		t.Errorf("a limit of another kind is named for itself, not called the session:\n%s", out)
	}
	for _, dir := range []string{"~/.claude/projects", "~/.claude-work/projects"} {
		if !strings.Contains(out, dir) {
			t.Errorf("history must say it read %s:\n%s", dir, out)
		}
	}

	out = captureStdout(t, func() error { return cmdHistory([]string{"--config", path, "--days", "3", "--profile", "work"}) })
	if strings.Contains(out, render.When(t1, now)) || !strings.Contains(out, render.When(t2, now)) {
		t.Errorf("--profile work reads work's transcripts alone:\n%s", out)
	}
	if strings.Contains(out, "~/.claude/projects") {
		t.Errorf("--profile work names only the folder it read:\n%s", out)
	}
}

func TestHistoryRejectsADaysUnderOne(t *testing.T) {
	appWorld(t, twoProfileTOML)
	err := cmdHistory([]string{"--days", "0"})
	if got := errCode(t, err); got != codeUsage || exitStatus(err) != 2 {
		t.Errorf("--days 0: code %q, exit %d; want usage, exit 2", got, exitStatus(err))
	}
}

// An account the config no longer has is marked as status marks it, counts
// are singular for one, and the numbers line up.
func TestHistoryUsageMarksGoneAccountsAndAligns(t *testing.T) {
	now := time.Now()
	path := historyWorld(t, now)
	t.Setenv("NO_COLOR", "1")
	render.SetColor(false)
	var buf bytes.Buffer
	if err := historyUsage(&buf, path, 1, false, now); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gone (gone)") {
		t.Errorf("an account the config no longer has must be marked (gone):\n%s", out)
	}
	if strings.Contains(out, "day(s)") || !strings.Contains(out, "last 1 day,") {
		t.Errorf("one day is one day:\n%s", out)
	}
	if !strings.Contains(out, "2 switches") {
		t.Errorf("the switches are counted:\n%s", out)
	}
	if strings.Contains(out, "1 readings") {
		t.Errorf("one reading is one reading:\n%s", out)
	}
	// The peaks are columns: where the session's figure ends is the same on
	// every row that has one.
	var ends []int
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "w1") || strings.Contains(line, "gone (gone)") {
			ends = append(ends, strings.Index(line, "%"))
		}
	}
	if len(ends) != 2 || ends[0] != ends[1] {
		t.Errorf("figures must line up (%v):\n%s", ends, out)
	}
}
