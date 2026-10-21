package poller

import (
	"context"
	"errors"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// The usage endpoint names the live credential's organization, not its seat,
// and several seats can share one organization. Taking the first of them in
// priority order filed the live account's reading under another seat every
// re-attribution, until the seat check put it back seconds later: the other
// seat showed the live one's 12% while it was really at 86%, a reading a
// rotation could have chosen it on (observed 2026-10-20, three seats in one
// team organization).
func seatCfg() *config.Config {
	return &config.Config{
		Priority: []string{"m1", "m2", "m3"},
		Accounts: []config.Account{
			{ID: "m1", OrgID: "org-team"}, {ID: "m2", OrgID: "org-team"}, {ID: "m3", OrgID: "org-team"},
		},
		Profiles: []config.Profile{{Name: "default", Pool: []string{"m1", "m2", "m3"}}},
	}
}

func seatWorld(t *testing.T, active string, vaulted map[string]string) (*Poller, *state.State) {
	t.Helper()
	st := &state.State{Accounts: map[string]*state.Account{},
		Profiles: map[string]*state.ProfileState{"default": {Active: active}}}
	p := New(seatCfg(), st, quiet())
	withFakes(t, p, map[string]string{"live-token": "org-team"})
	readVault = func(id string) (*keychain.Blob, error) {
		if tok, ok := vaulted[id]; ok {
			return &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: tok}}, nil
		}
		return nil, errors.New("not vaulted")
	}
	p.SetLive("default", &fakeItem{token: "live-token"})
	return p, st
}

func TestAttributionKeepsTheCurrentSeatOfASharedOrganization(t *testing.T) {
	p, st := seatWorld(t, "m2", map[string]string{"m1": "m1-token", "m2": "m2-old", "m3": "m3-token"})
	if _, err := p.PollActiveIn(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if got := st.Profiles["default"].Active; got != "m2" {
		t.Fatalf("attributed to %q, want m2: it was live, and the organization alone cannot say otherwise", got)
	}
	if st.Accounts["m1"] != nil && st.Accounts["m1"].Last != nil {
		t.Error("the live reading was filed under m1")
	}
}

func TestAttributionMatchesTheStoredTokenFirst(t *testing.T) {
	for _, active := range []string{"", "m1"} {
		p, st := seatWorld(t, active, map[string]string{"m1": "m1-token", "m3": "live-token"})
		if _, err := p.PollActiveIn(context.Background(), "default"); err != nil {
			t.Fatal(err)
		}
		if got := st.Profiles["default"].Active; got != "m3" {
			t.Errorf("active %q before: attributed to %q, want m3, whose stored token is the live one", active, got)
		}
	}
}

func TestAttributionFallsBackToPriorityWithNothingElseToGoOn(t *testing.T) {
	p, st := seatWorld(t, "", nil)
	if _, err := p.PollActiveIn(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	if got := st.Profiles["default"].Active; got != "m1" {
		t.Errorf("attributed to %q, want m1, the first in priority, as before", got)
	}
}
