package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// D34 (docs/PROFILES.md §10): a shared account is used by one profile at a
// time. Rotation treats an account live in another profile as unavailable
// and takes the next candidate. The caller names those accounts in
// Input.Unavailable; Decide, failover and Explain honour it as Best and
// Forecast always have.

func TestDecideTakesTheNextCandidateWhenTheBestIsInUseElsewhere(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now,
		Unavailable: map[string]string{"personal": "in use by work"},
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(91, 30, now.Add(time.Hour)),
			"work-b":   reading(20, 20, now.Add(time.Hour)),
			"personal": reading(5, 5, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-b" {
		t.Fatalf("got %v, want work-b: personal is in use by work", d)
	}
}

// With nothing else, the profile waits, and says which account it could
// not take and why.
func TestDecideWaitsWhenTheOnlyRoomIsInUseElsewhere(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now, Pool: []string{"work-a", "work-b"},
		Unavailable: map[string]string{"work-b": "in use by work"},
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(91, 30, now.Add(time.Hour)),
			"work-b": reading(20, 20, now.Add(time.Hour)),
		})}
	d := Decide(in)
	if d.Kind == Switch {
		t.Fatalf("got %v; work-b is in use by work", d)
	}
	if !strings.Contains(d.Reason, "work-b: in use by work") {
		t.Fatalf("reason %q does not say work-b is in use elsewhere", d.Reason)
	}
}

func TestDecideWithNoActiveAccountSkipsOneInUseElsewhere(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now,
		Unavailable: map[string]string{"work-b": "in use by work"},
		St: st("", map[string]*state.Account{
			"work-a": reading(40, 30, now.Add(time.Hour)),
			"work-b": reading(5, 5, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "work-a" {
		t.Fatalf("got %v, want work-a", d)
	}
	// Past the margin fallback too: an account short of the margin but in
	// use elsewhere is not taken either.
	in.St.Accounts["work-a"] = reading(99, 30, now.Add(time.Hour))
	in.St.Accounts["work-b"] = reading(80, 30, now.Add(time.Hour))
	if d := Decide(in); d.Kind == Switch {
		t.Fatalf("got %v; work-b is in use by work and work-a is over", d)
	}
}

func TestBlindFailoverSkipsAnAccountInUseElsewhere(t *testing.T) {
	in := Input{Cfg: blindCfg(), Now: now,
		Unavailable: map[string]string{"work-b": "in use by work"},
		St: st("work-a", map[string]*state.Account{
			"work-a":   blindActive(3, 40),
			"work-b":   reading(5, 5, now.Add(time.Hour)),
			"personal": reading(20, 20, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Switch || d.Target != "personal" || !d.Failover {
		t.Fatalf("got %v, want a failover to personal", d)
	}
	in.Unavailable["personal"] = "in use by research"
	if d := Decide(in); d.Kind != Stay || !strings.Contains(d.Reason, "no healthy account") {
		t.Fatalf("got %v, want a hold: every healthy account is in use elsewhere", d)
	}
}

// `why` says an account is in use elsewhere, never that it is ready.
func TestExplainSaysAnAccountIsInUseElsewhere(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now,
		Unavailable: map[string]string{"personal": "in use by work"},
		St: st("work-a", map[string]*state.Account{
			"work-a":   reading(91, 30, now.Add(time.Hour)),
			"work-b":   reading(20, 20, now.Add(time.Hour)),
			"personal": reading(5, 5, now.Add(time.Hour)),
		})}
	dec, vs := Explain(in)
	if dec.Target != "work-b" {
		t.Fatalf("decision %v, want work-b", dec)
	}
	for _, v := range vs {
		if v.ID != "personal" {
			continue
		}
		if v.Eligible || v.Why != "in use by work" {
			t.Fatalf("personal: %+v, want not eligible, \"in use by work\"", v)
		}
		return
	}
	t.Fatal("personal is not listed")
}

// The active account is this profile's own: naming it unavailable changes
// nothing about it.
func TestUnavailableNeverAppliesToTheActiveAccount(t *testing.T) {
	in := Input{Cfg: cfg(), Now: now,
		Unavailable: map[string]string{"work-a": "in use by work"},
		St: st("work-a", map[string]*state.Account{
			"work-a": reading(40, 30, now.Add(time.Hour)),
			"work-b": reading(20, 20, now.Add(time.Hour)),
		})}
	if d := Decide(in); d.Kind != Stay {
		t.Fatalf("got %v, want stay", d)
	}
	_, vs := Explain(in)
	if !vs[0].Active || !vs[0].Eligible || strings.Contains(vs[0].Why, "in use") {
		t.Fatalf("active verdict %+v", vs[0])
	}
}
