package poller

import (
	"context"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
)

// IMPROVEMENTS F5: `account list --json` reports refresh_expires_at from
// state.json, so the app can remind before a vaulted account needs a sign-in
// without reading the keychain. A scheduled poll of a vaulted account reads
// its entry anyway; it records the refresh token's expiry with the access
// token's, as the live poll always has.
func TestTickRecordsTheRefreshExpiry(t *testing.T) {
	st := twoProfileState()
	st.Profiles["work"].Active = ""
	p := New(twoProfileCfg(), st, quiet())
	withStatus(t, p, map[string]int{"tok-a": 401}, nil)
	rexp := time.Now().Add(4 * 24 * time.Hour).Truncate(time.Millisecond)
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id,
			ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshTokenExpiresAt: rexp.UnixMilli()}}, nil
	}
	p.Tick(context.Background())
	a := p.st.Accounts["a"]
	if a == nil || !a.RefreshExpiry.Equal(rexp) {
		t.Fatalf("a's RefreshExpiry = %v, want %v", a, rexp)
	}

	// A credential that reports none (login --direct) is unknown: what was
	// recorded for an earlier token is not kept as though it still held.
	readVault = func(id string) (*keychain.Blob, error) {
		return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id}}, nil
	}
	p.nextPoll["a"] = time.Time{}
	p.Tick(context.Background())
	if a := p.st.Accounts["a"]; !a.RefreshExpiry.IsZero() {
		t.Errorf("RefreshExpiry = %v, want unknown", a.RefreshExpiry)
	}
}
