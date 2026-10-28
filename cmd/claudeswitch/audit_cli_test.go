package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// auditWorld gives the test a HOME with these events in its audit log, colour
// off and a terminal of the given width.
func auditWorld(t *testing.T, cols string, evs ...audit.Event) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COLUMNS", cols)
	t.Setenv("NO_COLOR", "1")
	render.SetColor(false)
	log, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if err := log.Write(e); err != nil {
			t.Fatal(err)
		}
	}
}

// -n -1 sliced the event list with a negative bound and panicked; -n 0
// said the log was empty.
func TestAuditRejectsACountUnderOne(t *testing.T) {
	auditWorld(t, "100", audit.Event{Kind: "switch", From: "a", To: "b"})
	for _, n := range []string{"-1", "0"} {
		out, code := runCLI(t, "audit", "-n", n)
		if code != 2 || strings.Contains(out, "panic") || !strings.Contains(out, "-n must be at least 1") {
			t.Errorf("audit -n %s: exit %d, want 2 with a clear error:\n%s", n, code, out)
		}
	}
}

// A kind audit does not know matches nothing, which read as an empty log.
func TestAuditRejectsAnUnknownKind(t *testing.T) {
	auditWorld(t, "100", audit.Event{Kind: "switch", From: "a", To: "b"})
	out, code := runCLI(t, "audit", "--kind", "bogus")
	if code != 2 || !strings.Contains(out, `"bogus"`) {
		t.Errorf("audit --kind bogus: exit %d, want 2 naming the kind:\n%s", code, out)
	}
	for _, k := range []string{"decision", "switch", "rejection", "unpin", "severity", "error", "login", "clean", "refresh"} {
		if !strings.Contains(out, k) {
			t.Errorf("the error must list the kinds; %q is missing:\n%s", k, out)
		}
	}
}

func TestAuditRejectsArguments(t *testing.T) {
	auditWorld(t, "100")
	if out, code := runCLI(t, "audit", "switch"); code != 2 || !strings.Contains(out, "audit takes no arguments") {
		t.Errorf("audit switch: exit %d, want 2:\n%s", code, out)
	}
}

// A filter that matches nothing is not an empty log.
func TestAuditSaysWhenNoEventMatches(t *testing.T) {
	auditWorld(t, "100", audit.Event{Kind: "switch", From: "a", To: "b"})
	out := captureStdout(t, func() error { return cmdAudit([]string{"--kind", "unpin"}) })
	if !strings.Contains(out, "no events match") || strings.Contains(out, "no audit events yet") {
		t.Errorf("a filter matching nothing must say so:\n%s", out)
	}
	out = captureStdout(t, func() error { return cmdAudit([]string{"--since", "1m"}) })
	if strings.Contains(out, "no audit events yet") {
		t.Errorf("--since matching nothing is not an empty log:\n%s", out)
	}
}

// Every row says which profile it concerns, once more than the default one
// appears; severity rows belong to no profile.
func TestAuditShowsTheProfile(t *testing.T) {
	now := time.Now()
	auditWorld(t, "120",
		audit.Event{At: now.Add(-2 * time.Minute), Kind: "switch", Profile: "work", From: "w1", To: "w2", Reason: "manual"},
		audit.Event{At: now.Add(-time.Minute), Kind: "decision", Profile: "default", Decision: "stay", Reason: "under"})
	out := captureStdout(t, func() error { return cmdAudit(nil) })
	if !strings.Contains(out, "PROFILE") {
		t.Fatalf("no PROFILE column:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "w1 → w2") && !strings.Contains(line, "work") {
			t.Errorf("the switch row must name its profile:\n%s", out)
		}
	}

	// One profile only: the column would say "default" on every row.
	auditWorld(t, "120", audit.Event{Kind: "switch", Profile: "default", From: "a", To: "b"})
	if out := captureStdout(t, func() error { return cmdAudit(nil) }); strings.Contains(out, "PROFILE") {
		t.Errorf("with only the default profile there is nothing to tell apart:\n%s", out)
	}
}

// Reasons were cut at 40 characters whatever the terminal, so a wide one
// showed "…over the 8…"; a narrow one still wrapped. Rows fit the width.
func TestAuditFitsTheTerminal(t *testing.T) {
	reason := "active account at 86% of its session window, over the 85% session trigger; work-2 resets in 9h with 40% unused, so it goes first"
	ev := audit.Event{At: time.Now(), Kind: "switch", Profile: "work", From: "work-1", To: "work-2", Reason: reason}
	auditWorld(t, "200", ev)
	out := captureStdout(t, func() error { return cmdAudit(nil) })
	if !strings.Contains(out, reason) {
		t.Errorf("at 200 columns the whole reason fits:\n%s", out)
	}
	auditWorld(t, "80", ev)
	out = captureStdout(t, func() error { return cmdAudit(nil) })
	for _, line := range strings.Split(out, "\n") {
		if n := utf8.RuneCountInString(line); n > 80 {
			t.Errorf("a %d-column row in an 80-column terminal:\n%s", n, line)
		}
	}
}

// One date format for this week and one with the date (owner's terms):
// "today 15:04", "Sun 23:42", "18 Oct 02:36"; a refusal says when it clears.
func TestAuditDatesSayTheDay(t *testing.T) {
	now := time.Now()
	old := now.AddDate(0, 0, -10)
	auditWorld(t, "120",
		audit.Event{At: old, Kind: "rejection", From: "a", Window: "five_hour", ResetsAt: old.Add(time.Hour)},
		audit.Event{At: now, Kind: "switch", From: "a", To: "b"})
	out := captureStdout(t, func() error { return cmdAudit(nil) })
	if !strings.Contains(out, "today "+now.Format("15:04")) {
		t.Errorf("today's event must say today:\n%s", out)
	}
	if !strings.Contains(out, old.Format("2 Jan 15:04")) || strings.Contains(out, old.Format("01-02")) {
		t.Errorf("an event ten days old must carry its date as 2 Jan:\n%s", out)
	}
	if !strings.Contains(out, "cleared "+old.Add(time.Hour).Format("2 Jan 15:04")) {
		t.Errorf("a refusal must say when it cleared, with the day:\n%s", out)
	}
	if strings.Contains(out, "five_hour") {
		t.Errorf("the window is the session in text for people:\n%s", out)
	}
}

// refresh rows (the daemon renewing a quiet profile's live token) named
// nothing at all.
func TestAuditShowsRefreshRows(t *testing.T) {
	auditWorld(t, "120", audit.Event{Kind: "refresh", Profile: "default", Account: "work-1",
		Reason: "live token near expiry on a quiet profile"})
	out := captureStdout(t, func() error { return cmdAudit(nil) })
	if !strings.Contains(out, "work-1") || !strings.Contains(out, "near expiry") {
		t.Errorf("a refresh row must name the account and why:\n%s", out)
	}
}

func TestAuditJSON(t *testing.T) {
	auditWorld(t, "120",
		audit.Event{Kind: "switch", From: "a", To: "b"},
		audit.Event{Kind: "decision", Decision: "stay"},
		audit.Event{Kind: "switch", From: "b", To: "a"})
	out, code := runCLI(t, "audit", "--kind", "switch", "--json")
	if code != 0 {
		t.Fatalf("audit --json: exit %d:\n%s", code, out)
	}
	var m struct {
		Events []audit.Event `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(m.Events) != 2 || m.Events[0].To != "b" || m.Events[1].To != "a" {
		t.Errorf("events = %+v, want the two switches, oldest first", m.Events)
	}
}
