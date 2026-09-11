// Package notify sends macOS notifications for the things worth interrupting
// someone about: a switch that happened, nothing left to switch to, and a
// credential about to expire.
//
// Never notify for routine polling. A daemon that talks constantly gets muted,
// and then it cannot tell you the one thing that mattered.
package notify

import (
	"strings"
	"time"
)

type Notifier struct {
	Enabled bool
	last    map[string]time.Time
	minGap  time.Duration
}

func New(enabled bool) *Notifier {
	return &Notifier{Enabled: enabled, last: map[string]time.Time{}, minGap: 10 * time.Minute}
}

// escape makes a string safe inside an AppleScript double-quoted literal.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// send is the platform's notification mechanism. A notification that cannot be
// delivered is not an error worth surfacing — the same information is in the log
// and in `cs status` — so failures are swallowed here and nowhere else.
func send(title, subtitle, message string) {
	deliver(title, subtitle, message)
}

// Send shows a notification. key throttles repeats: the same key will not fire
// twice inside the minimum gap, so a stuck condition does not spam.
func (n *Notifier) Send(key, title, message string) {
	if n == nil || !n.Enabled {
		return
	}
	// Rotations are always announced. The throttle exists to stop a stuck
	// condition repeating, which a switch by definition is not.
	if key != "switch" {
		if t, ok := n.last[key]; ok && time.Since(t) < n.minGap {
			return
		}
	}
	n.last[key] = time.Now()
	send("claudeswitch", title, message)
}

// Switched is the one notification that matters: it happened, and here is what
// you moved onto. The headroom is included because the next question after
// "it switched" is always "to what, and for how long".
func (n *Notifier) Switched(from, to, reason, headroom string) {
	body := reason
	if headroom != "" {
		body = headroom + " · " + reason
	}
	n.Send("switch", from+" → "+to, body)
}

// Exhausted fires when there is nothing left to rotate to.
func (n *Notifier) Exhausted(recovers string, at time.Time) {
	msg := "no account has quota left"
	if recovers != "" {
		msg = recovers + " recovers at " + at.Local().Format("15:04")
	}
	n.Send("exhausted", "all accounts are burnt", msg)
}

// RefreshExpiring warns before an idle account's refresh token dies, since a
// dead account cannot even be polled.
func (n *Notifier) RefreshExpiring(account string, in time.Duration) {
	n.Send("refresh:"+account, account+" needs a login",
		"its refresh token expires in "+in.Round(24*time.Hour).String())
}
