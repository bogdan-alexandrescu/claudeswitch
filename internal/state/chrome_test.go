package state

import (
	"testing"
	"time"
)

// IMPROVEMENTS C1: the account → Chrome profile mapping lives in
// claudeswitch's state, survives a reload, and lists in account order.
func TestChromeMappingSurvivesASaveAndLoad(t *testing.T) {
	path := isolate(t)
	st, err := Load(path, "default")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	st.SetChrome("b", "claudeswitch-b", at)
	st.SetChrome("a", "claudeswitch-a", at)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path, "default")
	if err != nil {
		t.Fatal(err)
	}
	cp := back.ChromeOf("a")
	if cp == nil || cp.Dir != "claudeswitch-a" || !cp.Added.Equal(at) {
		t.Fatalf("ChromeOf(a) after reload = %+v", cp)
	}
	list := back.ChromeList()
	if len(list) != 2 || list[0].Account != "a" || list[1].Account != "b" {
		t.Fatalf("ChromeList = %+v, want a then b", list)
	}
	if back.ChromeOf("nobody") != nil {
		t.Fatal("an unmapped account must have no Chrome profile")
	}
}

// The daemon never writes the mapping, so its saves must not undo a CLI's
// add or forget made while it held an older copy in memory.
func TestChromeMappingSurvivesTheDaemonsSaves(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}

	cli, _ := Load(path, "default")
	cli.SetChrome("a", "claudeswitch-a", time.Now())
	cli.SetChrome("b", "claudeswitch-b", time.Now())
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	// After its save the daemon sees what the CLI recorded.
	if daemon.ChromeOf("a") == nil || daemon.ChromeOf("b") == nil {
		t.Fatalf("daemon's copy after its save = %+v, want a and b", daemon.ChromeList())
	}

	cli2, _ := Load(path, "default")
	if !cli2.DropChrome("a") {
		t.Fatal("DropChrome(a) = false, want true")
	}
	if cli2.DropChrome("a") {
		t.Fatal("a second DropChrome(a) = true, want false")
	}
	if err := cli2.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if back.ChromeOf("a") != nil {
		t.Fatal("the daemon's save brought back a forgotten mapping")
	}
	if back.ChromeOf("b") == nil {
		t.Fatal("the daemon's save lost a mapping")
	}
}

// The browser-tool hint is given once per rotation: the state remembers, per
// profile, the rotation it was last given for, and keeps it across saves by
// either side.
func TestChromeHintIsRememberedPerProfile(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default", "work")
	cli, _ := Load(path, "default", "work")
	if got := cli.ChromeHinted("work"); got != "" {
		t.Fatalf("fresh ChromeHinted = %q", got)
	}
	cli.SetChromeHinted("work", "w2@123")
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default", "work")
	if got := back.ChromeHinted("work"); got != "w2@123" {
		t.Fatalf("ChromeHinted(work) = %q, want w2@123", got)
	}
	if got := back.ChromeHinted("default"); got != "" {
		t.Fatalf("ChromeHinted(default) = %q, want empty", got)
	}
}

// Owner decision (lane 13): the email `cs chrome add` names comes from
// state.json, recorded when an account is vaulted or identified, so the
// command never reads the keychain. Like the mapping, only the CLI writes it.
func TestEmailIsRecordedAndSurvivesTheDaemonsSaves(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli, _ := Load(path, "default")
	cli.SetEmail("a", "a@example.com")
	cli.SetEmail("b", "")
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.EmailOf("a"); got != "a@example.com" {
		t.Fatalf("EmailOf(a) = %q", got)
	}
	if got := back.EmailOf("b"); got != "" {
		t.Fatalf("EmailOf(b) = %q; an empty email records nothing", got)
	}
}

// A rename moves the account's Chrome mapping and email to the new id.
func TestRenameChromeAccountMovesMappingAndEmail(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default")
	st.SetChrome("old", "claudeswitch-old", time.Now())
	st.SetEmail("old", "o@example.com")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	st.RenameChromeAccount("old", "new")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if back.ChromeOf("old") != nil || back.EmailOf("old") != "" {
		t.Fatal("the old id kept its mapping or email")
	}
	if cp := back.ChromeOf("new"); cp == nil || cp.Dir != "claudeswitch-old" {
		t.Fatalf("ChromeOf(new) = %+v", cp)
	}
	if got := back.EmailOf("new"); got != "o@example.com" {
		t.Fatalf("EmailOf(new) = %q", got)
	}
}
