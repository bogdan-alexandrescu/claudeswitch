package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
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
