package render

import (
	"fmt"
	"strings"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// boxIndent is where a bordered table starts, the indent of every other view.
const boxIndent = "  "

func boxWidths(head []string, rows [][]string) []int {
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = visibleLen(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], visibleLen(c))
			}
		}
	}
	return widths
}

func boxTotal(widths []int) int {
	total := len(boxIndent) + 1
	for _, w := range widths {
		total += w + 3 // a space each side, and the bar after it
	}
	return total
}

// BoxWidth is how wide RenderBox draws head and rows, its last column held to
// lastAtMost, which is what Fit can cut it to.
func BoxWidth(head []string, rows [][]string, lastAtMost int) int {
	widths := boxWidths(head, rows)
	if n := len(widths); n > 0 {
		widths[n-1] = min(widths[n-1], lastAtMost)
	}
	return boxTotal(widths)
}

// Highlight has RenderBox draw a row (counted from 0, as added) on a tinted
// background, the way a selection stands out. Only with colour, and only
// where the depth has a tint that keeps the row legible.
func (t *table) Highlight(row int) {
	if t.lit == nil {
		t.lit = map[int]bool{}
	}
	t.lit[row] = true
}

// RenderBox draws the table with borders, for a terminal. A bordered table
// is for reading; piped output keeps Render, whose columns a script can split.
func (t *table) RenderBox() string {
	rows := t.rows
	widths := boxWidths(t.head, rows)
	if over := boxTotal(widths) - t.fit; t.fit > 0 && over > 0 && len(widths) > 0 {
		last := len(widths) - 1
		if room := widths[last] - over; room >= 1 {
			rows = nil
			for _, r := range t.rows {
				cut := append([]string{}, r...)
				if last < len(cut) {
					cut[last] = Fit(cut[last], room)
				}
				rows = append(rows, cut)
			}
			widths = boxWidths(t.head, rows)
		}
	}
	segments := make([]string, len(widths))
	for i, w := range widths {
		segments[i] = strings.Repeat("─", w+2)
	}
	rule := func(left, mid, right string) string {
		return boxIndent + paint(grey, left+strings.Join(segments, mid)+right) + "\n"
	}
	bar := paint(grey, "│")
	line := func(cells []string, weight string, lit bool) string {
		var b strings.Builder
		b.WriteString(bar)
		for i, w := range widths {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			if weight != "" {
				c = paint(weight, c)
			}
			if t.right[i] {
				c = padLeft(c, w)
			} else {
				c = padRight(c, w)
			}
			b.WriteString(" " + c + " " + bar)
		}
		s := b.String()
		if lit && useColor && rowBg != "" {
			// Every painted cell ends in a full reset, which ends the
			// background too: put it back after each one.
			s = rowBg + strings.ReplaceAll(s, reset, reset+rowBg) + reset
		}
		return boxIndent + s + "\n"
	}
	var b strings.Builder
	b.WriteString(rule("┌", "┬", "┐"))
	b.WriteString(line(t.head, bold, false))
	b.WriteString(rule("├", "┼", "┤"))
	for i, r := range rows {
		b.WriteString(line(r, "", t.lit[i]))
	}
	b.WriteString(rule("└", "┴", "┘"))
	return b.String()
}

// Figure is a window's reading as the status table grades it: tinted against
// the trigger and the API's own severity, with the "!" of a limit warning.
func Figure(v, trigger float64, last *usage.Usage, window string) string {
	s := fmt.Sprintf("%.0f%%", v)
	if severityOf(last, window) {
		s += "!"
	}
	return paint(levelFor(v, trigger, severityName(last, window)), s)
}

// ColorState tints a state word the way the status table does.
func ColorState(s string) string { return colorState(s) }

// ColourKey is the line that says what the colours mean, the same in every
// view that has them; "" where there is no colour to explain.
func ColourKey() string {
	if !useColor {
		return ""
	}
	// These four are colour swatches, not words to read in sequence. Without
	// a label and separators they run together into "ok climbing close act
	// now", which is what the line looks like the moment colour is stripped
	// — by a pipe, a paste, or a terminal that does not support it.
	return "  colour:  " + paint(meltwater, "ok") + "  ·  " + paint(amber, "climbing") +
		"  ·  " + paint(orange, "close") + "  ·  " + paint(red, "act now")
}
