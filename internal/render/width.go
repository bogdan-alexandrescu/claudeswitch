package render

import (
	"os"
	"strconv"

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

// layout says which optional columns fit at a given width. Columns are dropped
// in order of how much they earn their space: the bars and the plan are useful
// but not essential, and the numbers never go.
type layout struct {
	width  int
	bars   bool
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
		numCol = 5  // "100%!"
		barCol = 5  // "▰▰▰▰▰"
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
	if width >= need+2*(gap+barCol) {
		l.bars = true
		need += 2 * (gap + barCol)
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

// miniBar is a five-cell gauge. A number states a value; a bar states how
// worried to be about it without having to read the number at all.
func miniBar(pct float64) string {
	const cells = 5
	filled := int(pct/100*cells + 0.5)
	if filled > cells {
		filled = cells
	}
	if filled < 0 {
		filled = 0
	}
	out := make([]rune, 0, cells)
	for i := 0; i < cells; i++ {
		if i < filled {
			out = append(out, '▰')
		} else {
			out = append(out, '▱')
		}
	}
	return string(out)
}
