package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func twoProfileCfg() *config.Config {
	return &config.Config{
		HotThreshold: 60,
		PollActive:   config.Duration{Duration: 2 * time.Minute},
		PollHot:      config.Duration{Duration: 20 * time.Second},
		PollIdle:     config.Duration{Duration: 10 * time.Minute},
		Priority:     []string{"a", "b", "w1", "w2"},
		Accounts: []config.Account{
			{ID: "a", OrgID: "org-shared"}, {ID: "b"},
			{ID: "w1", OrgID: "org-shared"}, {ID: "w2"},
		},
		Profiles: []config.Profile{
			{Name: "default", Pool: []string{"a", "b"}},
			{Name: "work", Dir: "~/.claude-work", Pool: []string{"w1", "w2"}},
		},
	}
}

func twoProfileState() *state.State {
	return &state.State{
		Accounts: map[string]*state.Account{},
		Profiles: map[string]*state.ProfileState{
			"default": {Active: "a"},
			"work":    {Active: "w1"},
		},
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Every account active in some profile is polled at poll_active, not only
// the default profile's.
func TestAnAccountActiveInAnyProfilePollsAtTheActiveCadence(t *testing.T) {
	p := New(twoProfileCfg(), twoProfileState(), quiet())
	now := time.Now()
	for id, want := range map[string]time.Duration{
		"a": 2 * time.Minute, "w1": 2 * time.Minute, "b": 10 * time.Minute, "w2": 10 * time.Minute,
	} {
		acct := at(10, 10, now)
		acct.ID = id
		p.schedule(id, now, acct)
		if got := p.nextPoll[id].Sub(now); got != want {
			t.Errorf("%s: next poll in %v, want %v", id, got, want)
		}
	}
}

// D3: with two profiles, only a busy one polls hot. A hot but quiet profile
// is not spending, so it polls at poll_active.
func TestOnlyABusyProfilePollsHot(t *testing.T) {
	now := time.Now()
	for _, busy := range []bool{false, true} {
		p := New(twoProfileCfg(), twoProfileState(), quiet())
		var asked []string
		p.Busy = func(prof string) bool { asked = append(asked, prof); return busy }
		acct := at(10, 70, now) // over hot_threshold
		acct.ID = "w1"
		movingAt(p, acct, 2, now) // and moving within reach (DESIGN 4.3c)
		p.schedule("w1", now, acct)
		want := 2 * time.Minute
		if busy {
			want = 20 * time.Second
		}
		if got := p.nextPoll["w1"].Sub(now); got != want {
			t.Errorf("busy=%v: next poll in %v, want %v", busy, got, want)
		}
		if len(asked) == 0 || asked[0] != "work" {
			t.Errorf("busy=%v: asked about %v, want work", busy, asked)
		}
	}
}

// With no profiles configured nothing competes for the budget, and the hot
// cadence is exactly what it was, whatever the activity.
func TestWithoutProfilesHotPollingIgnoresActivity(t *testing.T) {
	cfg := twoProfileCfg()
	cfg.Profiles = nil
	st := &state.State{Accounts: map[string]*state.Account{},
		Profiles: map[string]*state.ProfileState{"default": {Active: "a"}}}
	p := New(cfg, st, quiet())
	p.Busy = func(string) bool { return false }
	now := time.Now()
	acct := at(10, 70, now)
	acct.ID = "a"
	movingAt(p, acct, 2, now) // hot needs movement within reach (DESIGN 4.3c)
	p.schedule("a", now, acct)
	if got := p.nextPoll["a"].Sub(now); got != 20*time.Second {
		t.Fatalf("next poll in %v, want poll_hot", got)
	}
}

// fakeItem is a profile's live credential.
type fakeItem struct {
	token string
	reads int
}

func (f *fakeItem) Name() string { return "fake-item" }
func (f *fakeItem) Read() (*keychain.Blob, error) {
	f.reads++
	if f.token == "" {
		return nil, errors.New("empty")
	}
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: f.token}}, nil
}
func (f *fakeItem) Write(*keychain.Blob) error { return errors.New("read only") }

// usageAPI answers with the recorded usage response and an organization per
// token, so attribution can be exercised without a network.
type usageAPI struct {
	body  []byte
	orgOf map[string]string
	calls []string
}

func (u *usageAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.calls = append(u.calls, tok)
	h := http.Header{}
	h.Set("anthropic-organization-id", u.orgOf[tok])
	return &http.Response{StatusCode: 200, Header: h,
		Body: io.NopCloser(strings.NewReader(string(u.body))), Request: r}, nil
}

func withFakes(t *testing.T, p *Poller, orgOf map[string]string) *usageAPI {
	t.Helper()
	body, err := os.ReadFile("../usage/testdata/usage_response.json")
	if err != nil {
		t.Fatal(err)
	}
	api := &usageAPI{body: body, orgOf: orgOf}
	p.budget = usage.NewBudget()
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}}
	old := readVault
	readVault = func(string) (*keychain.Blob, error) { return nil, errors.New("no vault in tests") }
	t.Cleanup(func() { readVault = old })
	return api
}

// Re-attribution reads the profile's own live item and records the account
// as that profile's active one. With two accounts in one organization, the
// one in this profile's pool is the answer.
func TestPollActiveInAttributesWithinTheProfile(t *testing.T) {
	st := twoProfileState()
	st.Profiles["work"].Active = ""
	p := New(twoProfileCfg(), st, quiet())
	withFakes(t, p, map[string]string{"work-token": "org-shared"})
	item := &fakeItem{token: "work-token"}
	p.SetLive("work", item)

	if _, err := p.PollActiveIn(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	if item.reads != 1 {
		t.Errorf("work's item read %d times, want 1", item.reads)
	}
	if got := st.Profiles["work"].Active; got != "w1" {
		t.Errorf("work attributed to %q, want w1 (a shares the org but is default's)", got)
	}
	if got := st.Profiles["default"].Active; got != "a" {
		t.Errorf("default changed to %q; only work was re-attributed", got)
	}
}

// A profile whose item was never resolved is not read through this
// process's environment instead.
func TestPollActiveInRefusesAnUnresolvedProfile(t *testing.T) {
	p := New(twoProfileCfg(), twoProfileState(), quiet())
	api := withFakes(t, p, nil)
	if _, err := p.PollActiveIn(context.Background(), "work"); err == nil {
		t.Fatal("want an error for a profile with no live item")
	}
	if len(api.calls) != 0 {
		t.Errorf("made %d calls", len(api.calls))
	}
}

// An active account with no vault entry is polled through the live item of
// the profile it is active in.
func TestAnUnvaultedActiveAccountIsReadThroughItsProfilesItem(t *testing.T) {
	p := New(twoProfileCfg(), twoProfileState(), quiet())
	withFakes(t, p, nil)
	p.SetLive("work", &fakeItem{token: "work-token"})
	p.SetLive("default", &fakeItem{token: "default-token"})
	if tok, err := p.tokenFor("w1"); err != nil || tok != "work-token" {
		t.Fatalf("tokenFor(w1) = %q, %v; want work's live token", tok, err)
	}
	if _, err := p.tokenFor("w2"); err == nil {
		t.Fatal("w2 is live nowhere and has no vault entry; there is no token for it")
	}
}

// The pre-decision re-read covers the profile's own pool only.
func TestRefreshCandidatesInReadsOnlyThePool(t *testing.T) {
	p := New(twoProfileCfg(), twoProfileState(), quiet())
	api := withFakes(t, p, nil)
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id}}, nil
	}
	p.RefreshCandidatesIn(context.Background(), time.Minute, "work")
	if strings.Join(api.calls, ",") != "tok-w2" {
		t.Fatalf("read %v, want only w2 (work's pool, minus its active w1)", api.calls)
	}
}
