package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/oauth"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// expiringLive is a profile's live item holding one token with an expiry.
type expiringLive struct {
	name  string
	token string
	exp   time.Time
}

func (e *expiringLive) Name() string { return e.name }
func (e *expiringLive) Read() (*keychain.Blob, error) {
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: e.token, ExpiresAt: e.exp.UnixMilli()}}, nil
}
func (e *expiringLive) Write(*keychain.Blob) error { return nil }

// renewRig is two profiles with work's live item holding w1's token,
// expiring at exp, and both profiles quiet.
func renewRig(t *testing.T, exp time.Time) (*rig, *expiringLive) {
	t.Helper()
	r := newRig(t, twoProfiles(), true)
	r.d.st.Profile("default").SetActive("a")
	r.d.st.Profile("work").SetActive("w1")
	item := &expiringLive{name: "item-work", token: "w1-token", exp: exp}
	r.prof("work").live = item
	return r, item
}

func liveRefreshes(r *rig) []vaultCall {
	var out []vaultCall
	for _, c := range r.v.ops("refresh") {
		if c.item != nil {
			out = append(out, c)
		}
	}
	return out
}

// R2: a quiet profile whose live access token has expired is renewed by the
// daemon, through RefreshIn with that profile's own item (Claude Code's
// credential lock is held there), then read again. Audited as kind refresh.
func TestAQuietProfilesExpiredLiveTokenIsRenewed(t *testing.T) {
	r, item := renewRig(t, time.Now().Add(-time.Hour))
	r.d.maintainVault(context.Background())

	got := liveRefreshes(r)
	if len(got) != 1 || got[0].account != "w1" || got[0].item != item {
		t.Fatalf("live refreshes = %+v, want w1 through work's item", got)
	}
	reread := false
	for _, c := range r.p.calls {
		if c.op == "pollactive" && c.profile == "work" {
			reread = true
		}
	}
	if !reread {
		t.Error("the renewed profile was not read again")
	}
	ev := r.aud.kind("refresh")
	if len(ev) != 1 || ev[0].Profile != "work" || ev[0].Account != "w1" || ev[0].Err != "" {
		t.Errorf("refresh audit = %+v, want one for work/w1", ev)
	}
	if !strings.Contains(r.logs.String(), "renewed") {
		t.Errorf("the renewal was not logged: %s", r.logs.String())
	}
}

// Within refresh_window counts as well; a token with longer to run does not.
func TestALiveTokenIsRenewedOnlyWithinTheRefreshWindow(t *testing.T) {
	r, _ := renewRig(t, time.Now().Add(30*time.Minute)) // window is 1h
	r.d.maintainVault(context.Background())
	if n := len(liveRefreshes(r)); n != 1 {
		t.Errorf("30m to expiry, 1h window: %d live refreshes, want 1", n)
	}
	r2, _ := renewRig(t, time.Now().Add(3*time.Hour))
	r2.d.maintainVault(context.Background())
	if n := len(liveRefreshes(r2)); n != 0 {
		t.Errorf("3h to expiry: %d live refreshes, want 0", n)
	}
}

// A busy profile is never refreshed by the daemon: its session renews its
// own token, and a refresh would revoke the one it is using.
func TestABusyProfilesLiveTokenIsNeverRenewedByTheDaemon(t *testing.T) {
	r, _ := renewRig(t, time.Now().Add(-time.Hour))
	r.dets["work"].last = time.Now() // a transcript write just now (D15)
	r.d.maintainVault(context.Background())
	if got := liveRefreshes(r); len(got) != 0 {
		t.Fatalf("a busy profile's live item was refreshed: %+v", got)
	}
}

// At most one attempt per profile every ten minutes.
func TestLiveRenewalIsAttemptedAtMostEveryTenMinutes(t *testing.T) {
	r, _ := renewRig(t, time.Now().Add(-time.Hour))
	r.v.refreshErr = map[string]error{"w1": context.DeadlineExceeded}
	r.d.maintainVault(context.Background())
	r.d.maintainVault(context.Background())
	if n := len(liveRefreshes(r)); n != 1 {
		t.Fatalf("%d attempts in a row, want 1", n)
	}
	r.prof("work").liveRenewAt = time.Now().Add(-11 * time.Minute)
	r.d.maintainVault(context.Background())
	if n := len(liveRefreshes(r)); n != 2 {
		t.Errorf("%d attempts after ten minutes, want 2", n)
	}
}

// A dead refresh token parks the profile as needing a sign-in: the account's
// LastErr says so (the app's banner reads it), a notification goes out, and
// the audit records the failure.
func TestAFailedLiveRenewalNeedsASignIn(t *testing.T) {
	r, _ := renewRig(t, time.Now().Add(-time.Hour))
	r.v.refreshErr = map[string]error{"w1": &oauth.NeedsLoginError{Detail: "invalid_grant"}}
	r.d.maintainVault(context.Background())

	if a := r.d.st.Accounts["w1"]; a == nil || !state.NeedsLoginErr(a.LastErr) {
		t.Fatalf("w1 = %+v, want a needs-login LastErr", a)
	}
	sent := strings.Join(r.nt.sent, "\n")
	if !strings.Contains(sent, "relogin:w1") {
		t.Errorf("no sign-in notification: %s", sent)
	}
	ev := r.aud.kind("refresh")
	if len(ev) != 1 || ev[0].Err == "" || ev[0].Profile != "work" {
		t.Errorf("refresh audit = %+v, want one failure for work", ev)
	}
}

// R4: a restarted daemon whose profiles were read recently makes no startup
// call to confirm them; one that cannot resume reads as before.
func TestStartupSkipsTheLiveReadWhenTheLastReadingIsRecent(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	r.p.resume = map[string]bool{"work": true}
	r.d.start(context.Background())
	polled := map[string]bool{}
	for _, c := range r.p.calls {
		if c.op == "pollactive" {
			polled[c.profile] = true
		}
	}
	if polled["work"] {
		t.Error("work resumed from its last reading, yet was read at startup")
	}
	if !polled["default"] {
		t.Error("default could not resume and was not read at startup")
	}
}

// R4: a quiet profile's live account is read at poll_idle, so a reading
// older than poll_active is expected there and not called stale.
func TestAQuietProfileIsNotWarnedAboutAStaleReading(t *testing.T) {
	r := newRig(t, twoProfiles(), true)
	r.d.st.Profile("work").SetActive("w1")
	put(r.d.st, "w1", 10)
	put(r.d.st, "w2", 10)
	r.d.st.Accounts["w1"].LastAt = time.Now().Add(-6 * time.Minute)
	r.d.evaluate(context.Background(), r.prof("work"), "poll")
	if strings.Contains(r.logs.String(), "deciding on a stale reading") {
		t.Errorf("a quiet profile's 6m-old reading was called stale")
	}
}
