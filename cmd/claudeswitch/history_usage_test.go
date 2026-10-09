package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// IMPROVEMENTS F7: `cs history --usage --json` is, per account, the series
// of readings the daemon kept, plus the switches, over the last --days.

const historyTOML = `
[[account]]
id = "w1"

[[account]]
id = "w2"

[[profile]]
name = "default"
pool = []

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1", "w2"]
`

func historyWorld(t *testing.T, now time.Time) string {
	t.Helper()
	path := appWorld(t, historyTOML)
	dir := filepath.Join(os.Getenv("HOME"), ".local", "state", "claudeswitch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ts := func(d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }
	readings := strings.Join([]string{
		`{"at":"` + ts(40*24*time.Hour) + `","account":"w1","five_hour":99,"seven_day":99}`,
		`{"at":"` + ts(2*time.Hour) + `","account":"w1","five_hour":10,"seven_day":40}`,
		`{"at":"` + ts(time.Hour) + `","account":"w1","five_hour":20,"seven_day":null}`,
		`{"at":"` + ts(time.Hour) + `","account":"gone","five_hour":5,"seven_day":6}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "readings.jsonl"), []byte(readings), 0o600); err != nil {
		t.Fatal(err)
	}
	audit := strings.Join([]string{
		`{"at":"` + ts(40*24*time.Hour) + `","kind":"switch","profile":"work","from":"w2","to":"w1"}`,
		`{"at":"` + ts(90*time.Minute) + `","kind":"switch","profile":"work","from":"w1","to":"w2","reason":"over the 85% session trigger","forced":true}`,
		`{"at":"` + ts(80*time.Minute) + `","kind":"decision","profile":"work","decision":"stay"}`,
		`{"at":"` + ts(70*time.Minute) + `","kind":"switch","profile":"work","to":"w1","reason":"seeded"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(audit), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoryUsageJSON(t *testing.T) {
	now := time.Now()
	path := historyWorld(t, now)
	var buf bytes.Buffer
	if err := historyUsage(&buf, path, 30, true, now); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	if m["days"] != 30.0 || m["from"] == nil || m["to"] == nil {
		t.Errorf("header: %v", m)
	}
	accts := m["accounts"].([]any)
	var ids []string
	for _, a := range accts {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "w1,w2,gone" {
		t.Fatalf("accounts %v: configured in rotation order, then those only the log names", ids)
	}
	w1 := accts[0].(map[string]any)
	if w1["profile"] != "work" || w1["configured"] != true {
		t.Errorf("w1: %v", w1)
	}
	series := w1["series"].([]any)
	if len(series) != 2 {
		t.Fatalf("w1 series %v: the 40-day-old reading is outside 30 days", series)
	}
	p0, p1 := series[0].(map[string]any), series[1].(map[string]any)
	if p0["five_hour"] != 10.0 || p0["seven_day"] != 40.0 || p1["five_hour"] != 20.0 || p1["seven_day"] != nil {
		t.Errorf("series %v", series)
	}
	for _, k := range []string{"at", "five_hour", "seven_day"} {
		if _, ok := p1[k]; !ok {
			t.Errorf("a point lacks %q", k)
		}
	}
	if w2 := accts[1].(map[string]any); len(w2["series"].([]any)) != 0 {
		t.Errorf("w2 has no readings: %v", w2)
	}
	if g := accts[2].(map[string]any); g["configured"] != false || g["profile"] != nil {
		t.Errorf("gone: %v", g)
	}
	sw := m["switches"].([]any)
	if len(sw) != 2 {
		t.Fatalf("switches %v", sw)
	}
	s0, s1 := sw[0].(map[string]any), sw[1].(map[string]any)
	if s0["profile"] != "work" || s0["from"] != "w1" || s0["to"] != "w2" || s0["forced"] != true ||
		s0["reason"] != "over the 85% session trigger" || s0["at"] == nil {
		t.Errorf("switch: %v", s0)
	}
	if s1["from"] != nil {
		t.Errorf("a switch with no from: %v", s1)
	}

	buf.Reset()
	if err := historyUsage(&buf, path, 1, true, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	m = decodeJSON(t, buf.Bytes())
	if n := len(m["accounts"].([]any)[0].(map[string]any)["series"].([]any)); n != 0 {
		t.Errorf("--days 1, two days later: %d readings", n)
	}
}

func TestHistoryUsageText(t *testing.T) {
	now := time.Now()
	path := historyWorld(t, now)
	var buf bytes.Buffer
	if err := historyUsage(&buf, path, 30, false, now); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"w1", "2 readings", "peak 5h 20%", "7d 40%", "w2", "no readings", "2 switches"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
}

func TestHistoryJSONNeedsUsage(t *testing.T) {
	appWorld(t, historyTOML)
	if got := errCode(t, cmdHistory([]string{"--json"})); got != codeUsage {
		t.Errorf("history --json without --usage: %q", got)
	}
	if got := errCode(t, cmdHistory([]string{"--usage", "--days", "0", "--json"})); got != codeUsage {
		t.Errorf("--days 0: %q", got)
	}
}
