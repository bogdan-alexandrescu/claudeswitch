package main

import (
	"os"
	"strings"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// cliEnv, when set, makes this test binary run as the claudeswitch CLI with
// its value (arguments joined by cliSep) as argv: runCLI's subprocess.
const (
	cliEnv = "CLAUDESWITCH_TEST_CLI"
	cliSep = "\x1f"
)

// TestMain runs every test here with fake security and claude binaries first
// on PATH and a temporary HOME, and fails the run if either is invoked.
func TestMain(m *testing.M) {
	if argv, ok := os.LookupEnv(cliEnv); ok {
		os.Args = append([]string{"claudeswitch"}, strings.Split(argv, cliSep)...)
		main()
		os.Exit(0)
	}
	testshim.Main(m)
}
