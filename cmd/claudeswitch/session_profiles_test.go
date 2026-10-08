package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
)

// Owner, 2026-10-08: cs session covers every profile; --profile P narrows
// it to one.

// sessionWorld is multiWorld with a switch and two messages in each
// profile: default (~/.claude) switched spare → personal an hour ago, work
// (~/.claude-work) w2 → w1 two hours ago.
func sessionWorld(t *testing.T) string {
	t.Helper()
	path, _, _ := multiWorld(t)
	h := os.Getenv("HOME")
	now := time.Now().UTC().Truncate(time.Second)
	write := func(dir string, msgs map[time.Duration]int64) {
		p := filepath.Join(h, dir, "projects", "proj")
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		var body string
		for ago, in := range msgs {
			body += fmt.Sprintf(`{"timestamp": %q, "requestId": "%s-%d", "message": {"model": "m", "usage": {"input_tokens": %d}}}`+"\n",
				now.Add(-ago).Format(time.RFC3339), dir, ago, in)
		}
		if err := os.WriteFile(filepath.Join(p, "t.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude", map[time.Duration]int64{150 * time.Minute: 10, 30 * time.Minute: 100})
	write(".claude-work", map[time.Duration]int64{150 * time.Minute: 1000, 30 * time.Minute: 10000})
	log, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []audit.Event{
		{Kind: "switch", Profile: "work", At: now.Add(-2 * time.Hour), From: "w2", To: "w1", Reason: "work over"},
		{Kind: "switch", Profile: "default", At: now.Add(-time.Hour), From: "spare", To: "personal", Reason: "default over"},
	} {
		if err := log.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

type sessionOut struct {
	Accounts []struct {
		Account string `json:"account"`
		Input   int64  `json:"input"`
	} `json:"accounts"`
	Switches     int `json:"switches"`
	SwitchEvents []struct {
		Profile string `json:"profile"`
		From    string `json:"from"`
		To      string `json:"to"`
	} `json:"switch_events"`
	Profiles []string `json:"profiles"`
}

func runSessionJSON(t *testing.T, args ...string) (sessionOut, map[string]int64) {
	t.Helper()
	out := captureStdout(t, func() error { return cmdSession(append([]string{"--json", "--since", "4h"}, args...)) })
	var s sessionOut
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	by := map[string]int64{}
	for _, a := range s.Accounts {
		by[a.Account] = a.Input
	}
	return s, by
}

func TestSessionCoversEveryProfile(t *testing.T) {
	path := sessionWorld(t)
	s, by := runSessionJSON(t, "--config", path)
	want := map[string]int64{"spare": 10, "personal": 100, "w2": 1000, "w1": 10000}
	for k, v := range want {
		if by[k] != v {
			t.Errorf("%s: %d, want %d (all %v)", k, by[k], v, by)
		}
	}
	if s.Switches != 2 || len(s.SwitchEvents) != 2 {
		t.Fatalf("switches = %d, %+v; want both profiles'", s.Switches, s.SwitchEvents)
	}
	if e := s.SwitchEvents[0]; e.Profile != "work" || e.From != "w2" || e.To != "w1" {
		t.Errorf("first switch = %+v, want work's", e)
	}
	if e := s.SwitchEvents[1]; e.Profile != "default" || e.To != "personal" {
		t.Errorf("second switch = %+v, want default's", e)
	}
	if strings.Join(s.Profiles, ",") != "default,work" {
		t.Errorf("profiles = %v", s.Profiles)
	}
}

func TestSessionOneProfile(t *testing.T) {
	path := sessionWorld(t)
	s, by := runSessionJSON(t, "--config", path, "--profile", "work")
	if len(by) != 2 || by["w2"] != 1000 || by["w1"] != 10000 {
		t.Errorf("accounts = %v, want work's only", by)
	}
	if s.Switches != 1 || len(s.SwitchEvents) != 1 || s.SwitchEvents[0].Profile != "work" {
		t.Errorf("switches = %d, %+v; want work's only", s.Switches, s.SwitchEvents)
	}
	if strings.Join(s.Profiles, ",") != "work" {
		t.Errorf("profiles = %v", s.Profiles)
	}
}

// The text report labels each switch with its profile when there are
// several.
func TestSessionTextLabelsSwitches(t *testing.T) {
	path := sessionWorld(t)
	out := captureStdout(t, func() error { return cmdSession([]string{"--config", path, "--since", "4h"}) })
	for _, want := range []string{"2 SWITCH(ES) IN THIS SPAN", "work", "w2 → w1", "default", "spare → personal"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "w2 → w1") && !strings.Contains(l, "work") {
			t.Errorf("work's switch unlabelled: %q", l)
		}
		if strings.Contains(l, "spare → personal") && !strings.Contains(l, "default") {
			t.Errorf("default's switch unlabelled: %q", l)
		}
	}
}
