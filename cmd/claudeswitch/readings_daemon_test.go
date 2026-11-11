package main

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/drain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/readings"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// fakeReadings records what the daemon hands the readings log.
type fakeReadings struct {
	mu   sync.Mutex
	seen []string
}

func (f *fakeReadings) Record(account string, at time.Time, u *usage.Usage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, account)
	return nil
}

func (f *fakeReadings) accounts() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := append([]string(nil), f.seen...)
	sort.Strings(s)
	return strings.Join(s, ",")
}

// IMPROVEMENTS F7: after each poll tick and each save tick the daemon hands
// every configured account's current reading to the readings log (which
// writes each reading once). An account with no reading, and a record the
// config does not name, are not handed over.
func TestDaemonRecordsReadings(t *testing.T) {
	r := newRig(t, twoProfiles(), false)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "w1", 20)
	put(st, "stranger", 30)
	rec := &fakeReadings{}
	r.d.readings = rec

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	tick := make(chan time.Time)
	save := make(chan time.Time)
	done := make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, tick, save) }()
	tick <- time.Now()
	save <- time.Now()
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := rec.accounts(); got != "a,a,w1,w1" {
		t.Errorf("recorded %q, want a and w1 on each tick", got)
	}
}

// runOnce runs the daemon through one poll tick and one save tick.
func runOnce(t *testing.T, r *rig) {
	t.Helper()
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	tick := make(chan time.Time)
	save := make(chan time.Time)
	done := make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, tick, save) }()
	tick <- time.Now()
	save <- time.Now()
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The drain forecast: the daemon hands every new reading to its meter too,
// and at each save writes each configured account's week speed into its
// record, with when it measured it and the last rise it saw.
func TestDaemonWritesWeekSpeeds(t *testing.T) {
	r := newRig(t, twoProfiles(), false)
	st := r.d.st
	st.Profile("default").SetActive("a")
	now := time.Now()
	put(st, "a", 10) // week 10, read now
	put(st, "w1", 20)
	put(st, "stranger", 30)
	r.d.meter = drain.NewMeter()
	r.d.meter.Add("a", now.Add(-10*time.Minute), 8)
	r.d.meter.Add("a", now.Add(-5*time.Minute), 9) // the first rise of the stretch

	before := time.Now()
	runOnce(t, r)

	a := st.Accounts["a"]
	if a.WeekSpeed < 11.9 || a.WeekSpeed > 12.1 {
		t.Errorf("a's speed %.2f points an hour, want 12 (a point in 5 minutes)", a.WeekSpeed)
	}
	if !a.WeekRiseAt.Equal(a.LastAt) {
		t.Errorf("a's last rise %v, want its reading's %v", a.WeekRiseAt, a.LastAt)
	}
	if a.WeekSpeedAt.Before(before) {
		t.Errorf("a's speed measured at %v, before the save", a.WeekSpeedAt)
	}
	if w := st.Accounts["w1"]; w.WeekSpeed != 0 || w.WeekSpeedAt.IsZero() || !w.WeekRiseAt.IsZero() {
		t.Errorf("w1 has one figure, so a speed of 0 measured now and no rise: %+v", w)
	}
	if s := st.Accounts["stranger"]; !s.WeekSpeedAt.IsZero() {
		t.Errorf("a record the config does not name was measured: %+v", s)
	}
}

// Without a meter (as before the forecast) the records stay as they were.
func TestDaemonWithoutAMeterMeasuresNothing(t *testing.T) {
	r := newRig(t, twoProfiles(), false)
	put(r.d.st, "a", 10)
	runOnce(t, r)
	if a := r.d.st.Accounts["a"]; !a.WeekSpeedAt.IsZero() {
		t.Errorf("measured without a meter: %+v", a)
	}
}

// At start the meter is seeded from the readings log's last day, so a
// restarted daemon knows each account's speed at once.
func TestMeterSeededFromTheReadingsLog(t *testing.T) {
	now := time.Now()
	week := func(v float64) *float64 { return &v }
	m := meterFrom([]readings.Reading{
		{At: now.Add(-10 * time.Minute), Account: "a", SevenDay: week(8)},
		{At: now.Add(-9 * time.Minute), Account: "b", SevenDay: nil}, // no week figure
		{At: now.Add(-5 * time.Minute), Account: "a", SevenDay: week(9)},
		{At: now, Account: "a", SevenDay: week(10)},
	})
	if s := m.Speed("a", now); s.PerHour < 11.9 || s.PerHour > 12.1 {
		t.Errorf("seeded speed %.2f points an hour, want 12", s.PerHour)
	}
}
