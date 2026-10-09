package main

import (
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// appLease is how far ahead `app heartbeat` sets app_notifies_until. The app
// beats every minute, so one missed beat does not lapse it, and a quit app
// lapses within two minutes.
const appLease = 2 * time.Minute

const appUsage = "usage: claudeswitch app heartbeat --json"

// cmdApp is `cs app`, the commands only the macOS app runs. `app heartbeat`
// (0.6.1) records that the app is running and posting its own actionable
// rotation notifications, so the daemon skips its plain one
// (state.AppNotifiesUntil). The app never writes state.json itself.
func cmdApp(args []string) error {
	fs := appFlags("app")
	fs.Bool("json", false, "machine-readable output")
	pos, err := parseApp(fs, args, appUsage)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "heartbeat" {
		return appErr(codeUsage, appUsage, "app takes one command: heartbeat")
	}
	until := time.Now().Add(appLease).UTC().Truncate(time.Second)
	if err := state.SetAppNotifiesUntil("", until); err != nil {
		return err
	}
	return emitJSON(map[string]any{"app_notifies_until": until.Format(time.RFC3339)})
}
