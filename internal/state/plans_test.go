package state

import "testing"

// Lane 15: the plan behind each account, recorded in state when the
// profile endpoint or a vault entry names it, so `cs account list` needs no
// keychain. Either side may record one, and neither side's save loses the
// other's.
func TestPlanIsRecordedAndSurvivesTheOtherSidesSaves(t *testing.T) {
	path := isolate(t)
	daemon, _ := Load(path, "default")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	cli, _ := Load(path, "default")
	cli.SetPlan("a", "Max 20x")
	cli.SetPlan("b", "")
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	daemon.SetPlan("c", "Pro")
	if err := daemon.SaveAs(OwnerDaemon); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if got := back.PlanOf("a"); got != "Max 20x" {
		t.Fatalf("PlanOf(a) = %q: the daemon's save lost the CLI's plan", got)
	}
	if got := back.PlanOf("c"); got != "Pro" {
		t.Fatalf("PlanOf(c) = %q", got)
	}
	if got := back.PlanOf("b"); got != "" {
		t.Fatalf("PlanOf(b) = %q; an empty plan records nothing", got)
	}
	// A CLI holding an older copy does not undo the daemon's.
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	if back, _ := Load(path, "default"); back.PlanOf("c") != "Pro" {
		t.Fatal("the CLI's save lost the daemon's plan")
	}
}

func TestRenameMovesThePlan(t *testing.T) {
	path := isolate(t)
	st, _ := Load(path, "default")
	st.SetPlan("old", "Max 5x")
	st.RenameChromeAccount("old", "new")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path, "default")
	if back.PlanOf("old") != "" || back.PlanOf("new") != "Max 5x" {
		t.Fatalf("plans after rename: %v", back.Plans)
	}
}
