import Foundation
import Testing
@testable import ClaudeSwitchCore

/// M12: when the best other account has no more room than the active one
/// (by the binding window: the higher of session and week), "Switch to best"
/// is off and reads "Already on the best", with the next best underneath.
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
}
