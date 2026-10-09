package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// F3: the pin safety valve. A pin holds automatic rotation; a pinned account
// that has been refused, is out of quota or needs a sign-in no longer can,
// so the pin is lifted and rotation goes on as usual. A hard pin stays.

func pinnedWorld(pinned *state.Account) Input {
	return Input{Cfg: preferCfg(""), Now: now, Pinned: "work-1", St: st("work-1", map[string]*state.Account{
		"work-1":    pinned,
		"work-team": weekly(10, 10, now.Add(2*time.Hour), now.Add(3*24*time.Hour)),
	})}
}

func TestAPinHoldsAnAccountThatCanStillServe(t *testing.T) {
	d := Decide(pinnedWorld(weekly(90, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))))
	if d.Kind != Stay || d.Unpin != "" || !strings.Contains(d.Reason, "pinned to work-1") {
		t.Fatalf("got %v (unpin %q), want the pin to hold at 90%%", d, d.Unpin)
	}
}

func TestAPinOnARefusedAccountIsLifted(t *testing.T) {
	a := weekly(60, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	a.BurntTil, a.BurntWin = now.Add(time.Hour), "five_hour"
	d := Decide(pinnedWorld(a))
	if d.Kind != Switch || d.Target != "work-team" {
		t.Fatalf("got %v, want a switch to work-team", d)
	}
	if want := "pin on work-1 lifted: it was refused"; d.Unpin != want || !strings.HasPrefix(d.Reason, want) {
		t.Fatalf("unpin %q, reason %q; want %q", d.Unpin, d.Reason, want)
	}
}

func TestAPinOnAnAccountAtOneHundredIsLifted(t *testing.T) {
	d := Decide(pinnedWorld(weekly(100, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))))
	if d.Kind != Switch || d.Unpin != "pin on work-1 lifted: it is out of quota" {
		t.Fatalf("got %v (unpin %q)", d, d.Unpin)
	}
	d = Decide(pinnedWorld(weekly(20, 100, now.Add(time.Hour), now.Add(3*24*time.Hour))))
	if d.Kind != Switch || d.Unpin == "" {
		t.Fatalf("weekly at 100%%: got %v (unpin %q), want the pin lifted", d, d.Unpin)
	}
}

// A reading at 100% whose window has since reset says nothing about now.
func TestAPinIsNotLiftedOnAReadingThatOutlivedItsWindow(t *testing.T) {
	a := weekly(100, 50, now.Add(-time.Minute), now.Add(3*24*time.Hour))
	a.LastAt = now.Add(-time.Hour)
	if d := Decide(pinnedWorld(a)); d.Unpin != "" || d.Kind != Stay {
		t.Fatalf("got %v (unpin %q), want the pin to hold", d, d.Unpin)
	}
}

func TestAPinOnAnAccountNeedingASignInIsLifted(t *testing.T) {
	a := weekly(40, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	a.RefreshExpiry = now.Add(-time.Minute)
	d := Decide(pinnedWorld(a))
	if d.Unpin != "pin on work-1 lifted: it needs a sign-in" {
		t.Fatalf("expired refresh token: unpin %q", d.Unpin)
	}
	// With the pin lifted, rotation runs as usual: work-1 is under its
	// trigger, so it stays.
	if d.Kind != Stay {
		t.Fatalf("got %v, want the usual decision (stay, under the trigger)", d)
	}

	a = weekly(40, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	a.LastErr, a.ReadFails = "usage API: 401 Unauthorized", 1
	if d := Decide(pinnedWorld(a)); d.Unpin != "pin on work-1 lifted: it needs a sign-in" {
		t.Fatalf("401 counted as a failed read: unpin %q", d.Unpin)
	}
}

// A 401 that was not counted as a failed read is a stale vault copy that
// the daemon re-captures (DESIGN 4.4): no evidence the account needs anyone.
func TestAPinIsNotLiftedOnAStaleCopy401(t *testing.T) {
	a := weekly(40, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	a.LastErr = "usage API: 401 Unauthorized"
	if d := Decide(pinnedWorld(a)); d.Unpin != "" {
		t.Fatalf("unpin %q on an uncounted 401", d.Unpin)
	}
}

func TestAHardPinStaysEvenThen(t *testing.T) {
	a := weekly(100, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	a.BurntTil, a.BurntWin = now.Add(time.Hour), "five_hour"
	in := pinnedWorld(a)
	in.PinHard = true
	if d := Decide(in); d.Kind != Stay || d.Unpin != "" {
		t.Fatalf("hard pin: got %v (unpin %q), want stay", d, d.Unpin)
	}
}

func TestPinLiftedIsOneDefinition(t *testing.T) {
	a := weekly(60, 50, now.Add(time.Hour), now.Add(3*24*time.Hour))
	if why := PinLifted(a, now); why != "" {
		t.Fatalf("PinLifted = %q for a healthy account", why)
	}
	if why := PinLifted(nil, now); why != "" {
		t.Fatalf("PinLifted(nil) = %q: no record is no evidence", why)
	}
	a.BurntTil = now.Add(time.Minute)
	if why := PinLifted(a, now); why != "it was refused" {
		t.Fatalf("PinLifted = %q", why)
	}
}
