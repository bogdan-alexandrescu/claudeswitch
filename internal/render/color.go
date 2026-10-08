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
// Turning colour on uses the depth the environment supports.
func SetColor(on bool) {
	useColor = on
	pal = PaletteFor(DepthFromEnv(os.Getenv))
}

// SetDepth turns colour on at a given depth, for tests and screenshots;
// DepthNone turns it off.
func SetDepth(d Depth) {
	useColor = d != DepthNone
	pal = PaletteFor(d)
}

// Depth is how many colours the terminal can show. The brand palette (see
// docs/BRAND.md) is defined at every depth, so the same role reads the same
// way whatever the terminal.
type Depth int

const (
	DepthNone Depth = iota
	Depth16
	Depth256
	DepthTrue
)

func (d Depth) String() string {
	switch d {
	case Depth16:
		return "16"
	case Depth256:
		return "256"
	case DepthTrue:
		return "truecolor"
	}
	return "none"
}

// DepthFromEnv is the colour depth a terminal advertises, ignoring whether
// colour is wanted at all (that is NO_COLOR's and the TTY check's business).
// COLORTERM is the one reliable truecolour signal; nearly everything else
// handles the 256-colour cube, so that is the default, and only a TERM known to
// stop at 16 colours gets the plain codes.
func DepthFromEnv(getenv func(string) string) Depth {
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return DepthTrue
	}
	term := strings.ToLower(getenv("TERM"))
	switch {
	case strings.Contains(term, "256color"), strings.Contains(term, "direct"):
		return Depth256
	case term == "linux", term == "ansi", term == "cons25", term == "xterm-color",
		term == "xterm-16color", strings.HasPrefix(term, "vt"):
		return Depth16
	}
	return Depth256
}

// Palette is the brand's colour roles as escape codes at one depth.
type Palette struct {
	Glacier   string // the accent: the mark, the active account, healthy bars
	Meltwater string // the second accent: healthy figures and outcomes
	Amber     string // climbing
	Orange    string // close to the trigger
	Red       string // refused, over the limit, act now
	Grey      string // secondary text
}

// PaletteFor returns the palette at a depth. The truecolour values are the
// brand's own; the 256-colour ones are the nearest the cube has; the 16-colour
// ones fall back to the colour each role is named for.
func PaletteFor(d Depth) Palette {
	switch d {
	case DepthTrue:
		return Palette{
			Glacier:   "\033[38;2;143;211;255m", // #8fd3ff
			Meltwater: "\033[38;2;182;240;220m", // #b6f0dc
			Amber:     "\033[38;2;240;199;94m",  // #f0c75e
			Orange:    "\033[38;2;255;160;102m", // #ffa066, between amber and red
			Red:       "\033[38;2;255;122;122m", // #ff7a7a
			Grey:      "\033[38;5;245m",
		}
	case Depth256:
		return Palette{
			Glacier:   "\033[38;5;117m",
			Meltwater: "\033[38;5;158m",
			Amber:     "\033[38;5;221m",
			Orange:    "\033[38;5;215m",
			Red:       "\033[38;5;203m",
			Grey:      "\033[38;5;245m",
		}
	case Depth16:
		return Palette{
			Glacier:   "\033[36m",
			Meltwater: "\033[32m",
			Amber:     "\033[33m",
			// No orange in sixteen colours: bold yellow keeps "close" apart
			// from "climbing" without borrowing red, which means act now.
			Orange: "\033[1;33m",
			Red:    "\033[31m",
			Grey:   "\033[90m",
		}
	}
	return Palette{}
}

// pal is the palette in use. Every colour below goes through it, so changing
// depth recolours the whole CLI at once.
var pal = PaletteFor(DepthFromEnv(os.Getenv))

const (
	reset = "\033[0m"
	bold  = "\033[1m"
	dim   = "\033[2m"
)

// Roles, by what they mean rather than by hue. A string selector rather than
// the code itself, so a depth change after start-up still applies.
const (
	glacier   = "glacier"
	meltwater = "meltwater"
	amber     = "amber"
	orange    = "orange"
	red       = "red"
	grey      = "grey"
)

// code resolves a role or a literal SGR sequence to an escape code.
func code(c string) string {
	switch c {
	case glacier:
		return pal.Glacier
	case meltwater:
		return pal.Meltwater
	case amber:
		return pal.Amber
	case orange:
		return pal.Orange
	case red:
		return pal.Red
	case grey:
		return pal.Grey
	}
	return c
}

func paint(c, s string) string {
	if !useColor || c == "" || s == "" {
		return s
	}
	return code(c) + s + reset
}

// levelFor grades a utilization against the thresholds that actually govern
// behaviour, rather than round numbers. Red means the daemon would act now;
// orange means it is close; amber means it has started climbing; meltwater means
// there is plenty of room.
//
// Tying the bands to switch_at keeps them meaningful when someone changes it: a
// trigger of 50 should make 45% orange, not meltwater.
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
		return amber
	}
	return meltwater
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
		return paint(amber, s)
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

// Dim, Bold, Grey, Good, Warn, Bad and Accent tint text with the same grammar status uses.
func Dim(s string) string  { return paint(dim, s) }
func Bold(s string) string { return paint(bold, s) }
func Grey(s string) string { return paint(grey, s) }
func Good(s string) string { return paint(meltwater, s) }
func Warn(s string) string { return paint(orange, s) }
func Bad(s string) string  { return paint(red, s) }
func Accent(s string) string {
	return paint(glacier, s)
}

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

// Mark is the brand mark in text: two concentric rings, as the app draws them.
const Mark = "◎"

// Header is the line `status`, `why` and `doctor` open with on a terminal:
// which build is speaking, whether a daemon is acting on what it shows, and how
// old the newest reading is. daemon is the phrase to show ("daemon live",
// "daemon not running"); a zero polled means nothing has been read yet.
func Header(version, daemon string, polled, now time.Time) string {
	age := "not polled yet"
	if !polled.IsZero() {
		age = "polled " + shortDur(now.Sub(polled)) + " ago"
		if now.Sub(polled) < time.Second {
			age = "polled just now"
		}
	}
	return "  " + paint(glacier, Mark+" claudeswitch") +
		paint(dim, " "+version+" · "+daemon+" · "+age) + "\n"
}
