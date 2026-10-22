package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/profile"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Two accounts pinned to their seats, so the live one's identity is known.
const loginConfig = `
switch_at = 85
switch_at_weekly = 95
hard_floor = 98

[[account]]
id = "a"
account_uuid = "ua"
org_id = "oa"
[[account]]
id = "b"
account_uuid = "ub"
org_id = "ob"
`

// loginWorld is a running profile on a, with Claude Code's .claude.json
// recording the login of uuid/org (none at all for "").
func loginWorld(t *testing.T, uuid, org string) (*config.Config, *state.State) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("COLUMNS", "160")
	t.Setenv("NO_COLOR", "1")
	unsetenvT(t, "CLAUDE_CONFIG_DIR")
	unsetenvT(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	path := filepath.Join(h, "config.toml")
	if err := os.WriteFile(path, []byte(loginConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "" {
		body := `{"oauthAccount": {"accountUuid": "` + uuid + `", "emailAddress": "` + uuid + `@example.com", ` +
			`"organizationUuid": "` + org + `"}}`
		if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	st, _ := state.Load("")
	st.Get("a").Last, st.Get("a").LastAt = pinReading(20, 20, now), now
	st.Get("b").Last, st.Get("b").LastAt = pinReading(10, 5, now), now
	st.Default().SetActive("a")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return cfg, st
}

// Claude Code "logged in as" another account than the one in use is why
// Claude in Chrome refuses to connect: the line says so (IMPROVEMENTS P3).
func TestStatuslineSaysWhenClaudeCodeIsLoggedInAsAnotherAccount(t *testing.T) {
	cfg, st := loginWorld(t, "ub", "ob")
	got := captureStdout(t, func() error { return statuslineBody(cfg, st) })
	if !strings.Contains(got, "login ub ≠") {
		t.Errorf("line %q does not say Claude Code is logged in as ub", got)
	}
}

// The line stays quiet when the two agree, and when nothing can be said.
func TestStatuslineIsQuietAboutAMatchingOrUnknownLogin(t *testing.T) {
	for _, c := range []struct{ uuid, org string }{{"ua", "oa"}, {"", ""}} {
		cfg, st := loginWorld(t, c.uuid, c.org)
		if got := captureStdout(t, func() error { return statuslineBody(cfg, st) }); strings.Contains(got, "login") {
			t.Errorf("recorded %q: line %q mentions the login", c.uuid, got)
		}
	}
}

// The same person in another organization is another seat: it differs too.
func TestALoginInAnotherOrganizationDiffers(t *testing.T) {
	cfg, st := loginWorld(t, "ua", "ob")
	if got := captureStdout(t, func() error { return statuslineBody(cfg, st) }); !strings.Contains(got, "login ua ≠") {
		t.Errorf("line %q: ua in ob is not the seat ua in oa", got)
	}
}

// doctor names the recorded login under its profile, and when it is another
// account, the command that lines them up.
func TestDoctorNamesTheRecordedLogin(t *testing.T) {
	// A profile whose live credential resolves on either platform: on Linux
	// doctor checks the credential file is there.
	resolve := func(in config.Profile) (profile.Resolved, error) {
		dir := t.TempDir()
		cred := filepath.Join(dir, ".credentials.json")
		if err := os.WriteFile(cred, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return profile.Resolved{Name: in.Name, Dir: dir, Service: "item", CredentialFile: cred, Pool: in.Pool}, nil
	}
	cfg, st := loginWorld(t, "ub", "ob")
	var b bytes.Buffer
	failed := doctorProfiles(&b, cfg, resolve, activeOf(cfg, st))
	out := b.String()
	if !strings.Contains(out, "login") || !strings.Contains(out, "ub@example.com") ||
		!strings.Contains(out, "not the live account a") || !strings.Contains(out, "claudeswitch whoami --sync") {
		t.Errorf("doctor does not name the mismatch and its fix:\n%s", out)
	}
	if failed {
		t.Error("a mismatched login failed doctor; it is a warning")
	}

	cfg, st = loginWorld(t, "ua", "oa")
	b.Reset()
	doctorProfiles(&b, cfg, resolve, activeOf(cfg, st))
	if out := b.String(); !strings.Contains(out, "ua@example.com") || strings.Contains(out, "not the live account") {
		t.Errorf("a matching login:\n%s", out)
	}
}

// `whoami --sync` writes the login it has just read the profile for: no
// second call, and it says what changed.
func TestWhoamiSyncWritesTheLoginItRead(t *testing.T) {
	cfg, _ := loginWorld(t, "ub", "ob")
	path := identityPathOf(cfg.EffectiveProfiles()[0])
	raw := []byte(`{"account": {"uuid": "ua", "email": "ua@example.com"}, "organization": {"uuid": "oa", "name": "A Org"}}`)
	before, after, err := syncLoginFrom(path, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if before.AccountUUID != "ub" || after.AccountUUID != "ua" || after.OrgName != "A Org" || after.Partial() {
		t.Errorf("before %+v after %+v", before, after)
	}
	if _, _, err := syncLoginFrom(filepath.Join(t.TempDir(), ".claude.json"), raw, time.Now()); !os.IsNotExist(err) {
		t.Errorf("a profile with no .claude.json: %v, want not-exist", err)
	}
}

// why says it too, with what lines them up (P5): "why does Claude in Chrome
// not connect" has an answer there.
func TestWhySaysWhenClaudeCodeIsLoggedInAsAnotherAccount(t *testing.T) {
	cfg, st := loginWorld(t, "ub", "ob")
	var b bytes.Buffer
	renderWhy(&b, cfg, st, time.Now(), "")
	if out := b.String(); !strings.Contains(out, "logged in as ub@example.com") ||
		!strings.Contains(out, "Claude in Chrome") || !strings.Contains(out, "whoami --sync") {
		t.Errorf("why does not say it:\n%s", out)
	}
	cfg, st = loginWorld(t, "ua", "oa")
	b.Reset()
	renderWhy(&b, cfg, st, time.Now(), "")
	if strings.Contains(b.String(), "logged in as") {
		t.Errorf("why mentions a login in step:\n%s", b.String())
	}
}
