package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/config"
	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Renders the real state file so a change to the status view can be seen
// without running the binary, which would read the credential store.
func TestPreviewFromLiveState(t *testing.T) {
	if os.Getenv("PREVIEW") == "" {
		t.Skip("set PREVIEW=1 to render the local state file")
	}
	home, _ := os.UserHomeDir()
	cfg, err := config.Load(filepath.Join(home, ".config", "claudeswitch", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(filepath.Join(home, ".local", "state", "claudeswitch", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	Status(os.Stdout, Options{Cfg: cfg, St: st, DaemonOwns: true})
}
