package config

import (
	"strings"
	"testing"
)

// Lane 7 hardening: account ids and profile names reach keychain item names
// (through security -i, where a newline ends the command and starts another)
// and recovery file names. Only plain names load.
func TestValidNameAcceptsPlainNames(t *testing.T) {
	for _, ok := range []string{"a", "personal", "work-1", "work_devops.team", "A4", "_x",
		strings.Repeat("x", 64)} {
		if err := ValidName("account id", ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func TestValidNameRefusesAnythingElse(t *testing.T) {
	for _, bad := range []string{"", "-x", ".x", "a..b", "a b", "a\nb", "a\"b", `a\b`, "a/b",
		"a@b", "é", "a\x00", "a\tb", strings.Repeat("x", 65)} {
		err := ValidName("account id", bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "account id") {
			t.Errorf("error for %q does not say what it was checking: %v", bad, err)
		}
	}
}

func TestConfigRefusesAnAccountIDThatIsNotAPlainName(t *testing.T) {
	body := "[[account]]\nid = \"evil\\nfind-generic-password -w\"\n"
	if _, err := loadString(t, body); err == nil || !strings.Contains(err.Error(), "account id") {
		t.Fatalf("an id with a newline loaded: %v", err)
	}
	if _, err := loadString(t, "[[account]]\nid = \"-rm\"\n"); err == nil {
		t.Fatal("an id starting with - loaded")
	}
}

func TestConfigRefusesAProfileNameThatIsNotAPlainName(t *testing.T) {
	body := profileAccounts + `
[[profile]]
name = "default"
pool = ["personal", "a4", "work-1", "work-2"]

[[profile]]
name = "../escape"
dir  = "~/.claude-x"
`
	if _, err := loadString(t, body); err == nil || !strings.Contains(err.Error(), "profile name") {
		t.Fatalf("a profile named ../escape loaded: %v", err)
	}
}
