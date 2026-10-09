package state

import (
	"path/filepath"
	"testing"
	"time"
)

// 0.6.1: the app's heartbeat (`cs app heartbeat`) records app_notifies_until.
// While it is in the future the daemon leaves rotation notices to the app.
// The CLI owns the field: a daemon save keeps the disk's value, and the
// daemon reads it from disk, since its own copy is only as fresh as its
// last save.
func TestAppNotifiesUntilIsTheCLIsAndReadFromDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Now()

	daemon, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	if daemon.AppNotifyingAt(now) {
		t.Fatal("no heartbeat yet, but the app is said to be notifying")
	}

	cli, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cli.AppNotifiesUntil = now.Add(2 * time.Minute)
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if !daemon.AppNotifyingAt(now) {
		t.Fatal("the daemon did not see the heartbeat written after its load")
	}
	if daemon.AppNotifyingAt(now.Add(3 * time.Minute)) {
		t.Fatal("a heartbeat two minutes old still counts")
	}

	// The daemon's save must not take the heartbeat back.
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AppNotifiesUntil.Equal(cli.AppNotifiesUntil) {
		t.Fatalf("after a daemon save app_notifies_until = %v, want %v", again.AppNotifiesUntil, cli.AppNotifiesUntil)
	}

	// Another CLI command, loaded before the heartbeat, keeps the later one.
	stale := &State{path: path, Accounts: map[string]*Account{}, Profiles: map[string]*ProfileState{}}
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}
	again, _ = Load(path)
	if !again.AppNotifiesUntil.Equal(cli.AppNotifiesUntil) {
		t.Fatalf("a CLI save from before the heartbeat dropped it: %v", again.AppNotifiesUntil)
	}
}
