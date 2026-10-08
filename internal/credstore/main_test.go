package credstore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain is internal/testshim's Main, written out here because testshim
// imports this package: fake security and claude first on PATH, this
// package's security binary pointed at the fake, a temporary HOME, and a
// failed run if either fake is ever invoked.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "claudeswitch-shim-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	marker := filepath.Join(dir, "invoked")
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	for _, d := range []string{bin, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	for _, name := range []string{"security", "claude"} {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + marker + "'\nexit 97\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	restore := UseSecurityBinary(filepath.Join(bin, "security"))

	code := m.Run()
	restore()
	if b, err := os.ReadFile(marker); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: a test ran a real-world binary through the shims:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
