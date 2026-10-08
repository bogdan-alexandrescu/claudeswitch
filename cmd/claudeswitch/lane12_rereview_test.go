package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogdan-alexandrescu/claudeswitch/internal/state"
)

// Lane 12 final re-review.

// A daemon that recorded the profile in an earlier run (or before this
// edit) has not loaded the config the seed just wrote: the seed waits for
// the hash of that config, not for the name.
func TestSeedDoesNotTakeAStaleProfileMarker(t *testing.T) {
	home := createHome(t)
	path := writeConfig(t, createWithDefault)
	rec := installSeed(t, &fakeVault{})
	daemonSeams(t, true, 150*time.Millisecond)
	st, _ := state.Load("")
	st.DaemonProfiles = map[string]string{"default": "", "work": filepath.Join(home, ".claude-work")}
	st.DaemonConfigHash = "a-config-from-before"
	if err := st.SaveAs(state.OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	err := profileCreate(&bytes.Buffer{}, createOptions{cfgPath: path, name: "work", pool: []string{"a2"}, seed: "a2"})
	if got := errCode(t, err); got != codeDaemonNotLoaded {
		t.Errorf("code %q (%v)", got, err)
	}
	if rec.created {
		t.Fatal("seeded on a stale marker")
	}
}

// `cs uninstall` stops the service through launchctl/systemctl by
// absolute path, as `cs daemon` does (serviceWorld fails a bare name).
func TestUninstallRunsServiceToolsByAbsolutePath(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		f, _, _ := serviceWorld(t, goos)
		if err := cmdUninstall([]string{"--yes"}); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) == 0 {
			t.Errorf("%s: the service was not stopped", goos)
		}
	}
}
