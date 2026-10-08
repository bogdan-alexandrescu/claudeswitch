package poller

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// D17: a profile found holding an account from another profile's pool is
// recorded as it is, and said loudly.
func TestPollActiveInFlagsAnAccountFromAnotherPool(t *testing.T) {
	st := twoProfileState()
	st.Accounts["b"] = &state.Account{ID: "b", OrgID: "org-b"}
	var logs strings.Builder
	p := New(twoProfileCfg(), st, slog.New(slog.NewTextHandler(&logs, nil)))
	withFakes(t, p, map[string]string{"work-token": "org-b"})
	p.SetLive("work", &fakeItem{token: "work-token"})

	if _, err := p.PollActiveIn(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	if got := st.Profiles["work"].Active; got != "b" {
		t.Fatalf("work attributed to %q, want the truth, b", got)
	}
	out := logs.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "another profile's pool") ||
		!strings.Contains(out, "pool_of=default") {
		t.Fatalf("no cross-pool error logged:\n%s", out)
	}

	// The same finding on the next poll is not said again.
	if _, err := p.PollActiveIn(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), "another profile's pool"); n != 1 {
		t.Fatalf("%d cross-pool lines over two polls, want 1", n)
	}
}
