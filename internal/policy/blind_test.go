package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// A2 (docs/IMPROVEMENTS.md): failover when blind. DESIGN 4.4 holds on an
// unknown active account; once its usage has been unreadable for
// blind_failover_polls consecutive polls, holding has stopped being safe.

func blindCfg() *config.Config {
	c := cfg()
	c.PollActive = config.Duration{Duration: 2 * time.Minute}
	return c
}

// blindActive is an account whose last good reading is old and whose polls
// have failed n times in a row since.
func blindActive(n int, five float64) *state.Account {
	a := reading(five, 20, now.Add(time.Hour))
	a.LastAt = now.Add(-10 * time.Minute)
	a.ReadFails = n
	a.LastErr = "usage API returned 500"
	return a
}

func TestTheDefaultBlindFailoverIsThreePolls(t *testing.T) {
	if got := cfg().BlindPolls(); got != 3 {
		t.Fatalf("BlindPolls() unset = %d, want 3", got)
	}
	zero := 0
	c := cfg()
	c.BlindFailoverPolls = &zero
	if got := c.BlindPolls(); got != 0 {
		t.Fatalf("explicit 0 read back as %d; zero must mean off", got)
	}
}

func TestBlindForThreePollsFailsOverToAHealthyAccount(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": blindActive(3, 40),
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b", d)
	}
	if d.Forced {
		t.Error("a blind failover must not be forced: it waits for an idle gap like any rotation")
	}
	if !strings.Contains(d.Reason, "unreadable") {
		t.Errorf("reason %q does not say the active account was unreadable", d.Reason)
	}
}

// Never read at all used to mean "hold" forever; blind for long enough it
// fails over too.
func TestNeverReadAndBlindFailsOver(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": {ID: "work-a", ReadFails: 3, LastErr: "usage API returned 500"},
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want switch to work-b", d)
	}
}

func TestFewerFailuresThanTheThresholdHold(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": blindActive(2, 40),
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay after two failed polls", d)
	}
}

func TestBlindFailoverZeroDisablesIt(t *testing.T) {
	c := blindCfg()
	zero := 0
	c.BlindFailoverPolls = &zero
	in := Input{Cfg: c, Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": blindActive(9, 40),
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay with blind_failover_polls = 0", d)
	}
}

// Three failures in quick succession (hot polling every 20 s) are not three
// polls' worth of blindness: the last good reading must also be at least that
// many active poll intervals old.
func TestAQuickBurstOfFailuresIsNotBlindness(t *testing.T) {
	a := blindActive(3, 40)
	a.LastAt = now.Add(-90 * time.Second) // under 3 × poll_active (6 min)
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay: the last reading is only 90 s old", d)
	}
}

// An access token that simply expired on an idle session is not trouble:
// Claude Code refreshes it on the next message.
func TestAnExpiredTokenOnAnIdleSessionIsNotBlindness(t *testing.T) {
	a := blindActive(5, 40)
	a.TokenExpiry = now.Add(-30 * time.Minute)
	a.LastErr = "usage API rejected the token (401): the account may need a re-login"
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	d := Decide(in)
	if d.Kind != Stay {
		t.Fatalf("got %v, want stay: expired token on an idle session", d)
	}
	if !strings.Contains(d.Reason, "expired") {
		t.Errorf("reason %q should say the token expired on an idle session", d.Reason)
	}

	// The same expired token while the session is busy is not explained by
	// idleness: Claude Code would have refreshed it to keep working.
	in.SessionBusy = true
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("busy: got %v, want switch to work-b", d)
	}
}

// A token that has not expired explains nothing, idle or not.
func TestAValidTokenOnAnIdleSessionStillFailsOver(t *testing.T) {
	a := blindActive(3, 40)
	a.TokenExpiry = now.Add(3 * time.Hour)
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Switch {
		t.Fatalf("got %v, want switch", d)
	}
}

// The target must be healthy: readable, and under its trigger by the landing
// margin. Failing over onto an account that is itself unreadable, or nearly
// full, trades one problem for another — hold instead, and never as a Wait.
func TestBlindFailoverNeedsAHealthyTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target *state.Account
	}{
		{"inside the margin", reading(80, 20, now.Add(time.Hour))},
		{"itself unreadable", func() *state.Account {
			a := reading(10, 10, now.Add(time.Hour))
			a.ReadFails = 1
			return a
		}()},
		{"never read", &state.Account{ID: "work-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
				"work-a": blindActive(3, 40),
				"work-b": tc.target,
			})}
			d := Decide(in)
			if d.Kind != Stay {
				t.Fatalf("got %v, want stay: no healthy account to fail over to", d)
			}
			if !strings.Contains(d.Reason, "unreadable") {
				t.Errorf("reason %q should still say the active account is unreadable", d.Reason)
			}
		})
	}
}

func TestBlindFailoverRespectsTheCooldown(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now, LastSwitch: now.Add(-time.Minute),
		St: st("work-a", map[string]*state.Account{
			"work-a": blindActive(3, 40),
			"work-b": reading(20, 20, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay inside the cooldown", d)
	}
}

// Rate limiting is not blindness: ReadFails does not count 429s (the poller's
// job), so an account refused only by the usage endpoint keeps its count at 0
// and is held, as DESIGN 4.4 always said.
func TestARateLimitedActiveIsHeld(t *testing.T) {
	a := reading(40, 20, now.Add(time.Hour))
	a.LastAt = now.Add(-12 * time.Minute)
	a.LastErr = "usage API rate limited us"
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": a,
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay", d)
	}
}

func TestWhySaysTheActiveAccountIsBlind(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now, St: st("work-a", map[string]*state.Account{
		"work-a": blindActive(4, 40),
		"work-b": reading(20, 20, now.Add(time.Hour)),
	})}
	_, verdicts := Explain(in)
	if len(verdicts) == 0 || verdicts[0].ID != "work-a" {
		t.Fatalf("verdicts = %+v", verdicts)
	}
	if !strings.Contains(verdicts[0].Why, "unreadable for 4") {
		t.Errorf("active verdict %q should say it has been unreadable for 4 polls", verdicts[0].Why)
	}
}
