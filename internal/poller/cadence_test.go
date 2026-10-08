package poller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// usageBody is a minimal usage response: the two windows the poller reads.
func usageBody(five, seven float64, fiveResets, sevenResets time.Time) string {
	return fmt.Sprintf(`{"five_hour":{"utilization":%g,"resets_at":%q},`+
		`"seven_day":{"utilization":%g,"resets_at":%q}}`,
		five, fiveResets.UTC().Format(time.RFC3339), seven, sevenResets.UTC().Format(time.RFC3339))
}

// scriptAPI answers each token with whatever its function says right now.
type scriptAPI struct {
	five  map[string]float64
	calls map[string]int
	now   func() time.Time
}

func (s *scriptAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.calls[tok]++
	now := s.now()
	body := usageBody(s.five[tok], 10, now.Add(3*time.Hour), now.Add(72*time.Hour))
	return &http.Response{StatusCode: 200, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func cadenceCfg() *config.Config {
	return &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 98, HotThreshold: 60,
		PollActive: config.Duration{Duration: 3 * time.Minute},
		PollHot:    config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		APIBudget:  12,
		Priority:   []string{"a", "b"},
		Accounts:   []config.Account{{ID: "a"}, {ID: "b"}},
	}
}

// clockedPoller is a poller on a fake clock, reading through scriptAPI, with
// every account's vault entry holding "tok-<id>".
func clockedPoller(t *testing.T, cfg *config.Config, st *state.State) (*Poller, *scriptAPI, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := New(cfg, st, quiet())
	p.now = now
	p.budget = usage.NewBudgetWithClock(now, func(d time.Duration) { clock = clock.Add(d) })
	api := &scriptAPI{five: map[string]float64{}, calls: map[string]int{}, now: now}
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}, Now: now}
	old := readVault
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id}}, nil
	}
	t.Cleanup(func() { readVault = old })
	return p, api, &clock
}

func activeA() *state.State {
	return &state.State{Accounts: map[string]*state.Account{},
		Profiles: map[string]*state.ProfileState{state.DefaultProfile: {Active: "a"}}}
}

// read polls account a once at its current scripted figure and returns the
// interval its next poll was scheduled at.
func read(t *testing.T, p *Poller, api *scriptAPI, clock *time.Time, five float64) time.Duration {
	t.Helper()
	api.five["tok-a"] = five
	acct := p.st.Get("a")
	if r := p.fetchInto(context.Background(), acct, "tok-a", usage.Scheduled); r != usage.ReasonOK || acct.LastErr != "" {
		t.Fatalf("poll refused: %q %s", r, acct.LastErr)
	}
	p.schedule("a", *clock, acct)
	return p.nextPoll["a"].Sub(*clock)
}

// The account in use is polled at poll_hot only while its utilization is
// moving and the trigger is within reach of the lookahead at that rate.
// Standing still near (or over) the line is not hot: nothing is changing,
// so a faster reading would say the same thing.
func TestHotOnlyWhileMovingAndWithinReach(t *testing.T) {
	for _, c := range []struct {
		name       string
		prev, cur  float64
		want       time.Duration
		quietOther bool
	}{
		{"still, over the hot threshold", 90, 90, 3 * time.Minute, false},
		{"moving, trigger far out of reach", 40, 41, 3 * time.Minute, false},
		{"moving, trigger within reach", 70, 71, time.Minute, false},
		{"moving fast, below the hot threshold", 55, 57, time.Minute, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
			read(t, p, api, clock, c.prev)
			*clock = clock.Add(time.Minute)
			if got := read(t, p, api, clock, c.cur); got != c.want {
				t.Errorf("%v -> %v over a minute: next poll in %v, want %v", c.prev, c.cur, got, c.want)
			}
		})
	}
}

// D3 still holds: with two profiles a quiet one never polls hot, however its
// account is moving.
func TestAQuietProfileNeverPollsHot(t *testing.T) {
	cfg := cadenceCfg()
	cfg.Profiles = []config.Profile{
		{Name: "default", Pool: []string{"a"}},
		{Name: "work", Dir: "~/.claude-work", Pool: []string{"b"}},
	}
	p, api, clock := clockedPoller(t, cfg, activeA())
	p.Busy = func(string) bool { return false }
	read(t, p, api, clock, 70)
	*clock = clock.Add(time.Minute)
	if got := read(t, p, api, clock, 72); got != 3*time.Minute {
		t.Errorf("quiet profile: next poll in %v, want poll_active", got)
	}
}

// Once the account stops moving, the cadence drifts back to poll_active by
// doubling rather than jumping, so a pause between turns does not lose the
// close watch at once.
func TestCoolingDriftsBackToTheActiveCadence(t *testing.T) {
	p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
	read(t, p, api, clock, 70)
	*clock = clock.Add(time.Minute)
	if got := read(t, p, api, clock, 71); got != time.Minute {
		t.Fatalf("not hot to begin with: %v", got)
	}
	var got []time.Duration
	for i := 0; i < 4; i++ {
		*clock = clock.Add(StillAfter) // no rise for longer than StillAfter
		got = append(got, read(t, p, api, clock, 71))
	}
	want := []time.Duration{2 * time.Minute, 3 * time.Minute, 3 * time.Minute, 3 * time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cooling intervals %v, want %v", got, want)
		}
	}
}

// An account whose own allowance is down to the reserve is deferred, not
// called — and the tick goes on to the next due account instead of ending.
func TestTickDefersAnAccountAtItsReserveAndReadsTheNext(t *testing.T) {
	p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
	p.budget.SetAllowance(10_000)
	for i := 0; i < usage.AccountBurst-usage.AccountReserve; i++ {
		p.budget.Allow("tok-a", usage.Scheduled)
	}
	api.five["tok-a"], api.five["tok-b"] = 10, 10
	p.Tick(context.Background())
	if api.calls["tok-a"] != 0 {
		t.Errorf("a was called with its allowance at the reserve")
	}
	if api.calls["tok-b"] != 1 {
		t.Errorf("b was not read in a's place: %d calls", api.calls["tok-b"])
	}
	if want := clock.Add(usage.AccountRefill); !p.nextPoll["a"].Equal(want) {
		t.Errorf("a deferred to %v, want when its allowance next fits (%v)", p.nextPoll["a"].Sub(*clock), usage.AccountRefill)
	}
}

// Re-attribution reads the live account; that reading stands in for its next
// scheduled poll rather than being a second call on the same allowance.
func TestAReattributionReadCountsAsTheActivePoll(t *testing.T) {
	cfg := cadenceCfg()
	cfg.Accounts[0].OrgID = "org-a"
	p, api, clock := clockedPoller(t, cfg, activeA())
	p.client.HTTP.Transport = orgAPI{api, map[string]string{"tok-a": "org-a"}}
	readVault = func(string) (*keychain.Blob, error) { return nil, errors.New("live only") }
	p.SetLive(state.DefaultProfile, &fakeItem{token: "tok-a"})
	api.five["tok-a"] = 10
	if _, err := p.PollActiveIn(context.Background(), state.DefaultProfile); err != nil {
		t.Fatal(err)
	}
	if want := clock.Add(3 * time.Minute); !p.nextPoll["a"].Equal(want) {
		t.Errorf("after re-attribution a's next poll is in %v, want poll_active", p.nextPoll["a"].Sub(*clock))
	}
	p.Tick(context.Background())
	if api.calls["tok-a"] != 1 {
		t.Errorf("a was read %d times; the tick repeated the re-attribution's call", api.calls["tok-a"])
	}
}

// Reading a profile's live item marks its token live in the shared ledger,
// so a 429 on it from any caller — the vault's included — gets the live cap.
func TestReattributionMarksTheLiveToken(t *testing.T) {
	cfg := cadenceCfg()
	cfg.Accounts[0].OrgID = "org-a"
	p, api, clock := clockedPoller(t, cfg, activeA())
	p.client.HTTP.Transport = orgAPI{api, map[string]string{"tok-live": "org-a"}}
	p.SetLive(state.DefaultProfile, &fakeItem{token: "tok-live"})
	api.five["tok-live"] = 10
	if _, err := p.PollActiveIn(context.Background(), state.DefaultProfile); err != nil {
		t.Fatal(err)
	}
	var d time.Duration
	for i := 0; i < 5; i++ {
		p.budget.Penalize("tok-live", 0)
		d, _ = p.budget.CurrentBackoff("tok-live")
		*clock = clock.Add(d + time.Second)
	}
	if d > usage.MaxLiveBackoff {
		t.Errorf("the live token backed off %v, cap %v", d, usage.MaxLiveBackoff)
	}
}

// Routine reads keep the full hot-reserve floor, so the reserve rebuilds
// (owner decision 2026-10-07: steady 3m reads). The one exception: a read of
// the account in use whose reading is overdue (config.OverdueAfter) may go
// down to the swap reserve + 1, so that after a hot spell the reading never
// reaches the daemon's 4m stale-decision cap. No recovery window: at the
// overdue cadence the reserve still rebuilds.
func TestOnlyAnOverdueReadOfTheAccountInUseGoesBelowTheHotReserve(t *testing.T) {
	for _, c := range []struct {
		name      string
		id        string
		age       time.Duration
		wantCalls int
	}{
		{"in use, due but not overdue", "a", 3 * time.Minute, 0},
		{"in use, overdue", "a", config.OverdueAfter(3 * time.Minute), 1},
		{"idle, however old", "b", time.Hour, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, api, clock := clockedPoller(t, cadenceCfg(), activeA())
			p.budget.SetAllowance(10_000)
			tok := "tok-" + c.id
			for i := 0; i < usage.AccountBurst-usage.AccountReserve-3; i++ {
				p.budget.Allow(tok, usage.Swap) // 3 above the swap reserve
			}
			acct := p.st.Get(c.id)
			acct.Last = at(50, 10, *clock).Last
			acct.LastAt = clock.Add(-c.age)
			api.five[tok] = 50
			for _, other := range []string{"a", "b"} {
				if other != c.id {
					p.nextPoll[other] = clock.Add(time.Hour) // only c.id is due
				}
			}
			p.Tick(context.Background())
			if api.calls[tok] != c.wantCalls {
				t.Errorf("%s read %d times, want %d", c.id, api.calls[tok], c.wantCalls)
			}
		})
	}
}

// orgAPI adds an organization header to scriptAPI's answers.
type orgAPI struct {
	*scriptAPI
	orgOf map[string]string
}

func (o orgAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := o.scriptAPI.RoundTrip(r)
	if err == nil {
		resp.Header.Set("anthropic-organization-id", o.orgOf[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")])
	}
	return resp, err
}
