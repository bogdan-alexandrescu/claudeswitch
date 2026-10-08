package render

import (
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// DefaultWidth is assumed when the terminal cannot be measured — piped output,
// a CI log, a pager. Eighty is the conventional floor and the width this layout
// is designed to fit.
const DefaultWidth = 80

// terminalWidth measures the output terminal.
//
// This matters more than it sounds: the table was 99 columns in an 80-column
// terminal, so every row wrapped and the alignment the layout depends on was
// destroyed. A table that does not fit is worse than a narrower one.
func terminalWidth() int {
	// An explicit COLUMNS wins, so the layout can be forced for a screenshot or
	// a test.
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 20 {
			return n
		}
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 20 {
			return int(ws.Col)
		}
	}
	return DefaultWidth
}

// MaxTable is the widest the status table is allowed to grow, whatever the
// terminal. Past this the eye has to travel too far between a name and its
// state, and a wide terminal is no reason to make every row longer.
const MaxTable = 100

// Dot bars come in two sizes. Twelve dots is the most two bars can have and
// still leave a full table (plan, clears and a typical account name) inside
// MaxTable; six is the narrow form, kept because a short bar still says how
// worried to be at a glance, which the number alone does not.
const (
	barDots       = 12
	barDotsNarrow = 6
)

// layout says which optional columns fit at a given width. Columns are dropped
// in order of how much they earn their space: the bars and the plan are useful
// but not essential, and the numbers never go.
type layout struct {
	width  int
	dots   int // dots per bar; zero drops the bars
	clears bool
	plan   bool
	burn   bool
	reason int // characters available for a switch reason
}

func layoutFor(width, nameWidth int) layout {
	// Widths of the pieces, so the arithmetic is checkable rather than guessed.
	// An earlier version guessed and produced an 83-column table in an
	// 80-column terminal, which wraps and destroys the alignment entirely.
	const (
		indent = 2
		gap    = 2
		marker = 1
		numCol = 6  // "~100%!"
		clearW = 6  // "2d 12h"
		planW  = 11 // "Max 5x team"
		stateW = 17 // "no headroom · 5h"
	)
	// Always present: indent, marker, name, both numbers, state.
	need := indent + marker + gap + nameWidth + gap + numCol + gap + numCol + gap + stateW

	l := layout{width: width}
	if width >= need+gap+clearW {
		l.clears = true
		need += gap + clearW
	}
	// The bars are held to MaxTable as well as to the terminal: they are the
	// one column that would otherwise grow to fill whatever room there is.
	room := width
	if room > MaxTable {
		room = MaxTable
	}
	for _, n := range []int{barDots, barDotsNarrow} {
		if room >= need+2*(gap+n) {
			l.dots = n
			need += 2 * (gap + n)
			break
		}
	}
	if width >= need+gap+planW {
		l.plan = true
		need += gap + planW
	}
	// Burn rate lives inside the state column, so it needs room there rather
	// than a column of its own.
	l.burn = width >= need+12

	l.reason = width - (indent + 12 + gap + nameWidth*2 + 4)
	if l.reason < 10 {
		l.reason = 10
	}
	if l.reason > 44 {
		l.reason = 44
	}
	return l
}

// Dot bar glyphs. A lit dot is spent quota; the threshold dot is where the
// profile rotates away. Without colour, bold cannot set the threshold apart
// from a lit dot, so it gets a glyph of its own.
const (
	dotLit       = "●"
	dotUnlit     = "·"
	dotThreshold = "◉"
)

// dotBar is a gauge of n dots. A number states a value; a bar states how
// worried to be about it without having to read the number at all, and the
// threshold dot shows how far there is to go before the account rotates.
//
// tone is the reading's level from levelFor; a healthy reading is drawn in the
// accent rather than meltwater, which is kept for the figures. frozen greys the
// whole bar, for an account whose figures are leftovers.
func dotBar(pct, threshold float64, n int, tone string, frozen bool) string {
	lit := int(pct/100*float64(n) + 0.5)
	if lit > n {
		lit = n
	}
	if lit < 0 {
		lit = 0
	}
	// The threshold dot is the one whose span holds the trigger: at twelve
	// dots, 85% falls in the eleventh.
	thr := -1
	if threshold > 0 {
		thr = int(math.Ceil(threshold/100*float64(n))) - 1
		if thr >= n {
			thr = n - 1
		}
		if thr < 0 {
			thr = 0
		}
	}
	litTone := tone
	switch {
	case frozen:
		litTone = grey
	case tone == "" || tone == meltwater:
		litTone = glacier
	}

	var b strings.Builder
	run, runTone := "", ""
	flush := func() {
		b.WriteString(paint(runTone, run))
		run = ""
	}
	for i := 0; i < n; i++ {
		glyph, t := dotUnlit, dim
		switch {
		case i == thr:
			flush()
			if useColor {
				b.WriteString(paint(bold, dotLit))
			} else {
				b.WriteString(dotThreshold)
			}
			continue
		case i < lit:
			glyph, t = dotLit, litTone
		}
		if t != runTone {
			flush()
			runTone = t
		}
		run += glyph
	}
	flush()
	return b.String()
}
