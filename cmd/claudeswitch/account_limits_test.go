package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// limitWorld is resetsWorld (every account at session 20%, week 30%) with
// p1 given extra lines in its [[account]] block.
func limitWorld(t *testing.T, extra string) string {
	t.Helper()
	path, _ := resetsWorld(t, "160")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const head = "id = \"p1\"\n"
	if !strings.Contains(string(raw), head) {
		t.Fatalf("no p1 block:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), head, head+extra, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func accountsByID(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	out := map[string]map[string]any{}
	for _, a := range doc.Accounts {
		out[a["id"].(string)] = a
	}
	return out
}

func TestListJSONReportsAnAccountsOwnLimits(t *testing.T) {
	path := limitWorld(t, "ignore_weekly_gte = 25\npick_priority = 3\n")
	var buf bytes.Buffer
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	all := accountsByID(t, buf.Bytes())
	p1, t1 := all["p1"], all["t1"]
	if p1["ignore_weekly_gte"] != 25.0 || p1["pick_priority"] != 3.0 {
		t.Errorf("p1: %v %v", p1["ignore_weekly_gte"], p1["pick_priority"])
	}
	if t1["ignore_weekly_gte"] != nil || t1["pick_priority"] != nil {
		t.Errorf("no limit set must read null: %v %v", t1["ignore_weekly_gte"], t1["pick_priority"])
	}
	if got := p1["reading"].(map[string]any)["binding"]; got != "seven_day" {
		t.Errorf("the week is what binds p1 under its own limit: %v", got)
	}
}

func TestStatusJSONJudgesAnAccountByItsOwnLimit(t *testing.T) {
	path := limitWorld(t, "ignore_weekly_gte = 25\n")
	out, code := runCLI(t, "status", "--json", "--config", path)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	all := accountsByID(t, []byte(out))
	if p1 := all["p1"]; p1["state"] != "no_headroom" || p1["usable"] != false {
		t.Errorf("p1 is over its own limit: %v usable=%v", p1["state"], p1["usable"])
	}
	if t2 := all["t2"]; t2["state"] != "available" || t2["usable"] != true {
		t.Errorf("t2 has no limit and the same reading: %v usable=%v", t2["state"], t2["usable"])
	}
}

func TestAManualSwitchIsToldAboutAnAccountsOwnLimit(t *testing.T) {
	path := limitWorld(t, "ignore_weekly_gte = 25\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	if ok, _ := hasRoom(cfg, st, "p1", time.Now()); ok {
		t.Error("p1 is over its own limit")
	}
	if ok, _ := hasRoom(cfg, st, "t2", time.Now()); !ok {
		t.Error("t2 has no limit and room")
	}
}

func TestTheDaemonNoticesAChangedAccountLimit(t *testing.T) {
	for _, next := range []config.Account{{ID: "a", IgnoreWeeklyGTE: 65}, {ID: "a", PickPriority: 1}} {
		got := configChanges(&config.Config{Accounts: []config.Account{{ID: "a"}}},
			&config.Config{Accounts: []config.Account{next}})
		if !strings.Contains(strings.Join(got, "\n"), "account settings changed: a") {
			t.Errorf("%+v: %v", next, got)
		}
	}
}

// The status line only asks what rotation will do once a figure nears a
// trigger, and an account's weekly limit can sit below the session one.
func TestTheStatusLineAsksAboutAWeeklyLimitBelowTheSessionOne(t *testing.T) {
	if !slNearTrigger(70, &config.Config{SwitchAt: 85, SwitchAtWeekly: 65}) {
		t.Error("70% is past a 65% weekly limit")
	}
	if slNearTrigger(70, &config.Config{SwitchAt: 85, SwitchAtWeekly: 98}) {
		t.Error("70% is under both triggers")
	}
}
