// Package testshim keeps tests away from the real keychain and the real
// Claude Code. Every package whose tests could reach `security` or `claude`
// runs its tests through Main.
//
// It puts fake `security`, `claude`, `launchctl` and `systemctl` first on
// PATH, points credstore at the
// fake security binary (credstore execs /usr/bin/security by absolute path, so
// PATH alone would not catch it), gives the run a temporary HOME and XDG
// directories, and fails the run if any fake was ever invoked. A test that
// reaches the keychain raises the very prompts this program exists to avoid,
// one that runs `claude` may start a real login, and one that runs launchctl
// or systemctl would reload the real daemon.
//
// It also puts fake browser launchers first on PATH (`open` and the Linux
// Chrome binaries, see Browsers). They never launch anything: each records its
// argv in a log a test reads with Launches. Unlike security and claude, running
// them is allowed, since `cs chrome` exists to run them.
package testshim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/credstore"
)

// Browsers are the launchers `cs chrome` may exec: macOS's `open`, and the
// Chrome and Chromium binaries a Linux machine has on PATH.
var Browsers = []string{"open", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}

var (
	launchMu  sync.Mutex
	launchLog string
	// marker is where the forbidden fakes (security, claude, launchctl,
	// systemctl) record each run.
	marker string
)

// Invocations is every run of a forbidden fake so far, one line each ("" for
// none), so a test can assert that one call reached none of them; Run still
// fails the whole run afterwards if any did.
func Invocations() string {
	if marker == "" {
		return ""
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		return ""
	}
	return string(b)
}

// argSep separates the arguments of one recorded launch. A unit separator
// cannot appear in any argument a test passes, unlike a space.
const argSep = "\x1f"

// Launches returns every browser launch recorded since the last ResetLaunches,
// one []string per launch: the launcher's name, then its arguments.
func Launches() [][]string {
	launchMu.Lock()
	defer launchMu.Unlock()
	if launchLog == "" {
		return nil
	}
	b, err := os.ReadFile(launchLog)
	if err != nil {
		return nil
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line != "" {
			out = append(out, strings.Split(line, argSep))
		}
	}
	return out
}

// ResetLaunches empties the launch log.
func ResetLaunches() {
	launchMu.Lock()
	defer launchMu.Unlock()
	if launchLog != "" {
		_ = os.WriteFile(launchLog, nil, 0o600)
	}
}

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

	marker = filepath.Join(dir, "invoked")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "testshim:", err)
		return 2
	}
	for _, name := range []string{"security", "claude", "launchctl", "systemctl"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + marker + "'\nexit 97\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "testshim:", err)
			return 2
		}
	}
	launchLog = filepath.Join(dir, "launches")
	if err := os.WriteFile(launchLog, nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "testshim:", err)
		return 2
	}
	for _, name := range Browsers {
		// One line per launch: the name, then each argument after a unit
		// separator (octal 037). printf's format is fixed, so no argument is
		// interpreted.
		script := "#!/bin/sh\n{ printf '%s' '" + name + "'; for a in \"$@\"; do printf '\\037%s' \"$a\"; done; " +
			"printf '\\n'; } >> '" + launchLog + "'\nexit 0\n"
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
