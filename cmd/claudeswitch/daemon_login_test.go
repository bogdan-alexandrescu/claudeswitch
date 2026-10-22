package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// tokenLive is a live item holding tok.
type tokenLive struct{ name, tok string }

func (l tokenLive) Name() string { return l.name }
func (l tokenLive) Read() (*keychain.Blob, error) {
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: l.tok}}, nil
}
func (l tokenLive) Write(*keychain.Blob) error { return nil }

type loginSync struct{ path, account, token string }

// loginRig is one profile, work, live on a (seat ua@oa), whose Claude Code
// records the login of uuid/org.
func loginRig(t *testing.T, live bool, uuid, org string) (*rig, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Priority = []string{"a", "b"}
	cfg.Accounts = []config.Account{{ID: "a", AccountUUID: "ua", OrgID: "oa"}, {ID: "b", AccountUUID: "ub", OrgID: "ob"}}
	cfg.Profiles = []config.Profile{{Name: "work", Dir: dir, Pool: []string{"a", "b"}}}
	path := filepath.Join(dir, ".claude.json")
	body := `{"oauthAccount": {"accountUuid": "` + uuid + `", "emailAddress": "` + uuid + `@example.com", ` +
		`"organizationUuid": "` + org + `", "profileFetchedAt": 1}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, cfg, live)
	r.d.st.Profile("work").SetActive("a")
	r.d.profs[0].live = tokenLive{name: "item-work", tok: "a-token"}
	return r, path
}

func (r *rig) loginEvents() []string {
	var out []string
	for _, e := range r.aud.events {
		if e.Kind == "login" {
			out = append(out, e.From+" → "+e.To)
		}
	}
	return out
}

// A swap that left Claude Code logged in as another account (its profile
// read failed, or something else wrote the file) is put right on a later
// poll, with the live token, and recorded (P1, P5). A failing one is retried
// at most every loginRecheckEvery.
func TestTheDaemonBringsAStrayLoginInLine(t *testing.T) {
	r, path := loginRig(t, true, "ub", "ob")
	r.d.reconcileLogin(context.Background(), r.d.profs[0])
	if len(r.v.loginSyncs) != 1 || r.v.loginSyncs[0] != (loginSync{path, "a", "a-token"}) {
		t.Fatalf("syncs %+v, want one for a in %s with its live token", r.v.loginSyncs, path)
	}
	if ev := r.loginEvents(); len(ev) != 1 || ev[0] != "ub@example.com → a" {
		t.Errorf("audit %v, want the change recorded", ev)
	}
	r.d.reconcileLogin(context.Background(), r.d.profs[0])
	if len(r.v.loginSyncs) != 1 {
		t.Errorf("tried again at once: %+v", r.v.loginSyncs)
	}
}

// A dry run changes nothing: it says so, once.
func TestADryRunOnlyReportsAStrayLogin(t *testing.T) {
	r, _ := loginRig(t, false, "ub", "ob")
	for i := 0; i < 2; i++ {
		r.d.profs[0].loginAt = time.Time{} // the retry clock is not what is tested
		r.d.reconcileLogin(context.Background(), r.d.profs[0])
	}
	if len(r.v.loginSyncs) != 0 {
		t.Errorf("a dry run wrote the login: %+v", r.v.loginSyncs)
	}
	n := 0
	for _, e := range r.aud.events {
		if e.Kind == "login" && e.DryRun {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d dry-run login events, want 1", n)
	}
}

// A login in step, complete, is left alone, and costs no call.
func TestALoginInStepIsLeftAlone(t *testing.T) {
	r, path := loginRig(t, true, "ua", "oa")
	r.d.reconcileLogin(context.Background(), r.d.profs[0])
	if len(r.v.loginSyncs) != 0 {
		t.Errorf("synced a login in step: %+v (%s)", r.v.loginSyncs, path)
	}
}
