package state

import (
	"testing"
	"time"
)

func workGhost() *Ghost {
	return &Ghost{Profile: "work", Why: GhostRemoved, Dir: "~/.claude-work", Service: "Claude Code-credentials-1234abcd",
		Account: "w1", Seat: "u1@o1", Since: time.Now().Add(-time.Hour)}
}

// Lane 7 review (owner decision: ghosts): a removed or re-pointed profile's
// old live item is guarded until released, across daemon restarts.
func TestGhostsSurviveASaveAndLoad(t *testing.T) {
	path := isolate(t)
	st, err := Load(path, "default")
	if err != nil {
		t.Fatal(err)
	}
	st.AddGhost(workGhost())
	if err := st.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path, "default")
	if err != nil {
		t.Fatal(err)
	}
	gs := back.GhostList()
	if len(gs) != 1 || gs[0].Profile != "work" || gs[0].Account != "w1" || gs[0].Service == "" ||
		gs[0].Why != GhostRemoved {
		t.Fatalf("ghosts after reload: %+v", gs)
	}
}

// `cs profile forget` (a CLI save) removes a ghost the daemon holds in
// memory, and the daemon's next save does not bring it back; a ghost the
// daemon adds is not lost to a CLI save in between.
func TestGhostMergeHonoursForgetAndKeepsNewOnes(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	daemon.AddGhost(workGhost())
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	cli, _ := Load(path, "default")
	if n := cli.DropGhostsOf("work"); n != 1 {
		t.Fatalf("forget dropped %d", n)
	}
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}

	// The daemon still has it in memory; its next save must adopt the forget.
	other := workGhost()
	other.Profile, other.Service, other.Account = "lab", "svc-lab", "l1"
	daemon.AddGhost(other)
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, g := range daemon.GhostList() {
		got[g.Profile] = true
	}
	if got["work"] || !got["lab"] {
		t.Fatalf("daemon's ghosts after merge: %v", got)
	}
	disk, _ := Load(path, "default")
	if len(disk.GhostList()) != 1 || disk.GhostList()[0].Profile != "lab" {
		t.Fatalf("disk ghosts: %+v", disk.GhostList())
	}

	// A CLI save that knows nothing of a ghost keeps it.
	cli2, _ := Load(path, "default")
	cli2.Ghosts = nil
	if err := cli2.Save(); err != nil {
		t.Fatal(err)
	}
	if d, _ := Load(path, "default"); len(d.GhostList()) != 1 {
		t.Fatal("a CLI save that never touched ghosts dropped one")
	}
}

func TestGhostReleaseIsPersisted(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default")
	g := workGhost()
	st.AddGhost(g)
	_ = st.SaveAs(OwnerDaemon)
	st.DropGhost(g.Key())
	_ = st.SaveAs(OwnerDaemon)
	if d, _ := Load(path, "default"); len(d.GhostList()) != 0 {
		t.Fatal("a released ghost came back")
	}
}

// Renaming an account renames it in the ghosts that guard it.
func TestRenameGhostAccount(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default")
	st.AddGhost(workGhost())
	_ = st.Save()
	st.RenameGhostAccount("w1", "w-one")
	_ = st.Save()
	d, _ := Load(path, "default")
	if gs := d.GhostList(); len(gs) != 1 || gs[0].Account != "w-one" {
		t.Fatalf("ghosts %+v", gs)
	}
}
