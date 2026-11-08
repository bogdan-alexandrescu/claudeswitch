package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// resetsWorld is sharedTOML with every account read at 20% of its session
// and 30% of its week, the session clearing in 2h 30m and the week in 3d.
func resetsWorld(t *testing.T, columns string) (path string, now time.Time) {
	t.Helper()
	path = appWorld(t, sharedTOML)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", columns)
	render.SetColor(false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now()
	st, _ := state.Load("", cfg.ProfileNames()...)
	for _, id := range []string{"p1", "t1", "t2", "s1"} {
		st.Get(id).Last, st.Get(id).LastAt = pinReading(20, 30, now), now
		st.SetEmail(id, id+".someone@a-rather-long-company-domain.example")
		st.SetPlan(id, "Ultra team")
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path, now
}

func rowOf(out, id string) string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == id {
			return line
		}
	}
	return ""
}

// "When does it reset?" had no answer in account list: the figures stood
// alone. Each now says when its window clears, as a time of day where the
// terminal has room.
func TestAccountListSaysWhenEachWindowClears(t *testing.T) {
	path, now := resetsWorld(t, "160")
	var buf bytes.Buffer
	if err := accountList(&buf, path, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	r5, r7 := pinReading(20, 30, now).FiveHour.ResetsAt, pinReading(20, 30, now).SevenDay.ResetsAt
	row := rowOf(out, "p1")
	for _, want := range []string{"20% · " + render.When(*r5, now), "30% · " + render.When(*r7, now)} {
		if !strings.Contains(row, want) {
			t.Errorf("p1's row lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "when its window clears") {
		t.Errorf("the footer must say what the time beside a figure is:\n%s", out)
	}
}

// Where a time of day does not fit, the figure says how long until it
// clears, and the table still fits the terminal.
func TestAccountListClearsFitNarrowTerminals(t *testing.T) {
	for _, cols := range []int{80, 93} {
		path, _ := resetsWorld(t, itoa(cols))
		var buf bytes.Buffer
		if err := accountList(&buf, path, false); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, line := range strings.Split(out, "\n") {
			if n := utf8.RuneCountInString(line); n > cols {
				t.Errorf("a %d-column line in a %d-column terminal:\n%s", n, cols, line)
			}
		}
		if cols == 93 && !strings.Contains(rowOf(out, "p1"), "20% · 2h 30m") {
			t.Errorf("%d columns: the session's figure gives the time until it clears:\n%s", cols, out)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// account list --json carries each window's reset, for the app.
func TestAccountListJSONCarriesEachWindowsReset(t *testing.T) {
	path, now := resetsWorld(t, "100")
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, buf.Bytes())
	var p1 map[string]any
	for _, a := range m["accounts"].([]any) {
		if a.(map[string]any)["id"] == "p1" {
			p1 = a.(map[string]any)
		}
	}
	rd, _ := p1["reading"].(map[string]any)
	r := pinReading(20, 30, now)
	for key, want := range map[string]time.Time{
		"five_hour_resets_at": *r.FiveHour.ResetsAt, "seven_day_resets_at": *r.SevenDay.ResetsAt,
	} {
		s, _ := rd[key].(string)
		got, err := time.Parse(time.RFC3339Nano, s)
		if err != nil || !got.Equal(want.UTC()) {
			t.Errorf("%s = %v, want %s", key, rd[key], want.UTC().Format(time.RFC3339Nano))
		}
	}
	// An account never read has no reading at all.
	for _, a := range m["accounts"].([]any) {
		if a.(map[string]any)["id"] == "loose" && a.(map[string]any)["reading"] != nil {
			t.Errorf("loose was never read: %v", a)
		}
	}
}
