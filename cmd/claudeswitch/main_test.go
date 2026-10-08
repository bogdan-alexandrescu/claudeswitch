package main

import (
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// TestMain runs every test here with fake security and claude binaries first
// on PATH and a temporary HOME, and fails the run if either is invoked.
func TestMain(m *testing.M) { testshim.Main(m) }
