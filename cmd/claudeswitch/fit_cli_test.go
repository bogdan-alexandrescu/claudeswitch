package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Real pools hold long names: the profile heading's "pools …" line ran to
// 190 columns, pool list's to 118.
const widePoolsTOML = `
[[account]]
id = "personal-max"
[[account]]
id = "work-research-team"
[[account]]
id = "work-platform-team"
[[account]]
id = "work-devops-oncall"
[[account]]
id = "work-design-review"

[[pool]]
name     = "mine"
accounts = ["personal-max"]

[[pool]]
name     = "team"
accounts = ["work-research-team", "work-platform-team", "work-devops-oncall", "work-design-review"]

[[profile]]
name  = "default"
pools = ["mine", "team"]

[[profile]]
name  = "work"
dir   = "~/.claude-work"
pools = ["team"]
`

func linesWithin(t *testing.T, name, out string, width int) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > width {
			t.Errorf("%s: a %d-column line at %d columns:\n%s", name, n, width, line)
		}
	}
}

func TestPoolsLinesFitTheTerminal(t *testing.T) {
	path := appWorld(t, widePoolsTOML)
	t.Setenv("COLUMNS", "80")
	t.Setenv("NO_COLOR", "1")
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
	st.Default().SetActive("personal-max")

	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, now, "")
	linesWithin(t, "status", b.String(), 80)
	if !strings.Contains(b.String(), "work-design-review") {
		t.Errorf("fitting must keep every account named:\n%s", b.String())
	}
	b.Reset()
	renderWhy(&b, cfg, st, now, "")
	linesWithin(t, "why", b.String(), 80)

	b.Reset()
	if err := poolList(&b, path, false); err != nil {
		t.Fatal(err)
	}
	linesWithin(t, "pool list", b.String(), 80)

	old := cliLiveFor
	t.Cleanup(func() { cliLiveFor = old })
	cliLiveFor = func(config.Profile) (keychain.Live, error) { return nil, keychain.ErrNotFound }
	b.Reset()
	if err := profileList(&b, path); err != nil {
		t.Fatal(err)
	}
	linesWithin(t, "profile list", b.String(), 80)
}
