package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// `cs chrome check` (owner decision 2026-10-23): what was done by hand all
// day. A fresh Claude Code process (`claude -p --chrome`) asks the bridge which
// browsers the live account's channel has, the one question a running session
// cannot be trusted to answer after a switch (GROUND_TRUTH §56). When none
// answers and the account has a Chrome profile of its own, it opens that
// profile (C4) and asks once more.

func checkWorld(t *testing.T) (string, *[]string) {
	t.Helper()
	cfg := chromeWorld(t, "darwin")
	st := chromeState(t)
	st.Get("w1")
	st.Get("w2")
	st.Get("personal")
	st.Profile("work").SetActive("w2")
	st.SetChrome("w2", "claudeswitch-w2", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var probed []string
	old, oldSettle := chromeProbe, chromeCheckSettle
	t.Cleanup(func() { chromeProbe, chromeCheckSettle = old, oldSettle })
	chromeCheckSettle = 0
	chromeProbe = func(configDir string) ([]probedBrowser, error) {
		probed = append(probed, configDir)
		return nil, nil
	}
	return cfg, &probed
}

func TestChromeCheckReportsTheLiveAccountsBrowser(t *testing.T) {
	cfg, probed := checkWorld(t)
	at := time.Date(2026, 10, 23, 18, 18, 20, 0, time.Local)
	chromeProbe = func(configDir string) ([]probedBrowser, error) {
		*probed = append(*probed, configDir)
		return []probedBrowser{{DeviceID: "9d485bc2-f480", Name: "Browser 1", ConnectedAt: at, InUse: true}}, nil
	}
	testshim.ResetLaunches()
	var out bytes.Buffer
	if err := runChrome(&out, &bytes.Buffer{}, []string{"check", "--profile", "work", "--config", cfg}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"work", "w2", "9d485bc2", "18:18", "claudeswitch-w2", "reaches"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if len(*probed) != 1 || (*probed)[0] != "~/.claude-work" {
		t.Errorf("probed %q, want once in the work profile's dir", *probed)
	}
	if l := testshim.Launches(); len(l) != 0 {
		t.Errorf("opened %q although a browser answered", l)
	}
}

func TestChromeCheckOpensTheProfileWhenNothingAnswers(t *testing.T) {
	cfg, probed := checkWorld(t)
	testshim.ResetLaunches()
	var out bytes.Buffer
	err := runChrome(&out, &bytes.Buffer{}, []string{"check", "--profile", "work", "--config", cfg, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeJSON(t, out.Bytes())
	if m["account"] != "w2" || m["reachable"] != false || m["opened"] != true {
		t.Fatalf("answer = %v", m)
	}
	if len(*probed) != 2 {
		t.Errorf("probed %d times, want twice (before and after opening)", len(*probed))
	}
	if l := testshim.Launches(); len(l) != 1 || l[0][len(l[0])-1] != "--profile-directory=claudeswitch-w2" {
		t.Errorf("launches = %q", l)
	}
}
