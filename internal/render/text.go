package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// Text for people, shared by every command so they say things the same way:
// the terminal's width, dates, a line cut to fit, and the names of the two
// windows.

// Width is the output terminal's width in columns: COLUMNS when set, else
// the terminal's own, else DefaultWidth.
func Width() int { return terminalWidth() }

// When is a moment as a person reads it, in one of two forms: within six days
// either side of today, "today 15:04" or "Sun 23:42"; further off,
// "28 Oct 02:36", with the year when it is not this one ("28 Oct 2025
// 02:36"). A weekday further than that names two days at once, and "10-28"
// reads as a range or a score.
func When(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	day := func(x time.Time) time.Time { y, m, d := x.Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }
	days := int(day(t).Sub(day(now)).Round(24*time.Hour) / (24 * time.Hour))
	switch {
	case days == 0:
		return "today " + t.Format("15:04")
	case days >= -6 && days <= 6:
		return t.Format("Mon 15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan 15:04")
	}
	return t.Format("2 Jan 2006 15:04")
}

// Fit cuts s to w columns, ending in "…" when it had to cut. Colour escapes
// take no columns and are kept, and a cut inside a coloured run closes it, so
// a cut never leaves the rest of the screen tinted. It counts runes, never
// bytes: slicing bytes split "→" and "…" into invalid UTF-8.
func Fit(s string, w int) string {
	if w <= 0 || visibleLen(s) <= w {
		return s
	}
	var b strings.Builder
	n, inEscape, coloured := 0, false, false
	for _, r := range s {
		switch {
		case inEscape:
			b.WriteRune(r)
			if r == 'm' {
				inEscape = false
			}
			continue
		case r == '\033':
			inEscape, coloured = true, true
			b.WriteRune(r)
			continue
		}
		if n == w-1 {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString("…")
	if coloured && useColor {
		b.WriteString(reset)
	}
	return b.String()
}

// Wrap breaks s into lines of at most width columns, at spaces. Colour
// escapes take no columns; a colour running across a break is closed at the
// end of one line and opened again at the start of the next, so a broken
// line never tints what follows it. Runs of spaces become one, and a word
// longer than width has a line to itself.
func Wrap(s string, width int) []string {
	if width < 1 || visibleLen(s) <= width {
		return []string{strings.ReplaceAll(s, nbsp, " ")}
	}
	lines := wrapWords(glueCommands(s, width), width)
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, nbsp, " ")
	}
	return lines
}

// nbsp holds words together while wrapping; it is a space again after.
const nbsp = "\u00a0"

// glueCommands keeps each "claudeswitch …" command, up to the end of its
// clause, on one line: it is what a reader copies, and a break inside it
// copies half a command.
func glueCommands(s string, width int) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "claudeswitch ")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i:]
		end := len(s)
		for _, stop := range []string{";", ",", ")", " (", " — ", " · "} {
			if j := strings.Index(s, stop); j >= 0 && j < end {
				end = j
			}
		}
		cmd := s[:end]
		if visibleLen(cmd) <= width {
			cmd = strings.ReplaceAll(cmd, " ", nbsp)
		}
		b.WriteString(cmd)
		s = s[end:]
	}
}

// WrapPhrases is Wrap for a list of phrases joined with " · ": it breaks
// between phrases, ending a line with the "·", and within one only when it
// is wider than a line. "~65% would expire unused at reset" is one thing to
// read, not five words.
func WrapPhrases(s string, width int) []string {
	if width < 1 || visibleLen(s) <= width {
		return []string{s}
	}
	parts := strings.Split(s, " · ")
	for i, p := range parts {
		if visibleLen(p) < width {
			parts[i] = strings.ReplaceAll(p, " ", nbsp)
		}
	}
	return Wrap(strings.Join(parts, nbsp+"· "), width)
}

// wrapWords is Wrap's line breaking, at spaces.
func wrapWords(s string, width int) []string {
	var lines []string
	var line strings.Builder
	n, active, carry := 0, "", "" // columns so far; the colour in force; escapes owed to the next word
	flush := func() {
		if active != "" {
			line.WriteString(reset)
		}
		lines = append(lines, line.String())
		line.Reset()
		n = 0
		if active != "" {
			line.WriteString(active)
		}
	}
	write := func(word string) {
		line.WriteString(word)
		// The colour this word leaves in force.
		for rest := word; ; {
			i := strings.Index(rest, "\033[")
			if i < 0 {
				break
			}
			j := strings.IndexByte(rest[i:], 'm')
			if j < 0 {
				break
			}
			if code := rest[i : i+j+1]; code == reset {
				active = ""
			} else {
				active = code
			}
			rest = rest[i+j+1:]
		}
	}
	for _, word := range strings.Split(s, " ") {
		switch w := visibleLen(word); {
		case word == "":
			continue
		case w == 0:
			// An escape alone opens or closes a colour: it goes with the
			// next word, so a break never strands it at a line's end.
			carry += word
			continue
		default:
			if n > 0 && n+1+w > width {
				flush()
			}
			if n > 0 {
				line.WriteString(" ")
				n++
			}
			write(carry + word)
			carry, n = "", n+w
		}
	}
	if carry != "" {
		write(carry)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

// WindowName is a window's name in text for people: "session" for the
// five-hour window, "week" for the weekly one (the app's words). The API's
// keys, five_hour and seven_day, stay in JSON.
func WindowName(key string) string {
	switch key {
	case usage.FiveHourKey, "session":
		return "session"
	case usage.SevenDayKey, "weekly", "weekly_all":
		return "week"
	}
	return key
}

// Thresholds is a profile's two triggers in words: "rotates at session
// ≥85% / week ≥98%". Two bare figures did not say which window was which.
func Thresholds(cfg *config.Config) string {
	return fmt.Sprintf("rotates at session ≥%.0f%% / week ≥%.0f%%",
		cfg.TriggerFor(usage.FiveHourKey), cfg.TriggerFor(usage.SevenDayKey))
}

// Timing is when a rotation goes, from switch_when and max_switch_wait: "at
// an idle gap, or mid-turn after 30s". "swap idle, forced after 30s"
// named the settings, not what they do.
func Timing(cfg *config.Config) string {
	if cfg.SwitchWhen == "immediate" {
		return "at once, mid-turn if need be"
	}
	return "at an idle gap, or mid-turn after " + span(cfg.MaxSwitchWait.Duration)
}

// HardFloor is what the hard floor does: "past 98% (the hard floor),
// mid-turn at once".
func HardFloor(cfg *config.Config) string {
	return fmt.Sprintf("past %.0f%% (the hard floor), mid-turn at once", cfg.HardFloor)
}

// Joined is parts joined with " · " on lines that begin with indent, broken
// between parts so no line passes width; a part longer than a line has one
// to itself.
func Joined(parts []string, indent string, width int) string {
	var b strings.Builder
	line := ""
	for _, p := range parts {
		switch {
		case line == "":
			line = indent + p
		case visibleLen(line)+3+visibleLen(p) > width:
			b.WriteString(line + "\n")
			line = indent + p
		default:
			line += " · " + p
		}
	}
	if line != "" {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Plural is n and the noun, in the singular for one: "1 reading",
// "3 readings". plural is the noun's plural form.
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
