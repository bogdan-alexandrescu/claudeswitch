//go:build darwin

package credstore

import (
	"strings"
	"testing"
)

// security -i reads one command per line. A name carrying a newline would end
// the add-generic-password command early and run whatever followed as a second
// command, so a control character never reaches the line.
func TestEscapeForSecurityNeverPassesAControlCharacter(t *testing.T) {
	for _, s := range []string{"a\nb", "a\rb", "a\x00b", "a\tb", "a\x7fb", "a\x1bb"} {
		got := escapeForSecurity(s)
		for _, r := range got {
			if r < 0x20 || r == 0x7f {
				t.Errorf("escapeForSecurity(%q) = %q keeps a control character", s, got)
			}
		}
	}
	if got := escapeForSecurity(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("quotes and backslashes: got %s", got)
	}
}

func TestAWriteToANameWithAControlCharacterIsRefusedWithoutRunningSecurity(t *testing.T) {
	f := &fakeKeychain{exists: true}
	f.install(t)
	err := Write(VaultService("x\ndelete-generic-password -s y"), vaultBlob())
	if err == nil {
		t.Fatal("a name with a newline was written")
	}
	if len(f.commands) != 0 {
		t.Fatalf("security was run: %q", f.commands)
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Errorf("error does not say why: %v", err)
	}
	if err := checkLiveWrite("Claude Code-credentials\n", "", vaultBlob()); err == nil {
		t.Error("checkLiveWrite accepted a name with a newline")
	}
}
