package poller

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// fakeAPI answers the usage endpoint without a network: 429 for the token
// "refused", 500 for anything else. Neither path reaches the keychain.
type fakeAPI struct{ calls map[string]int }

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.calls[tok]++
	code := http.StatusInternalServerError
	h := http.Header{}
	if tok == "refused" {
		code = http.StatusTooManyRequests
		h.Set("Retry-After", "0")
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
}

func testPoller() (*Poller, *fakeAPI) {
	api := &fakeAPI{calls: map[string]int{}}
	p := New(&config.Config{}, &state.State{Accounts: map[string]*state.Account{}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.budget = usage.NewBudget() // never the real ledger
	p.client = &usage.Client{HTTP: &http.Client{Transport: api}}
	return p, api
}

// Every poll used to be recorded twice — once by the caller asking the budget,
// again inside fetchInto — so the window allowed half the calls it says.
func TestAPollIsCountedOnce(t *testing.T) {
	p, api := testPoller()
	before := p.budget.Remaining()
	p.fetchInto(context.Background(), &state.Account{ID: "a"}, "tok", usage.Scheduled)
	if api.calls["tok"] != 1 {
		t.Fatalf("made %d calls, want 1", api.calls["tok"])
	}
	if spent := before - p.budget.Remaining(); spent != 1 {
		t.Errorf("one call spent %d from the budget", spent)
	}
}

// The refused account waits; the others go on being read.
func TestARefusalPausesOnlyThatAccount(t *testing.T) {
	p, api := testPoller()
	ctx := context.Background()
	live := &state.Account{ID: "live"}

	if r := p.fetchInto(ctx, live, "refused", usage.Scheduled); r != usage.ReasonOK {
		t.Fatalf("first call refused locally: %q", r)
	}
	if r := p.fetchInto(ctx, live, "refused", usage.Scheduled); r != usage.ReasonLockout {
		t.Errorf("the refused account called again during its backoff: %q", r)
	}
	if api.calls["refused"] != 1 {
		t.Errorf("refused account reached the API %d times, want 1", api.calls["refused"])
	}
	if r := p.fetchInto(ctx, &state.Account{ID: "idle"}, "idle", usage.Scheduled); r != usage.ReasonOK {
		t.Errorf("an idle account was held by another account's lock: %q", r)
	}
	if api.calls["idle"] != 1 {
		t.Errorf("idle account reached the API %d times, want 1", api.calls["idle"])
	}
}

// The daemon's budget is the one on disk that every process shares.
func TestNewUsesTheSharedBudget(t *testing.T) {
	p := New(&config.Config{}, &state.State{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if p.budget != usage.Shared() {
		t.Error("poller.New made a private budget; its calls are invisible to every other process")
	}
}
