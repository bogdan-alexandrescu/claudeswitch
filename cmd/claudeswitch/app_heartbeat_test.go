package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/detector"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// 0.6.1 (decided 2026-10-08): while the app runs and posts its own
// actionable rotation notifications, it records a heartbeat through the CLI
// and the daemon skips its own rotation notice. The app never writes
// state.json itself.
func TestAppHeartbeatRecordsTwoMinutes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	before := time.Now()
	out := captureStdout(t, func() error { return cmdApp([]string{"heartbeat", "--json"}) })
	m := decodeJSON(t, []byte(out))
	got, err := time.Parse(time.RFC3339, m["app_notifies_until"].(string))
	if err != nil {
		t.Fatalf("app_notifies_until %v: %v", m["app_notifies_until"], err)
	}
	if got.Before(before.Add(2*time.Minute).Add(-time.Second)) || got.After(time.Now().Add(2*time.Minute)) {
		t.Errorf("app_notifies_until = %v, want two minutes from now", got)
	}
	st, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !st.AppNotifyingAt(time.Now()) || st.AppNotifyingAt(time.Now().Add(3*time.Minute)) {
		t.Errorf("state holds app_notifies_until = %v, want about two minutes ahead", st.AppNotifiesUntil)
	}
}

func TestAppHeartbeatRefusesOtherVerbs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if code := errCode(t, cmdApp([]string{"beat", "--json"})); code != codeUsage {
		t.Errorf("code %q, want usage", code)
	}
}

// rotateAndExhaust runs the single-profile scenario of
// TestDaemonWithNoProfilesBehavesAsBefore: a switch from a to b, then a
// rejection with nothing left. It returns the notifications sent.
func rotateAndExhaust(t *testing.T, appNotifies func(time.Time) bool) []string {
	t.Helper()
	r := newRig(t, testCfg(), true)
	r.d.appNotifies = appNotifies
	r.p.attribute["default"] = "a"
	put(r.d.st, "a", 90)
	put(r.d.st, "b", 10)
	il := r.d.profs[0]
	r.d.start(context.Background())
	r.d.onRejection(context.Background(), il, detector.Rejection{Type: detector.FiveHour,
		ResetsAt: time.Now().Add(time.Hour)})
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("swaps = %+v, want one into b", swaps)
	}
	return r.nt.sent
}

func TestDaemonLeavesRotationNoticesToARunningApp(t *testing.T) {
	sent := rotateAndExhaust(t, func(time.Time) bool { return true })
	for _, s := range sent {
		if strings.HasPrefix(s, "switched|") {
			t.Errorf("the app is notifying, yet the daemon sent %q", s)
		}
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "exhausted|") {
		t.Errorf("notifications = %q, want only the exhaustion: every other notice stays", sent)
	}
}

func TestDaemonNotifiesRotationsWithoutTheApp(t *testing.T) {
	for name, fn := range map[string]func(time.Time) bool{
		"no heartbeat seam": nil,
		"heartbeat expired": func(time.Time) bool { return false },
	} {
		sent := rotateAndExhaust(t, fn)
		if len(sent) != 2 || !strings.HasPrefix(sent[0], "switched|a → b|") {
			t.Errorf("%s: notifications = %q, want the switch then the exhaustion", name, sent)
		}
	}
}
