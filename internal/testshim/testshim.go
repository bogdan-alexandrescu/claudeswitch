// Package testshim keeps tests away from the real keychain and the real
// Claude Code. Every package whose tests could reach `security` or `claude`
// runs its tests through Main.
//
// It puts fake `security` and `claude` first on PATH, points credstore at the
// fake security binary (credstore execs /usr/bin/security by absolute path, so
// PATH alone would not catch it), gives the run a temporary HOME and XDG
// directories, and fails the run if either fake was ever invoked. A test that
// reaches the keychain raises the very prompts this program exists to avoid,
// and one that runs `claude` may start a real login.
package testshim

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/credstore"
)

// Main runs m with the shims in place and exits.
func Main(m *testing.M) { os.Exit(Run(m)) }

// Run is Main without the exit, for a TestMain that has more to do.
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "claudeswitch-shim-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testshim:", err)
		return 2
	}
	defer os.RemoveAll(dir)

	marker := filepath.Join(dir, "invoked")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "testshim:", err)
		return 2
	}
	for _, name := range []string{"security", "claude"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + marker + "'\nexit 97\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "testshim:", err)
			return 2
		}
	}
	env := map[string]string{
		"PATH":            bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME":            filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "home", ".config"),
		"XDG_STATE_HOME":  filepath.Join(dir, "home", ".local", "state"),
		"XDG_DATA_HOME":   filepath.Join(dir, "home", ".local", "share"),
	}
	for k, v := range env {
		if k != "PATH" {
			if err := os.MkdirAll(v, 0o700); err != nil {
				fmt.Fprintln(os.Stderr, "testshim:", err)
				return 2
			}
		}
		os.Setenv(k, v)
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	restore := credstore.UseSecurityBinary(filepath.Join(bin, "security"))
	defer restore()

	code := m.Run()
	if b, err := os.ReadFile(marker); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: a test ran a real-world binary through the shims:\n%s", b)
		return 1
	}
	return code
}
