//go:build darwin

package notify

import "os/exec"

func deliver(title, subtitle, message string) {
	script := `display notification "` + escape(message) +
		`" with title "` + escape(title) + `" subtitle "` + escape(subtitle) + `"`
	_ = exec.Command("osascript", "-e", script).Run()
}
