package poller

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// R2: the usage endpoint answers an expired access token with 429, not 401
// (GROUND_TRUTH §46). Sending one bought a strike every time, for hours. A
// token past its expiry is never sent: the poll is parked, with no call and no
// strike, and its LastErr says why.
func TestAnExpiredTokenIsNeverSentToTheUsageAPI(t *testing.T) {
	p, api := testPoller()
	a := &state.Account{ID: "a"}
	r := p.fetchInto(context.Background(), a, "refused", time.Now().Add(-time.Minute), usage.Scheduled)
	if r != usage.ReasonExpired {
		t.Fatalf("reason %q, want %q", r, usage.ReasonExpired)
	}
	if api.calls["refused"] != 0 {
		t.Fatalf("an expired token reached the usage API %d times", api.calls["refused"])
	}
	if _, strikes := p.budget.CurrentBackoff("refused"); strikes != 0 {
		t.Errorf("an expired token was given %d strikes", strikes)
	}
	if !strings.Contains(a.LastErr, "expired") {
		t.Errorf("LastErr %q does not say the token expired", a.LastErr)
	}
	if a.ReadFails != 0 {
		t.Errorf("ReadFails = %d: a parked poll is not an unreadable one", a.ReadFails)
	}
	// A token with time left, or with no expiry recorded, is sent.
	p.fetchInto(context.Background(), a, "fresh", time.Now().Add(time.Hour), usage.Scheduled)
	p.fetchInto(context.Background(), a, "unknown", time.Time{}, usage.Scheduled)
	if api.calls["fresh"] != 1 || api.calls["unknown"] != 1 {
		t.Errorf("calls = %v, want one each for fresh and unknown", api.calls)
	}
}

// A parked poll keeps a needs-login verdict rather than overwriting it: the
// sign-in banner reads LastErr.
func TestAParkedPollKeepsANeedsLoginError(t *testing.T) {
	p, _ := testPoller()
	a := &state.Account{ID: "a", LastErr: "this account needs an interactive login (`claude` then /login): invalid_grant"}
	p.fetchInto(context.Background(), a, "tok", time.Now().Add(-time.Minute), usage.Scheduled)
	if !state.NeedsLoginErr(a.LastErr) {
		t.Errorf("LastErr %q lost the needs-login verdict", a.LastErr)
	}
}

// The scheduled poll of an account whose stored token has expired makes no
// call, and does not come round again on the very next tick.
func TestTickParksAnExpiredToken(t *testing.T) {
	st := twoProfileState()
	st.Profiles["work"].Active = ""
	st.Profiles["default"].Active = ""
	p := New(twoProfileCfg(), st, quiet())
	p.budget = usage.NewBudget()
	api := &fakeAPI{calls: map[string]int{}}
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}}
	exp := time.Now().Add(-20 * time.Minute)
	old := readVault
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id, ExpiresAt: exp.UnixMilli()}}, nil
	}
	t.Cleanup(func() { readVault = old })

	p.Tick(context.Background())
	if len(api.calls) != 0 {
		t.Fatalf("an expired token was sent: %v", api.calls)
	}
	a := st.Accounts["a"]
	if a == nil || !strings.Contains(a.LastErr, "expired") {
		t.Fatalf("a = %+v, want LastErr saying the token expired", a)
	}
	if next := p.nextPoll["a"]; !next.After(time.Now()) {
		t.Errorf("next poll %v: a parked account must not be due again at once", next)
	}
}

// The live read of a profile whose token expired is parked too: no call, the
// attributed account says why, and nothing counts towards blind failover.
func TestPollActiveInParksAnExpiredLiveToken(t *testing.T) {
	st := twoProfileState()
	p := New(twoProfileCfg(), st, quiet())
	api := &fakeAPI{calls: map[string]int{}}
	p.budget = usage.NewBudget()
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}}
	exp := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	p.SetLive("work", &expiringItem{token: "work-token", exp: exp})
	if _, err := p.PollActiveIn(context.Background(), "work"); err == nil {
		t.Fatal("want an error: the live token is parked")
	}
	if api.calls["work-token"] != 0 {
		t.Fatalf("the expired live token was sent %d times", api.calls["work-token"])
	}
	w1 := st.Accounts["w1"]
	if w1 == nil || !strings.Contains(w1.LastErr, "expired") || w1.ReadFails != 0 {
		t.Fatalf("w1 = %+v, want LastErr saying expired and no failure counted", w1)
	}
	if !w1.TokenExpiry.Equal(exp) {
		t.Errorf("TokenExpiry = %v, want %v", w1.TokenExpiry, exp)
	}
	if st.Profiles["work"].Active != "w1" {
		t.Errorf("attribution changed to %q", st.Profiles["work"].Active)
	}
}

// R4: a quiet profile's live account is read at poll_idle, not poll_active;
// a busy one's at poll_active as before.
func TestAQuietProfilesLiveAccountIsReadAtPollIdle(t *testing.T) {
	now := time.Now()
	for _, busy := range []bool{false, true} {
		p := New(twoProfileCfg(), twoProfileState(), quiet())
		p.Busy = func(string) bool { return busy }
		acct := at(10, 10, now)
		acct.ID = "w1"
		p.schedule("w1", now, acct)
		want := 2 * time.Minute
		if !busy {
			want = 10 * time.Minute
		}
		if got := p.nextPoll["w1"].Sub(now); got != want {
			t.Errorf("busy=%v: next poll in %v, want %v", busy, got, want)
		}
	}
}

// The overdue pull-in follows the same cadence: a quiet profile's live
// account is not due at poll_active age, and is again once the profile is
// busy.
func TestAQuietProfilesLiveAccountIsNotOverdueAtPollActive(t *testing.T) {
	now := time.Now()
	st := twoProfileState()
	st.Accounts["w1"] = at(10, 10, now.Add(-5*time.Minute))
	st.Accounts["w1"].ID = "w1"
	p := New(twoProfileCfg(), st, quiet())
	busy := false
	p.Busy = func(string) bool { return busy }
	p.nextPoll["w1"] = now.Add(5 * time.Minute)
	if p.overdueActive("w1", now) {
		t.Error("a quiet profile's live account read 5m ago is overdue at poll_active")
	}
	busy = true
	if !p.overdueActive("w1", now) {
		t.Error("once its profile is busy, a 5m-old reading of the account in use is overdue")
	}
}

// R4: a restarted poller starts each account's schedule from its last
// reading. A reading taken a minute ago is not due again at once.
func TestARestartedPollerStartsFromTheLastReading(t *testing.T) {
	now := time.Now()
	st := twoProfileState()
	st.Profiles["default"].Active = ""
	st.Profiles["work"].Active = ""
	for id, age := range map[string]time.Duration{"a": time.Minute, "b": 15 * time.Minute} {
		acct := at(10, 10, now.Add(-age))
		acct.ID = id
		st.Accounts[id] = acct
	}
	p := New(twoProfileCfg(), st, quiet())
	var due []string
	for _, a := range p.due(now) {
		due = append(due, a.ID)
	}
	got := strings.Join(due, ",")
	if got != "b,w1,w2" {
		t.Errorf("due at restart: %s, want b,w1,w2 (a was read a minute ago; w1, w2 never)", got)
	}
	if next := p.nextPoll["a"]; !next.Equal(now.Add(-time.Minute).Add(10 * time.Minute)) {
		t.Errorf("a's next poll %v, want its last reading + poll_idle", next)
	}
}

// R4: at startup a profile whose live token is the one last attributed, and
// whose account was read recently, needs no call to confirm it.
func TestResumeSkipsTheStartupReadWhenTheReadingIsRecent(t *testing.T) {
	now := time.Now()
	exp := now.Add(4 * time.Hour).Truncate(time.Millisecond)
	for _, c := range []struct {
		name    string
		key     string
		age     time.Duration
		expired bool
		want    bool
	}{
		{"recent, same token", usage.CredKey("work-token"), time.Minute, false, true},
		{"another token", usage.CredKey("other-token"), time.Minute, false, false},
		{"old reading", usage.CredKey("work-token"), time.Hour, false, false},
		{"expired token", usage.CredKey("work-token"), time.Minute, true, false},
	} {
		st := twoProfileState()
		st.Profiles["work"].LiveKey = c.key
		acct := at(10, 10, now.Add(-c.age))
		acct.ID = "w1"
		st.Accounts["w1"] = acct
		p := New(twoProfileCfg(), st, quiet())
		p.budget = usage.NewBudget()
		e := exp
		if c.expired {
			e = now.Add(-time.Minute)
		}
		p.SetLive("work", &expiringItem{token: "work-token", exp: e})
		if got := p.ResumeIn("work"); got != c.want {
			t.Errorf("%s: ResumeIn = %v, want %v", c.name, got, c.want)
		}
		if c.want {
			if !acct.TokenExpiry.Equal(exp) {
				t.Errorf("%s: TokenExpiry %v, want %v", c.name, acct.TokenExpiry, exp)
			}
			if _, ok := p.nextPoll["w1"]; !ok {
				t.Errorf("%s: no schedule seeded for w1", c.name)
			}
		}
	}
}
