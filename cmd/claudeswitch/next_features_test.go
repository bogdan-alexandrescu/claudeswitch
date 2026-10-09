package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// IMPROVEMENTS F1-F3 at the CLI and the daemon: the setting, the JSON keys
// the app reads, the pin valve and the runway notification.

// --- F1: prefer -----------------------------------------------------------

func TestConfigSchemaHasPrefer(t *testing.T) {
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, "", []string{"schema"}, true, ""); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Settings []struct {
			Key, Type, Default string
			Enum, Scopes       []string
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for i, s := range out.Settings {
		keys = append(keys, s.Key)
		if s.Key != "prefer" {
			continue
		}
		if s.Type != "enum" || s.Default != "room" || !slices.Equal(s.Enum, []string{"room", "expiring"}) ||
			!slices.Equal(s.Scopes, []string{"global", "profile"}) {
			t.Fatalf("prefer in the schema: %+v", s)
		}
		// The Rotation group: listed with the rotation settings, after
		// landing_margin.
		if i == 0 || out.Settings[i-1].Key != "landing_margin" {
			t.Fatalf("prefer follows %q, want landing_margin", out.Settings[i-1].Key)
		}
		return
	}
	t.Fatalf("no prefer in the schema: %v", keys)
}

func TestConfigSetPrefer(t *testing.T) {
	path := appWorld(t, appTOML)
	var buf bytes.Buffer
	if err := runConfigCmd(&buf, path, []string{"set", "prefer", "expiring"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["previous"] != "room" || m["value"] != "expiring" {
		t.Fatalf("answered %v", m)
	}
	if got := loadOrFail(t, path).Preference(); got != config.PreferExpiring {
		t.Fatalf("read back %q", got)
	}
	if !strings.Contains(readFile(t, path), "# keep this comment") {
		t.Fatal("the edit was not textual")
	}
	if got := errCode(t, runConfigCmd(&buf, path, []string{"set", "prefer", "soonest"}, true, "")); got != codeInvalidValue {
		t.Fatalf("a bad value: %q", got)
	}
	buf.Reset()
	if err := runConfigCmd(&buf, path, []string{}, true, ""); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["prefer"] != "expiring" {
		t.Fatalf("config --json prefer = %v", m["prefer"])
	}
}

func TestProfileSetPrefer(t *testing.T) {
	path := appWorld(t, profilesTOML)
	var buf bytes.Buffer
	if err := profileSet(&buf, path, "work", "prefer", "expiring", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["override"] != "expiring" || m["effective"] != "expiring" {
		t.Fatalf("answered %v", m)
	}
	cfg := loadOrFail(t, path)
	if cfg.PreferFor("work") != config.PreferExpiring || cfg.PreferFor("default") != config.PreferRoom {
		t.Fatalf("work %q, default %q", cfg.PreferFor("work"), cfg.PreferFor("default"))
	}
	buf.Reset()
	if err := runConfigCmd(&buf, path, []string{"get", "prefer"}, true, "work"); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["value"] != "expiring" || m["override"] != "expiring" {
		t.Fatalf("get --profile: %v", m)
	}
	if got := errCode(t, profileSet(&buf, path, "work", "prefer", "most", true)); got != codeInvalidValue {
		t.Fatalf("a bad value: %q", got)
	}
	buf.Reset()
	if err := profileSet(&buf, path, "work", "prefer", "inherit", true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["override"] != nil || m["effective"] != "room" {
		t.Fatalf("inherit answered %v", m)
	}
}

// --- why --json and status --json keys (F1, F2) ---------------------------

// runwayState is a single-profile world: work-1 in use, read twice three
// minutes apart and rising a point a minute; work-team idle with room.
func runwayState(t *testing.T, now time.Time) (*config.Config, *state.State) {
	t.Helper()
	path := appWorld(t, `
[[account]]
id = "work-1"
[[account]]
id = "work-team"
`)
	cfg := loadOrFail(t, path)
	st, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	a := at(40, 10, now)
	a.Last.FiveHour.ResetsAt = ptrTime(now.Add(4 * time.Hour))
	a.Last.SevenDay.ResetsAt = ptrTime(now.Add(5 * 24 * time.Hour))
	a.PrevWorst, a.PrevAt = 37, now.Add(-3*time.Minute)
	st.Accounts["work-1"] = a
	b := at(5, 10, now)
	b.Last.FiveHour.ResetsAt = ptrTime(now.Add(4 * time.Hour))
	b.Last.SevenDay.ResetsAt = ptrTime(now.Add(9 * time.Hour))
	st.Accounts["work-team"] = b
	st.Default().SetActive("work-1")
	return cfg, st
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestWhyJSONCarriesTheWeeklyAndRunwayKeys(t *testing.T) {
	now := time.Now()
	cfg, st := runwayState(t, now)
	m := whyJSON(cfg, st, now, "")
	// work-1: 45 points to its trigger at 1/min; work-team: 80 more.
	if m["pool_forecast"] != "dry" {
		t.Fatalf("pool_forecast = %v", m["pool_forecast"])
	}
	dry, ok := m["pool_dry_at"].(time.Time)
	if !ok || dry.Sub(now.Add(125*time.Minute)).Abs() > time.Second {
		t.Fatalf("pool_dry_at = %v, want ~now+2h05m", m["pool_dry_at"])
	}
	for _, a := range m["accounts"].([]map[string]any) {
		for _, k := range []string{"trigger_at", "weekly_resets_at", "weekly_unused"} {
			if _, ok := a[k]; !ok {
				t.Fatalf("account %v lacks %q", a["id"], k)
			}
		}
		if a["id"] == "work-team" && a["weekly_unused"] != 90.0 {
			t.Fatalf("work-team weekly_unused = %v", a["weekly_unused"])
		}
	}
	// No readings to forecast from: null, never a guess.
	st.Accounts["work-1"].PrevAt = time.Time{}
	m = whyJSON(cfg, st, now, "")
	if m["pool_dry_at"] != nil || m["pool_forecast"] != "unknown" {
		t.Fatalf("one reading: pool_dry_at %v, pool_forecast %v", m["pool_dry_at"], m["pool_forecast"])
	}
	for _, a := range m["accounts"].([]map[string]any) {
		if a["trigger_at"] != nil {
			t.Fatalf("%v trigger_at = %v, want null", a["id"], a["trigger_at"])
		}
	}
}

func TestWhyJSONPerProfileRunway(t *testing.T) {
	now := time.Now()
	path := appWorld(t, profilesTOML)
	cfg := loadOrFail(t, path)
	st, _ := state.Load("")
	m := whyJSON(cfg, st, now, "")
	for _, p := range m["profiles"].([]map[string]any) {
		for _, k := range []string{"pool_dry_at", "pool_forecast", "pool_refills_at"} {
			if _, ok := p[k]; !ok {
				t.Fatalf("profile %v lacks %q", p["profile"], k)
			}
		}
		if p["pool_dry_at"] != nil {
			t.Fatalf("nothing read, yet pool_dry_at = %v", p["pool_dry_at"])
		}
	}
}

func TestStatusJSONCarriesTheRunway(t *testing.T) {
	now := time.Now()
	cfg, st := runwayState(t, now)
	m := runwayJSON(cfg, st, now)
	if m.profiles[config.DefaultProfile]["pool_forecast"] != "dry" {
		t.Fatalf("runway = %v", m.profiles)
	}
	at, ok := m.accounts["work-1"].(time.Time)
	if !ok || at.Sub(now.Add(45*time.Minute)).Abs() > time.Second {
		t.Fatalf("work-1 trigger_at = %v", m.accounts["work-1"])
	}
	if _, ok := m.accounts["work-team"].(time.Time); !ok {
		t.Fatalf("work-team trigger_at = %v", m.accounts["work-team"])
	}
}

// --- F3: pin --hard and the valve ------------------------------------------

func TestAccountPinHard(t *testing.T) {
	path := appWorld(t, appTOML)
	st, _ := state.Load("")
	st.Get("a1")
	st.Profile("default").SetActive("a1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountPin(&buf, path, "a1", true, true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["pinned"] != "a1" || m["pin_hard"] != true {
		t.Fatalf("pin --hard answered %v", m)
	}
	if st, _ := state.Load(""); !st.Default().PinHard {
		t.Fatal("pin_hard was not saved")
	}
	buf.Reset()
	if err := accountList(&buf, path, true); err != nil {
		t.Fatal(err)
	}
	for _, a := range decodeJSON(t, buf.Bytes())["accounts"].([]any) {
		a := a.(map[string]any)
		if want := a["id"] == "a1"; a["pin_hard"] != want {
			t.Fatalf("%v pin_hard = %v", a["id"], a["pin_hard"])
		}
	}
	// A plain pin replaces a hard one; unpin clears both.
	buf.Reset()
	if err := accountPin(&buf, path, "a1", false, true); err != nil {
		t.Fatal(err)
	}
	if m := decodeJSON(t, buf.Bytes()); m["pin_hard"] != false {
		t.Fatalf("pin answered %v", m)
	}
	if st, _ := state.Load(""); st.Default().PinHard {
		t.Fatal("a plain pin kept pin_hard")
	}
	_ = accountPin(&buf, path, "a1", true, true)
	if err := accountUnpin(&buf, path, "", "", true); err != nil {
		t.Fatal(err)
	}
	if st, _ := state.Load(""); st.Default().Pinned != "" || st.Default().PinHard {
		t.Fatalf("unpin left %+v", st.Default())
	}
}

func refusedRig(t *testing.T, live, hard bool) *rig {
	t.Helper()
	r := newRig(t, testCfg(), live)
	st := r.d.st
	st.Default().SetActive("a")
	st.Default().Pinned, st.Default().PinHard = "a", hard
	put(st, "a", 40)
	put(st, "b", 10)
	st.Accounts["a"].BurntTil, st.Accounts["a"].BurntWin = time.Now().Add(time.Hour), "five_hour"
	return r
}

func TestDaemonLiftsThePinOnARefusedAccount(t *testing.T) {
	r := refusedRig(t, true, false)
	r.d.evaluate(context.Background(), r.d.profs[0], "rejection")

	if p := r.d.st.Default(); p.Pinned != "" {
		t.Fatalf("the pin stayed: %+v", p)
	}
	if swaps := r.v.ops("swap"); len(swaps) != 1 || swaps[0].account != "b" {
		t.Fatalf("swaps = %+v, want one to b", swaps)
	}
	want := "pin on a lifted: it was refused"
	ev := r.aud.kind("unpin")
	if len(ev) != 1 || ev[0].Reason != want || ev[0].From != "a" || ev[0].Profile != "default" || ev[0].DryRun {
		t.Fatalf("unpin audit = %+v", ev)
	}
	if !strings.Contains(r.logs.String(), want) {
		t.Fatalf("the log does not say %q:\n%s", want, r.logs.String())
	}
	if !slices.ContainsFunc(r.nt.sent, func(s string) bool { return strings.Contains(s, want) }) {
		t.Fatalf("no notification says %q: %v", want, r.nt.sent)
	}
	// Said once: the pin is gone, so the next tick has nothing to lift.
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if n := len(r.aud.kind("unpin")); n != 1 {
		t.Fatalf("%d unpin events after a second tick", n)
	}
}

func TestDaemonKeepsAHardPin(t *testing.T) {
	r := refusedRig(t, true, true)
	r.d.evaluate(context.Background(), r.d.profs[0], "rejection")
	if p := r.d.st.Default(); p.Pinned != "a" || !p.PinHard {
		t.Fatalf("a hard pin was lifted: %+v", p)
	}
	if len(r.v.ops("swap")) != 0 || len(r.aud.kind("unpin")) != 0 {
		t.Fatalf("a hard pin rotated: swaps %v, unpins %v", r.v.ops("swap"), r.aud.kind("unpin"))
	}
}

// Dry run changes nothing: the pin stays, and the audit log says what a
// live daemon would have done, once.
func TestDaemonInDryRunReportsTheLiftWithoutMakingIt(t *testing.T) {
	r := refusedRig(t, false, false)
	r.d.evaluate(context.Background(), r.d.profs[0], "rejection")
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if p := r.d.st.Default(); p.Pinned != "a" {
		t.Fatalf("dry run lifted the pin: %+v", p)
	}
	ev := r.aud.kind("unpin")
	if len(ev) != 1 || !ev[0].DryRun {
		t.Fatalf("unpin audit in dry run = %+v", ev)
	}
}

// --- F2: the daemon's runway notification -----------------------------------

func runwayRig(t *testing.T, five float64) *rig {
	t.Helper()
	r := newRig(t, testCfg(), true)
	st := r.d.st
	now := time.Now()
	st.Default().SetActive("a")
	a := at(five, 10, now)
	a.ID = "a"
	a.Last.FiveHour.ResetsAt = ptrTime(now.Add(4 * time.Hour))
	a.Last.SevenDay.ResetsAt = ptrTime(now.Add(4 * 24 * time.Hour))
	_, w := a.Last.Worst()
	a.PrevWorst, a.PrevAt = w-1, now.Add(-time.Minute) // 1 point a minute
	st.Accounts["a"] = a
	b := at(90, 10, now) // over its trigger, resetting after the pool runs dry
	b.ID = "b"
	b.Last.FiveHour.ResetsAt = ptrTime(now.Add(5 * time.Hour))
	b.Last.SevenDay.ResetsAt = ptrTime(now.Add(5 * 24 * time.Hour))
	st.Accounts["b"] = b
	return r
}

func runwayNotes(r *rig) []string {
	var out []string
	for _, s := range r.nt.sent {
		if strings.HasPrefix(s, "runway:") {
			out = append(out, s)
		}
	}
	return out
}

func TestDaemonNotifiesOnceWhenThePoolRunsDryWithinTwoHours(t *testing.T) {
	r := runwayRig(t, 40) // 45 minutes to the trigger, nothing after it
	for range 3 {
		r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	}
	notes := runwayNotes(r)
	if len(notes) != 1 || !strings.Contains(notes[0], "runs dry") {
		t.Fatalf("runway notifications = %v, want one", notes)
	}
}

func TestDaemonDoesNotNotifyARunwayBeyondTwoHours(t *testing.T) {
	r := runwayRig(t, 40)
	a := r.d.st.Accounts["a"]
	a.PrevWorst = 39.8 // 0.2 a minute: 225 minutes
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if notes := runwayNotes(r); len(notes) != 0 {
		t.Fatalf("notified a runway of 3h45m: %v", notes)
	}
	// Unknown is never notified.
	a.PrevAt = time.Time{}
	r.d.evaluate(context.Background(), r.d.profs[0], "poll")
	if notes := runwayNotes(r); len(notes) != 0 {
		t.Fatalf("notified an unknown runway: %v", notes)
	}
}

// `cs audit` shows an unpin with its reason, and --kind unpin selects it.
func TestAuditShowsUnpin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	render.SetColor(false)
	log, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Write(audit.Event{Kind: "unpin", Profile: "work", From: "work-1", Reason: "pin on work-1 lifted: it was refused"})
	_ = log.Write(audit.Event{Kind: "decision", Decision: "stay", Reason: "fine"})
	out := captureStdout(t, func() error { return cmdAudit([]string{"--kind", "unpin"}) })
	if !strings.Contains(out, "unpin") || !strings.Contains(out, "pin on work-1 lifted: it was refused") ||
		strings.Contains(out, "fine") {
		t.Fatalf("audit --kind unpin:\n%s", out)
	}
}

// why --json's decision names the pin it lifts.
func TestWhyJSONDecisionCarriesUnpin(t *testing.T) {
	now := time.Now()
	cfg, st := runwayState(t, now)
	st.Default().Pinned = "work-1"
	st.Accounts["work-1"].BurntTil, st.Accounts["work-1"].BurntWin = now.Add(time.Hour), "five_hour"
	dec := whyJSON(cfg, st, now, "")["decision"].(map[string]any)
	if dec["unpin"] != "pin on work-1 lifted: it was refused" || dec["target"] != "work-team" {
		t.Fatalf("decision = %v", dec)
	}
	st.Default().PinHard = true
	if dec := whyJSON(cfg, st, now, "")["decision"].(map[string]any); dec["unpin"] != nil || dec["kind"] != "stay" {
		t.Fatalf("hard pin: decision = %v", dec)
	}
}
