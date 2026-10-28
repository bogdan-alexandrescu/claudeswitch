package main

import (
	"bytes"
	"strings"
	"testing"
)

// The command list and the pin's words use the views' terms: a person
// switches (the daemon rotates; "swap" is its internals), and a dead
// credential needs sign-in.
func TestTheCommandListAndPinSpeakTheViewsTerms(t *testing.T) {
	var b bytes.Buffer
	usageText(&b)
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, "swap") {
			t.Errorf("the command list says swap: %q", line)
		}
	}
	t.Setenv("HOME", t.TempDir())
	out, _, _ := runCLISplit(t, "account", "--help")
	if strings.Contains(out, "needs a sign-in") || !strings.Contains(out, "needs sign-in") {
		t.Errorf("account's help must say \"needs sign-in\":\n%s", out)
	}
}
