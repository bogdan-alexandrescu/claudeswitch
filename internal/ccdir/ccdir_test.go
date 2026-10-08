package ccdir

import (
	"os"
	"path/filepath"
	"testing"
)

// unsetenv removes a variable for the length of a test. t.Setenv registers the
// restore; the Unsetenv after it is what makes the variable truly absent, which
// is not the same as empty for CLAUDE_SECURESTORAGE_CONFIG_DIR.
func unsetenv(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

func fakeHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	return h
}

func mustDir(t *testing.T, f func() (string, error)) string {
	t.Helper()
	d, err := f()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDirIsDotClaudeWhenUnset(t *testing.T) {
	h := fakeHome(t)
	unsetenv(t, EnvConfigDir)
	if got, want := mustDir(t, Dir), filepath.Join(h, ".claude"); got != want {
		t.Fatalf("Dir() = %s, want %s", got, want)
	}
}

func TestDirHonoursClaudeConfigDir(t *testing.T) {
	fakeHome(t)
	t.Setenv(EnvConfigDir, "/opt/work")
	if got := mustDir(t, Dir); got != "/opt/work" {
		t.Fatalf("Dir() = %s, want /opt/work", got)
	}
}

// An empty CLAUDE_CONFIG_DIR is treated as unset, as claudeswitch always did
// for settings.json; Claude Code would use "" (its cwd), which is never meant.
func TestDirTreatsEmptyAsUnset(t *testing.T) {
	h := fakeHome(t)
	t.Setenv(EnvConfigDir, "")
	if got, want := mustDir(t, Dir), filepath.Join(h, ".claude"); got != want {
		t.Fatalf("Dir() = %s, want %s", got, want)
	}
}

// A literal ~ reaches us from a plist or a quoted assignment. Taken literally
// it is a directory named "~" under whatever the cwd is, which the daemon's cwd
// makes a different place from the one the person meant.
func TestDirExpandsALeadingTilde(t *testing.T) {
	h := fakeHome(t)
	t.Setenv(EnvConfigDir, "~/.claude-work")
	if got, want := mustDir(t, Dir), filepath.Join(h, ".claude-work"); got != want {
		t.Fatalf("Dir() = %s, want %s", got, want)
	}
}

func TestProjectsIsUnderTheConfigDir(t *testing.T) {
	fakeHome(t)
	t.Setenv(EnvConfigDir, "/opt/work")
	if got := mustDir(t, Projects); got != "/opt/work/projects" {
		t.Fatalf("Projects() = %s", got)
	}
}

// Claude Code 2.1.293: join(CLAUDE_CONFIG_DIR || homedir(), ".claude.json").
func TestGlobalConfigIsInHomeWhenUnset(t *testing.T) {
	h := fakeHome(t)
	unsetenv(t, EnvConfigDir)
	if got, want := mustDir(t, GlobalConfig), filepath.Join(h, ".claude.json"); got != want {
		t.Fatalf("GlobalConfig() = %s, want %s", got, want)
	}
}

func TestGlobalConfigIsInTheConfigDirWhenSet(t *testing.T) {
	fakeHome(t)
	d := t.TempDir()
	t.Setenv(EnvConfigDir, d)
	if got, want := mustDir(t, GlobalConfig), filepath.Join(d, ".claude.json"); got != want {
		t.Fatalf("GlobalConfig() = %s, want %s", got, want)
	}
}

// Claude Code prefers a legacy <config dir>/.config.json when one exists.
func TestGlobalConfigPrefersTheLegacyFile(t *testing.T) {
	fakeHome(t)
	d := t.TempDir()
	t.Setenv(EnvConfigDir, d)
	legacy := filepath.Join(d, ".config.json")
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustDir(t, GlobalConfig); got != legacy {
		t.Fatalf("GlobalConfig() = %s, want %s", got, legacy)
	}
}

func TestSecureStorageDirFollowsTheConfigDir(t *testing.T) {
	fakeHome(t)
	unsetenv(t, EnvSecureStorageDir)
	t.Setenv(EnvConfigDir, "/opt/work")
	if got := mustDir(t, SecureStorageDir); got != "/opt/work" {
		t.Fatalf("SecureStorageDir() = %s", got)
	}
}

func TestSecureStorageDirOverride(t *testing.T) {
	fakeHome(t)
	t.Setenv(EnvConfigDir, "/opt/work")
	t.Setenv(EnvSecureStorageDir, "/opt/keys")
	if got := mustDir(t, SecureStorageDir); got != "/opt/keys" {
		t.Fatalf("SecureStorageDir() = %s", got)
	}
}

// Set but empty means ~/.claude, whatever CLAUDE_CONFIG_DIR says.
func TestSecureStorageDirSetButEmpty(t *testing.T) {
	h := fakeHome(t)
	t.Setenv(EnvConfigDir, "/opt/work")
	t.Setenv(EnvSecureStorageDir, "")
	if got, want := mustDir(t, SecureStorageDir), filepath.Join(h, ".claude"); got != want {
		t.Fatalf("SecureStorageDir() = %s, want %s", got, want)
	}
}
