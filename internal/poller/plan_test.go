package poller

import (
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/keychain"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 15: the daemon records the plan a vault entry names when it reads
// the entry to poll, so `cs account list` can show it without the keychain.
func TestPollingRecordsTheVaultedPlan(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{{ID: "a"}, {ID: "b"}}}
	st := &state.State{Accounts: map[string]*state.Account{}, Profiles: map[string]*state.ProfileState{}}
	p := New(cfg, st, quiet())
	old := readVault
	readVault = func(id string) (*keychain.Blob, error) {
		b := &keychain.Blob{ClaudeAIOAuth: &keychain.OAuth{AccessToken: "tok-" + id}}
		if id == "a" {
			b.Meta = &keychain.Meta{Plan: "Max 20x"}
		}
		return b, nil
	}
	t.Cleanup(func() { readVault = old })
	for _, id := range []string{"a", "b"} {
		if _, _, err := p.tokenInfo(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.PlanOf("a"); got != "Max 20x" {
		t.Errorf("PlanOf(a) = %q", got)
	}
	if got := st.PlanOf("b"); got != "" {
		t.Errorf("PlanOf(b) = %q: an entry with no plan records none", got)
	}
}
