package credstore

import (
	"path/filepath"
	"testing"
)

// Claude Code locks its secure-storage dir before writing a refreshed token
// (GROUND_TRUTH §43). Each live item says which dir that is, so a swap takes
// the same lock as the profile it writes.
func TestALiveItemNamesTheDirClaudeCodeLocks(t *testing.T) {
	work := filepath.Join(t.TempDir(), ".claude-work")
	l, ok := LiveItem("Claude Code-credentials-abcd1234", filepath.Join(work, ".credentials.json")).(LockDirer)
	if !ok {
		t.Fatal("a configured profile's item cannot name its lock dir")
	}
	if l.LockDir() != work {
		t.Errorf("LockDir = %q, want %q", l.LockDir(), work)
	}

	env, ok := EnvLive().(LockDirer)
	if !ok {
		t.Fatal("the environment's item cannot name its lock dir")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", work)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	unsetenv(t, "CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if env.LockDir() != work {
		t.Errorf("env LockDir = %q, want CLAUDE_CONFIG_DIR %q", env.LockDir(), work)
	}
	other := filepath.Join(t.TempDir(), "secure")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", other)
	if env.LockDir() != other {
		t.Errorf("env LockDir = %q, want CLAUDE_SECURESTORAGE_CONFIG_DIR %q", env.LockDir(), other)
	}
}
