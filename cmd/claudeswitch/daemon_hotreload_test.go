package main

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
)

// D21: the set of profiles hot-reloads. A profile added to the config is
// started in place, one removed is stopped, one whose dir changed is stopped
// and started again with fresh resolution — all on the daemon's loop, never
// in the middle of a swap, with no goroutine left behind.

const thirdProfile = "\n[[profile]]\nname = \"third\"\ndir  = \"~/.claude-third\"\npool = []\n"

// logged reports whether a log line carries the message and the profile.
func logged(logs, msg, prof string) bool {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `msg="`+msg+`"`) && strings.Contains(line, "profile="+prof) &&
			strings.Contains(line, "level=INFO") {
			return true
		}
	}
	return false
}

func reloadRig(t *testing.T, body string, live bool) (*rig, string, chan struct{}) {
	t.Helper()
	path := writeConfig(t, body)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, live)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	stop := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		r.d.stopProfiles()
	})
	r.d.startDetectors(stop)
	return r, path, stop
}

func rewrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDaemonReloadStartsAnAddedProfile(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	rewrite(t, path, reloadTwoProfiles+thirdProfile)
	r.d.reload(context.Background())

	if len(r.d.profs) != 3 || r.prof("third").conf.Dir != "~/.claude-third" {
		t.Fatalf("profiles after reload: %d", len(r.d.profs))
	}
	det := r.dets["third"]
	if det == nil {
		t.Fatal("no detector built for the added profile")
	}
	eventually(t, "the added profile's detector to run", func() bool { return det.runs.Load() == 1 })
	if r.p.lives["third"] == nil {
		t.Error("the poller was not handed the added profile's live item")
	}
	if _, ok := r.d.st.Profiles["third"]; !ok {
		t.Error("the added profile has no state")
	}
	logs := r.logs.String()
	if !logged(logs, "profile started", "third") {
		t.Errorf("no INFO profile started for third:\n%s", logs)
	}
	if strings.Contains(logs, "restart") {
		t.Errorf("a hot reload must not ask for a restart:\n%s", logs)
	}
	// Its rejections reach the loop through a forwarder of its own.
	det.out <- detector.Rejection{Type: detector.FiveHour, ResetsAt: time.Now().Add(time.Hour)}
	select {
	case got := <-r.d.rej:
		if got.il.name != "third" {
			t.Errorf("rejection tagged %q", got.il.name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the added profile's rejection never reached the loop")
	}
}

func TestDaemonReloadStopsARemovedProfile(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	work := r.dets["work"]
	eventually(t, "work's detector to run", func() bool { return work.runs.Load() == 1 })
	put(r.d.st, "w1", 10)
	r.d.st.Profile("work").SetActive("w1")
	r.prof("work").wantSwitchSince = time.Now()

	body := strings.Replace(reloadTwoProfiles, "\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\"]\n", "", 1)
	if body == reloadTwoProfiles {
		t.Fatal("setup: work was not removed from the config")
	}
	rewrite(t, path, body)
	r.d.reload(context.Background())

	for _, il := range r.d.profs {
		if il.name == "work" {
			t.Fatal("work is still running")
		}
	}
	// Stopped before reload returned: the detector, and its forwarder.
	if work.exits.Load() != 1 {
		t.Error("work's detector was not stopped")
	}
	if r.p.lives["work"] != nil {
		t.Error("the poller still has work's live item")
	}
	if !logged(r.logs.String(), "profile stopped", "work") {
		t.Errorf("no INFO profile stopped for work:\n%s", r.logs)
	}
	// Decision: the state keeps a removed profile's record (state.json never
	// drops profiles, and a save would merge it back from disk anyway). Its
	// active account moves to the ghost guarding the old item, so a release
	// or a forget is not undone by re-deriving it at the next start.
	if in := r.d.st.Profiles["work"]; in == nil || in.Active != "" {
		t.Errorf("work's state record: %+v", in)
	}
	if gs := r.d.st.GhostList(); len(gs) != 1 || gs[0].Account != "w1" {
		t.Errorf("work's ghost: %+v", gs)
	}
	// A rejection the stopped detector still holds goes nowhere.
	work.out <- detector.Rejection{Type: detector.FiveHour, ResetsAt: time.Now().Add(time.Hour)}
	select {
	case got := <-r.d.rej:
		t.Fatalf("a stopped profile's rejection reached the loop: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDaemonReloadRestartsARepointedProfile(t *testing.T) {
	r, path, _ := reloadRig(t, reloadTwoProfiles, false)
	oldIL, oldDet := r.prof("work"), r.dets["work"]
	eventually(t, "work's detector to run", func() bool { return oldDet.runs.Load() == 1 })
	r.d.st.Profile("work").SetActive("w1")

	rewrite(t, path, strings.Replace(reloadTwoProfiles, "~/.claude-work", "~/.claude-work2", 1))
	r.d.reload(context.Background())

	nowIL := r.prof("work")
	if nowIL == oldIL || nowIL.conf.Dir != "~/.claude-work2" {
		t.Fatalf("work was not rebuilt for its new dir: %+v", nowIL.conf)
	}
	if oldDet.exits.Load() != 1 {
		t.Error("the old detector is still running")
	}
	newDet := r.dets["work"]
	if newDet == oldDet {
		t.Fatal("no new detector")
	}
	eventually(t, "the new detector to run", func() bool { return newDet.runs.Load() == 1 })
	// What was active described the old dir's item: forgotten, and held
	// rather than swapped until the new item is attributed.
	if a := r.d.st.Profile("work").Active; a != "" {
		t.Errorf("work still believes %q is live in a credential it no longer reads", a)
	}
	if !nowIL.hold.holding() {
		t.Error("a re-pointed profile must hold until its live credential is attributed")
	}
	logs := r.logs.String()
	if !logged(logs, "profile stopped", "work") || !logged(logs, "profile started", "work") {
		t.Errorf("re-point must log stopped then started:\n%s", logs)
	}
}

// Swaps run on the loop's goroutine, so a reload waits for one in progress:
// the profile is stopped only after its swap has finished and been recorded.
func TestDaemonReloadNeverStopsAProfileMidSwap(t *testing.T) {
	body := strings.Replace(reloadTwoProfiles, `pool = ["w1"]`, `pool = ["w1", "w2"]`, 1)
	path := writeConfig(t, body)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, true)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	st := r.d.st
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")
	put(st, "a", 10)
	put(st, "b", 10)
	put(st, "w1", 90)
	put(st, "w2", 10)

	inSwap, release := make(chan struct{}), make(chan struct{})
	r.v.swapHook = func() {
		close(inSwap)
		<-release
	}
	changed := make(chan struct{}, 1)
	r.d.cfgChanged = changed

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	tick := make(chan time.Time, 1)
	done := make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, tick, make(chan time.Time)) }()

	tick <- time.Now()
	<-inSwap
	// Remove work while its swap is held open.
	rewrite(t, path, strings.Replace(body, "\n[[profile]]\nname = \"work\"\ndir  = \"~/.claude-work\"\npool = [\"w1\", \"w2\"]\n", "", 1))
	changed <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	if logged(r.logs.String(), "profile stopped", "work") {
		t.Fatal("work was stopped while its swap was in progress")
	}
	close(release)
	eventually(t, "work to stop after its swap", func() bool { return logged(r.logs.String(), "profile stopped", "work") })
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	logs := r.logs.String()
	if i, j := strings.Index(logs, `msg=switched`), strings.Index(logs, `msg="profile stopped"`); i < 0 || j < i {
		t.Errorf("the swap must finish (and be recorded) before the profile stops:\n%s", logs)
	}
	// The finished swap was recorded: the ghost the removal left guards w2.
	if gs := st.GhostList(); len(gs) != 1 || gs[0].Account != "w2" {
		t.Errorf("the finished swap was not recorded: ghosts %+v", gs)
	}
}

// Add and remove a profile over and over while the loop runs and the other
// detectors deliver refusals: under -race nothing touches state off the loop,
// and every detector and forwarder started is stopped.
func TestDaemonReloadAddRemoveRepeatedlyLeaksNothing(t *testing.T) {
	path := writeConfig(t, reloadTwoProfiles)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, false)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	// Unbuffered: a send returns once the loop has taken the signal, and the
	// loop reloads before it selects again, so each rewrite is seen.
	changed := make(chan struct{})
	r.d.cfgChanged = changed

	base := runtime.NumGoroutine()
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	tick := make(chan time.Time)
	save := make(chan time.Time)
	done := make(chan error, 1)
	r.d.startDetectors(stop)
	go func() { done <- r.d.run(context.Background(), stop, sig, tick, save) }()

	def := r.dets["default"]
	feed := make(chan struct{})
	go func() {
		defer close(feed)
		for i := 0; i < 200; i++ {
			def.out <- detector.Rejection{Type: detector.FiveHour, ResetsAt: time.Now().Add(time.Hour)}
		}
	}()

	const rounds = 15
	for i := 0; i < rounds; i++ {
		body := reloadTwoProfiles
		if i%2 == 0 {
			body += thirdProfile
		}
		rewrite(t, path, body)
		changed <- struct{}{}
		tick <- time.Now() // a loop iteration: the reload before it has been applied
		save <- time.Now()
	}
	<-feed
	sig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Run's own shutdown stops every profile; nothing it started survives.
	eventually(t, "every goroutine the daemon started to end", func() bool {
		return runtime.NumGoroutine() <= base
	})
	logs := r.logs.String()
	if n := strings.Count(logs, `msg="profile started" profile=third`); n != (rounds+1)/2 {
		t.Errorf("third started %d times, want %d", n, (rounds+1)/2)
	}
	if n := strings.Count(logs, `msg="profile stopped" profile=third`); n != rounds/2 {
		t.Errorf("third stopped %d times by reload, want %d", n, rounds/2)
	}
}
