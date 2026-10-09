package policy

import (
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// F2: the pool runway forecast. Unknown stays unknown: a forecast needs
// recent readings, and never guesses from too few.

// burning is an account read now at five/seven, whose previous reading
// (span earlier) was rise points lower on its worst window.
func burning(five, seven, rise float64, span time.Duration) *state.Account {
	a := weekly(five, seven, now.Add(4*time.Hour), now.Add(5*24*time.Hour))
	_, worst := a.Last.Worst()
	a.PrevWorst, a.PrevAt = worst-rise, now.Add(-span)
	return a
}

func runwayWorld(active *state.Account, others map[string]*state.Account) Input {
	accts := map[string]*state.Account{"work-1": active}
	pool := []string{"work-1"}
	for _, id := range []string{"work-team", "research"} {
		if a, ok := others[id]; ok {
			accts[id] = a
			pool = append(pool, id)
		}
	}
	return Input{Cfg: preferCfg(""), Now: now, St: st("work-1", accts), Pool: pool}
}

func TestRunwayOfAOneAccountPool(t *testing.T) {
	// 45 points below the 85% trigger at 1 point a minute: 45 minutes.
	r := Forecast(runwayWorld(burning(40, 10, 3, 3*time.Minute), nil))
	if r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("got %+v, want dry in 45m", r)
	}
	if at, ok := r.TriggerAt["work-1"]; !ok || !at.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("trigger_at work-1 = %v, %v; want in 45m", at, ok)
	}
}

// The pool is spent in the order rotation would spend it, at the pace the
// account in use is burning: work-1 to its trigger, then the roomiest
// eligible account, then the next.
func TestRunwaySpendsThePoolInRotationOrder(t *testing.T) {
	in := runwayWorld(burning(75, 10, 2, 2*time.Minute), map[string]*state.Account{
		"work-team": weekly(65, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)), // 20 points
		"research":  weekly(55, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)), // 30 points
	})
	r := Forecast(in)
	want := map[string]time.Duration{"work-1": 10 * time.Minute, "research": 40 * time.Minute, "work-team": 60 * time.Minute}
	for id, d := range want {
		if at, ok := r.TriggerAt[id]; !ok || !at.Equal(now.Add(d)) {
			t.Fatalf("trigger_at %s = %v (%v), want +%s", id, at, ok, d)
		}
	}
	if r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %+v, want dry in 1h", r)
	}
}

// An account rotation would not land on (inside the landing margin,
// reserved, refused) is not spent, and has no ETA.
func TestRunwaySkipsAccountsRotationWouldNotUse(t *testing.T) {
	in := runwayWorld(burning(75, 10, 2, 2*time.Minute), map[string]*state.Account{
		"work-team": weekly(80, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)), // inside the margin
	})
	in.Pool = []string{"work-1", "work-team"}
	r := Forecast(in)
	if _, ok := r.TriggerAt["work-team"]; ok {
		t.Fatalf("work-team has an ETA, but rotation would not land on it: %+v", r)
	}
	if r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("got %+v, want dry when work-1 reaches its trigger", r)
	}
}

// A reset that gives an account room back before the pool would run dry
// means it does not run dry at this pace: refills, not a made-up time.
func TestRunwayRefillsWhenAResetComesFirst(t *testing.T) {
	a := burning(75, 10, 2, 2*time.Minute)
	in := runwayWorld(a, map[string]*state.Account{
		"work-team": weekly(90, 10, now.Add(20*time.Minute), now.Add(5*24*time.Hour)), // over; resets in 20m
	})
	in.Pool = []string{"work-1", "work-team"}
	r := Forecast(in)
	if r.Kind != RunwayDry {
		t.Fatalf("work-1 reaches its trigger in 10m, before work-team's reset: got %+v", r)
	}
	in.St.Accounts["work-1"] = burning(55, 10, 2, 2*time.Minute) // 30 points: 30m
	r = Forecast(in)
	if r.Kind != RunwayRefills || !r.DryAt.IsZero() || !r.RefillsAt.Equal(now.Add(20*time.Minute)) ||
		r.RefillsAccount != "work-team" {
		t.Fatalf("got %+v, want refills at work-team's reset in 20m", r)
	}
	if _, ok := r.TriggerAt["work-1"]; ok {
		t.Fatalf("work-1's ETA lies past a reset that changes the pool: %+v", r.TriggerAt)
	}
}

func TestRunwayIsUnknownFromTooFewReadings(t *testing.T) {
	one := weekly(40, 10, now.Add(4*time.Hour), now.Add(5*24*time.Hour)) // a single reading
	cases := map[string]*state.Account{"one reading": one}

	old := burning(40, 10, 3, 3*time.Minute)
	old.LastAt, old.PrevAt = now.Add(-40*time.Minute), now.Add(-43*time.Minute)
	cases["stale readings"] = old

	far := burning(40, 10, 3, 3*time.Hour)
	cases["readings hours apart"] = far

	reset := burning(40, 10, -20, 3*time.Minute)
	cases["a window reset between the readings"] = reset

	for name, a := range cases {
		r := Forecast(runwayWorld(a, nil))
		if r.Kind != RunwayUnknown || !r.DryAt.IsZero() || len(r.TriggerAt) != 0 {
			t.Errorf("%s: got %+v, want unknown", name, r)
		}
	}
	if r := Forecast(runwayWorld(nil, nil)); r.Kind != RunwayUnknown {
		t.Errorf("no account in use: got %+v, want unknown", r)
	}
}

// An unread account in the pool has room nobody knows: the pool's runway is
// unknown, never computed as though it were empty.
func TestRunwayIsUnknownWithAnUnreadAccountInThePool(t *testing.T) {
	in := runwayWorld(burning(40, 10, 3, 3*time.Minute), map[string]*state.Account{"research": {ID: "research"}})
	in.Pool = []string{"work-1", "research"}
	if r := Forecast(in); r.Kind != RunwayUnknown || !r.DryAt.IsZero() {
		t.Fatalf("got %+v, want unknown", r)
	}
	// One that needs a sign-in is not spent by rotation at all.
	in.Unavailable = map[string]string{"research": "needs a sign-in"}
	if r := Forecast(in); r.Kind != RunwayDry {
		t.Fatalf("got %+v, want dry: research is out of the pool's reach", r)
	}
}

// Two flat readings: the account is not being spent. A rate seen rising
// within the forecast window carries over a flat pair (whole-point readings
// at a slow burn); an older one does not.
func TestRunwayOfAnAccountNotBeingSpent(t *testing.T) {
	a := burning(40, 10, 0, 3*time.Minute)
	if r := Forecast(runwayWorld(a, nil)); r.Kind != RunwayIdle || !r.DryAt.IsZero() {
		t.Fatalf("flat pair: got %+v, want idle", r)
	}
	a.RiseRate, a.RiseAt = 0.5, now.Add(-6*time.Minute)
	in := runwayWorld(a, nil)
	in.Pool = []string{"work-1"}
	if r := Forecast(in); r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("recent rise: got %+v, want dry in 90m at 0.5/min", r)
	}
	a.RiseAt = now.Add(-2 * time.Hour)
	if r := Forecast(runwayWorld(a, nil)); r.Kind != RunwayIdle {
		t.Fatalf("an old rate: got %+v, want idle", r)
	}
}

// Over its trigger with nowhere to go, the pool is dry now.
func TestRunwayOfAPoolAlreadyDry(t *testing.T) {
	in := runwayWorld(burning(90, 10, 2, 2*time.Minute), nil)
	in.Pool = []string{"work-1"}
	r := Forecast(in)
	if r.Kind != RunwayDry || !r.DryAt.Equal(now) {
		t.Fatalf("got %+v, want dry now", r)
	}
	if _, ok := r.TriggerAt["work-1"]; ok {
		t.Fatalf("work-1 is past its trigger, yet has an ETA")
	}
}

// A reset of an account rotation has not reached yet gives back only what it
// used before, which the forecast has not counted as spent: it does not
// stop the forecast. A reset once an account is being spent, or of one
// rotation cannot use, does.
func TestRunwayIgnoresAResetBeforeAnAccountIsReached(t *testing.T) {
	in := runwayWorld(burning(75, 10, 1, time.Minute), map[string]*state.Account{ // 10m to its trigger
		"research": weekly(15, 10, now.Add(5*time.Minute), now.Add(5*24*time.Hour)), // reached at 10m
	})
	r := Forecast(in)
	if r.Kind != RunwayDry || !r.DryAt.Equal(now.Add(80*time.Minute)) {
		t.Fatalf("got %+v, want dry at +80m: research resets before it is reached", r)
	}
	in.St.Accounts["research"] = weekly(15, 10, now.Add(30*time.Minute), now.Add(5*24*time.Hour))
	if r := Forecast(in); r.Kind != RunwayRefills || !r.RefillsAt.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("got %+v, want refills at +30m: research is being spent then", r)
	}
}
