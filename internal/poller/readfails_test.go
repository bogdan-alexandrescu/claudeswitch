package poller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// statusAPI answers each token with a fixed status: 200 carries the recorded
// usage response, 429 a refusal, anything else an error.
type statusAPI struct {
	body []byte
	code map[string]int
	org  map[string]string
}

func (s *statusAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	code, ok := s.code[tok]
	if !ok {
		code = http.StatusOK
	}
	h := http.Header{}
	body := "{}"
	switch code {
	case http.StatusOK:
		body = string(s.body)
		h.Set("anthropic-organization-id", s.org[tok])
	case http.StatusTooManyRequests:
		h.Set("Retry-After", "0")
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func withStatus(t *testing.T, p *Poller, code map[string]int, org map[string]string) {
	t.Helper()
	body, err := os.ReadFile("../usage/testdata/usage_response.json")
	if err != nil {
		t.Fatal(err)
	}
	p.budget = usage.NewBudget()
	p.client = &usage.Client{HTTP: &http.Client{Transport: &statusAPI{body: body, code: code, org: org}}}
	old := readVault
	readVault = func(string) (*keychain.Blob, error) { return nil, errors.New("no vault in tests") }
	t.Cleanup(func() { readVault = old })
}

// A2: the blind-failover count is consecutive unreadable polls. A failed call
// counts; a successful one clears the count.
func TestFailedReadsAreCountedAndASuccessClearsThem(t *testing.T) {
	p, _ := testPoller()
	withStatus(t, p, map[string]int{"bad": 500, "expired": 401}, nil)
	ctx := context.Background()
	a := &state.Account{ID: "a"}
	p.fetchInto(ctx, a, "bad", time.Time{}, usage.Scheduled)
	p.fetchInto(ctx, a, "bad", time.Time{}, usage.Scheduled)
	p.fetchInto(ctx, a, "expired", time.Time{}, usage.Scheduled)
	if a.ReadFails != 3 {
		t.Fatalf("ReadFails = %d after three failed reads, want 3", a.ReadFails)
	}
	p.fetchInto(ctx, a, "good", time.Time{}, usage.Scheduled)
	if a.ReadFails != 0 || a.Last == nil {
		t.Fatalf("ReadFails = %d after a good read (Last %v), want 0", a.ReadFails, a.Last)
	}
}

// A 429 is the usage endpoint's own burst limit (GROUND_TRUTH §42), which
// recovers within 15 minutes and says nothing about the account. It neither
// counts nor clears; nor does a poll the budget declined to make.
func TestRateLimitingIsNotUnreadable(t *testing.T) {
	p, _ := testPoller()
	withStatus(t, p, map[string]int{"bad": 500, "refused": 429}, nil)
	ctx := context.Background()
	a := &state.Account{ID: "a"}
	p.fetchInto(ctx, a, "bad", time.Time{}, usage.Scheduled)
	if r := p.fetchInto(ctx, a, "refused", time.Time{}, usage.Scheduled); r != usage.ReasonOK {
		t.Fatalf("429 call not made: %q", r)
	}
	if r := p.fetchInto(ctx, a, "refused", time.Time{}, usage.Scheduled); r == usage.ReasonOK {
		t.Fatalf("second call during backoff was made")
	}
	if a.ReadFails != 1 {
		t.Fatalf("ReadFails = %d, want 1: a 429 and a declined poll do not count", a.ReadFails)
	}
}

// The scheduled poll records when the access token it used expires, so the
// policy can tell "expired on an idle session" from real trouble. Since R2 a
// token past its expiry is parked, not sent, so nothing counts as a failure.
func TestTickRecordsTheTokenExpiry(t *testing.T) {
	st := twoProfileState()
	st.Profiles["work"].Active = "" // so a is the one account due first
	p := New(twoProfileCfg(), st, quiet())
	withStatus(t, p, map[string]int{"tok-a": 401}, nil)
	exp := time.Now().Add(-20 * time.Minute).Truncate(time.Millisecond)
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id, ExpiresAt: exp.UnixMilli()}}, nil
	}
	p.Tick(context.Background())
	a := p.st.Accounts["a"]
	if a == nil || !a.TokenExpiry.Equal(exp) {
		t.Fatalf("a's TokenExpiry = %v, want %v", a, exp)
	}
	if a.ReadFails != 0 {
		t.Errorf("ReadFails = %d, want 0: an expired token is parked, not sent", a.ReadFails)
	}
}

// Re-attribution that cannot read the live credential's usage counts against
// the account already attributed to that profile: it is the same account
// being unreadable, through its own live token.
func TestAnUnreadableLiveCredentialCountsTowardsTheActiveAccount(t *testing.T) {
	st := twoProfileState()
	p := New(twoProfileCfg(), st, quiet())
	withStatus(t, p, map[string]int{"work-token": 500}, nil)
	exp := time.Now().Add(2 * time.Hour).Truncate(time.Millisecond)
	p.SetLive("work", &expiringItem{token: "work-token", exp: exp})
	if _, err := p.PollActiveIn(context.Background(), "work"); err == nil {
		t.Fatal("want an error: the live credential could not be read")
	}
	w1 := st.Accounts["w1"]
	if w1 == nil || w1.ReadFails != 1 {
		t.Fatalf("w1 = %+v, want ReadFails 1", w1)
	}
	if !w1.TokenExpiry.Equal(exp) {
		t.Errorf("w1 TokenExpiry = %v, want the live token's %v", w1.TokenExpiry, exp)
	}
	if st.Profiles["work"].Active != "w1" {
		t.Errorf("attribution changed to %q", st.Profiles["work"].Active)
	}
}

// The streak's start is when the FIRST counted failure happened; later
// failures do not move it, and a good read clears it. The policy measures
// blindness from it, not from the age of the last reading.
func TestTheFailureStreakRecordsWhenItBegan(t *testing.T) {
	p, _ := testPoller()
	withStatus(t, p, map[string]int{"bad": 500}, nil)
	ctx := context.Background()
	a := &state.Account{ID: "a"}
	before := time.Now()
	p.fetchInto(ctx, a, "bad", time.Time{}, usage.Scheduled)
	first := a.FailSince
	if first.Before(before) || first.After(time.Now()) {
		t.Fatalf("FailSince = %v, want the time of the first failure", first)
	}
	p.fetchInto(ctx, a, "bad", time.Time{}, usage.Scheduled)
	if !a.FailSince.Equal(first) {
		t.Errorf("a second failure moved FailSince from %v to %v", first, a.FailSince)
	}
	p.fetchInto(ctx, a, "good", time.Time{}, usage.Scheduled)
	if !a.FailSince.IsZero() {
		t.Errorf("a good read left FailSince = %v", a.FailSince)
	}
}

// Claude Code's own refresh revokes the token our vault copy of the active
// account holds, until the daemon re-captures it. A 401 on that stale copy,
// while the profile's live credential holds a different token, says nothing
// about the account and must not count toward blind failover.
func TestARejectedStaleVaultTokenOfTheActiveAccountIsNotCounted(t *testing.T) {
	st := twoProfileState()
	st.Profiles["work"].Active = "" // so a is the one account due first
	p := New(twoProfileCfg(), st, quiet())
	withStatus(t, p, map[string]int{"vault-a": 401}, nil)
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "vault-" + id}}, nil
	}
	p.SetLive("default", &fakeItem{token: "live-a"})
	p.Tick(context.Background())
	a := p.st.Accounts["a"]
	if a == nil {
		t.Fatal("a was not polled")
	}
	if a.ReadFails != 0 || !a.FailSince.IsZero() {
		t.Fatalf("stale vault token counted: ReadFails %d, FailSince %v", a.ReadFails, a.FailSince)
	}

	// The live item holding that same rejected token is real trouble.
	p2 := New(twoProfileCfg(), st, quiet())
	withStatus(t, p2, map[string]int{"vault-a": 401}, nil)
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "vault-" + id}}, nil
	}
	p2.SetLive("default", &fakeItem{token: "vault-a"})
	p2.Tick(context.Background())
	if got := p2.st.Accounts["a"].ReadFails; got != 1 {
		t.Fatalf("live token itself rejected: ReadFails %d, want 1", got)
	}
}

type expiringItem struct {
	token string
	exp   time.Time
}

func (f *expiringItem) Name() string { return "expiring-item" }
func (f *expiringItem) Read() (*keychain.Blob, error) {
	return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: f.token, ExpiresAt: f.exp.UnixMilli()}}, nil
}
func (f *expiringItem) Write(*keychain.Blob) error { return errors.New("read only") }
