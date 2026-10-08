import Foundation
import Testing
@testable import ClaudeSwitchCore

/// M12: when the best other account has no more room than the active one,
/// "Switch to best" is off and reads "Already on the best", with the next
/// best underneath. Room is the policy's (owner, 2026-10-08): the CLI's
/// `on_best` decides. The tests built on `card` leave it unset, which is
/// the fallback for an older binary: the higher of session and week.
@Suite struct AlreadyBestTests {
    func card(active: Double?, best: Double?) throws -> ProfileCard {
        var c = try #require(try cardsSnapshot().card("default"))
        var a = try #require(c.accounts.first { $0.id == "work-1" })
        var b = try #require(c.accounts.first { $0.id == "work-2" })
        a.bindingPct = active
        b.bindingPct = best
        c.active = a
        c.best = b
        c.bestWhy = nil
        c.onBest = nil
        return c
    }

    @Test func testABetterAccountKeepsTheButton() throws {
        let c = try card(active: 80, best: 12)
        #expect(!c.alreadyOnBest)
        #expect(c.bestTitle == "Switch to best")
        #expect(c.bestSubtitle == "work-2 · 12%")
    }

    @Test func testNoMoreRoomIsAlreadyOnTheBest() throws {
        for (active, best) in [(30.0, 30.0), (30.0, 55.0)] {
            let c = try card(active: active, best: best)
            #expect(c.alreadyOnBest, "best \(best) vs active \(active)")
            #expect(c.bestTitle == "Already on the best")
            #expect(c.bestSubtitle == "next: work-2 · \(Int(best))%")
        }
    }

    @Test func testAnUnknownReadingDoesNotClaimIt() throws {
        #expect(!(try card(active: nil, best: 10)).alreadyOnBest)
        #expect(!(try card(active: 10, best: nil)).alreadyOnBest)
    }

    @Test func testNoBestHasNoSubtitle() throws {
        var c = try card(active: 10, best: 20)
        c.best = nil
        #expect(!c.alreadyOnBest)
        #expect(c.bestTitle == "Switch to best")
        #expect(c.bestSubtitle == nil)
    }

    // The CLI's on_best decides whenever it is present.

    /// Session 80% against an 85% trigger (5 points) while the best is at
    /// 85% of its week against 98% (13 points): raw utilization says
    /// already on the best, the policy's room says switch. The CLI wins.
    @Test func testTheCLIsRoomOverridesRawUtilization() throws {
        var c = try card(active: 80, best: 85)
        #expect(c.alreadyOnBest, "the fallback compares raw percentages")
        c.onBest = false
        #expect(!c.alreadyOnBest)
        #expect(c.bestTitle == "Switch to best")
        #expect(c.bestSubtitle == "work-2 · 85%")
    }

    @Test func testOnBestFromTheCLIDisablesTheButton() throws {
        var c = try card(active: 80, best: 12)
        #expect(!c.alreadyOnBest)
        c.onBest = true
        #expect(c.alreadyOnBest)
        #expect(c.bestTitle == "Already on the best")
    }

    /// No best account is never "already on the best", whatever on_best says.
    @Test func testOnBestWithoutABestClaimsNothing() throws {
        var c = try card(active: 10, best: 20)
        c.best = nil
        c.onBest = true
        #expect(!c.alreadyOnBest)
    }

    /// The disagreeing case as `why --json` reports it.
    @Test func testTheDisagreeingFixture() throws {
        let w = try #require(WhyReport(data: try fixture("why-m12-room.json")))
        let p = try #require(w.primary)
        #expect(p.best == "work-2")
        #expect(p.onBest == false)
        #expect(p.activeRoom == 5)
        #expect(p.bestRoom == 13)
        let s = Snapshot.build(state: StateFile(data: try fixture("state.json")), why: w,
                               settings: ConfigSettings(data: try fixture("config.json")),
                               profiles: nil, now: fixtureNow)
        var c = try #require(s.card("default"))
        #expect(c.onBest == false)
        // Whatever the state file's readings, the CLI's room decides.
        var a = try #require(c.active)
        var b = try #require(c.best)
        a.bindingPct = 80
        b.bindingPct = 85
        c.active = a
        c.best = b
        #expect(!c.alreadyOnBest)
        #expect(c.bestTitle == "Switch to best")
    }

    /// An older binary has no on_best: the card falls back.
    @Test func testAnOlderBinaryHasNoOnBest() throws {
        let p = try #require(WhyReport(data: try fixture("why.json"))?.primary)
        #expect(p.onBest == nil && p.activeRoom == nil && p.bestRoom == nil)
    }

    /// A null room is unknown, not 0.
    @Test func testNullRoomsDecodeAsUnknown() throws {
        let j = #"{"profiles": [{"profile": "default", "accounts": [], "best": null, "on_best": false,"#
            + #" "active_room": null, "best_room": null}]}"#
        let p = try #require(WhyReport(data: Data(j.utf8))?.primary)
        #expect(p.onBest == false)
        #expect(p.activeRoom == nil && p.bestRoom == nil)
    }
}
