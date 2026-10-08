package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolate keeps the state lock (which lives next to DefaultPath, under $HOME)
// out of the real state directory, and returns a fresh state path.
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return filepath.Join(t.TempDir(), "state.json")
}

func writeRaw(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyState is the shape a 0.4.x binary reads and writes. It has no idea
// profiles exist, so it drops "profiles" whenever it saves.
type legacyState struct {
	Version    int                 `json:"version"`
	Accounts   map[string]*Account `json:"accounts"`
	Active     string              `json:"active_account,omitempty"`
	Pinned     string              `json:"pinned,omitempty"`
	LastSwitch time.Time           `json:"last_switch,omitzero"`
	ActiveAt   time.Time           `json:"active_at,omitzero"`
	DaemonLive bool                `json:"daemon_live"`
}

const oldFile = `{
  "version": 1,
  "accounts": {"personal": {"id": "personal", "org_id": "o1"}, "work": {"id": "work"}},
  "active_account": "personal",
  "pinned": "work",
  "last_switch": "2026-10-01T10:00:00Z",
  "active_at": "2026-10-01T10:00:05Z",
  "daemon_live": true,
  "vaulted": ["personal", "work"]
}`

func TestOldStateFileLoadsIntoTheDefaultProfile(t *testing.T) {
	path := isolate(t)
	writeRaw(t, path, oldFile)

	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	in := st.Profiles[DefaultProfile]
	if in == nil {
		t.Fatalf("no %q profile after migrating an old file: %+v", DefaultProfile, st.Profiles)
	}
	if in.Active != "personal" || in.Pinned != "work" {
		t.Fatalf("migrated active/pinned = %q/%q, want personal/work", in.Active, in.Pinned)
	}
	if want := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC); !in.LastSwitch.Equal(want) {
		t.Fatalf("LastSwitch = %v, want %v", in.LastSwitch, want)
	}
	if want := time.Date(2026, 10, 1, 10, 0, 5, 0, time.UTC); !in.ActiveAt.Equal(want) {
		t.Fatalf("ActiveAt = %v, want %v", in.ActiveAt, want)
	}
	if st.Default() != in {
		t.Fatal("Default() must be the migrated profile, not a fresh one")
	}
	// Global fields stay global.
	if len(st.Accounts) != 2 || !st.DaemonLive || len(st.Vaulted) != 2 {
		t.Fatalf("global fields lost in migration: %+v", st)
	}
}

func TestProfilesRoundTrip(t *testing.T) {
	path := isolate(t)
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	st, _ := Load(path)
	st.Get("personal")
	st.Get("w1")
	*st.Default() = ProfileState{Active: "personal", ActiveAt: at, LastSwitch: at.Add(-time.Hour), Pinned: ""}
	*st.Profile("work") = ProfileState{Active: "w1", ActiveAt: at.Add(time.Minute), Pinned: "w1"}
	if err := st.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want default and work", got.Profiles)
	}
	d, w := got.Profiles[DefaultProfile], got.Profiles["work"]
	if d.Active != "personal" || !d.ActiveAt.Equal(at) || !d.LastSwitch.Equal(at.Add(-time.Hour)) || d.Pinned != "" {
		t.Fatalf("default did not round-trip: %+v", d)
	}
	if w.Active != "w1" || !w.ActiveAt.Equal(at.Add(time.Minute)) || w.Pinned != "w1" || !w.LastSwitch.IsZero() {
		t.Fatalf("work did not round-trip: %+v", w)
	}
}

// Profile creates on first use, so a caller can always write through it.
func TestProfileIsCreatedOnDemand(t *testing.T) {
	var st State
	st.Default().SetActive("x")
	if st.Profiles[DefaultProfile].Active != "x" || st.Profiles[DefaultProfile].ActiveAt.IsZero() {
		t.Fatalf("SetActive through Default() did not stick: %+v", st.Profiles)
	}
	if st.Profile("work") != st.Profile("work") {
		t.Fatal("Profile must return the same record each time")
	}
}

// D10: the multi-profile release writes "profiles" only. The old top-level
// fields are still READ (an old file migrates, see above), but writing them
// would let a stale mirror outlive the profile it copied.
func TestSaveWritesNoLegacyTopLevelFields(t *testing.T) {
	path := isolate(t)
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	st, _ := Load(path)
	st.Get("personal")
	*st.Default() = ProfileState{Active: "personal", ActiveAt: at, LastSwitch: at, Pinned: "personal"}
	*st.Profile("work") = ProfileState{Active: "w1", ActiveAt: at.Add(time.Hour)}
	if err := st.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"active_account", "pinned", "last_switch", "active_at"} {
		if v, ok := top[k]; ok {
			t.Errorf("state.json still writes the legacy top-level %q = %s", k, v)
		}
	}
	if _, ok := top["profiles"]; !ok {
		t.Fatalf("state.json has no profiles: %s", b)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Default(); d.Active != "personal" || d.Pinned != "personal" || !d.ActiveAt.Equal(at) {
		t.Fatalf("default did not round-trip without the mirror: %+v", d)
	}
}

// A file an older binary saved last (it drops "profiles") still migrates on
// the next load, so a daemon left running across the upgrade loses nothing.
func TestAFileAnOlderBinarySavedStillMigrates(t *testing.T) {
	path := isolate(t)
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	old := legacyState{Version: 1, Accounts: map[string]*Account{}, Active: "work", ActiveAt: at, Pinned: "personal"}
	b, _ := json.Marshal(old)
	writeRaw(t, path, string(b))

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Default(); d.Active != "work" || !d.ActiveAt.Equal(at) || d.Pinned != "personal" {
		t.Fatalf("the older binary's file did not migrate: %+v", d)
	}
}

// A file holding both shapes was written by this version, which writes them
// identically; "profiles" is authoritative.
func TestProfilesWinOverTheLegacyMirror(t *testing.T) {
	path := isolate(t)
	writeRaw(t, path, `{"version":1,"accounts":{},
	  "active_account":"stale",
	  "profiles":{"default":{"active_account":"fresh"}}}`)
	st, _ := Load(path)
	if st.Default().Active != "fresh" {
		t.Fatalf("got %q, want fresh", st.Default().Active)
	}
}

// The ownership merge, run per profile: Active goes to whichever side
// established it more recently, an exact tie keeps the writer's own, the daemon
// takes Pinned from disk and the later LastSwitch, the CLI keeps its own.
func TestOwnershipMergeAppliesPerProfile(t *testing.T) {
	path := isolate(t)
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	// Both sides start from the same file.
	seed, _ := Load(path)
	*seed.Default() = ProfileState{Active: "a", ActiveAt: t0}
	*seed.Profile("work") = ProfileState{Active: "w1", ActiveAt: t0}
	if err := seed.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	daemon, _ := Load(path)
	cli, _ := Load(path)

	// CLI: a newer swap on "work", a pin on default, a switch time on work.
	cli.Profile("work").Active, cli.Profile("work").ActiveAt = "w2", t0.Add(time.Minute)
	cli.Profile("work").LastSwitch = t0.Add(time.Minute)
	cli.Default().Pinned = "a"
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}

	// Daemon: still believes w1 on work (older), observes "b" on default (newer).
	daemon.Default().Active, daemon.Default().ActiveAt = "b", t0.Add(2*time.Minute)
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	got, _ := Load(path)
	d, w := got.Default(), got.Profile("work")
	if d.Active != "b" {
		t.Fatalf("default: the daemon's newer observation must win, got %q", d.Active)
	}
	if w.Active != "w2" {
		t.Fatalf("work: a stale daemon save reverted a newer swap, got %q", w.Active)
	}
	if d.Pinned != "a" {
		t.Fatalf("default: the daemon must keep the CLI's pin, got %q", d.Pinned)
	}
	if !w.LastSwitch.Equal(t0.Add(time.Minute)) {
		t.Fatalf("work: the daemon must keep the later LastSwitch, got %v", w.LastSwitch)
	}
	// The daemon's in-memory view now carries the merge too.
	if daemon.Profile("work").Active != "w2" || daemon.Default().Pinned != "a" {
		t.Fatalf("daemon did not absorb the merge: %+v %+v", daemon.Default(), daemon.Profile("work"))
	}

	// CLI saving again with its now-older view of default keeps the daemon's.
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(path)
	if got.Default().Active != "b" {
		t.Fatalf("default: the CLI's older belief clobbered the daemon's, got %q", got.Default().Active)
	}
}

func TestOwnershipMergeTieKeepsTheWritersOwn(t *testing.T) {
	path := isolate(t)
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	cli, _ := Load(path)
	*cli.Profile("work") = ProfileState{Active: "w1", ActiveAt: t0}
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	daemon, _ := Load(path)
	daemon.Profile("work").Active = "w2" // same ActiveAt: a tie
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); got.Profile("work").Active != "w2" {
		t.Fatalf("on a tie the writer keeps its own, got %q", got.Profile("work").Active)
	}

	cli.Profile("work").Active = "w3" // still t0: a tie from the CLI side
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); got.Profile("work").Active != "w3" {
		t.Fatalf("on a tie the writer keeps its own, got %q", got.Profile("work").Active)
	}
}

// A profile that appeared on disk after this process loaded must survive its
// save, whichever side is writing.
func TestMergeKeepsAProfileThisWriterNeverSaw(t *testing.T) {
	path := isolate(t)
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	for _, as := range []owner{OwnerCLI, OwnerDaemon} {
		os.Remove(path)
		early, _ := Load(path)
		early.Default().SetActive("a")

		other, _ := Load(path)
		*other.Profile("work") = ProfileState{Active: "w1", ActiveAt: t0, Pinned: "w1", LastSwitch: t0}
		if err := other.SaveAs(OwnerCLI); err != nil {
			t.Fatal(err)
		}
		if err := early.SaveAs(as); err != nil {
			t.Fatal(err)
		}
		got, _ := Load(path)
		w := got.Profiles["work"]
		if w == nil || w.Active != "w1" || w.Pinned != "w1" || !w.LastSwitch.Equal(t0) {
			t.Fatalf("owner %d: a profile saved by someone else was lost: %+v", as, w)
		}
	}
}

func TestReconcileClearsActivePerProfile(t *testing.T) {
	s := &State{Accounts: map[string]*Account{"keep": {ID: "keep"}, "gone": {ID: "gone"}}}
	s.Default().Active = "gone"
	s.Profile("work").Active = "keep"
	s.Reconcile(map[string]string{"keep": ""})
	if s.Default().Active != "" {
		t.Fatalf("default still points at a dropped account: %q", s.Default().Active)
	}
	if s.Profile("work").Active != "keep" {
		t.Fatalf("work lost a valid active account: %q", s.Profile("work").Active)
	}
}
