package vault

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// refuseAll answers every call with the §42 refusal.
type refuseAll struct{}

func (refuseAll) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("Retry-After", "0")
	return &http.Response{StatusCode: http.StatusTooManyRequests, Header: h,
		Body: io.NopCloser(strings.NewReader(`{"type":"rate_limit_error"}`)), Request: r}, nil
}

// backoffAfter drives five consecutive refusals of token through call,
// waiting out each lock, and returns the last backoff.
func backoffAfter(t *testing.T, v *Vault, clock *time.Time, token string, call func()) time.Duration {
	t.Helper()
	var d time.Duration
	for i := 0; i < 5; i++ {
		call()
		d, _ = v.budget.CurrentBackoff(token)
		*clock = clock.Add(d + time.Second)
	}
	return d
}

func refusingVault() (*Vault, *time.Time) {
	clock := time.Now()
	v := New(quietLogger())
	v.client = &usage.Client{HTTP: &http.Client{Transport: refuseAll{}}}
	v.budget = usage.NewBudgetWithClock(func() time.Time { return clock }, func(d time.Duration) { clock = clock.Add(d) })
	v.budget.SetAllowance(10_000)
	return v, &clock
}

// A 429 on a token some profile is running on — marked live in the shared
// ledger by whoever read it there — backs off no further than the live cap,
// even when the vault (swap verification, store) is the caller.
func TestAVaultCallRefusedOnALiveTokenUsesTheLiveCap(t *testing.T) {
	v, clock := refusingVault()
	v.budget.MarkLive("live-tok")
	got := backoffAfter(t, v, clock, "live-tok", func() {
		_, _ = v.fetch(context.Background(), "live-tok", usage.Swap)
	})
	if got > usage.MaxLiveBackoff {
		t.Errorf("live token locked for %v, cap %v", got, usage.MaxLiveBackoff)
	}
}

// The seat probe only ever asks about live tokens.
func TestASeatProbeRefusalUsesTheLiveCap(t *testing.T) {
	v, clock := refusingVault()
	got := backoffAfter(t, v, clock, "live-tok", func() {
		v.probeMu.Lock()
		v.probes = nil
		v.probeMu.Unlock()
		v.seatBehind(context.Background(), "live-tok")
	})
	if got > usage.MaxLiveBackoff {
		t.Errorf("probed live token locked for %v, cap %v", got, usage.MaxLiveBackoff)
	}
}
