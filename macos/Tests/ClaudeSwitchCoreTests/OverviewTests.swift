import Foundation
import Testing
@testable import ClaudeSwitchCore

// Settings → Overview (owner decision 2026-10-26: status tiles and needs-you):
// the window's first page. "Needs you" only when something does, a tile per
// area with its live figure and the pane it opens, and the last few things
// that happened.

@Suite struct OverviewTests {
    /// The fixture snapshot with every account usable (the fixture has one
    /// that needs signing in again).
    func snapshot() throws -> Snapshot {
        var s = try cardsSnapshot()
        for k in s.accounts.indices where s.accounts[k].status == .needsLogin { s.accounts[k].status = .available }
        return s
    }

    func input(_ s: Snapshot) -> OverviewInput {
        OverviewInput(daemon: s.daemon, cards: s.cards, accounts: s.accounts, pools: 3, chrome: [],
                      clean: OverviewClean(on: true, every: 24 * 3600, lastAt: fixtureNow.addingTimeInterval(-7200),
                                           lastBytes: 3_650_722_201, lastRemoved: 198),
                      update: OverviewUpdate(current: "0.8.12", available: nil,
                                             lastCheck: fixtureNow.addingTimeInterval(-3600), enabled: true),
                      switches: [], now: fixtureNow)
    }

    @Test func testSixTilesEachOpeningItsPane() throws {
        let o = Overview.build(input(try snapshot()))
        #expect(o.tiles.map(\.title) == ["Daemon", "Profiles", "Accounts", "Rotation", "Cleanup", "Updates"])
        #expect(o.tiles.map(\.target) == [.daemon, .profiles, .accounts, .rotation, .cleanup, .update])
        let profiles = try #require(o.tiles.first { $0.title == "Profiles" })
        #expect(profiles.primary == "2 profiles")
        #expect(profiles.secondary.hasPrefix("default: work-1"))
        let rotation = try #require(o.tiles.first { $0.title == "Rotation" })
        #expect(rotation.primary == "Next: stay")
        let cleanup = try #require(o.tiles.first { $0.title == "Cleanup" })
        #expect(cleanup.primary == "Daily")
        #expect(cleanup.secondary == "last run 2h ago · 3.4 GB")
        let updates = try #require(o.tiles.first { $0.title == "Updates" })
        #expect(updates.primary == "Up to date" && updates.secondary == "This is 0.8.12 · checked 1h ago")
        #expect(o.needs.isEmpty, "nothing needs the person: \(o.needs)")
    }

    @Test func testNeedsYouListsWhatOnlyThePersonCanDo() throws {
        var s = try snapshot()
        s.accounts[0].status = .needsLogin
        var i = input(s)
        i.chrome = ["Claude in Chrome in \"Work\" does not follow switches to work-2"]
        i.update.available = "0.9.0"
        i.daemon?.health = .never
        let o = Overview.build(i)
        #expect(o.needs.map(\.target) == [.daemon, .accounts, .accounts, .update])
        #expect(o.needs[0].text.contains("not installed"))
        #expect(o.needs[1].text == "\(s.accounts[0].id) needs signing in again" && o.needs[1].action == "Sign in…")
        #expect(o.needs[3].text == "ClaudeSwitch 0.9.0 is available" && o.needs[3].action == "Update…")
        let accounts = try #require(o.tiles.first { $0.title == "Accounts" })
        #expect(accounts.attention && accounts.secondary.hasPrefix("1 needs sign-in"))
        #expect(o.tiles.first { $0.title == "Daemon" }?.primary == "Not installed")
        #expect(o.tiles.first { $0.title == "Updates" }?.primary == "0.9.0 available")
    }

    @Test func testTheCleanupTileSaysWhenItIsOffOrHasNotRun() throws {
        var i = input(try snapshot())
        i.clean = OverviewClean(on: false, every: nil, lastAt: nil, lastBytes: 0, lastRemoved: 0)
        var tile = try #require(Overview.build(i).tiles.first { $0.title == "Cleanup" })
        #expect(tile.primary == "Off" && tile.secondary == "no run yet")
        i.clean = OverviewClean(on: true, every: 6 * 3600, lastAt: nil, lastBytes: 0, lastRemoved: 0)
        tile = try #require(Overview.build(i).tiles.first { $0.title == "Cleanup" })
        #expect(tile.primary == "Every 6h")
    }

    /// Never checked is not "up to date": the tile says so instead.
    @Test func testTheUpdatesTileDoesNotClaimUpToDateBeforeAnyCheck() throws {
        var i = input(try snapshot())
        i.update.lastCheck = nil
        let tile = try #require(Overview.build(i).tiles.first { $0.title == "Updates" })
        #expect(tile.primary == "Not checked yet" && tile.secondary == "This is 0.8.12")
        #expect(!tile.attention)
    }

    @Test func testRecentIsNewestFirstAndShort() throws {
        var i = input(try snapshot())
        i.switches = [
            OverviewSwitch(at: fixtureNow.addingTimeInterval(-90_000), profile: "default", from: "a", to: "b",
                           reason: "manual"),
            OverviewSwitch(at: fixtureNow.addingTimeInterval(-600), profile: "review", from: "b", to: "c",
                           reason: "at 86% of its session, over the 85% trigger"),
        ]
        let o = Overview.build(i)
        #expect(o.recent.count == 3)
        #expect(o.recent[0].text == "review: b → c (at 86% of its session, over the 85% trigger)")
        #expect(o.recent[1].text == "cleanup moved 3.4 GB (198 items)")
        #expect(o.recent[2].text == "default: a → b (manual)")
        #expect(o.recent[0].when == Overview.when(fixtureNow.addingTimeInterval(-600), now: fixtureNow))
    }
}
