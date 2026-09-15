package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// slTestCfg mirrors the shipped defaults: rotate at 85, swap mid-turn at 96.
// (rotate_test.go already defines testCfg for this package.)
// Pinned rather than taken from the defaults: these tests are about where the
// bands fall relative to the thresholds, and the cases below name the boundary
// numbers outright. Reading the thresholds from config.Default* made every one
// of them fail the day a default moved, for no reason connected to what they
// check.
func slTestCfg() *config.Config {
	return &config.Config{SwitchAt: 85, HardFloor: 96}
}

func TestSlBarClampsAndFills(t *testing.T) {
	for _, c := range []struct {
		pct  float64
		want string
	}{
		{0, "░░░░░░░░░░"},
		{50, "▓▓▓▓▓░░░░░"},
		{76, "▓▓▓▓▓▓▓▓░░"},
		{100, "▓▓▓▓▓▓▓▓▓▓"},
		{150, "▓▓▓▓▓▓▓▓▓▓"}, // over-range must not overflow the field
		{-20, "░░░░░░░░░░"}, // nor underflow it
	} {
		if got := slBar(c.pct, 10); got != c.want {
			t.Errorf("slBar(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestSlBarWidthIsStableInRunes(t *testing.T) {
	// The status line is a fixed-width surface; a bar that changes visual width
	// with the value would make the rest of the line jump around.
	for _, v := range []float64{0, 1, 33, 67, 99, 100} {
		if n := len([]rune(slBar(v, 10))); n != 10 {
			t.Errorf("slBar(%v) is %d runes, want 10", v, n)
		}
	}
}

func TestSlUntil(t *testing.T) {
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { u := now.Add(d); return &u }
	var zero time.Time
	for _, c := range []struct {
		name string
		in   *time.Time
		want string
	}{
		{"nil", nil, "?"},
		{"zero", &zero, "?"},
		{"past", at(-time.Hour), "now"},
		{"exactly now", at(0), "now"},
		{"minutes", at(40 * time.Minute), "40m"},
		{"hours and minutes", at(3*time.Hour + 39*time.Minute), "3h39m"},
		{"pads minutes", at(3*time.Hour + 5*time.Minute), "3h05m"},
		{"whole days", at(5 * 24 * time.Hour), "5d"},
		{"days and hours", at(5*24*time.Hour + 9*time.Hour), "5d9h"},
	} {
		if got := slUntil(c.in, now); got != c.want {
			t.Errorf("%s: slUntil = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSlAge(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{22 * time.Minute, "22m"},
		{time.Hour + 5*time.Minute, "1h05m"},
	} {
		if got := slAge(c.in); got != c.want {
			t.Errorf("slAge(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlWindowUnknownDoesNotRenderAZeroBar(t *testing.T) {
	// A window with no reading must not look like a window reading 0%.
	got := slWindow("session", usage.Window{}, false, "", time.Now(), slTestCfg(), false)
	if got != "session no reading" {
		t.Errorf("unknown window rendered %q", got)
	}
}

func TestSlWindowMarksBindingAndTrend(t *testing.T) {
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	u := 76.0
	r := now.Add(3*time.Hour + 39*time.Minute)
	w := usage.Window{Utilization: &u, ResetsAt: &r}

	plain := slWindow("session", w, false, "", now, slTestCfg(), false)
	if plain != "session ▓▓▓▓▓▓▓▓░░ 76% 3h39m" {
		t.Errorf("plain = %q", plain)
	}
	marked := slWindow("session", w, true, "↑", now, slTestCfg(), false)
	if marked != "▸session ▓▓▓▓▓▓▓▓░░ 76%↑ 3h39m" {
		t.Errorf("marked = %q", marked)
	}
}

func TestSlBandTracksConfiguredThresholds(t *testing.T) {
	cfg := slTestCfg() // 85 / 96
	for _, c := range []struct {
		pct  float64
		want string
		name string
	}{
		{0, ansiGreen, "idle"},
		{59.9, ansiGreen, "just under yellow"},
		{60, ansiYellow, "yellow boundary"},
		{84.9, ansiYellow, "just under switch"},
		{85, ansiOrange, "at switch_at"},
		{95.9, ansiOrange, "just under hard floor"},
		{96, ansiRed, "at hard_floor"},
		{100, ansiRed, "exhausted"},
	} {
		if got := slBand(c.pct, cfg); got != c.want {
			t.Errorf("%s: slBand(%v) wrong band", c.name, c.pct)
		}
	}
}

func TestSlBandFollowsConfigNotHardcodedNumbers(t *testing.T) {
	// A user who lowers switch_at must see orange earlier, or the colour and the
	// appended "rotating soon" would disagree.
	cfg := &config.Config{SwitchAt: 50, HardFloor: 70}
	if slBand(55, cfg) != ansiOrange {
		t.Error("55%% with switch_at=50 should be orange")
	}
	if slBand(75, cfg) != ansiRed {
		t.Error("75%% with hard_floor=70 should be red")
	}
}

func TestSlPaintIsInertWhenColourOff(t *testing.T) {
	if got := slPaint(ansiRed, "97%", false); got != "97%" {
		t.Errorf("colour off should pass text through, got %q", got)
	}
	if got := slPaint(ansiRed, "97%", true); got != ansiRed+"97%"+ansiReset {
		t.Errorf("colour on should wrap and reset, got %q", got)
	}
}

func TestSlWindowColouredResetsAfterEveryRun(t *testing.T) {
	// An unreset escape bleeds into the rest of Claude Code's status line.
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	u := 97.0
	r := now.Add(12 * time.Minute)
	got := slWindow("session", usage.Window{Utilization: &u, ResetsAt: &r}, true, "↑", now, slTestCfg(), true)
	// slPaint emits colour + text + reset, so every escape sequence in the output
	// must be one of a matched pair. An unreset run bleeds into the rest of the
	// host status line.
	escapes := strings.Count(got, "\033[")
	resets := strings.Count(got, ansiReset)
	if escapes != 2*resets {
		t.Errorf("unbalanced escapes: %d sequences, %d resets, in %q", escapes, resets, got)
	}
	if !strings.Contains(got, ansiRed) {
		t.Errorf("97%% should render red, got %q", got)
	}
}
