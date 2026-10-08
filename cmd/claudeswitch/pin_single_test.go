package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/policy"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// These pin the single-profile output of status, why, plan and the status
// line, as it was before profiles reached the CLI. With no [[profile]]
// blocks every view must stay exactly as it was.

const pinConfig = `
switch_at = 85
switch_at_weekly = 95
hard_floor = 98

[[account]]
id = "a"
[[account]]
id = "b"
`

func pinReading(five, seven float64, now time.Time) *usage.Usage {
	r5, r7 := now.Add(2*time.Hour+30*time.Minute+20*time.Second), now.Add(3*24*time.Hour+20*time.Second)
	return &usage.Usage{
		FiveHour:  usage.Window{Utilization: &five, ResetsAt: &r5},
		SevenDay:  usage.Window{Utilization: &seven, ResetsAt: &r7},
		FetchedAt: now,
	}
}

// pinWorld writes the fixture config and state under a temporary HOME, with
// colour off and a fixed width, and returns the config path.
func pinWorld(t *testing.T, five float64) (string, *config.Config, *state.State) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("COLUMNS", "120")
	t.Setenv("NO_COLOR", "1")
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	render.SetColor(false)
	t.Cleanup(func() { render.SetColor(false) })
	path := filepath.Join(h, "config.toml")
	if err := os.WriteFile(path, []byte(pinConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st, _ := state.Load("")
	st.Get("a").Last, st.Get("a").LastAt = pinReading(five, 20, now), now
	st.Get("b").Last, st.Get("b").LastAt = pinReading(10, 5, now), now
	st.Default().SetActive("a")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return path, cfg, st
}

// captureStdout runs f with os.Stdout sent to a buffer.
func captureStdout(t *testing.T, f func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	ferr := f()
	w.Close()
	os.Stdout = old
	out := <-done
	if ferr != nil {
		t.Fatalf("command failed: %v\n%s", ferr, out)
	}
	return out
}

func TestPinSingleProfileStatusRender(t *testing.T) {
	_, cfg, st := pinWorld(t, 40)
	dec := policy.Decide(policy.Input{Cfg: cfg, St: st, Now: time.Now(),
		LastSwitch: st.Default().LastSwitch, Pinned: st.Default().Pinned, Lookahead: lookahead(cfg)})
	var b bytes.Buffer
	render.Status(&b, render.Options{Cfg: cfg, St: st, Decision: &dec,
		Vaulted: map[string]bool{"a": true, "b": true}, Plans: map[string]string{}, Known: knownAccounts(cfg)})
	checkGolden(t, "status", b.String())
}

func TestPinSingleProfileWhyRender(t *testing.T) {
	_, cfg, st := pinWorld(t, 90)
	dec, verdicts := policy.Explain(policy.Input{Cfg: cfg, St: st, Now: time.Now(),
		LastSwitch: st.Default().LastSwitch, Pinned: st.Default().Pinned, Lookahead: lookahead(cfg)})
	var b bytes.Buffer
	render.Why(&b, render.WhyOptions{Cfg: cfg, St: st, Decision: dec, Verdicts: verdicts})
	checkGolden(t, "why", b.String())
}

func TestPinSingleProfilePlan(t *testing.T) {
	path, _, _ := pinWorld(t, 90)
	got := captureStdout(t, func() error { return cmdPlan([]string{"--config", path, "--refresh=false"}) })
	checkGolden(t, "plan", got)
}

func TestPinSingleProfileStatusline(t *testing.T) {
	path, _, _ := pinWorld(t, 90)
	got := captureStdout(t, func() error { return cmdStatusline([]string{"--config", path}) })
	checkGolden(t, "statusline", got)
}

// checkGolden compares output with testdata/pin/<name>.golden, written from
// the code before profiles reached the CLI. CLAUDESWITCH_UPDATE_GOLDEN=1
// rewrites it, which must only ever be done on purpose.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "pin", name+".golden")
	if os.Getenv("CLAUDESWITCH_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("single-profile %s output changed.\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}
