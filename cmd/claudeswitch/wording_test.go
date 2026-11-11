package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The pool line said "default pool: …" for a profile that lists two pools:
// it is the profile's pools, together, that last or not.
func TestThePoolLineNamesAProfilesPools(t *testing.T) {
	path := appWorld(t, widePoolsTOML)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "200")
	render.SetColor(false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	now := time.Now()
	for _, a := range cfg.Accounts {
		st.Get(a.ID).Last, st.Get(a.ID).LastAt = pinReading(20, 30, now), now
	}
	// The live account being spent, measured just now.
	live := st.Get("personal-max")
	live.WeekSpeed, live.WeekSpeedAt, live.WeekRiseAt = 6, now, now
	st.Default().SetActive("personal-max")
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, now, "default")
	out := b.String()
	if strings.Contains(out, "default pool:") || !strings.Contains(out, "default's pools:") {
		t.Errorf("default lists two pools; the forecast is theirs:\n%s", out)
	}
}
