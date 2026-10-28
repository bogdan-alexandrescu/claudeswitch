import Foundation
import Testing
@testable import ClaudeSwitchCore

/// D38 and D39: any pool can be deleted, its accounts moved where chosen and
/// the profiles that listed it no longer listing it; any pool renamed.
@Suite final class PoolDeleteRenameTests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testDeletingAPoolInUseSaysWhoLosesWhat() throws {
        let plan = PoolDeletePlan.make("team", pools: try sharedPools(), profiles: try sharedProfiles())
        #expect(plan.accounts == ["team-1", "team-2", "team-3"])
        #expect(plan.users == ["default", "work"])
        #expect(plan.destinations.map(\.pick) == [.pool("personal"), .pool("ops"), .pool("spare"), .none])
        #expect(plan.initial == .pool("personal"), "the first other pool")
        #expect(plan.summary == "Deletes the pool “team”. Its 3 accounts move to the pool “personal”; no account is deleted. "
                + "The profile “default” then uses only “personal”. The profile “work” then uses only “ops”.")
        #expect(plan.caption == "Its accounts: team-1, team-2, team-3")
    }

    /// It starts where the same profiles use the accounts, so deleting a
    /// pool does not share them by default: personal is default's alone, and
    /// so are the accounts in no pool.
    @Test func testItStartsWhereTheSameProfilesUseThem() throws {
        let plan = PoolDeletePlan.make("personal", pools: try sharedPools(), profiles: try sharedProfiles())
        #expect(plan.initial == PoolPick.none)
        #expect(plan.summary(to: PoolPick.none) == "Deletes the pool “personal”. Its account “personal” is then in no pool; "
                + "no account is deleted. The profile “default” then uses “team”, then the accounts in no pool.",
                "where the accounts go is part of what default is left using")
        #expect(plan.summary(to: .pool("ops")) == "Deletes the pool “personal”. Its account “personal” moves to the pool “ops”; "
                + "no account is deleted. The profile “default” then uses only “team”.")
        #expect(plan.caption == "Its account: personal")
        let ops = PoolDeletePlan.make("ops", pools: try sharedPools(), profiles: try sharedProfiles())
        #expect(ops.initial == .pool("personal"), "no pool only work uses: the first other")
        #expect(ops.summary == "Deletes the pool “ops”. Its account “ops-1” moves to the pool “personal”; no account is deleted. "
                + "The profile “work” then uses only “team”.")
    }

    @Test func testDeletingAnEmptyPoolNoOneUses() throws {
        let plan = PoolDeletePlan.make("spare", pools: try sharedPools(), profiles: try sharedProfiles())
        #expect(plan.accounts.isEmpty && plan.users.isEmpty && plan.initial == nil)
        #expect(plan.summary == "Deletes the pool “spare”. It has no accounts and no profile uses it, so nothing else changes.")
        #expect(plan.caption == nil)
    }

    @Test func testTheCallsSayWhereTheAccountsGo() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-pool-list-shared")
        _ = cli.poolDelete("team", to: .pool("personal"))
        #expect(lastCall() == ["pool", "delete", "team", "--to=personal", "--json"])
        _ = cli.poolDelete("team", to: PoolPick.none)
        #expect(lastCall() == ["pool", "delete", "team", "--no-pool", "--json"])
        _ = cli.poolDelete("spare", to: nil)
        #expect(lastCall() == ["pool", "delete", "spare", "--json"])
        _ = cli.poolRename("team", to: "crew")
        #expect(lastCall() == ["pool", "rename", "team", "crew", "--json"])
    }
}
