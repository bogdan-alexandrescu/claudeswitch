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

// The runway line said "default pool: …" for a profile that lists two pools:
// it is the profile's pools, together, that run dry or not.
func TestTheRunwayLineNamesAProfilesPools(t *testing.T) {
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
	// The live account burning a point a minute, read just now.
	live := st.Get("personal-max")
	live.PrevAt, live.PrevWorst = now.Add(-2*time.Minute), 28
	st.Default().SetActive("personal-max")
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, now, "default")
	out := b.String()
	if strings.Contains(out, "default pool:") || !strings.Contains(out, "default's pools:") {
		t.Errorf("default lists two pools; the runway is theirs:\n%s", out)
	}
}
