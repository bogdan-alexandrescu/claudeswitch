package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 16: the app's "Switch to best" button names the account a manual
// switch would land on now.

func TestBestIsTheRoomiestOtherAccount(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(40, 30, now.Add(time.Hour)),
		"work-b":   reading(30, 30, now.Add(time.Hour)),
		"personal": reading(5, 5, now.Add(time.Hour)),
	})}
	if id, why := Best(in, "default"); id != "personal" || why != "" {
		t.Fatalf("Best = %q, %q; want personal", id, why)
	}
}

// A pin or the cooldown holds automatic rotation; a manual switch is what
// the person asks for, so neither hides the best account.
func TestBestIgnoresPinAndCooldown(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pinned: "work-a", LastSwitch: now.Add(-time.Minute),
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(40, 30, now.Add(time.Hour)),
			"work-b": reading(10, 10, now.Add(time.Hour)),
		})}
	if id, _ := Best(in, "default"); id != "work-b" {
		t.Fatalf("Best = %q, want work-b", id)
	}
}

// Owner decision (lane 16): Best holds targets to the landing margin like
// automatic rotation. With nothing clearing it, the best account is still
// offered, and best_why warns how little room it has.
func TestBestOffersAnAccountInsideTheMarginWithAWarning(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(80, 30, now.Add(time.Hour)),
	})}
	id, why := Best(in, "default")
	if id != "work-b" {
		t.Fatalf("Best = %q, want work-b (5 points of room)", id)
	}
	if !strings.Contains(why, "work-b's session window has only 5 points below its 85% trigger") ||
		!strings.Contains(why, "landing margin") {
		t.Fatalf("best_why = %q, want the margin warning", why)
	}
}

// The margin is a session-window rule (owner decision 2026-10-07): Best
// offers an account near its weekly trigger, with no warning, as long as it
// is under it.
func TestBestAppliesNoMarginToTheWeeklyWindow(t *testing.T) {
	c := cfg()
	c.SwitchAtWeekly = 98
	in := Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(10, 96, now.Add(time.Hour)), // 2 points under its weekly trigger
	})}
	if id, why := Best(in, "default"); id != "work-b" || why != "" {
		t.Fatalf("Best = %q, %q; want work-b with no warning", id, why)
	}
}

// An account clearing the margin needs no warning.
func TestBestClearOfTheMarginHasNoWarning(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": reading(90, 30, now.Add(time.Hour)),
		"work-b": reading(20, 30, now.Add(time.Hour)),
	})}
	if id, why := Best(in, "default"); id != "work-b" || why != "" {
		t.Fatalf("Best = %q, %q", id, why)
	}
}

// Never an account live (or maybe live) in another profile, or one that
// needs a sign-in: the caller names them in Unavailable.
func TestBestSkipsUnavailableAccounts(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now,
		Unavailable: map[string]string{"personal": "live in work"},
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(90, 30, now.Add(time.Hour)),
			"work-b":   reading(40, 30, now.Add(time.Hour)),
			"personal": reading(5, 5, now.Add(time.Hour)),
		})}
	if id, _ := Best(in, "default"); id != "work-b" {
		t.Fatalf("Best = %q, want work-b (personal is live elsewhere)", id)
	}
	in.Unavailable["work-b"] = "needs a sign-in"
	id, why := Best(in, "default")
	if id != "" || !strings.Contains(why, "personal: live in work") || !strings.Contains(why, "work-b: needs a sign-in") {
		t.Fatalf("Best = %q, %q", id, why)
	}
}

// The profile live on an account outside its pool (added there by hand,
// then moved to another profile's pool) moves off it.
func TestDecideMovesOffAnActiveAccountOutsideThePool(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pool: []string{"work-a", "work-b"},
		St: st("personal", map[string]*state.Account{
			"work-a":   reading(30, 30, now.Add(time.Hour)),
			"work-b":   reading(10, 10, now.Add(time.Hour)),
			"personal": reading(5, 5, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" || !strings.Contains(d.Reason, "personal is not in this profile's accounts") {
		t.Fatalf("got %v", d)
	}
}

func TestBestNoneWhenThePoolHasNoOtherAccount(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pool: []string{"work-a"},
		St: st("work-a", map[string]*state.Account{"work-a": reading(40, 30, now.Add(time.Hour))})}
	id, why := Best(in, "work")
	if id != "" || !strings.Contains(why, "work has no other account") {
		t.Fatalf("Best = %q, %q", id, why)
	}
}

func TestBestNoneWhenNothingElseHasRoom(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a":   reading(40, 30, now.Add(time.Hour)),
		"work-b":   reading(99, 30, now.Add(time.Hour)),
		"personal": reading(90, 30, now.Add(time.Hour)),
	})}
	id, why := Best(in, "work")
	if id != "" || !strings.Contains(why, "no other account in work has room") {
		t.Fatalf("Best = %q, %q", id, why)
	}
}
