package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
)

var escapes = regexp.MustCompile("\033\\[[0-9;]*m")

func TestHoursOfUseIsRoundedToWhatItCanSay(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2 * time.Minute:                 "~5m",
		44 * time.Minute:                "~45m",
		89 * time.Minute:                "~90m",
		90 * time.Minute:                "~2h",
		3*time.Hour + 48*time.Minute:    "~4h",
		62*time.Hour + 20*time.Minute:   "~62h",
		10*time.Hour + 29*time.Minute:   "~10h",
		10*time.Hour + 31*time.Minute:   "~11h",
		0:                               "~5m",
		time.Duration(-5 * time.Minute): "~5m",
	} {
		if got := HoursOfUse(d); got != want {
			t.Errorf("HoursOfUse(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestLastsSaysEachKind(t *testing.T) {
	for _, c := range []struct {
		e    drain.Estimate
		want string
	}{
		{drain.Estimate{Kind: drain.Hours, Left: 4 * time.Hour}, "~4h"},
		{drain.Estimate{Kind: drain.ToReset, Left: 40 * time.Hour}, "lasts to reset"},
		{drain.Estimate{Kind: drain.Idle}, "∞"},
		{drain.Estimate{Kind: drain.Drained}, "drained"},
		{drain.Estimate{Kind: drain.Unknown}, "-"},
		{drain.Estimate{Kind: drain.Hours, Left: 4 * time.Hour, Elsewhere: true}, "~4h ↑"},
		{drain.Estimate{Kind: drain.Drained, Elsewhere: true}, "drained ↑"},
	} {
		if got := LastsWord(c.e); got != c.want {
			t.Errorf("LastsWord(%+v) = %q, want %q", c.e, got, c.want)
		}
		SetDepth(Depth256)
		painted := Lasts(c.e)
		SetColor(false)
		if plain := escapes.ReplaceAllString(painted, ""); plain != c.want {
			t.Errorf("Lasts(%+v) reads %q, want %q", c.e, plain, c.want)
		}
	}
}

// A drained account is the one to act on; one under an hour is close.
func TestLastsIsPaintedByHowCloseItIs(t *testing.T) {
	SetDepth(Depth256)
	t.Cleanup(func() { SetColor(false) })
	if got := Lasts(drain.Estimate{Kind: drain.Drained}); !strings.HasPrefix(got, pal.Red) {
		t.Errorf("drained is not red: %q", got)
	}
	if got := Lasts(drain.Estimate{Kind: drain.Hours, Left: 40 * time.Minute}); !strings.HasPrefix(got, pal.Orange) {
		t.Errorf("under an hour is not orange: %q", got)
	}
	if got := Lasts(drain.Estimate{Kind: drain.Hours, Left: 5 * time.Hour}); strings.Contains(got, "\033[") {
		t.Errorf("hours to spare are painted: %q", got)
	}
}
