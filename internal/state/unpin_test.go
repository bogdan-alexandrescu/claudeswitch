package state

import "testing"

// F3: the CLI owns a pin, so a daemon save takes the disk's Pinned. A pin
// the daemon lifted (LiftPin) is the one exception: its save clears that
// pin on disk, unless the CLI has pinned something else since.

func TestADaemonLiftedPinStaysLifted(t *testing.T) {
	path := isolate(t)
	cli, _ := Load(path, "default", "work")
	cli.Profile("work").Pinned, cli.Profile("work").PinHard = "work-1", false
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}

	daemon, _ := Load(path, "default", "work")
	daemon.Profile("work").LiftPin()
	if p := daemon.Profile("work"); p.Pinned != "" || p.PinHard {
		t.Fatalf("after LiftPin: %+v", p)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default", "work")
	if p := back.Profile("work"); p.Pinned != "" {
		t.Fatalf("the lifted pin came back from disk: %+v", p)
	}
	// And a later daemon save does not lift a pin the CLI sets afterwards.
	cli2, _ := Load(path, "default", "work")
	cli2.Profile("work").Pinned = "work-1"
	if err := cli2.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	if p := daemon.Profile("work"); p.Pinned != "work-1" {
		t.Fatalf("the daemon dropped a pin set after it lifted one: %+v", p)
	}
}

func TestALiftedPinDoesNotUndoANewerPin(t *testing.T) {
	path := isolate(t)
	cli, _ := Load(path, "default", "work")
	cli.Profile("work").Pinned = "work-1"
	_ = cli.Save()

	daemon, _ := Load(path, "default", "work")
	daemon.Profile("work").LiftPin()

	// Meanwhile the person pins another account, hard.
	cli2, _ := Load(path, "default", "work")
	cli2.Profile("work").Pinned, cli2.Profile("work").PinHard = "work-team", true
	_ = cli2.Save()

	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	if p := daemon.Profile("work"); p.Pinned != "work-team" || !p.PinHard {
		t.Fatalf("the lift undid a newer pin: %+v", p)
	}
}

func TestPinHardRoundTrips(t *testing.T) {
	path := isolate(t)
	cli, _ := Load(path, "default")
	cli.Default().Pinned, cli.Default().PinHard = "work-1", true
	_ = cli.Save()
	daemon, _ := Load(path, "default")
	_ = daemon.SaveAs(OwnerDaemon)
	back, _ := Load(path, "default")
	if p := back.Default(); p.Pinned != "work-1" || !p.PinHard {
		t.Fatalf("got %+v", p)
	}
}

// One definition of an error only a sign-in cures, shared by render, the
// account list and the pin valve.
func TestNeedsLoginErr(t *testing.T) {
	for _, s := range []string{"usage API: 401 Unauthorized", "invalid_grant", "no stored credential for work-1",
		"needs an interactive login", "not in the vault"} {
		if !NeedsLoginErr(s) {
			t.Errorf("NeedsLoginErr(%q) = false", s)
		}
	}
	for _, s := range []string{"", "rate limited", "dial tcp: timeout"} {
		if NeedsLoginErr(s) {
			t.Errorf("NeedsLoginErr(%q) = true", s)
		}
	}
}
