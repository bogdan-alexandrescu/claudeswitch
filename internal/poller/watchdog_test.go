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

// The watchdog exists to catch a wedged daemon. A daemon serving out a
// rate-limit lock is the opposite of wedged, and killing it for that built a
// machine that could not recover: the API asked for an hour, the watchdog gave
// up after ten minutes, the service manager restarted the process, and the
// fresh one inherited the same lock and was killed again — twelve times in two
// hours, without a single reading.
func TestTheWatchdogDoesNotFireWhileDeliberatelyWaiting(t *testing.T) {
	p := New(&config.Config{}, &state.State{Accounts: map[string]*state.Account{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.budget = usage.NewBudget() // never the real ledger
	p.started = time.Now().Add(-time.Hour)
	p.lastOK = time.Now().Add(-time.Hour)

	if _, tooLong := p.Blind(10 * time.Minute); !tooLong {
		t.Fatal("an hour with no reading and no lock must trip the watchdog")
	}

	p.budget.Penalize("tok", usage.MaxLock)
	if _, tooLong := p.Blind(10 * time.Minute); tooLong {
		t.Error("the watchdog must not fire while the budget is deliberately locked")
	}

	p.budget.Succeeded("tok")
	if _, tooLong := p.Blind(10 * time.Minute); !tooLong {
		t.Error("once the lock clears, real blindness must trip the watchdog again")
	}
}
