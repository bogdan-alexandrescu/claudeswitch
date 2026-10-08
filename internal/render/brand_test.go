package render

import (
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// The depth follows COLORTERM first, then TERM, and defaults to the 256-colour
// cube, which nearly every terminal in use handles.
func TestDepthFromEnv(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want Depth
	}{
		{map[string]string{"COLORTERM": "truecolor", "TERM": "xterm-256color"}, DepthTrue},
		{map[string]string{"COLORTERM": "24bit"}, DepthTrue},
		{map[string]string{"COLORTERM": "TrueColor", "TERM": "linux"}, DepthTrue},
		{map[string]string{"TERM": "xterm-256color"}, Depth256},
		{map[string]string{"TERM": "screen-256color", "COLORTERM": "yes"}, Depth256},
		{map[string]string{"TERM": "xterm"}, Depth256},
		{map[string]string{}, Depth256},
		{map[string]string{"TERM": "linux"}, Depth16},
		{map[string]string{"TERM": "vt100"}, Depth16},
		{map[string]string{"TERM": "xterm-color"}, Depth16},
		{map[string]string{"TERM": "ansi"}, Depth16},
	} {
		if got := DepthFromEnv(envOf(c.env)); got != c.want {
			t.Errorf("DepthFromEnv(%v) = %v, want %v", c.env, got, c.want)
		}
	}
}

// The palette is the brand's at each depth: these are the values BRAND.md
// documents, so a change here is a change to the brand.
func TestPaletteAtEachDepth(t *testing.T) {
	for _, c := range []struct {
		d                                       Depth
		glacier, melt, amber, orange, red, grey string
	}{
		{DepthTrue, "\033[38;2;143;211;255m", "\033[38;2;182;240;220m", "\033[38;2;240;199;94m",
			"\033[38;2;255;160;102m", "\033[38;2;255;122;122m", "\033[38;5;245m"},
		{Depth256, "\033[38;5;117m", "\033[38;5;158m", "\033[38;5;221m",
			"\033[38;5;215m", "\033[38;5;203m", "\033[38;5;245m"},
		{Depth16, "\033[36m", "\033[32m", "\033[33m", "\033[1;33m", "\033[31m", "\033[90m"},
	} {
		p := PaletteFor(c.d)
		got := []string{p.Glacier, p.Meltwater, p.Amber, p.Orange, p.Red, p.Grey}
		want := []string{c.glacier, c.melt, c.amber, c.orange, c.red, c.grey}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("depth %v role %d = %q, want %q", c.d, i, got[i], want[i])
			}
		}
	}
	if (PaletteFor(DepthNone) != Palette{}) {
		t.Error("no depth must mean no codes")
	}
}

// NO_COLOR wins over everything, CLICOLOR_FORCE over the TTY check, and a
// pipe gets plain text.
func TestColourDetectionUnchanged(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	if detectColor() {
		t.Error("NO_COLOR must switch colour off even when forced")
	}
	t.Setenv("NO_COLOR", "")
	if !detectColor() {
		t.Error("CLICOLOR_FORCE must switch colour on")
	}
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("TERM", "xterm-256color")
	if detectColor() {
		t.Error("test output is not a terminal, so colour must be off")
	}
}

func TestPaintFollowsDepth(t *testing.T) {
	t.Cleanup(func() { SetColor(false) })
	SetDepth(DepthTrue)
	if got := Accent("x"); got != "\033[38;2;143;211;255mx\033[0m" {
		t.Errorf("truecolour accent = %q", got)
	}
	SetDepth(Depth256)
	if got := Good("x"); got != "\033[38;5;158mx\033[0m" {
		t.Errorf("256-colour meltwater = %q", got)
	}
	SetDepth(Depth16)
	if got := Bad("x"); got != "\033[31mx\033[0m" {
		t.Errorf("16-colour red = %q", got)
	}
	SetDepth(DepthNone)
	if got := Bad("x"); got != "x" {
		t.Errorf("no colour = %q", got)
	}
}

func TestDotBarPlain(t *testing.T) {
	SetColor(false)
	for _, c := range []struct {
		pct, thr float64
		n        int
		want     string
	}{
		{40, 85, 12, "●●●●●·····◉·"},
		{0, 85, 12, "··········◉·"},
		{100, 95, 12, "●●●●●●●●●●●◉"},
		{20, 0, 6, "●·····"},
		{50, 85, 6, "●●●··◉"},
	} {
		if got := dotBar(c.pct, c.thr, c.n, "", false); got != c.want {
			t.Errorf("dotBar(%v, %v, %d) = %q, want %q", c.pct, c.thr, c.n, got, c.want)
		}
		if got := visibleLen(dotBar(c.pct, c.thr, c.n, red, false)); got != c.n {
			t.Errorf("dotBar is %d wide, want %d", got, c.n)
		}
	}
}

// Healthy dots are the accent, a warning keeps its own colour, the threshold
// is a bold dot in the default foreground, and unlit dots are dim.
func TestDotBarColoured(t *testing.T) {
	SetDepth(Depth256)
	t.Cleanup(func() { SetColor(false) })

	ok := dotBar(40, 85, 12, levelFor(40, 85, ""), false)
	want := pal.Glacier + "●●●●●" + reset + dim + "·····" + reset + bold + "●" + reset + dim + "·" + reset
	if ok != want {
		t.Errorf("healthy bar = %q, want %q", ok, want)
	}
	if strings.Contains(ok, pal.Meltwater) {
		t.Error("meltwater is for figures; the bar is the accent")
	}
	over := dotBar(97, 85, 12, levelFor(97, 85, ""), false)
	if !strings.HasPrefix(over, pal.Red+"●●●●●●●●●●"+reset) {
		t.Errorf("an over-trigger bar must be red, got %q", over)
	}
	climbing := dotBar(60, 85, 12, levelFor(60, 85, ""), false)
	if !strings.HasPrefix(climbing, pal.Amber) {
		t.Errorf("a climbing bar must be amber, got %q", climbing)
	}
	frozen := dotBar(40, 85, 12, levelFor(40, 85, ""), true)
	if !strings.HasPrefix(frozen, pal.Grey) || strings.Contains(frozen, pal.Glacier) {
		t.Errorf("a frozen bar must be grey, got %q", frozen)
	}
	if strings.Contains(stripColor(ok), dotThreshold) {
		t.Error("with colour the threshold is a bold ●, not ◉")
	}
}

// The table keeps inside MaxTable, and inside the terminal when that is
// narrower, whatever the account names.
func TestLayoutWithinBudget(t *testing.T) {
	const fixed = 2 + 1 + 2 + 2 + 6 + 2 + 6 + 2 + 17 // indent, marker, gaps, numbers, state
	for _, width := range []int{60, 72, 80, 90, 100, 120, 200} {
		for _, name := range []int{7, 9, 12, 16} {
			l := layoutFor(width, name)
			w := fixed + name
			if l.clears {
				w += 2 + 6
			}
			if l.dots > 0 {
				w += 2 * (2 + l.dots)
				if w > MaxTable {
					t.Errorf("width %d, name %d: bars take the table to %d", width, name, w)
				}
			}
			if l.plan {
				w += 2 + 11
			}
			if w > width {
				t.Errorf("width %d, name %d: table is %d wide", width, name, w)
			}
		}
	}
	if l := layoutFor(100, 9); l.dots != barDots || !l.plan {
		t.Errorf("a 100-column terminal should have full bars and the plan: %+v", l)
	}
	if l := layoutFor(80, 9); l.dots != barDotsNarrow {
		t.Errorf("an 80-column terminal should keep narrow bars: %+v", l)
	}
}

func TestHeader(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	SetColor(false)
	if got := Header("1.4.0", "daemon live", now.Add(-70*time.Second), now); got != "  ◎ claudeswitch 1.4.0 · daemon live · polled 1m ago\n" {
		t.Errorf("plain header = %q", got)
	}
	if got := Header("dev", "daemon not running", time.Time{}, now); got != "  ◎ claudeswitch dev · daemon not running · not polled yet\n" {
		t.Errorf("unpolled header = %q", got)
	}
	SetDepth(Depth256)
	t.Cleanup(func() { SetColor(false) })
	got := Header("1.4.0", "daemon live", now, now)
	want := "  " + pal.Glacier + "◎ claudeswitch" + reset + dim + " 1.4.0 · daemon live · polled just now" + reset + "\n"
	if got != want {
		t.Errorf("coloured header = %q, want %q", got, want)
	}
}
