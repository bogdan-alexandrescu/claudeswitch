import Foundation
import Testing
@testable import ClaudeSwitchCore

/// New pool is `pool create`, then a `pool move` for each account already
/// in a pool. Once the pool exists, a failed move is tried again alone:
/// Create again would be refused (the pool already exists).
@Suite struct AccountsNewPoolTests {
    @Test func testOnceThePoolExistsOnlyTheMovesLeftAreTriedAgain() throws {
        let pools = try sharedPools()
        let plan = NewPoolPlan.make(name: "lab", selected: ["loose-1", "team-2", "ops-1"], pools: pools)
        var run = NewPoolRun(plan)
        #expect(run.steps == [.create("lab", accounts: ["loose-1"]), .move("team-2", to: "lab"), .move("ops-1", to: "lab")])
        #expect(run.action == "Create…", "accounts move: it asks first")
        #expect(run.cancel == "Cancel")

        run.done(.create("lab", accounts: ["loose-1"]))
        // team-2's move is refused.
        #expect(run.failure(.move("team-2", to: "lab"), "the config changed meanwhile")
                == "The pool “lab” was created, but “team-2” could not be moved in: the config changed meanwhile")
        #expect(run.steps == [.move("team-2", to: "lab"), .move("ops-1", to: "lab")], "never created twice")
        #expect(run.action == "Move the Rest In")
        #expect(run.cancel == "Close", "the pool stays either way")

        run.done(.move("team-2", to: "lab"))
        #expect(run.steps == [.move("ops-1", to: "lab")])
        #expect(run.action == "Move “ops-1” In")
    }

    /// Checked accounts stop rotating where they do now: a new pool is one
    /// no profile uses yet.
    @Test func testTheSheetSaysWhereTheCheckedAccountsStopRotating() throws {
        let pools = try sharedPools()
        #expect(NewPoolPlan.stops(name: "lab", selected: [], pools: pools, defaultExists: true) == nil)
        #expect(NewPoolPlan.stops(name: "lab", selected: ["personal"], pools: pools, defaultExists: true)
                == "The account “personal” then stops rotating in “default” until a profile uses “lab”.")
        #expect(NewPoolPlan.stops(name: "lab", selected: ["loose-1", "team-2"], pools: pools, defaultExists: true)
                == "The accounts checked then stop rotating in “default” and “work” until a profile uses “lab”.",
                "an account in no pool is default's (D6)")
        #expect(NewPoolPlan.stops(name: "", selected: ["ops-1"], pools: pools, defaultExists: true)
                == "The account “ops-1” then stops rotating in “work” until a profile uses the new pool.")
        #expect(NewPoolPlan.stops(name: "lab", selected: ["loose-1"], pools: pools, defaultExists: false) == nil,
                "no profile rotates it now")
    }

    @Test func testARefusedCreateIsTriedAgainWhole() {
        let plan = NewPoolPlan.make(name: "lab", selected: ["a"], pools: PoolList(pools: [], unlisted: ["a"]))
        let run = NewPoolRun(plan)
        #expect(run.failure(.create("lab", accounts: ["a"]), "a pool named lab already exists")
                == "a pool named lab already exists")
        #expect(run.steps == [.create("lab", accounts: ["a"])])
        #expect(run.action == "Create")
        #expect(run.cancel == "Cancel")
    }
}
