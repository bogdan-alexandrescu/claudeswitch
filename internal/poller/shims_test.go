package poller

import (
	"os"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/testshim"
)

// Tests here must not reach the real keychain or Claude Code; see testshim.
func TestMain(m *testing.M) { os.Exit(testshim.Run(m)) }
