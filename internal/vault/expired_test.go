package vault

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// usageCalls records the token of every call to the usage endpoint.
func usageCalls(ep *fakeEndpoints) *[]string {
	var got []string
	ep.during = func(r *http.Request) {
		if r.URL.String() == usage.Endpoint {
			got = append(got, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		}
	}
	return &got
}

// R2: the usage endpoint answers an expired token with 429 (GROUND_TRUTH
// §46), so the swap's verify never sends one. The credential is installed
// and Claude Code renews it on first use; the swap stands, unverified, and
// no strike is recorded against the token.
func TestSwapDoesNotVerifyAnExpiredIncomingToken(t *testing.T) {
	v, ep, entries, it := swapWorld(t, "a-old", nil)
	entries["b"].ClaudeAIOAuth.ExpiresAt = ms(time.Now().Add(-time.Hour))
	calls := usageCalls(ep)

	res, err := v.SwapToWith(context.Background(), it, "b", "org-b", outgoingA())
	if err != nil {
		t.Fatal(err)
	}
	if res.RolledBack {
		t.Fatal("an unverified swap of an expired token was rolled back")
	}
	for _, tok := range *calls {
		if tok == "b-token" {
			t.Fatalf("the expired incoming token was sent to the usage API: %v", *calls)
		}
	}
	if it.blob.ClaudeAIOAuth.AccessToken != "b-token" {
		t.Errorf("live holds %q, want b's credential installed", it.blob.ClaudeAIOAuth.AccessToken)
	}
	if _, strikes := v.budget.CurrentBackoff("b-token"); strikes != 0 {
		t.Errorf("the expired token got %d strikes", strikes)
	}
}

// The seat probe behind HoldsAccount never sends an expired live token
// either: the answer is unknown, with no call.
func TestHoldsAccountDoesNotProbeAnExpiredLiveToken(t *testing.T) {
	entries := map[string]*keychain.Blob{"x": vaulted("x-vaulted", "ux@org")}
	v, ep := testVaultWith(t, entries, nil, map[string]string{"x-fresh-login": "ux@org"})
	live := item("b", "x-fresh-login")
	live.blob.ClaudeAIOAuth.ExpiresAt = ms(time.Now().Add(-time.Minute))
	if _, known := v.HoldsAccount(context.Background(), live, "x", "ux@org"); known {
		t.Error("an expired live token's seat cannot be known without asking")
	}
	if ep.profiles != 0 {
		t.Errorf("%d identity lookups with an expired token, want 0", ep.profiles)
	}
}

// Verify parks an expired token the same way: an error saying so, no call.
func TestVerifyDoesNotSendAnExpiredToken(t *testing.T) {
	entries := map[string]*keychain.Blob{"x": {ClaudeAIOAuth: &keychain.OAuth{AccessToken: "x-old",
		ExpiresAt: ms(time.Now().Add(-time.Minute))}}}
	v, ep := testVaultWith(t, entries, nil, nil)
	calls := usageCalls(ep)
	err := v.Verify(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("Verify = %v, want an error saying the token expired", err)
	}
	if len(*calls) != 0 {
		t.Errorf("usage calls %v, want none", *calls)
	}
}
