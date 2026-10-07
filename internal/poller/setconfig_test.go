package poller

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

// A config reloaded by the daemon must reach the poller: an account added
// while it runs has to be polled, promptly, without a restart.
func TestSetConfigMakesAnAddedAccountDueAtOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := &config.Config{Accounts: []config.Account{{ID: "a"}, {ID: "gone"}}}
	p := New(old, &state.State{Accounts: map[string]*state.Account{}, Active: "a"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.budget = usage.NewBudget()
	now := time.Now()
	p.nextPoll["a"] = now.Add(time.Hour)
	p.nextPoll["gone"] = now.Add(time.Hour)

	p.SetConfig(&config.Config{Accounts: []config.Account{{ID: "a"}, {ID: "new"}}})

	due := p.due(now)
	if len(due) != 1 || due[0].ID != "new" {
		t.Errorf("only the new account is due now, got %v", due)
	}
	if _, ok := p.nextPoll["gone"]; ok {
		t.Error("a removed account must not keep a schedule")
	}
}
