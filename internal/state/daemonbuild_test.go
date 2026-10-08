package state

import (
	"testing"
	"time"
)

// The daemon's build is the daemon's to write: a CLI save keeps what is on
// disk, as it does for DaemonLive and DaemonSince.
func TestCLISaveKeepsTheDaemonsBuild(t *testing.T) {
	path := isolate(t)
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	d, _ := Load(path)
	d.DaemonVersion, d.DaemonBuild, d.DaemonBuildTime = "0.5.0", "abc123", at
	if err := d.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli, _ := Load(path)
	cli.DaemonVersion, cli.DaemonBuild, cli.DaemonBuildTime = "", "", time.Time{}
	if err := cli.SaveAs(OwnerCLI); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(path)
	if got.DaemonVersion != "0.5.0" || got.DaemonBuild != "abc123" || !got.DaemonBuildTime.Equal(at) {
		t.Fatalf("a CLI save lost the daemon's build: %q %q %v", got.DaemonVersion, got.DaemonBuild, got.DaemonBuildTime)
	}
}
