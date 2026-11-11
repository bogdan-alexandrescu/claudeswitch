package main

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// boxWorld is resetsWorld on a terminal, with p1 live.
func boxWorld(t *testing.T, columns string) string {
	t.Helper()
	path, _ := resetsWorld(t, columns)
	oldOn, oldRun := headerOn, daemonRunning
	t.Cleanup(func() { headerOn, daemonRunning = oldOn, oldRun })
	headerOn = func() bool { return true }
	daemonRunning = func() bool { return true }
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	st.Default().SetActive("p1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

func listText(t *testing.T, path string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := accountList(&buf, path, false); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// boxLines are the table's lines, colour removed.
func boxLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(ansi.ReplaceAllString(out, ""), "\n") {
		if t := strings.TrimSpace(l); t != "" && strings.ContainsAny(t[:3], "┌├└│") {
			lines = append(lines, l)
		}
	}
	return lines
}

func checkBox(t *testing.T, out string, columns int) {
	t.Helper()
	lines := boxLines(out)
	if len(lines) != 9 { // top, head, rule, five accounts, bottom
		t.Fatalf("%d table lines, want 9:\n%s", len(lines), out)
	}
	want := utf8.RuneCountInString(lines[0])
	if want > columns {
		t.Errorf("a %d-column table in a %d-column terminal:\n%s", want, columns, out)
	}
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n != want {
			t.Errorf("a %d-column line in a %d-column table:\n%s", n, want, out)
		}
	}
}

func TestListIsABorderedTableOnATerminal(t *testing.T) {
	out := listText(t, boxWorld(t, "160"))
	checkBox(t, out, 160)
	for _, want := range []string{"┌", "├", "└", "▸ p1", "cs switch <#> to use it", "! limit warning", "20% · "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "EMAIL") || !strings.Contains(out, "PLAN") {
		t.Errorf("a wide terminal has room for every column:\n%s", out)
	}
}

func TestBorderedListIsPaintedLikeStatusAndStillLinesUp(t *testing.T) {
	path := boxWorld(t, "160")
	render.SetColor(true)
	t.Cleanup(func() { render.SetColor(false) })
	out := listText(t, path)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no colour:\n%q", out)
	}
	if !strings.Contains(out, "colour:") {
		t.Errorf("no colour key, which status has:\n%s", out)
	}
	checkBox(t, out, 160)
}

// The live account's row stands out the way a selection would, and only it.
func TestBorderedListHighlightsTheLiveRow(t *testing.T) {
	const bg = "\x1b[48;5;236m"
	path := boxWorld(t, "160")
	t.Setenv("COLORFGBG", "")
	render.SetDepth(render.Depth256)
	t.Cleanup(func() { render.SetColor(false) })
	out := listText(t, path)
	lit := 0
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, bg) {
			continue
		}
		lit++
		if plain := ansi.ReplaceAllString(l, ""); !strings.Contains(plain, "▸ p1") {
			t.Errorf("a background on a row that is not live:\n%s", plain)
		}
	}
	if lit != 1 {
		t.Errorf("%d highlighted lines, want the live row alone:\n%q", lit, out)
	}
	checkBox(t, out, 160)
}

func TestBorderedListGivesUpColumnsBeforeWrapping(t *testing.T) {
	for _, columns := range []string{"120", "100", "80"} {
		out := listText(t, boxWorld(t, columns))
		n := map[string]int{"120": 120, "100": 100, "80": 80}[columns]
		checkBox(t, out, n)
	}
}

func TestATooNarrowTerminalGetsThePlainTable(t *testing.T) {
	out := listText(t, boxWorld(t, "40"))
	if strings.Contains(out, "┌") {
		t.Errorf("a box that cannot fit:\n%s", out)
	}
}

// Pipes and scripts keep what they had.
func TestPipedListHasNoBorders(t *testing.T) {
	path, _ := resetsWorld(t, "160")
	out := listText(t, path)
	if strings.ContainsAny(out, "┌│└▸") {
		t.Errorf("borders in piped output:\n%s", out)
	}
	if !strings.Contains(out, "cs switch <#> to use that account") {
		t.Errorf("piped legend changed:\n%s", out)
	}
}

// The row's ▸ says an account is live; a star on its profile said it twice.
func TestBorderedListMarksLiveOnce(t *testing.T) {
	out := listText(t, boxWorld(t, "160"))
	if strings.Contains(out, "*") {
		t.Errorf("a star beside the ▸:\n%s", out)
	}
	if !strings.Contains(out, "▸ live now · ! limit warning · cs switch <#> to use it") {
		t.Errorf("legend:\n%s", out)
	}
}

// speedWorld is boxWorld with the daemon's speeds measured: p1 (live) at 10
// points an hour of use, t1 climbing at 6 while live in no profile, t2 not
// climbing, s1 never measured. Every week is at 30%, clearing in three days.
func speedWorld(t *testing.T, columns string) string {
	t.Helper()
	path := boxWorld(t, columns)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	now := time.Now()
	measure := func(id string, perHour float64, rise time.Duration) {
		a := st.Get(id)
		a.WeekSpeed, a.WeekSpeedAt = perHour, now
		if rise >= 0 {
			a.WeekRiseAt = now.Add(-rise)
		}
	}
	measure("p1", 10, 0)
	measure("t1", 6, 5*time.Minute)
	measure("t2", 0, -1)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path
}

// The drain forecast in cs list: how many hours of use each account has
// left before its switch point, after its week.
func TestBorderedListSaysHowLongEachAccountLasts(t *testing.T) {
	out := listText(t, speedWorld(t, "160"))
	checkBox(t, out, 160)
	lines := boxLines(out)
	head := strings.Fields(strings.NewReplacer("│", " ").Replace(lines[1]))
	if i := indexOf(head, "LASTS"); i < 0 || head[i-1] != "WEEK" {
		t.Fatalf("no LASTS after WEEK: %v", head)
	}
	for id, want := range map[string]string{
		"p1":    "│ ~7h ",         // 68 points at 10 an hour
		"t1":    "│ ~11h ↑ ",      // 68 at 6, and climbing elsewhere
		"t2":    "│ ∞ ",           // not climbing
		"s1":    "│ - ",           // never measured
		"loose": "│ -          │", // no reading at all
	} {
		row := ""
		for _, l := range lines {
			if strings.Contains(l, " "+id+" ") {
				row = l
			}
		}
		if !strings.Contains(row, want) {
			t.Errorf("%s: no %q in\n%s", id, want, row)
		}
	}
	if !strings.Contains(out, "↑ used elsewhere") {
		t.Errorf("the legend does not say what ↑ means:\n%s", out)
	}
}

// LASTS is worth more than the plan or the email: it is the last to go.
func TestLastsOutstaysThePlanAndTheEmail(t *testing.T) {
	for _, columns := range []string{"120", "100", "80"} {
		out := listText(t, speedWorld(t, columns))
		n, _ := strconv.Atoi(columns)
		checkBox(t, out, n)
		if strings.Contains(out, "EMAIL") && !strings.Contains(out, "LASTS") {
			t.Errorf("%s columns: the email outstayed LASTS:\n%s", columns, out)
		}
		if strings.Contains(out, "PLAN") && !strings.Contains(out, "LASTS") {
			t.Errorf("%s columns: the plan outstayed LASTS:\n%s", columns, out)
		}
	}
}

// Without a daemon's measurements there is nothing to forecast: no column of
// dashes, and the legend as it was.
func TestNoMeasurementsNoLastsColumn(t *testing.T) {
	out := listText(t, boxWorld(t, "160"))
	if strings.Contains(out, "LASTS") || strings.Contains(out, "used elsewhere") {
		t.Errorf("a LASTS column with nothing measured:\n%s", out)
	}
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// cs list --json carries each account's drain forecast, for the app and
// scripts: the state, the hours of use left, the speed, and whether someone
// elsewhere is spending it.
func TestAccountListJSONCarriesTheDrainForecast(t *testing.T) {
	path := speedWorld(t, "160")
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	drains := map[string]map[string]any{}
	for _, a := range decodeJSON(t, buf.Bytes())["accounts"].([]any) {
		a := a.(map[string]any)
		d, _ := a["drain"].(map[string]any)
		drains[a["id"].(string)] = d
	}
	for id, want := range map[string]map[string]any{
		"p1":    {"state": "hours", "hours_left": 6.8, "speed_per_hour": 10.0, "used_elsewhere": false},
		"t1":    {"state": "hours", "hours_left": 11.3, "speed_per_hour": 6.0, "used_elsewhere": true},
		"t2":    {"state": "idle", "hours_left": nil, "speed_per_hour": 0.0, "used_elsewhere": false},
		"s1":    {"state": "unknown", "hours_left": nil, "speed_per_hour": nil, "used_elsewhere": false},
		"loose": {"state": "unknown", "hours_left": nil, "speed_per_hour": nil, "used_elsewhere": false},
	} {
		for k, v := range want {
			if got, ok := drains[id][k]; !ok || got != v {
				t.Errorf("%s drain %s = %v, want %v", id, k, got, v)
			}
		}
	}
}

// cs status says how long each profile's pool lasts at its live account's
// speed: default's two pools hold p1, t1 and t2, 68 points of room each, and
// p1 is spent at 10 an hour. The work profile has nothing live, so nothing is
// said of it outside --detail.
func TestStatusSaysHowLongEachProfilesPoolLasts(t *testing.T) {
	path := speedWorld(t, "200")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "")
	out := b.String()
	if !strings.Contains(out, "default's pools: ~20h of use left across 3 accounts") {
		t.Errorf("no drain line for default:\n%s", out)
	}
	if strings.Contains(out, "work pool:") || strings.Contains(out, "runs dry") {
		t.Errorf("a line that says nothing, or the runway's:\n%s", out)
	}
	b.Reset()
	renderStatus(&b, cfg, st, render.Options{Detail: true}, nil, time.Now(), "work")
	if out := b.String(); !strings.Contains(out, "work pool: hours of use unknown") {
		t.Errorf("--detail does not say work's forecast is unknown:\n%s", out)
	}
}
