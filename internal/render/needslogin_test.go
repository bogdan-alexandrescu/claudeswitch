package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/usage"
)

func ptr(v float64) *float64 { return &v }

func statusFor(t *testing.T, lastErr string, lastAt time.Time) string {
	t.Helper()
	resets := time.Now().Add(2 * time.Hour)
	cfg := &config.Config{
		SwitchAt: 85, SwitchAtWeekly: 95, HardFloor: 96,
		PollActive: config.Duration{Duration: time.Minute},
		PollIdle:   config.Duration{Duration: 10 * time.Minute},
		Accounts:   []config.Account{{ID: "work-a"}},
		Priority:   []string{"work-a"},
	}
	st := &state.State{Active: "work-a", Accounts: map[string]*state.Account{
		"work-a": {
			Last: &usage.Usage{
				FiveHour: usage.Window{Utilization: ptr(10), ResetsAt: &resets},
				SevenDay: usage.Window{Utilization: ptr(20), ResetsAt: &resets},
			},
			LastAt:  lastAt,
			LastErr: lastErr,
		},
	}}
	var buf bytes.Buffer
	Status(&buf, Options{Cfg: cfg, St: st, DaemonOwns: true})
	return buf.String()
}

// The condition a person actually has to act on must be visible, and must say
// what to run. It used to be hidden twice over: the state column showed a
// headroom verdict computed from figures that could never be refreshed, and the
// warning appeared only if a staleness timer had also elapsed.
func TestAnAccountNeedingASignInSaysSoAndSaysWhatToRun(t *testing.T) {
	for _, tc := range []struct{ name, err, want string }{
		{"never captured", "account \"work-a\" is not in the vault: no stored credential", "claudeswitch add work-a"},
		{"revoked", "usage API rejected the token (401): the account may need a re-login", "claudeswitch add work-a"},
		{"refresh dead", "this account needs an interactive login (`claude` then /login): invalid_grant", "claudeswitch add work-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A *fresh* reading, so nothing else would have raised a warning.
			out := statusFor(t, tc.err, time.Now())
			if !strings.Contains(out, "needs login") {
				t.Errorf("the state column must say the account needs a login:\n%s", out)
			}
			if !strings.Contains(out, "needs a sign-in") {
				t.Errorf("a warning must name the account:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the warning must give the command to run (%q):\n%s", tc.want, out)
			}
		})
	}
}

// A healthy account must not be told to sign in.
func TestAHealthyAccountIsNotToldToSignIn(t *testing.T) {
	out := statusFor(t, "", time.Now())
	if strings.Contains(out, "needs a sign-in") || strings.Contains(out, "needs login") {
		t.Errorf("no sign-in prompt belongs on a healthy account:\n%s", out)
	}
}

// A transient poll failure is not a sign-in problem, and must not be dressed up
// as one — the account is fine and will be read on the next tick.
func TestATransientPollFailureIsNotASignInProblem(t *testing.T) {
	out := statusFor(t, "not polled: remaining call is reserved for a swap check", time.Now())
	if strings.Contains(out, "needs a sign-in") {
		t.Errorf("a deferred poll is not a credential problem:\n%s", out)
	}
}

// Figures for an account that cannot be read again are leftovers. Shown in the
// same colours as a live account they read as current, so a dead account looked
// like a healthy one with room to spare.
func TestFrozenFiguresAreGreyed(t *testing.T) {
	SetColor(true)
	t.Cleanup(func() { SetColor(false) })

	dead := statusFor(t, "usage API rejected the token (401): may need a re-login", time.Now())
	live := statusFor(t, "", time.Now())

	const greyCode = "\033[90m"
	deadRow := rowFor(dead, "work-a")
	liveRow := rowFor(live, "work-a")
	if !strings.Contains(deadRow, greyCode) {
		t.Errorf("a frozen row must be greyed, got %q", deadRow)
	}
	if strings.Contains(liveRow, greyCode) {
		t.Errorf("a live row must keep its severity colours, got %q", liveRow)
	}
	// Recolouring must not leave an inner reset behind, which would end the grey
	// partway across the cell.
	if i := strings.Index(deadRow, greyCode); i >= 0 {
		if j := strings.Index(deadRow[i+len(greyCode):], "10%"); j < 0 {
			t.Errorf("the figure should sit inside the grey run, got %q", deadRow)
		}
	}
}

func rowFor(out, account string) string {
	for _, line := range strings.Split(out, "\n") {
		// The table row, not the headline: only the row carries the bars.
		if strings.Contains(line, account) && strings.ContainsAny(line, "▰▱") {
			return line
		}
	}
	return ""
}

// The refresher discovers a spent refresh token, and it is the one condition
// here a person must act on: no amount of waiting renews it. It was logged and
// notified but never written to LastErr, which is the only thing stateOf reads
// — so the row went on showing whatever the last poll had said. In practice
// that was a stale rate-limit message, or "window reset · re-reading" forever,
// both of which read as transient.
func TestASpentRefreshTokenOutranksAStaleTransientError(t *testing.T) {
	const spent = "this account needs an interactive login (`claude` then /login): " +
		`{"error": "invalid_grant", "error_description": "Refresh token not found or invalid"}`

	out := statusFor(t, spent, time.Now())
	if !strings.Contains(out, "needs login") {
		t.Errorf("a spent refresh token must show in the state column:\n%s", out)
	}
	if strings.Contains(out, "window reset") {
		t.Errorf("it must not be reported as a window that will re-read:\n%s", out)
	}

	// And a merely rate-limited account is not told to sign in.
	out = statusFor(t, "usage API rate limited, retry in 52m50s", time.Now())
	if strings.Contains(out, "needs login") {
		t.Errorf("a transient refusal is not a login problem:\n%s", out)
	}
}
