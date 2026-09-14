package render

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Colour is switched off when output is not a terminal, so piping into a file
// or another program yields plain text. NO_COLOR is honoured because it is the
// convention people expect, and CLICOLOR_FORCE because someone piping into
// `less -R` still wants it.
//
// https://no-color.org
var useColor = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// SetColor overrides detection, for tests and for a future --color flag.
func SetColor(on bool) { useColor = on }

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
	// 256-colour orange: the 8-colour palette has no orange, and the step from
	// yellow straight to red loses the "getting close" band entirely.
	orange = "\033[38;5;208m"
	red    = "\033[31m"
	grey   = "\033[90m"
)

func paint(code, s string) string {
	if !useColor || code == "" {
		return s
	}
	return code + s + reset
}

// levelFor grades a utilization against the thresholds that actually govern
// behaviour, rather than round numbers. Red means the daemon would act now;
// orange means it is close; yellow means it has started climbing; green means
// there is plenty of room.
//
// Tying the bands to switch_at keeps them meaningful when someone changes it: a
// trigger of 50 should make 45% orange, not green.
func levelFor(v, switchAt float64, severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return red
	case "warning":
		if v < switchAt*0.85 {
			return orange
		}
	}
	switch {
	case switchAt <= 0:
		return ""
	case v >= switchAt:
		return red
	case v >= switchAt*0.85:
		return orange
	case v >= switchAt*0.6:
		return yellow
	}
	return green
}

// colorState tints the verdict to match: anything that stops work is red, a
// held-back reserve is dim since it is a choice rather than a problem.
func colorState(s string) string {
	switch {
	case strings.HasPrefix(s, "ACTIVE"):
		return paint(bold, s)
	case strings.Contains(s, "refills in"):
		// Not red: red is reserved for "you have to do something about this",
		// and an account that refills within the hour is the one case where
		// doing nothing is a working plan.
		return paint(yellow, s)
	case strings.Contains(s, "no headroom"), strings.Contains(s, "refused"):
		return paint(red, s)
	case strings.Contains(s, "reserved"):
		return paint(grey, s)
	case strings.Contains(s, "not set up"), strings.Contains(s, "unknown"):
		return paint(dim, s)
	}
	return s
}

// visibleLen is a string's display width, ignoring ANSI escape sequences.
//
// Printf's %-12s counts bytes, and an escape code is bytes with no width, so a
// coloured cell padded by Printf comes out short and the whole column drifts.
// Every column here is padded with this instead.
func visibleLen(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			n++
		}
	}
	return n
}

// stripColor removes escape sequences, leaving the text. Used when a cell has
// to be recoloured wholesale: painting over an existing colour would leave the
// inner reset sequence in place, which ends the new colour partway through.
func stripColor(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// padRight pads to a display width, colour or not.
func padRight(s string, w int) string {
	if d := w - visibleLen(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// padLeft right-aligns to a display width, for numeric columns.
func padLeft(s string, w int) string {
	if d := w - visibleLen(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// Table is the shared layout used by every command that shows rows. Exported so
// `accounts`, `session` and `history` look like `status` rather than each
// inventing its own alignment and colour.
type Table = table

// NewTable starts a table. Columns named in right are right-aligned.
func NewTable(head []string, right ...int) *Table {
	r := map[int]bool{}
	for _, i := range right {
		r[i] = true
	}
	return &table{head: head, right: r}
}

// Add appends a row.
func (t *table) Add(cells ...string) { t.add(cells...) }

// Render writes the table with the given indent.
func (t *table) Render(indent string) string { return t.render(indent) }

// Dim, Bold, Grey and Good tint text with the same grammar status uses.
func Dim(s string) string  { return paint(dim, s) }
func Bold(s string) string { return paint(bold, s) }
func Grey(s string) string { return paint(grey, s) }
func Good(s string) string { return paint(green, s) }
func Warn(s string) string { return paint(orange, s) }
func Bad(s string) string  { return paint(red, s) }

// Level tints a utilization against the trigger, so a number means the same
// thing wherever it appears.
func Level(v, switchAt float64, severity string) string {
	return paint(levelFor(v, switchAt, severity), fmt.Sprintf("%.0f%%", v))
}

// Duration renders a span the way a person reads a clock.
func Duration(d time.Duration) string { return shortDur(d) }

// table lays out rows in columns sized to their widest cell, so the header and
// the data line up whatever colour is applied.
type table struct {
	head  []string
	rows  [][]string
	right map[int]bool // columns to right-align
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render(indent string) string {
	n := len(t.head)
	for _, r := range t.rows {
		if len(r) > n {
			n = len(r)
		}
	}
	widths := make([]int, n)
	for i, h := range t.head {
		widths[i] = visibleLen(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			if i < len(widths) && visibleLen(c) > widths[i] {
				widths[i] = visibleLen(c)
			}
		}
	}
	var b strings.Builder
	line := func(cells []string, dimmed bool) {
		b.WriteString(indent)
		for i, c := range cells {
			if i >= len(widths) {
				continue
			}
			if dimmed {
				c = paint(dim, c)
			}
			if t.right[i] {
				b.WriteString(padLeft(c, widths[i]))
			} else {
				b.WriteString(padRight(c, widths[i]))
			}
			if i < len(cells)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}
	if len(t.head) > 0 {
		line(t.head, true)
	}
	for _, r := range t.rows {
		line(r, false)
	}
	return b.String()
}
