package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

const reloadTwoProfiles = `
switch_at = 85
priority = ["a", "b", "w1", "w2"]

[[account]]
id = "a"
[[account]]
id = "b"
[[account]]
id = "w1"
[[account]]
id = "w2"

[[profile]]
name = "default"
pool = ["a", "b"]

[[profile]]
name = "work"
dir  = "~/.claude-work"
pool = ["w1"]
`

// The config reload runs on the daemon's loop and reaches each profile: the
// pool, the thresholds and the poller all change, and nothing needs a restart.
func TestDaemonReloadAppliesPoolsAndThresholdsPerProfile(t *testing.T) {
	path := writeConfig(t, reloadTwoProfiles)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, false)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)

	body := strings.Replace(reloadTwoProfiles, `pool = ["w1"]`, `pool = ["w1", "w2"]
switch_at = 70`, 1)
	body = strings.Replace(body, "switch_at = 85", "switch_at = 80", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r.d.reload(context.Background())

	work := r.prof("work")
	if got := strings.Join(work.pool, ","); got != "w1,w2" {
		t.Errorf("work's pool after reload = %s, want w1,w2", got)
	}
	if work.cfg.SwitchAt != 70 || r.prof("default").cfg.SwitchAt != 80 {
		t.Errorf("thresholds: work %v (want 70), default %v (want 80)",
			work.cfg.SwitchAt, r.prof("default").cfg.SwitchAt)
	}
	if r.p.cfgSet != r.d.cfg || r.d.cfg == cfg {
		t.Error("the poller must be handed the reloaded config, and the daemon must adopt it")
	}
	if strings.Contains(r.logs.String(), "restart") {
		t.Errorf("nothing about the profile set changed, so no restart warning:\n%s", r.logs)
	}
}

// The hold is per profile: dropping the account live in one profile must not
// stop another profile from rotating.
func TestDaemonReloadHoldsOnlyTheProfileThatLostItsActiveAccount(t *testing.T) {
	path := writeConfig(t, reloadTwoProfiles)
	cfg := loadOrFail(t, path)
	r := newRig(t, cfg, false)
	r.d.reloader = newConfigReloader(path, cfg, r.d.log)
	st := r.d.st
	for _, id := range []string{"a", "b", "w1", "w2"} {
		put(st, id, 10)
	}
	st.Profile("default").SetActive("a")
	st.Profile("work").SetActive("w1")

	body := strings.Replace(reloadTwoProfiles, "[[account]]\nid = \"w1\"\n", "", 1)
	body = strings.Replace(body, `pool = ["w1"]`, `pool = ["w2"]`, 1)
	body = strings.Replace(body, `"w1", `, "", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r.d.reload(context.Background())

	if !r.prof("work").hold.holding() {
		t.Error("work lost its active account, so it must hold")
	}
	if r.prof("default").hold.holding() {
		t.Error("default kept its active account, so it must not hold")
	}
	if got := st.Profile("default").Active; got != "a" || got == state.Unattributed {
		t.Errorf("default's attribution is untouched, got %q", got)
	}
}
