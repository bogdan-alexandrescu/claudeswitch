package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// account list's table: a shared account live in work was listed under
// default, the first profile that may use it, marked "* live in its
// profile". It now says where it is live.
func TestAccountListSaysWhereASharedAccountIsLive(t *testing.T) {
	path := appWorld(t, sharedTOML)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "80")
	render.SetColor(false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st, _ := state.Load("", cfg.ProfileNames()...)
	for _, id := range []string{"p1", "t1", "t2", "s1"} {
		st.Get(id).Last, st.Get(id).LastAt = pinReading(20, 30, now), now
		// Real addresses are long; the table still fits 80 columns.
		st.SetEmail(id, id+".someone@a-rather-long-company-domain.example")
		st.SetPlan(id, "Max 20x team")
	}
	st.Get("t2").LastErr = "usage API rejected the token (401)"
	st.Default().SetActive("p1")
	st.Profile("work").SetActive("t1")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := accountList(&buf, path, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	var t1 string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "t1" {
			t1 = line
		}
	}
	if !strings.Contains(t1, "work*") {
		t.Errorf("t1 is live in work, and its row must say so:\n%s", out)
	}
	if strings.Contains(out, "live in its profile") {
		t.Errorf("\"live in its profile\" names no profile:\n%s", out)
	}
	if strings.Contains(out, "keychain lookups") {
		t.Errorf("the footer speaks of the program's internals:\n%s", out)
	}
	if !strings.Contains(out, "needs sign-in") || strings.Contains(out, "needs login") {
		t.Errorf("a dead credential's state reads needs sign-in:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if n := utf8.RuneCountInString(line); n > 80 {
			t.Errorf("a %d-column line in an 80-column terminal:\n%s", n, line)
		}
	}
}
