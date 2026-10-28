import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Overview's needs and tiles after the 2026-10-28 QA: a need's button does
/// the thing it names, and the tiles say what the panes they open would.
@Suite struct OverviewNeedsTests {
    func input() throws -> OverviewInput {
        var s = try cardsSnapshot()
        for k in s.accounts.indices { s.accounts[k].status = .available }
        return OverviewInput(daemon: s.daemon, cards: s.cards, accounts: s.accounts, pools: nil, chrome: [],
                             clean: OverviewClean(on: false, every: nil, lastAt: fixtureNow.addingTimeInterval(-7200),
                                                  lastBytes: 512_000_000, lastRemoved: 1),
                             update: OverviewUpdate(current: "0.8.18", available: nil, lastCheck: nil, enabled: false),
                             switches: [], now: fixtureNow)
    }

    @Test func testSignInStartsTheSignInForThatAccount() throws {
        var i = try input()
        i.accounts[0].status = .needsLogin
        let n = try #require(Overview.build(i).needs.first)
        #expect(n.action == "Sign in…" && n.target == .signIn && n.account == i.accounts[0].id)
    }

    @Test func testTheChromeNeedOffersTheChromeProfileOfItsOwn() throws {
        var i = try input()
        i.chromeNotices = [ChromeSignInNotice(profile: "work", chromeName: "Work", old: "a", account: "work-team",
                                              key: "k")]
        let n = try #require(Overview.build(i).needs.first)
        #expect(n.action == "Give work-team its own Chrome profile" && n.target == .chromeOwn && n.account == "work-team")
    }

    @Test func testFailingHealthChecksAndAMissingBinaryAreNeeds() throws {
        var i = try input()
        i.healthFailing = 2
        i.problem = "claudeswitch is not installed"
        let needs = Overview.build(i).needs
        #expect(needs.contains { $0.text == "claudeswitch is not installed" && $0.target == .advanced })
        #expect(needs.contains { $0.text == "2 health checks fail" && $0.target == .health })
    }

    @Test func testTheProfilesTileCountsProfilesNotSignedIn() throws {
        var i = try input()
        i.cards[1].signedIn = "no"
        let t = try #require(Overview.build(i).tiles.first { $0.title == "Profiles" })
        #expect(t.attention && t.secondary.hasPrefix("1 needs sign-in"))
    }

    @Test func testTheRotationTileNamesItsProfileWhenThereAreSeveral() throws {
        var i = try input()
        for k in i.cards.indices { i.cards[k].pinned = nil }  // nothing pressing: the first speaks
        let t = try #require(Overview.build(i).tiles.first { $0.title == "Rotation" })
        #expect(t.secondary.hasPrefix("default: session "))
    }

    @Test func testTheCleanupTileAndRecentSayWhatTheCleanupPaneSays() throws {
        let o = Overview.build(try input())
        #expect(o.tiles.first { $0.title == "Cleanup" }?.primary == "Schedule off")
        #expect(o.recent.contains { $0.text == "cleanup removed 1 item · " + Format.bytes(512_000_000) },
                "\(o.recent.map(\.text))")
    }

    @Test func testTheDaemonTileBelievesTheServiceOnceItIsRead() throws {
        var i = try input()
        i.daemon?.health = .polling
        i.daemon?.live = false
        i.daemonMode = "live"
        #expect(Overview.build(i).tiles.first { $0.title == "Daemon" }?.primary == "Live")
        i.daemonInstalled = false
        #expect(Overview.build(i).tiles.first { $0.title == "Daemon" }?.primary == "Not installed")
    }

    // QA pass 2

    /// Installed but never polled is not "not installed".
    @Test func testAnInstalledDaemonThatNeverPolledSaysSo() throws {
        var i = try input()
        i.daemon?.health = .never
        i.daemonInstalled = true
        let o = Overview.build(i)
        #expect(o.tiles.first { $0.title == "Daemon" }?.primary == "Not polling yet")
        #expect(o.needs.contains { $0.text.contains("installed") && $0.text.contains("not polled") && $0.target == .daemon })
    }

    @Test func testTheTilesUseTheAppsWords() throws {
        var i = try input()
        i.cards[1].signedIn = "no"
        let p = try #require(Overview.build(i).tiles.first { $0.title == "Profiles" })
        #expect(p.secondary.hasPrefix("1 needs sign-in"))
        i.cards = [i.cards[0]]
        let one = try #require(Overview.build(i).tiles.first { $0.title == "Profiles" })
        #expect(one.secondary.contains(" · session "), "the session figure is named")
    }

    /// The Rotation tile speaks for the profile that most needs it: a pool
    /// running dry soon, then a pending switch or wait, then a pin.
    @Test func testTheRotationTileSpeaksForThePressingProfile() throws {
        var i = try input()
        i.cards[1].poolDryAt = fixtureNow.addingTimeInterval(40 * 60)
        let o = Overview.build(i)
        let t = try #require(o.tiles.first { $0.title == "Rotation" })
        #expect(t.secondary.hasPrefix(i.cards[1].name + ": ") && t.attention)
        #expect(o.needs.contains { $0.text.contains("runs dry") && $0.target == .rotation })
    }

    @Test func testRecentSaysWhatAnUndoPutBack() throws {
        var i = try input()
        i.clean?.lastPutBack = 1
        #expect(Overview.build(i).recent.contains { $0.text.hasSuffix(" · 1 put back") })
    }
}

