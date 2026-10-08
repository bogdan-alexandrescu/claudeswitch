package config

import (
	"strings"
	"testing"
)

// D19: the block is [[profile]]. A config still saying [[instance]] (a dev
// build's) is refused with the rename hint, never silently ignored or aliased.
func TestInstanceBlockIsRefusedWithTheRenameHint(t *testing.T) {
	_, err := loadString(t, profileAccounts+`
[[instance]]
name = "work"
dir = "~/.claude-work"
pool = ["work-1", "work-2"]
`)
	if err == nil {
		t.Fatal("a config with [[instance]] loaded")
	}
	for _, want := range []string{"[[instance]]", "[[profile]]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
