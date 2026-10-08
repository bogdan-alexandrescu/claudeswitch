package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// multiWorld is pinWorld with two profiles: default (personal, CLAUDE_CONFIG_DIR
// unset) and work (w1, w2, ~/.claude-work, switch_at 75). Default holds
// personal at 30%, work holds w1 at 80%: over work's 75 but under the global 85.
func multiWorld(t *testing.T) (string, *config.Config, *state.State) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("COLUMNS", "120")
	t.Setenv("NO_COLOR", "1")
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	render.SetColor(false)
	path := filepath.Join(h, "config.toml")
	if err := os.WriteFile(path, []byte(twoProfileTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st, _ := state.Load("", cfg.ProfileNames()...)
	st.Get("personal").Last, st.Get("personal").LastAt = pinReading(30, 10, now), now
	st.Get("w1").Last, st.Get("w1").LastAt = pinReading(80, 10, now), now
	st.Get("w2").Last, st.Get("w2").LastAt = pinReading(5, 5, now), now
	st.Default().SetActive("personal")
	st.Profile("work").SetActive("w1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path, cfg, st
}

// blocks splits multi-profile output at each profile heading.
func blocks(t *testing.T, out string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, name := range []string{"default", "work"} {
		i := strings.Index(out, "profile "+name)
		if i < 0 {
			t.Fatalf("no block for profile %q:\n%s", name, out)
		}
		got[name] = out[i:]
	}
	if d, w := strings.Index(out, "profile default"), strings.Index(out, "profile work"); d > w {
		t.Fatalf("blocks out of config order:\n%s", out)
	} else {
		got["default"] = out[d:w]
	}
	return got
}

// The status line runs inside a session and shows that session's profile.
func TestStatuslineShowsItsOwnProfile(t *testing.T) {
	path, _, _ := multiWorld(t)

	setCCDir(t, "~/.claude-work")
	got := captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	if !strings.Contains(got, "work") || !strings.Contains(got, "w1") || strings.Contains(got, "personal") {
		t.Fatalf("a work session's status line = %q, want work's account w1", got)
	}
	// 80% is over work's own 75 (D4), so it says what work will do.
	if !strings.Contains(got, "rotating to w2") {
		t.Fatalf("work's status line ignores its own threshold or pool: %q", got)
	}

	setCCDir(t, "")
	got = captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	if !strings.Contains(got, "default") || !strings.Contains(got, "personal") || strings.Contains(got, "w1") {
		t.Fatalf("a default session's status line = %q, want default's account personal", got)
	}

	setCCDir(t, "/not/an/profile")
	got = captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	if !strings.Contains(got, "profile") || strings.Contains(got, "personal") || strings.Contains(got, "w1") {
		t.Fatalf("a session in no profile must say so, not show another's account: %q", got)
	}
}

func TestStatusShowsOneBlockPerProfile(t *testing.T) {
	_, cfg, st := multiWorld(t)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "")
	bl := blocks(t, b.String())

	if !strings.Contains(bl["default"], "personal") || strings.Contains(bl["default"], "w1") {
		t.Fatalf("default's block must show its pool only:\n%s", bl["default"])
	}
	if !strings.Contains(bl["work"], "w1") || !strings.Contains(bl["work"], "w2") || strings.Contains(bl["work"], "personal") {
		t.Fatalf("work's block must show its pool only:\n%s", bl["work"])
	}
	// D4: each block shows its effective thresholds.
	if !strings.Contains(bl["work"], "75%") || !strings.Contains(bl["default"], "85%") {
		t.Fatalf("blocks must show each profile's effective switch_at:\n%s", b.String())
	}
	if !strings.Contains(bl["work"], "w1 at 80%") || !strings.Contains(bl["work"], "rotating to w2") {
		t.Fatalf("work's headline must be its own active account and decision:\n%s", bl["work"])
	}
}

// Owner decision 2026-10-07: with several profiles the footer and legend
// appear once, at the end, and each block's header carries its thresholds.
func TestStatusMultiProfileFooterOnce(t *testing.T) {
	_, cfg, st := multiWorld(t)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "")
	out := b.String()
	for _, once := range []string{"! flagged by the API", "swap idle, forced after"} {
		if n := strings.Count(out, once); n != 1 {
			t.Fatalf("%q appears %d times, want once:\n%s", once, n, out)
		}
	}
	if strings.Index(out, "! flagged by the API") < strings.Index(out, "profile work") {
		t.Fatalf("the legend must come after the last block:\n%s", out)
	}
	bl := blocks(t, out)
	if !strings.Contains(bl["default"], "profile default (switch ≥85% / ≥98%)") ||
		!strings.Contains(bl["work"], "profile work (switch ≥75% / ≥98%)") {
		t.Fatalf("each block header must carry its thresholds:\n%s", out)
	}
}

// With no profile config, the view is the one pinned before profiles.
func TestStatusSingleProfileIsUnchanged(t *testing.T) {
	_, cfg, st := pinWorld(t, 40)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{Vaulted: map[string]bool{"a": true, "b": true},
		Plans: map[string]string{}, Known: knownAccounts(cfg)}, nil, time.Now(), "")
	checkGolden(t, "status", b.String())
}

func TestWhyShowsOneBlockPerProfile(t *testing.T) {
	_, cfg, st := multiWorld(t)
	var b bytes.Buffer
	renderWhy(&b, cfg, st, time.Now(), "")
	bl := blocks(t, b.String())
	if !strings.Contains(bl["work"], "rotating to w2") || strings.Contains(bl["work"], "personal") {
		t.Fatalf("work's reasoning must consider its pool only:\n%s", bl["work"])
	}
	if !strings.Contains(bl["default"], "staying put") || strings.Contains(bl["default"], "w2") {
		t.Fatalf("default's reasoning must consider its pool only:\n%s", bl["default"])
	}
	if !strings.Contains(bl["work"], "75%") {
		t.Fatalf("work's block must show its effective threshold:\n%s", bl["work"])
	}
}

func TestWhySingleProfileIsUnchanged(t *testing.T) {
	_, cfg, st := pinWorld(t, 90)
	var b bytes.Buffer
	renderWhy(&b, cfg, st, time.Now(), "")
	checkGolden(t, "why", b.String())
}

// why --json lists every profile and marks the caller's own, which is the
// one `use` acts on with no flag: the switch skill reads it.
func TestWhyJSONMarksTheCurrentProfile(t *testing.T) {
	_, cfg, st := multiWorld(t)
	setCCDir(t, "~/.claude-work")
	out := whyJSON(cfg, st, time.Now(), "")
	list, _ := out["profiles"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("profiles = %v", out)
	}
	for _, m := range list {
		if cur, _ := m["current"].(bool); cur != (m["profile"] == "work") {
			t.Fatalf("current flag wrong on %v", m)
		}
	}
}

// The session-start context describes the session's own profile.
func TestContextDescribesItsOwnProfile(t *testing.T) {
	_, cfg, st := multiWorld(t)
	setCCDir(t, "~/.claude-work")
	v, err := statuslineView(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	renderContextIn(&b, cfg, st, &v, time.Now(), true)
	got := b.String()
	if !strings.Contains(got, "profile work") || !strings.Contains(got, "active w1") ||
		!strings.Contains(got, "session 75%") || strings.Contains(got, "personal") {
		t.Fatalf("a work session's context:\n%s", got)
	}
}

func TestPlanShowsOneBlockPerProfile(t *testing.T) {
	path, _, _ := multiWorld(t)
	got := captureStdout(t, func() error { return cmdPlan([]string{"--config", path, "--refresh=false"}) })
	bl := blocks(t, got)
	if !strings.Contains(bl["work"], "target    w2") {
		t.Fatalf("work's plan:\n%s", bl["work"])
	}
	if !strings.Contains(bl["default"], "decision  stay") {
		t.Fatalf("default's plan:\n%s", bl["default"])
	}
}
