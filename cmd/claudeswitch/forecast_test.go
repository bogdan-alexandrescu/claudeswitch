package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// forecastOf is cs forecast's text in speedWorld (see account_list_box_test):
// p1 live at 10 points an hour, t1 climbing at 6 elsewhere, t2 not climbing,
// s1 never measured, loose never read; every week at 30% of a 98 switch point.
func forecastOf(t *testing.T, path, only string) string {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	var b bytes.Buffer
	renderForecast(&b, cfg, st, time.Now(), only)
	return ansi.ReplaceAllString(b.String(), "")
}

func lineOf(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "▸"))
		if len(f) > 0 && f[0] == id {
			return l
		}
	}
	return ""
}

// cs forecast is the whole picture: every account's week, switch point,
// speed, hours of use left and when its week clears, then each profile's pool.
func TestForecastShowsHowLongEveryAccountLasts(t *testing.T) {
	out := forecastOf(t, speedWorld(t, "160"), "")
	for _, h := range []string{"ACCOUNT", "WEEK", "SWITCH AT", "SPEED", "LASTS", "CLEARS"} {
		if !strings.Contains(out, h) {
			t.Fatalf("no %s column:\n%s", h, out)
		}
	}
	for id, want := range map[string][]string{
		"p1":    {"▸ p1", "30%", "98%", "10/h", "~7h"},
		"t1":    {"30%", "98%", "6/h", "~11h ↑"},
		"t2":    {"30%", "98%", " - ", "∞"},
		"s1":    {"30%", "98%", " - "},
		"loose": {"98%", " - "},
	} {
		row := lineOf(out, id)
		for _, w := range want {
			if !strings.Contains(row, w) {
				t.Errorf("%s: no %q in %q", id, w, row)
			}
		}
	}
	if strings.Contains(lineOf(out, "t1"), "▸") {
		t.Errorf("t1 is live nowhere here, yet marked live:\n%s", out)
	}
	if !strings.Contains(lineOf(out, "p1"), "Sat") && !strings.Contains(lineOf(out, "p1"), ":") {
		t.Errorf("p1 does not say when its week clears: %q", lineOf(out, "p1"))
	}
	for _, w := range []string{
		"default's pools: ~20h of use left across 3 accounts",
		"work pool: hours of use unknown (no speed measured lately)",
		"↑ used elsewhere",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("no %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "no speed measured in the last") {
		t.Errorf("says nothing was measured when it was:\n%s", out)
	}
}

// --profile shows that profile's pool and its accounts only.
func TestForecastForOneProfile(t *testing.T) {
	out := forecastOf(t, speedWorld(t, "160"), "work")
	if lineOf(out, "t1") == "" || lineOf(out, "t2") == "" {
		t.Fatalf("work's accounts are missing:\n%s", out)
	}
	if lineOf(out, "p1") != "" || lineOf(out, "s1") != "" || strings.Contains(out, "default's pools") {
		t.Errorf("another profile's accounts or pool:\n%s", out)
	}
}

// Without a daemon nothing is measured: a column of dashes would look like a
// fault, so the forecast says why.
func TestForecastWithoutADaemonSaysWhy(t *testing.T) {
	out := forecastOf(t, boxWorld(t, "160"), "")
	if !strings.Contains(out, "no speed measured in the last 30 minutes") {
		t.Errorf("does not say why every forecast is unknown:\n%s", out)
	}
}

func TestForecastJSON(t *testing.T) {
	path := speedWorld(t, "160")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	m := forecastJSON(cfg, st, time.Now(), "")
	var p1 map[string]any
	for _, a := range m["accounts"].([]map[string]any) {
		if a["id"] == "p1" {
			p1 = a
		}
	}
	if p1 == nil || p1["week"] != 30.0 || p1["switch_at"] != 98.0 || p1["live"] != true ||
		p1["week_clears_at"] == nil || p1["drain"].(map[string]any)["state"] != "hours" {
		t.Errorf("p1 = %v", p1)
	}
	var def map[string]any
	for _, p := range m["pools"].([]map[string]any) {
		if p["profile"] == "default" {
			def = p
		}
	}
	if def == nil || def["state"] != "hours" || def["hours_left"] != 20.4 || def["accounts"] != 3 ||
		def["speed_per_hour"] != 10.0 {
		t.Errorf("default's pool = %v", def)
	}
}
