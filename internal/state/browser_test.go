package state

import (
	"testing"
	"time"
)

// IMPROVEMENTS P4: the daemon records each profile's last Claude in Chrome
// call, so `cs why` can say a rotation is waiting for a browser task and a
// restarted daemon keeps holding for one.

func TestTheLastBrowserCallSurvivesASaveAndLoad(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	call := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	daemon.Profile("default").BrowserAt = call
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.Profile("default").BrowserAt; !got.Equal(call) {
		t.Fatalf("browser_at read back as %v, want %v", got, call)
	}
}

// The daemon owns it: a command that loaded state before the daemon's last
// save, and saves after it, keeps the daemon's newer call.
func TestACommandSaveKeepsTheDaemonsLastBrowserCall(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli, _ := Load(path, "default")

	call := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	daemon.Profile("default").BrowserAt = call
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli.Profile("default").Pinned = "work-1"
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.Profile("default").BrowserAt; !got.Equal(call) {
		t.Fatalf("after a command's save browser_at = %v, want the daemon's %v", got, call)
	}
}
