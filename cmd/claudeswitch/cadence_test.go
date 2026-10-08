package main

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// doctor explains the per-account arithmetic: how often one account is read
// in each state, against what its own allowance refills (GROUND_TRUTH §42).
func TestDoctorExplainsThePerAccountCallRate(t *testing.T) {
	cfg := testCfg()
	cfg.PollActive = config.Duration{Duration: 2 * time.Minute}
	cfg.PollHot = config.Duration{Duration: time.Minute}
	cfg.PollIdle = config.Duration{Duration: 10 * time.Minute}
	out := strings.Join(pollCadenceLines(cfg), "\n")
	for _, want := range []string{
		"[ok  ] account rate",
		"in use 30/h",
		"hot 60/h for up to 15m",
		"idle 6/h",
		"refills ~30/h",
		"a hot spell spends 7.5 of the 10 held for it",
		"only once its reading is 2m10s old (stale-decision warning at 4m0s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Below the floor is a warning with the real command, never a failure
// (owner decision 2026-10-07).
func TestDoctorWarnsAboutAnActiveCadenceBelowTheFloor(t *testing.T) {
	cfg := testCfg()
	cfg.PollActive = config.Duration{Duration: time.Minute}
	out := strings.Join(pollCadenceLines(cfg), "\n")
	for _, want := range []string{"[warn] account rate", "poll_active 1m0s runs at 2m0s", "in use 30/h",
		"fix: cs config poll_active 3m"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, bad := range []string{"FAIL", "cs config set"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in:\n%s", bad, out)
		}
	}
}

// A config every `cs config` on 0.3.x–0.5.0 produced: poll_active = "1m0s".
// It loads for the daemon at 2m, status warns, and `cs config` both lists
// and changes settings — and heals the pinned value on the way.
func TestAConfigWithTheOldPinnedCadenceStillWorks(t *testing.T) {
	path := writeConfig(t, "poll_active = \"1m0s\"\npriority = [\"a\"]\n\n[[account]]\nid = \"a\"\nscope = \"work\"\n")
	cfg := loadOrFail(t, path) // what the daemon and status load
	if cfg.PollActive.Duration != 2*time.Minute {
		t.Errorf("effective poll_active %v, want 2m", cfg.PollActive.Duration)
	}
	if w := strings.Join(cfg.Warnings(), "\n"); !strings.Contains(w, "cs config poll_active 3m") {
		t.Errorf("status shows no fix: %q", w)
	}
	if err := cmdConfig([]string{"--config", path}); err != nil {
		t.Fatalf("cs config refused to list: %v", err)
	}
	if err := cmdConfig([]string{"--config", path, "switch_at", "80"}); err != nil {
		t.Fatalf("cs config refused to change switch_at: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "\nswitch_at ") && !strings.HasPrefix(string(raw), "switch_at ") {
		t.Errorf("switch_at not written:\n%s", raw)
	}
	if strings.Contains(string(raw), "\npoll_active ") {
		t.Errorf("poll_active still pinned after a change to something else:\n%s", raw)
	}
	if err := cmdConfig([]string{"--config", path, "poll_active", "1m"}); err == nil {
		t.Error("setting poll_active 1m was accepted; it would only be raised to 2m")
	}
}

// The re-review blocker: a file pinned at 1m0s is fixed by setting another
// value, which must be accepted and written, not refused as "would run at
// 2m0s anyway" by the raise recorded on load.
func TestCsConfigFixesAPinnedFastActiveCadence(t *testing.T) {
	path := writeConfig(t, "poll_active = \"1m0s\"\npriority = [\"a\"]\n\n[[account]]\nid = \"a\"\nscope = \"work\"\n")
	if err := cmdConfig([]string{"--config", path, "poll_active", "3m"}); err != nil {
		t.Fatalf("cs config poll_active 3m refused: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !regexp.MustCompile(`(?m)^poll_active\s*=\s*"3m0s"`).Match(raw) {
		t.Errorf("3m not written:\n%s", raw)
	}
	if cfg := loadOrFail(t, path); cfg.PollActive.Duration != 3*time.Minute || len(cfg.Warnings()) != 0 {
		t.Errorf("after the fix: poll_active %v, warnings %v", cfg.PollActive.Duration, cfg.Warnings())
	}
}

// doctor's account-rate row shows the advanced numbers in force.
func TestDoctorShowsTheModelsAdvancedNumbers(t *testing.T) {
	cfg := testCfg()
	cfg.PollActive = config.Duration{Duration: 2 * time.Minute}
	hot, unseen := 6, 4.5
	cfg.HotReserve, cfg.UnseenCallsPerHour = &hot, &unseen
	out := strings.Join(pollCadenceLines(cfg), "\n")
	for _, want := range []string{"of the 6 held for it (hot_reserve)", "4.5/h set aside on a live account (unseen_calls_per_hour)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A high unseen_calls_per_hour leaves too little refill for poll_active:
// doctor warns and names the read interval that actually runs.
func TestDoctorWarnsWhenUnseenSpendStarvesTheActiveCadence(t *testing.T) {
	cfg := testCfg()
	cfg.PollActive = config.Duration{Duration: 2 * time.Minute}
	cfg.PollHot = config.Duration{Duration: time.Minute}
	ten := 10.0
	cfg.UnseenCallsPerHour = &ten
	out := strings.Join(pollCadenceLines(cfg), "\n")
	for _, want := range []string{"[warn] account rate", "the account in use is read about every 3m0s, not 2m0s",
		"unseen_calls_per_hour"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The watchdog's blind-exit ceiling stays at ten minutes however slow
// poll_active is, and the stale-decision warning at four (owner decision: at the 3m default, routine reads never warn).
func TestTheWatchdogCeilingsDoNotGrowWithPollActive(t *testing.T) {
	for _, c := range []struct {
		active      time.Duration
		blind, stal time.Duration
	}{
		{30 * time.Second, 5 * time.Minute, 90 * time.Second},
		{time.Minute, 10 * time.Minute, 3 * time.Minute},
		{2 * time.Minute, 10 * time.Minute, 4 * time.Minute},
		{3 * time.Minute, 10 * time.Minute, 4 * time.Minute},
		{5 * time.Minute, 10 * time.Minute, 4 * time.Minute},
	} {
		cfg := testCfg()
		cfg.PollActive = config.Duration{Duration: c.active}
		if got := blindLimit(cfg); got != c.blind {
			t.Errorf("poll_active %v: blind limit %v, want %v", c.active, got, c.blind)
		}
		if got := staleDecisionAfter(cfg); got != c.stal {
			t.Errorf("poll_active %v: stale-decision warning after %v, want %v", c.active, got, c.stal)
		}
	}
}

// Merge of lane 11 onto lane 12: a file pinned at poll_active = "1m0s" is
// healed by the single-line editor in the same edit as an unrelated
// `config set`, which keeps the file's other lines; and the schema the app
// builds its UI from carries the advanced settings.
func TestConfigSetHealsThePinnedPollAndTheSchemaHasTheAdvancedSettings(t *testing.T) {
	path := writeConfig(t, "# my notes\npoll_active = \"1m0s\"\nswitch_when = \"idle\"\npriority = [\"a\"]\n\n[[account]]\nid = \"a\"\nscope = \"work\"\n")
	var out bytes.Buffer
	if err := runConfigCmd(&out, path, []string{"set", "switch_at", "80"}, true, ""); err != nil {
		t.Fatalf("config set switch_at 80 --json: %v", err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if regexp.MustCompile(`(?m)^poll_active`).MatchString(text) {
		t.Errorf("the pinned 1m0s survived the edit:\n%s", text)
	}
	for _, keep := range []string{"# my notes", `switch_when = "idle"`, "switch_at = 80"} {
		if !strings.Contains(text, keep) {
			t.Errorf("edit lost or missed %q:\n%s", keep, text)
		}
	}
	cfg := loadOrFail(t, path)
	if cfg.PollActive.Duration != 3*time.Minute || len(cfg.Warnings()) != 0 {
		t.Errorf("after the edit: poll_active %v, warnings %v", cfg.PollActive.Duration, cfg.Warnings())
	}

	out.Reset()
	if err := runConfigCmd(&out, path, []string{"schema"}, true, ""); err != nil {
		t.Fatal(err)
	}
	var sch struct {
		Settings []struct {
			Key     string   `json:"key"`
			Default string   `json:"default"`
			Min     *float64 `json:"min"`
			Max     *float64 `json:"max"`
			Desc    string   `json:"description"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(out.Bytes(), &sch); err != nil {
		t.Fatal(err, out.String())
	}
	want := map[string]string{"hot_reserve": "10", "unseen_calls_per_hour": "2", "poll_active": "3m0s"}
	for _, s := range sch.Settings {
		if d, ok := want[s.Key]; ok {
			if s.Default != d || s.Desc == "" {
				t.Errorf("%s: default %q (want %q), description %q", s.Key, s.Default, d, s.Desc)
			}
			if s.Key != "poll_active" && (s.Min == nil || s.Max == nil) {
				t.Errorf("%s has no range", s.Key)
			}
			delete(want, s.Key)
		}
	}
	if len(want) > 0 {
		t.Errorf("schema is missing %v", want)
	}
}
