package state

import (
	"encoding/json"
	"os"
	"testing"
)

// D19 renamed "instance" to "profile" before release, but dev builds wrote
// "instances" (and ghosts with "instance"). Those files still load; the next
// save writes "profiles" only.
func TestADevBuildsInstancesKeyStillLoads(t *testing.T) {
	path := isolate(t)
	writeRaw(t, path, `{"version":1,"accounts":{},
	  "instances":{"default":{"active_account":"a"},"work":{"active_account":"w1","pinned":"w1"}},
	  "ghosts":{"old|svc|":{"instance":"old","why":"removed","service":"svc","account":"w2"}}}`)
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Default().Active != "a" {
		t.Fatalf("default = %+v, want active a", st.Default())
	}
	if w := st.Profiles["work"]; w == nil || w.Active != "w1" || w.Pinned != "w1" {
		t.Fatalf("work = %+v, want active and pinned w1", w)
	}
	gs := st.GhostList()
	if len(gs) != 1 || gs[0].Profile != "old" {
		t.Fatalf("ghosts = %+v, want one of profile old", gs)
	}

	if err := st.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["instances"]; ok {
		t.Errorf("save still writes \"instances\": %s", b)
	}
	if _, ok := top["profiles"]; !ok {
		t.Errorf("save wrote no \"profiles\": %s", b)
	}
	var ghosts map[string]map[string]any
	_ = json.Unmarshal(top["ghosts"], &ghosts)
	if len(ghosts) != 1 {
		t.Errorf("ghosts written as %s, want one", top["ghosts"])
	}
	for _, g := range ghosts {
		if _, ok := g["instance"]; ok || g["profile"] != "old" {
			t.Errorf("ghost written as %v, want profile old and no instance", g)
		}
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w := back.Profiles["work"]; w == nil || w.Active != "w1" {
		t.Fatalf("work did not survive the save: %+v", w)
	}
	if gs := back.GhostList(); len(gs) != 1 || gs[0].Profile != "old" {
		t.Fatalf("ghost did not survive the save: %+v", gs)
	}
}

// When a file has both, "profiles" is the authority.
func TestProfilesWinOverADevBuildsInstances(t *testing.T) {
	path := isolate(t)
	writeRaw(t, path, `{"version":1,"accounts":{},
	  "instances":{"default":{"active_account":"stale"}},
	  "profiles":{"default":{"active_account":"fresh"}}}`)
	st, _ := Load(path)
	if st.Default().Active != "fresh" {
		t.Fatalf("got %q, want fresh", st.Default().Active)
	}
}
