package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
)

// IMPROVEMENTS F2 in the status view: one pool line per profile, and the
// per-account ETAs under --detail.

func runwayStatus(r *policy.Runway, profile string, detail bool) string {
	cfg, st := modelsWorld()
	var b bytes.Buffer
	o := Options{Cfg: cfg, St: st, DaemonOwns: true, Detail: detail, Runway: r}
	if profile != "" {
		o.Profile, o.Pool = profile, []string{"work-a", "work-b"}
		st.Profiles[profile] = st.Default()
	}
	Status(&b, o)
	return b.String()
}

func TestStatusSaysWhenThePoolRunsDry(t *testing.T) {
	d := time.Now().Add(72 * time.Hour)
	dry := time.Date(d.Year(), d.Month(), d.Day(), 14, 0, 0, 0, time.Local)
	day := dry.Format("Mon") // not today: today reads "today 14:00"
	r := &policy.Runway{Kind: policy.RunwayDry, DryAt: dry, Pace: 1,
		TriggerAt: map[string]time.Time{"work-a": dry.Add(-time.Hour), "work-b": dry}}
	out := runwayStatus(r, "work", false)
	if !strings.Contains(out, "work pool: runs dry "+day+" 14:00 at this pace") {
		t.Fatalf("no pool line:\n%s", out)
	}
	if strings.Contains(out, "reaches its trigger") {
		t.Fatalf("per-account ETAs outside --detail:\n%s", out)
	}
	if out := runwayStatus(r, "", false); !strings.Contains(out, "pool: runs dry "+day+" 14:00 at this pace") {
		t.Fatalf("no pool line for the implicit profile:\n%s", out)
	}
	detail := runwayStatus(r, "work", true)
	if !strings.Contains(detail, "reaches its trigger "+day+" 13:00 at this pace") ||
		!strings.Contains(detail, "work pool: runs dry "+day+" 14:00 at this pace") {
		t.Fatalf("--detail lacks the ETAs or the pool line:\n%s", detail)
	}
}

func TestStatusSaysWhenAResetComesFirst(t *testing.T) {
	d := time.Now().Add(72 * time.Hour)
	at := time.Date(d.Year(), d.Month(), d.Day(), 15, 30, 0, 0, time.Local)
	r := &policy.Runway{Kind: policy.RunwayRefills, RefillsAt: at, RefillsAccount: "work-b", Pace: 1}
	out := runwayStatus(r, "work", false)
	if !strings.Contains(out, "work pool: does not run dry before work-b resets "+at.Format("Mon")+" 15:30 at this pace") {
		t.Fatalf("no refill line:\n%s", out)
	}
}

// Nothing is said without a forecast in the compact view (unknown is not
// shown as a guess, nor as an empty line); --detail says why there is none.
func TestStatusSaysNothingOfAnUnknownRunway(t *testing.T) {
	plain := runwayStatus(nil, "work", false)
	for _, r := range []*policy.Runway{{Kind: policy.RunwayUnknown}, {Kind: policy.RunwayIdle}} {
		if out := runwayStatus(r, "work", false); out != plain {
			t.Fatalf("%s changed the compact view:\n%s", r.Kind, out)
		}
	}
	if out := runwayStatus(&policy.Runway{Kind: policy.RunwayUnknown}, "work", true); !strings.Contains(out,
		"work pool: runway unknown (too few recent readings)") {
		t.Fatalf("--detail does not say the runway is unknown:\n%s", out)
	}
	if out := runwayStatus(&policy.Runway{Kind: policy.RunwayIdle}, "work", true); !strings.Contains(out,
		"work pool: not being spent") {
		t.Fatalf("--detail does not say the pool is idle:\n%s", out)
	}
}
