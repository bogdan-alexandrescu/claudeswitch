package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// The pool line under the status table is the drain forecast (IMPROVEMENTS
// "Drain forecast"): how many hours of use the pool's room lasts at the live
// account's speed. The runway forecast (F2) keeps the per-account ETAs under
// --detail.

func poolStatus(p *drain.PoolEstimate, r *policy.Runway, profile string, detail bool) string {
	cfg, st := modelsWorld()
	var b bytes.Buffer
	o := Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: detail, Drain: p, Runway: r}
	if profile != "" {
		o.Profile, o.Pool = profile, []string{"work-a", "work-b"}
		st.Profiles[profile] = st.Default()
	}
	Status(&b, o)
	return b.String()
}

func TestStatusSaysHowLongThePoolLasts(t *testing.T) {
	p := &drain.PoolEstimate{Kind: drain.Hours, Left: 10*time.Hour + 40*time.Minute, Accounts: 5, PerHour: 6}
	if out := poolStatus(p, nil, "work", false); !strings.Contains(out, "work pool: ~11h of use left across 5 accounts") {
		t.Fatalf("no pool line:\n%s", out)
	}
	if out := poolStatus(p, nil, "", false); !strings.Contains(out, "  pool: ~11h of use left across 5 accounts") {
		t.Fatalf("no pool line for the implicit profile:\n%s", out)
	}
	if out := poolStatus(p, nil, "work", true); !strings.Contains(out, "work pool: ~11h of use left across 5 accounts") {
		t.Fatalf("no pool line under --detail:\n%s", out)
	}
	one := &drain.PoolEstimate{Kind: drain.Hours, Left: 40 * time.Minute, Accounts: 1, PerHour: 6}
	if out := poolStatus(one, nil, "work", false); !strings.Contains(out, "work pool: ~40m of use left in 1 account") {
		t.Fatalf("one account is not a pool of them:\n%s", out)
	}
}

// An account not being spent lasts for ever: the owner's ∞.
func TestStatusSaysAnIdlePoolIsInfinite(t *testing.T) {
	p := &drain.PoolEstimate{Kind: drain.Idle, Accounts: 2}
	if out := poolStatus(p, nil, "work", false); !strings.Contains(out, "work pool: ∞, not being spent (2 accounts with room)") {
		t.Fatalf("no idle line:\n%s", out)
	}
}

// Nothing is said of an unknown or drained pool in the compact view (the
// table and the headline already show a pool with no room); --detail says it.
func TestStatusSaysAnUnknownOrDrainedPoolOnlyUnderDetail(t *testing.T) {
	plain := poolStatus(nil, nil, "work", false)
	for _, k := range []drain.Kind{drain.Unknown, drain.Drained} {
		if out := poolStatus(&drain.PoolEstimate{Kind: k}, nil, "work", false); out != plain {
			t.Fatalf("%s changed the compact view:\n%s", k, out)
		}
	}
	if out := poolStatus(&drain.PoolEstimate{Kind: drain.Unknown, Accounts: 3}, nil, "work", true); !strings.Contains(out,
		"work pool: hours of use unknown (no speed measured lately)") {
		t.Fatalf("--detail does not say the forecast is unknown:\n%s", out)
	}
	if out := poolStatus(&drain.PoolEstimate{Kind: drain.Drained}, nil, "work", true); !strings.Contains(out,
		"work pool: drained, no account under its switch point") {
		t.Fatalf("--detail does not say the pool is drained:\n%s", out)
	}
}

// The runway's clock time is the daemon's warning now, not the status view's
// pool line; its per-account ETAs stay under --detail.
func TestTheRunwayNoLongerSpeaksForThePool(t *testing.T) {
	d := time.Now().Add(72 * time.Hour)
	dry := time.Date(d.Year(), d.Month(), d.Day(), 14, 0, 0, 0, time.Local)
	day := dry.Format("Mon") // not today: today reads "today 14:00"
	r := &policy.Runway{Kind: policy.RunwayDry, DryAt: dry, Pace: 1,
		TriggerAt: map[string]time.Time{"work-a": dry.Add(-time.Hour), "work-b": dry}}
	p := &drain.PoolEstimate{Kind: drain.Hours, Left: 4 * time.Hour, Accounts: 2, PerHour: 6}
	out := poolStatus(p, r, "work", false)
	if strings.Contains(out, "runs dry") || strings.Contains(out, "reaches its trigger") {
		t.Fatalf("the runway still speaks in the compact view:\n%s", out)
	}
	detail := poolStatus(p, r, "work", true)
	if !strings.Contains(detail, "reaches its trigger "+day+" 13:00 at this pace") || strings.Contains(detail, "runs dry") {
		t.Fatalf("--detail lost the ETAs or kept the runway's pool line:\n%s", detail)
	}
}
