import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The Accounts pane, second QA pass: New pool says where every account is
/// and asks before moving any; a rename says it was done; the Plan column
/// never cuts a plan in the middle.
@Suite struct Accounts2PaneTests {
    /// An account in no pool said nothing, beside others that say their pool.
    @Test func testNewPoolSaysWhereEveryAccountIs() throws {
        let pools = try sharedPools()
        #expect(NewPoolText.note(for: "team-2", checked: false, name: "lab", pools: pools) == "in “team”")
        #expect(NewPoolText.note(for: "team-2", checked: true, name: "lab", pools: pools) == "leaves “team”")
        #expect(NewPoolText.note(for: "loose-1", checked: false, name: "lab", pools: pools) == "in no pool")
        #expect(NewPoolText.note(for: "loose-1", checked: true, name: "lab", pools: pools) == "joins “lab”")
        #expect(NewPoolText.note(for: "loose-1", checked: true, name: "", pools: pools) == "joins the new pool",
                "before a name is typed")
        #expect(NewPoolText.note(for: "loose-1", checked: false, name: "lab", pools: nil) == nil, "no pools known")
    }

    /// Create opens a confirmation when accounts leave their pools, so it
    /// says so with an ellipsis.
    @Test func testCreateAsksFirstWhenAccountsMove() throws {
        let pools = try sharedPools()
        #expect(NewPoolRun(NewPoolPlan.make(name: "lab", selected: ["team-2"], pools: pools)).action == "Create…")
        #expect(NewPoolRun(NewPoolPlan.make(name: "lab", selected: ["loose-1"], pools: pools)).action == "Create",
                "nothing moves: nothing to confirm")
        #expect(NewPoolRun(NewPoolPlan.make(name: "lab", selected: [], pools: pools)).action == "Create")
    }

    @Test func testARenameSaysItWasDone() {
        #expect(AccountRename.note("personal", to: "home") == "Renamed “personal” to “home”")
    }

    /// "Max 5x (team, enterprise seat)" cut in the middle read "Max 5x
    /// (…rise seat)": the column shows the plan before its parenthesis, the
    /// tooltip all of it.
    @Test func testThePlanColumnNeverCutsAPlanInTheMiddle() {
        #expect(PlanColumn.text("Max 5x (team, enterprise seat)") == "Max 5x")
        #expect(PlanColumn.text("Max 20x team (past_due)") == "Max 20x team")
        #expect(PlanColumn.text("Max 20x") == "Max 20x")
        #expect(PlanColumn.text(nil) == "—", "no plan recorded")
        #expect(PlanColumn.help("Max 5x (team, enterprise seat)") == "Max 5x (team, enterprise seat)")
        #expect(PlanColumn.help(nil) == "No plan recorded yet")
    }
}
