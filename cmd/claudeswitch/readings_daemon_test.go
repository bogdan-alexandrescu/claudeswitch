package main

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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
