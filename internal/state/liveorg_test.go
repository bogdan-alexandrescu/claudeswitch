package state

import (
	"testing"
	"time"
)

// The live token's key and organization travel with Active: whichever side
// established Active more recently (a verified swap in the CLI, or the
// daemon reading the item) wins all three in a merge.
func TestLiveOrgTravelsWithActive(t *testing.T) {
	path := isolate(t)
	seed, _ := Load(path, "default")
	seed.Profile("work").SetActive("work-1")
	seed.Profile("work").LiveKey, seed.Profile("work").LiveOrg = "k-old", "org-old"
	if err := seed.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	daemon, _ := Load(path, "default")
	cli, _ := Load(path, "default")

	// A CLI swap establishes a newer Active, with its token's key and org.
	time.Sleep(2 * time.Millisecond)
	w := cli.Profile("work")
	w.SetActive("work-team")
	w.LiveKey, w.LiveOrg = "k-new", "org-new"
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	// The daemon, with its older belief, saves after it.
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(path, "default")
	g := got.Profile("work")
	if g.Active != "work-team" || g.LiveKey != "k-new" || g.LiveOrg != "org-new" {
		t.Fatalf("after the daemon's older save: active %q key %q org %q; want the swap's", g.Active, g.LiveKey, g.LiveOrg)
	}
}
