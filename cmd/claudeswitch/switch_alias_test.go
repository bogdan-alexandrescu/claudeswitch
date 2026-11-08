package main

import (
	"strings"
	"testing"
)

// `cs switch` is `cs use`, under the word people reach for.
func TestSwitchIsUse(t *testing.T) {
	path := appWorld(t, sharedTOML)
	for _, tail := range [][]string{{"p1", "--dry-run", "--config", path}, {"nobody", "--config", path}, {}} {
		want, wantCode := runCLI(t, append([]string{"use"}, tail...)...)
		got, code := runCLI(t, append([]string{"switch"}, tail...)...)
		if code != wantCode || got != want {
			t.Errorf("switch %v: exit %d, want %d\n%s\nwant:\n%s", tail, code, wantCode, got, want)
		}
	}
	out, _, code := runCLISplit(t, "switch", "--help")
	if code != 0 || !strings.Contains(out, "usage: claudeswitch use <id|number>") {
		t.Errorf("switch --help: exit %d\n%s", code, out)
	}
	out, _, _ = runCLISplit(t, "help")
	if !strings.Contains(out, "\n  switch ") {
		t.Errorf("help does not list switch:\n%s", out)
	}
}
