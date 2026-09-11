//go:build linux

package notify

import "os/exec"

// deliver uses notify-send, which is present on most desktop Linux and absent on
// servers. Absence is fine: the same information is in the log and in
// `cs status`, so a headless machine simply gets no pop-ups.
func deliver(title, subtitle, message string) {
	body := message
	if subtitle != "" {
		body = subtitle + "\n" + message
	}
	_ = exec.Command("notify-send", "--app-name=claudeswitch", title, body).Run()
}
