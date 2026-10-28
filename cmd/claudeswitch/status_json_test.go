package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// status --json judges each account as the daemon does: by the window
// nearest its own trigger, against the thresholds of the profile it is in.

const judgedTOML = `
switch_at = 85
switch_at_weekly = 98

[[account]]
id = "personal"
[[account]]
id = "w1"
[[account]]
id = "w2"

[[profile]]
name = "default"

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1", "w2"]
switch_at = 75
`

// judgedStatus writes the state for the config and runs status --json.
func judgedStatus(t *testing.T, body string, readings map[string][2]float64, args ...string) map[string]any {
	t.Helper()
	path := appWorld(t, body)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st, _ := state.Load("", cfg.ProfileNames()...)
	for id, r := range readings {
		st.Get(id).Last, st.Get(id).LastAt = pinReading(r[0], r[1], now), now
	}
	st.Default().SetActive("personal")
	if len(cfg.Profiles) > 0 {
		st.Profile("work").SetActive("w2")
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		return cmdStatus(append([]string{"--config", path, "--json", "--refresh=false"}, args...))
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return m
}

func jsonAccount(t *testing.T, m map[string]any, id string) map[string]any {
	t.Helper()
	for _, a := range m["accounts"].([]any) {
		if a := a.(map[string]any); a["id"] == id {
			return a
		}
	}
	t.Fatalf("no account %q in %v", id, m["accounts"])
	return nil
}

// A weekly-only account at 90% of its week, against 98, has room: the
// session trigger (85) was applied to whichever figure was higher.
func TestStatusJSONJudgesEachWindowByItsOwnTrigger(t *testing.T) {
	m := judgedStatus(t, "switch_at = 85\nswitch_at_weekly = 98\n[[account]]\nid = \"personal\"\n[[account]]\nid = \"wk\"\n[[account]]\nid = \"ss\"\n",
		map[string][2]float64{"personal": {10, 10}, "wk": {10, 90}, "ss": {80, 90}})
	wk := jsonAccount(t, m, "wk")
	if wk["state"] != "available" || wk["usable"] != true || wk["binding_window"] != "seven_day" {
		t.Errorf("90%% of the week against 98 is available, judged on the week: %v", wk)
	}
	// At 80% of the session and 90% of the week the session is nearer its
	// trigger (5 points against 8): that is the window it is judged on.
	ss := jsonAccount(t, m, "ss")
	if ss["binding_window"] != "five_hour" || ss["utilization"] != 80.0 {
		t.Errorf("binding_window must be the window nearest its trigger: %v", ss)
	}
	th := m["thresholds"].(map[string]any)
	if th["switch_at_weekly"] != 98.0 {
		t.Errorf("thresholds lacks switch_at_weekly: %v", th)
	}
}

// An account is judged against its own profile's trigger (D4): w1 at 80% of
// the session is over work's 75, whatever the top-level 85 says.
func TestStatusJSONUsesTheProfilesThresholds(t *testing.T) {
	m := judgedStatus(t, judgedTOML, map[string][2]float64{"personal": {10, 10}, "w1": {80, 10}, "w2": {5, 5}})
	if w1 := jsonAccount(t, m, "w1"); w1["state"] != "no_headroom" || w1["usable"] != false {
		t.Errorf("80%% of the session is over work's 75: %v", w1)
	}
	if p := jsonAccount(t, m, "personal"); p["state"] != "available" {
		t.Errorf("personal at 10%% is available: %v", p)
	}
}

// --profile keeps one profile, as why --json --profile does.
func TestStatusJSONKeepsOnlyTheProfileAskedFor(t *testing.T) {
	m := judgedStatus(t, judgedTOML, map[string][2]float64{"personal": {10, 10}, "w1": {20, 10}, "w2": {5, 5}},
		"--profile", "work")
	var ids []string
	for _, a := range m["accounts"].([]any) {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	if len(ids) != 2 || ids[0] != "w1" || ids[1] != "w2" {
		t.Errorf("--profile work lists %v, want work's pool [w1 w2]", ids)
	}
	profiles := m["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["profile"] != "work" {
		t.Errorf("--profile work lists profiles %v", profiles)
	}
	if m["active"] != "w2" {
		t.Errorf("active = %v, want work's live account w2", m["active"])
	}
}

// A --profile that names no profile is not_found, as docs/APP_CLI.md has it
// for every name that is not there, not "failed".
func TestAnUnknownProfileIsNotFound(t *testing.T) {
	path := appWorld(t, judgedTOML)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, checkProfileFlag(cfg, "nosuch")); got != codeNotFound {
		t.Errorf("--profile nosuch: code %q, want not_found", got)
	}
}
