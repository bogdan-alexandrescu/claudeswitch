package render

import (
	"fmt"
	"math"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
)

// HoursOfUse is a drain forecast's time as a person reads it: "~45m" to the
// nearest five minutes under an hour and a half, "~4h" to the nearest hour
// past that. It is a forecast, so it never says more than it knows.
func HoursOfUse(d time.Duration) string {
	if d < 90*time.Minute {
		return fmt.Sprintf("~%dm", max(5, int(math.Round(d.Minutes()/5))*5))
	}
	return fmt.Sprintf("~%dh", int(math.Round(d.Hours())))
}

// ElsewhereMark is what follows an account's forecast while it climbs with no
// profile here using it.
const ElsewhereMark = "↑"

// LastsWord is an account's drain forecast in words: "~4h" of use left,
// "lasts to reset", "∞" (not being spent), "drained", or "-" (unknown), with
// ElsewhereMark when someone elsewhere is spending it.
func LastsWord(e drain.Estimate) string {
	s := lastsBare(e)
	if e.Elsewhere {
		s += " " + ElsewhereMark
	}
	return s
}

func lastsBare(e drain.Estimate) string {
	switch e.Kind {
	case drain.Hours:
		return HoursOfUse(e.Left)
	case drain.ToReset:
		return "lasts to reset"
	case drain.Idle:
		return "∞"
	case drain.Drained:
		return "drained"
	}
	return "-"
}

// Lasts is LastsWord painted: drained in red, under an hour in orange,
// lasting to the reset in meltwater, idle and unknown in grey.
func Lasts(e drain.Estimate) string {
	s := lastsBare(e)
	switch {
	case e.Kind == drain.Drained:
		s = paint(red, s)
	case e.Kind == drain.Hours && e.Left < time.Hour:
		s = paint(orange, s)
	case e.Kind == drain.ToReset:
		s = paint(meltwater, s)
	case e.Kind == drain.Idle, e.Kind == drain.Unknown:
		s = paint(grey, s)
	}
	if e.Elsewhere {
		s += " " + paint(amber, ElsewhereMark)
	}
	return s
}
