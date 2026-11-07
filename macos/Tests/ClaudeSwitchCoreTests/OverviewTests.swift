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
        #expect(o.needs.map(\.target) == [.daemon, .signIn, .accounts, .update])
        #expect(o.needs[0].text.contains("not installed"))
        #expect(o.needs[1].text == "\(s.accounts[0].id) needs sign-in" && o.needs[1].action == "Sign In…")
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
        #expect(tile.primary == "Schedule off" && tile.secondary == "no run yet")
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
            OverviewSwitch(at: fixtureNow.addingTimeInterval(-7200), profile: "default", from: "a", to: "b", reason: "manual"),
            OverviewSwitch(at: fixtureNow.addingTimeInterval(-600), profile: "review", from: "b", to: "c",
                           reason: "at 86% of its session, over the 85% trigger"),
        ]
        i.clean = OverviewClean(on: true, every: nil, lastAt: fixtureNow.addingTimeInterval(-3600), lastBytes: 3_650_722_201,
                                lastRemoved: 198)
        let o = Overview.build(i)
        #expect(o.recent.map(\.title) == ["b → c", "Cleanup removed 198 items", "a → b"])
        #expect(o.recent.map(\.profile) == ["review", nil, "default"])
        #expect(o.recent.map(\.detail) == ["session 86%", "3.4 GB", "manual"])
        #expect(o.recent.map(\.symbol) == ["arrow.triangle.2.circlepath", "trash", "arrow.triangle.2.circlepath"])
        #expect(o.recent[0].when == Overview.when(fixtureNow.addingTimeInterval(-600), now: fixtureNow))
    }

    /// The row shows a few words; the tooltip keeps the whole reason, whose
    /// profile, and the date an older line's "Oct 25" leaves out.
    @Test func testARecentLinesTooltipHasTheWholeOfIt() throws {
        var i = input(try snapshot())
        let at = fixtureNow.addingTimeInterval(-3 * 86_400)
        let reason = "active account at 99% of its week, over the 98% trigger"
        i.switches = [OverviewSwitch(at: at, profile: "default", from: "a", to: "b", reason: reason)]
        i.clean = nil
        let date = at.formatted(date: .abbreviated, time: .shortened)
        #expect(Overview.build(i).recent[0].help == "default: a → b\n" + reason + "\n" + date)
        i.switches = [OverviewSwitch(at: at, profile: nil, from: "a", to: "b", reason: nil)]
        let bare = Overview.build(i).recent[0]
        #expect(bare.help == "a → b\n" + date)
        #expect(bare.profile == nil, "with one profile no line names it")
        #expect(bare.detail == nil)
    }

    @Test func testACleanupLineSaysWhatItFreedAndWhatWasPutBack() throws {
        var i = input(try snapshot())
        let at = fixtureNow.addingTimeInterval(-60)
        i.switches = []
        i.clean = OverviewClean(on: true, every: nil, lastAt: at, lastBytes: 536_870_912, lastRemoved: 1, lastPutBack: 1)
        let e = try #require(Overview.build(i).recent.first)
        #expect(e.kind == .cleanup)
        #expect(e.title == "Cleanup removed 1 item")
        #expect(e.detail == "512 MB · 1 put back")
        #expect(e.help == "Cleanup removed 1 item · 512 MB · 1 put back\n" + at.formatted(date: .abbreviated, time: .shortened))
    }

    @Test func testRecentHoldsFiveAtMost() throws {
        var i = input(try snapshot())
        i.clean = nil
        i.switches = (1...8).map {
            OverviewSwitch(at: fixtureNow.addingTimeInterval(-Double($0) * 60), profile: nil, from: "a\($0)", to: "b", reason: nil)
        }
        #expect(Overview.build(i).recent.map(\.title) == (1...5).map { "a\($0) → b" })
    }

    /// The daemon's sentences, as the real audit log has them, in a few words.
    @Test func testAReasonInAFewWords() {
        #expect(Overview.shortReason("active account at 86% of its session, over the 85% trigger") == "session 86%")
        #expect(Overview.shortReason("active account at 99% of its week, over the 98% trigger") == "week 99%")
        #expect(Overview.shortReason("active account at 99% of its weekly, over the 98% trigger") == "week 99%")
        #expect(Overview.shortReason("work-1 at 93% of the week") == "week 93%")
        #expect(Overview.shortReason("active account at 97%, over the 95% trigger") == "97%")
        #expect(Overview.shortReason("requested with `cs use`") == "by hand")
        #expect(Overview.shortReason("active account was refused on its five_hour window") == "refused")
        #expect(Overview.shortReason("pin on work-1 lifted: it is out of quota; active account at 99% of its week, over the 98% trigger")
            == "week 99%", "the figure says more than that the pin went; the tooltip keeps the whole")
        #expect(Overview.shortReason("pin on work-1 lifted: it was refused") == "refused")
        #expect(Overview.shortReason("pin on work-1 lifted by the daemon") == "pin lifted")
        #expect(Overview.shortReason("restored after vaulting another account") == "restored after vaulting another account",
                "anything else as it is: the row shows it whole, or leaves it to the tooltip")
        #expect(Overview.shortReason("") == nil)
    }
}
