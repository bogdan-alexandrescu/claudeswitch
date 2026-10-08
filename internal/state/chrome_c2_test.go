package state

import (
	"testing"
	"time"
)

// IMPROVEMENTS C2: an account mapped to a Chrome profile the person already
// had is marked so, and survives a reload.
func TestChromeExistingMappingSurvivesASaveAndLoad(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default")
	st.SetChromeExisting("research", "Profile 2", time.Now())
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	cp := back.ChromeOf("research")
	if cp == nil || cp.Dir != "Profile 2" || !cp.Existing {
		t.Fatalf("ChromeOf(research) = %+v", cp)
	}
}

// The account a rotation came from is kept with its time: the daemon's save
// adopts a newer `cs use` switch whole, from and time together.
func TestLastFromTravelsWithLastSwitch(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	daemon.Get("personal")
	daemon.Get("research")
	daemon.Get("work-1")
	ps := daemon.Profile("default")
	ps.SetActive("research")
	ps.LastSwitch, ps.LastFrom = time.Now().Add(-time.Hour), "personal"
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	cli, _ := Load(path, "default")
	if got := cli.Profile("default").LastFrom; got != "personal" {
		t.Fatalf("after reload last_from = %q", got)
	}
	c := cli.Profile("default")
	c.SetActive("work-1")
	c.LastSwitch, c.LastFrom = time.Now(), "research"
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	if got := daemon.Profile("default").LastFrom; got != "research" {
		t.Fatalf("daemon kept last_from %q, want the CLI's newer research", got)
	}
}
