package render

import (
	"strings"
	"testing"
)

// highlighted draws a two-row box at depth d with the second row highlighted,
// its figure painted, and returns the drawing and that row's line.
func highlighted(t *testing.T, d Depth, colorfgbg string) (string, string) {
	t.Helper()
	t.Setenv("COLORFGBG", colorfgbg)
	SetDepth(d)
	t.Cleanup(func() { SetColor(false) })
	tb := NewTable([]string{"#", "ACCOUNT", "WEEK"}, 2)
	tb.Add("1", "work-1", Good("12%"))
	tb.Add("2", Bold("work-2"), Bad("97%!"))
	tb.Highlight(1)
	out := tb.RenderBox()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "work-2") {
			return out, l
		}
	}
	t.Fatalf("no row for work-2:\n%s", out)
	return "", ""
}

func TestAHighlightedRowHasItsBackgroundFromBarToBar(t *testing.T) {
	const bg = "\033[48;5;236m"
	_, line := highlighted(t, Depth256, "")
	body, ok := strings.CutPrefix(line, boxIndent)
	if !ok || !strings.HasPrefix(body, bg) {
		t.Fatalf("the row does not open with its background:\n%q", line)
	}
	if !strings.HasSuffix(body, reset) {
		t.Errorf("the background runs past the row:\n%q", line)
	}
	// Every painted cell ends in a full reset; the background must come back
	// after each one, or it stops at the first coloured cell.
	inner := strings.TrimSuffix(body, reset)
	if n, back := strings.Count(inner, reset), strings.Count(inner, reset+bg); n == 0 || n != back {
		t.Errorf("%d resets, %d followed by the background:\n%q", n, back, line)
	}
}

func TestOnlyTheHighlightedRowHasABackground(t *testing.T) {
	out, line := highlighted(t, Depth256, "")
	for _, l := range strings.Split(out, "\n") {
		if l != line && strings.Contains(l, "\033[48") {
			t.Errorf("a background on another line:\n%q", l)
		}
	}
}

func TestAHighlightKeepsTheRowItsWidth(t *testing.T) {
	out, _ := highlighted(t, DepthTrue, "")
	want := -1
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		n := visibleLen(l)
		if want < 0 {
			want = n
		}
		if n != want {
			t.Errorf("a %d-column line in a %d-column table:\n%q", n, want, l)
		}
	}
}

func TestALightTerminalGetsALightHighlight(t *testing.T) {
	if _, line := highlighted(t, DepthTrue, "0;15"); !strings.Contains(line, "\033[48;2;221;233;245m") {
		t.Errorf("black on white wants the light tint:\n%q", line)
	}
	if _, line := highlighted(t, DepthTrue, "15;0"); !strings.Contains(line, "\033[48;2;38;52;68m") {
		t.Errorf("white on black wants the dark tint:\n%q", line)
	}
	if _, line := highlighted(t, Depth256, "0;15"); !strings.Contains(line, "\033[48;5;254m") {
		t.Errorf("black on white at 256 colours wants the light grey:\n%q", line)
	}
}

// At sixteen colours the only grey background is the grey that secondary
// text is drawn in, which would vanish on it; ▸ and bold mark the row there.
func TestSixteenColoursHaveNoHighlight(t *testing.T) {
	if out, _ := highlighted(t, Depth16, ""); strings.Contains(out, "\033[4") || strings.Contains(out, "\033[10") {
		t.Errorf("a background at sixteen colours:\n%q", out)
	}
}

func TestNoColourMeansNoHighlight(t *testing.T) {
	plain := NewTable([]string{"#", "ACCOUNT", "WEEK"}, 2)
	plain.Add("1", "work-1", "12%")
	plain.Add("2", "work-2", "97%!")
	out, _ := highlighted(t, DepthNone, "")
	if want := plain.RenderBox(); out != want {
		t.Errorf("without colour a highlight changed the table:\n%s\nwant:\n%s", out, want)
	}
}

func TestLightBackgroundReadsCOLORFGBG(t *testing.T) {
	for v, want := range map[string]bool{
		"":             false, // no hint: dark, as most terminals are
		"15;0":         false,
		"12;8":         false,
		"0;15":         true,
		"0;7":          true,
		"0;default;15": true,
		"15;default;0": false,
		"0;default":    false,
		"garbage":      false,
	} {
		env := func(k string) string {
			if k == "COLORFGBG" {
				return v
			}
			return ""
		}
		if got := LightBackground(env); got != want {
			t.Errorf("COLORFGBG=%q: light %v, want %v", v, got, want)
		}
	}
}
