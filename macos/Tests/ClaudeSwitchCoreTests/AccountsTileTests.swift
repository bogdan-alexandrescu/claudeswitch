import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Overview's Accounts tile claims "all available" only when every account
/// is: an account whose usage is unknown, or whose reading predates a reset,
/// is said, not counted as fine.
@Suite struct AccountsTileTests {
    func tile(_ statuses: [AccountStatus]) throws -> OverviewTile {
        var s = try cardsSnapshot()
        s.accounts = Array(s.accounts.prefix(statuses.count))
        for (k, st) in statuses.enumerated() { s.accounts[k].status = st }
        let i = OverviewInput(daemon: s.daemon, cards: s.cards, accounts: s.accounts, pools: nil, chrome: [],
                              clean: nil, update: OverviewUpdate(current: "0.8.18", available: nil,
                                                                 lastCheck: nil, enabled: false),
                              switches: [], now: fixtureNow)
        return try #require(Overview.build(i).tiles.first { $0.title == "Accounts" })
    }

    @Test func testOnlyEveryAccountAvailableIsAllAvailable() throws {
        #expect(try tile([.available, .available]).secondary == "all available")
        #expect(try tile([.available, .unknown]).secondary == "1 unknown")
        #expect(try tile([.windowReset, .available, .unknown]).secondary == "1 unknown · 1 read before a reset")
    }
}
