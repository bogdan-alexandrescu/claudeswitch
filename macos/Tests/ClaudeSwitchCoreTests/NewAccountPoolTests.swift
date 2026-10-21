import Foundation
import Testing
@testable import ClaudeSwitchCore

/// D37: a new account goes into the pool chosen in the Add account sheet,
/// which starts on a pool only the profile it was opened from uses, so
/// nothing is shared unless chosen.
@Suite final class NewAccountPoolTests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    func lastCall() -> [String] { FakeAppCLI.calls(dir).last ?? [] }

    @Test func testEveryPoolIsOfferedWithWhoUsesItThenNoPool() throws {
        let options = NewAccountPool.options(pools: try sharedPools(), profiles: try sharedProfiles())
        #expect(options.map(\.pick) == [.pool("personal"), .pool("team"), .pool("ops"), .pool("spare"), .none])
        #expect(options.first { $0.pick == .pool("team") }?.label == "team — used by default, work")
        #expect(options.first { $0.pick == .pool("spare") }?.label == "spare — no profile uses it")
        #expect(options.last?.label == "No pool — default uses it last")
    }

    @Test func testALegacyConfigOffersEachProfilesOwnPool() throws {
        let legacy = try #require(PoolList(data: try fixture("cli-pool-list.json")))
        let options = NewAccountPool.options(pools: legacy, profiles: nil)
        #expect(options.first?.label == "default — default's own")
    }

    @Test func testItStartsOnAPoolOnlyThatProfileUses() throws {
        let (pools, profiles) = (try sharedPools(), try sharedProfiles())
        #expect(NewAccountPool.initial(profile: "default", pools: pools, profiles: profiles) == .pool("personal"))
        #expect(NewAccountPool.initial(profile: "work", pools: pools, profiles: profiles) == .pool("ops"))
        // A default that uses only shared pools starts on No pool.
        var shared = profiles
        if let i = shared.profiles.firstIndex(where: { $0.name == "default" }) { shared.profiles[i].pools = ["team"] }
        #expect(NewAccountPool.initial(profile: "default", pools: pools, profiles: shared) == PoolPick.none)
    }

    @Test func testThePoolIsPassedToTheCLI() throws {
        try FakeAppCLI.reply(dir, fixture: "cli-login-start")
        _ = cli.loginStart(account: "w3", profile: nil, browser: nil, org: nil, pool: .pool("mine"))
        #expect(lastCall() == ["login", "w3", "--direct", "--json", "--no-open", "--pool=mine"])
        _ = cli.loginStart(account: "w3", profile: nil, browser: nil, org: nil, pool: PoolPick.none)
        #expect(lastCall() == ["login", "w3", "--direct", "--json", "--no-open", "--no-pool"])
        try FakeAppCLI.reply(dir, fixture: "cli-login-code")
        _ = cli.add(account: "x", from: "default", profile: nil, pool: .pool("ops"))
        #expect(lastCall() == ["add", "x", "--from=default", "--pool=ops", "--json"])
    }

    @Test func testTheAnswersNameThePool() throws {
        let start = try #require(LoginStart(JSON(data: Data(#"""
            {"url": "https://example.com/a", "pending": {"account": "n", "join_pool": "mine", "new_account": true}}
            """#.utf8))!))
        #expect(start.joinPool == "mine")
        let added = try #require(AccountAdded(JSON(data: Data(#"""
            {"account": "n", "pool_name": "mine", "pool": [], "profiles": ["default"]}
            """#.utf8))!))
        #expect(added.poolName == "mine")
    }
}
