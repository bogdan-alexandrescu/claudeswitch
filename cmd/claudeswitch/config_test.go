package main

import (
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
)

// Every setting `cs config` offers must survive being written. It reported
// "poll_active 1m0s → 2m0s, written" and the file never had the line
// (observed 2026-09-16).
func TestEveryConfigSettingIsWritten(t *testing.T) {
	values := map[string]string{
		"switch_at": "80", "switch_at_weekly": "95", "hard_floor": "99",
		"switch_when": "immediate", "max_switch_wait": "45s", "cooldown": "7m0s",
		"poll_active": "2m0s", "poll_hot": "1m0s", "poll_idle": "15m0s",
		"api_budget": "10", "refresh_window": "2h0m0s", "refresh_probe": "12h0m0s",
	}
	for _, s := range settings() {
		v, ok := values[s.name]
		if !ok {
			t.Errorf("no test value for setting %q; add one", s.name)
			continue
		}
		path := filepath.Join(t.TempDir(), "config.toml")
		cfg, _ := config.Load(path) // missing file: the defaults
		cfg.Priority = []string{"a"}
		cfg.Accounts = []config.Account{{ID: "a", Scope: "work"}}
		if err := s.set(cfg, v); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s=%s is not a valid test value: %v", s.name, v, err)
		}
		if err := cfg.Write(path); err != nil {
			t.Fatal(err)
		}
		back, err := config.Load(path)
		if err != nil {
			t.Fatalf("%s: written config does not load: %v", s.name, err)
		}
		if got := s.get(back); got != v {
			t.Errorf("%s: set %s, read back %s", s.name, v, got)
		}
	}
}
