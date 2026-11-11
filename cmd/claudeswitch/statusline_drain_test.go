package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// statuslineDrain is the status line with account a (live, week at 20%,
// switch point 95, clearing in three days) measured by set.
func statuslineDrain(t *testing.T, set func(a *state.Account, now time.Time)) string {
	t.Helper()
	_, cfg, st := pinWorld(t, 40)
	set(st.Get("a"), time.Now())
	return captureStdout(t, func() error { return statuslineBody(cfg, st) })
}

// spent is a measured, moving speed.
func spent(perHour float64) func(*state.Account, time.Time) {
	return func(a *state.Account, now time.Time) {
		a.WeekSpeed, a.WeekSpeedAt, a.WeekRiseAt = perHour, now, now
	}
}

// The status line says how long the live account lasts, right after its week:
// the owner's status line reads the week by position, so the forecast follows
// it rather than coming between.
func TestStatuslineSaysHowLongTheAccountLasts(t *testing.T) {
	got := statuslineDrain(t, spent(10)) // 75 points at 10 an hour
	w := strings.Index(got, "week")
	if d := strings.Index(got, "~8h left"); w < 0 || d < w {
		t.Fatalf("no ~8h left after the week:\n%s", got)
	}
	m := strings.Index(got[w:], "%")
	if after := strings.TrimLeft(got[w+m+1:], " 0123456789dhm"); !strings.HasPrefix(after, "~8h left") {
		t.Errorf("the forecast does not follow the week straight away:\n%s", got)
	}
}

func TestStatuslineSaysEachForecast(t *testing.T) {
	for want, set := range map[string]func(*state.Account, time.Time){
		"lasts to reset": spent(0.5), // 150h of use; the week clears in 3 days
		"∞ left":         func(a *state.Account, now time.Time) { a.WeekSpeedAt = now },
	} {
		if got := statuslineDrain(t, set); !strings.Contains(got, want) {
			t.Errorf("no %q:\n%s", want, got)
		}
	}
}

// Unknown and drained say nothing: the week figure already says drained, and
// a guess is worse than a gap.
func TestStatuslineSaysNothingOfAnUnknownOrDrainedAccount(t *testing.T) {
	for name, set := range map[string]func(*state.Account, time.Time){
		"unknown": func(a *state.Account, _ time.Time) { a.WeekSpeed = 10 },
		"drained": func(a *state.Account, now time.Time) {
			spent(10)(a, now)
			*a.Last.SevenDay.Utilization = 96
		},
	} {
		if got := statuslineDrain(t, set); strings.Contains(got, " left") || strings.Contains(got, "lasts") {
			t.Errorf("%s: a forecast on the line:\n%s", name, got)
		}
	}
}
