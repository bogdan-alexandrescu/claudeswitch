package state

import (
	"slices"
	"testing"
	"time"
)

// Lanes 12 and 13 each added state that merges on save. A daemon save and a
// CLI save interleaved must keep all of it at once: vaulted, ghosts, Chrome
// profiles, hints and emails, and the daemon's own fields.
func TestInterleavedDaemonAndCLISavesKeepEveryMergedField(t *testing.T) {
	path := isolate(t)

	seed, _ := Load(path, "default")
	seed.AddVaulted("v1")
	seed.AddVaulted("v2")
	seed.AddGhost(workGhost())
	seed.SetChrome("a", "claudeswitch-a", time.Now())
	seed.SetEmail("a", "a@example.com")
	seed.DaemonConfigHash = "hash-1"
	seed.DaemonProfiles = map[string]string{"work": ""}
	if err := seed.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	// Both processes load the same disk state.
	daemon, _ := Load(path, "default")
	cli, _ := Load(path, "default")
	cli2, _ := Load(path, "default")

	// The CLI changes everything it owns and saves.
	cli.AddVaulted("v3")
	cli.DropVaulted("v1")
	cli.SetChrome("b", "claudeswitch-b", time.Now())
	cli.SetChromeHinted("work", "rot-1")
	cli.SetEmail("b", "b@example.com")
	cli.DropGhostsOf("work")
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}

	// The daemon, stale, adds a ghost and its own fields, then saves.
	g := workGhost()
	g.Profile, g.Service, g.Account = "lab", "svc-lab", "l1"
	daemon.AddGhost(g)
	daemon.DaemonConfigHash = "hash-2"
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	// A second, stale CLI adds its own and saves after the daemon.
	cli2.AddVaulted("v4")
	cli2.SetChrome("c", "claudeswitch-c", time.Now())
	cli2.SetEmail("c", "c@example.com")
	if err := cli2.Save(); err != nil {
		t.Fatal(err)
	}

	back, _ := Load(path, "default")
	for _, id := range []string{"v2", "v3", "v4"} {
		if !slices.Contains(back.Vaulted, id) {
			t.Errorf("vaulted lost %s: %v", id, back.Vaulted)
		}
	}
	if slices.Contains(back.Vaulted, "v1") {
		t.Errorf("vaulted brought back a dropped v1: %v", back.Vaulted)
	}
	if len(back.Ghosts) != 1 || back.Ghosts[g.Key()] == nil {
		t.Errorf("ghosts = %v, want only the daemon's lab ghost", back.Ghosts)
	}
	for _, id := range []string{"a", "b", "c"} {
		if back.ChromeOf(id) == nil {
			t.Errorf("chrome profile %s lost", id)
		}
	}
	for id, want := range map[string]string{"a": "a@example.com", "b": "b@example.com", "c": "c@example.com"} {
		if back.Emails[id] != want {
			t.Errorf("email %s = %q, want %q", id, back.Emails[id], want)
		}
	}
	if back.ChromeHints["work"] != "rot-1" {
		t.Errorf("chrome hint = %v", back.ChromeHints)
	}
	if back.DaemonConfigHash != "hash-2" || back.DaemonProfiles["work"] != "" {
		t.Errorf("daemon fields = %q %v, want the daemon's kept by the CLI save", back.DaemonConfigHash, back.DaemonProfiles)
	}
}
