package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/audit"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/render"
)

// The multi-profile header said "(switch ≥85% / ≥98%)": two figures, and no
// word for which window is which.
func TestProfileHeadingNamesBothTriggers(t *testing.T) {
	_, cfg, st := multiWorld(t)
	var b bytes.Buffer
	renderStatus(&b, cfg, st, render.Options{}, nil, time.Now(), "")
	out := b.String()
	if !strings.Contains(out, "profile work (rotates at session ≥75% / week ≥98%)") {
		t.Errorf("the heading must say which trigger is which:\n%s", out)
	}
	if strings.Contains(out, "swap") {
		t.Errorf("the daemon rotates; \"swap\" is a word for its internals:\n%s", out)
	}
}

// A severity change on a profile's unattributed live credential is recorded
// under an internal key, "active@work"; audit said it as such.
func TestAuditNamesAnUnattributedLiveAccount(t *testing.T) {
	pct := 80.0
	auditWorld(t, "120", audit.Event{Kind: "severity", Account: "active@work", LimitKind: "session",
		FromSeverity: "normal", ToSeverity: "warning", Percent: &pct})
	out := captureStdout(t, func() error { return cmdAudit(nil) })
	if strings.Contains(out, "active@work") || !strings.Contains(out, "work's live account") {
		t.Errorf("the row must say whose it is in words:\n%s", out)
	}
}
