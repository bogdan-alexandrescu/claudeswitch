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

// One pool is a pool: the heading said "pools devops (…)" for a profile
// listing one.
func TestOnePoolIsAPool(t *testing.T) {
	path := appWorld(t, widePoolsTOML)
	t.Setenv("NO_COLOR", "1")
	render.SetColor(false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load("", cfg.ProfileNames()...)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "work")
	if out := b.String(); strings.Contains(out, "pools team") || !strings.Contains(out, "pool team (") {
		t.Errorf("work lists one pool:\n%s", out)
	}
	b.Reset()
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "default")
	if out := b.String(); !strings.Contains(out, "pools mine (") {
		t.Errorf("default lists two pools:\n%s", out)
	}
}
