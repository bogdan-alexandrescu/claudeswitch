package main

import (
	"os"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// No test in this package may reach the real keychain, the real Claude Code, or
// the real config and state; see testshim.
func TestMain(m *testing.M) { os.Exit(testshim.Run(m)) }
