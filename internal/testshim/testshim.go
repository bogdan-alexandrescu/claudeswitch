// Package testshim keeps a package's tests away from the real machine: fake
// `security` and `claude` go first on PATH, HOME points at a temporary
// directory, and a test run that calls either fake fails.
//
// Use it from a package's TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testshim.Run(m)) }
package testshim

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Run installs the shims, runs the tests, and returns the exit code: the tests'
// own, or 1 when they passed but reached a shimmed binary.
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "claudeswitch-shims-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer os.RemoveAll(dir)
	log := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	for _, d := range []string{bin, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	for _, name := range []string{"security", "claude"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %q\nexit 1\n", name, log)
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("HOME", home)

	code := m.Run()

	if b, err := os.ReadFile(log); err == nil && len(b) > 0 {
		fmt.Fprintf(os.Stderr, "tests invoked a shimmed binary:\n%s", b)
		if code == 0 {
			code = 1
		}
	}
	return code
}
