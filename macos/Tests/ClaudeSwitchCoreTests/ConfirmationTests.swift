import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Every action that changes where an account lives, what is guarded, or
/// whether the daemon swaps for real is confirmed first, naming what it
/// touches (lane 14 review).
@Suite struct ConfirmationTests {
    @Test func testRemovingFromAPoolNamesTheAccountAndWhereItGoes() {
        let c = Confirm.poolRemove(account: "work-2", from: "review")
        #expect(c.title == "Remove work-2 from review?")
        #expect(c.message.contains("default"), "it returns to the default profile's pool")
        #expect(c.action == "Remove")
        #expect(c.destructive)
    }

    @Test func testMovingNamesBothProfiles() {
        let c = Confirm.poolMove(account: "work-2", from: "default", to: "review")
        #expect(c.title == "Move work-2 to review?")
        #expect(c.message.contains("default") && c.message.contains("review"))
        #expect(c.action == "Move")
    }

    @Test func testForgettingAGhostSaysItStopsBeingGuarded() {
        let c = Confirm.forgetGhost(profile: "old", account: "work-2")
        #expect(c.title == "Forget old?")
        #expect(c.message.contains("work-2"))
        #expect(c.message.contains("no longer guarded"))
        #expect(c.destructive)
    }

    @Test func testClearingARecoveryCopy() {
        let c = Confirm.recoveryClear(slot: "r1", account: "personal")
        #expect(c.title == "Clear recovery copy r1?")
        #expect(c.message.contains("personal"))
        #expect(c.destructive)
        #expect(Confirm.recoveryClear(slot: "r2", account: nil).message.contains("r2"))
    }

    @Test func testRestoringToAnotherAccountIsCalledOut() {
        let same = Confirm.recoveryRestore(slot: "r1", to: "personal", itsAccount: "personal")
        #expect(same.title == "Restore r1 to personal?")
        #expect(!same.message.contains("another account"))
        let other = Confirm.recoveryRestore(slot: "r1", to: "work-1", itsAccount: "personal")
        #expect(other.message.contains("another account"))
        #expect(other.message.contains("personal"))
        #expect(other.destructive)
    }

    @Test func testGoingLiveSaysItSwapsForReal() {
        for c in [Confirm.daemonLive(install: false), Confirm.daemonLive(install: true)] {
            #expect(c.message.contains("switches"))
            #expect(!c.destructive)
        }
        #expect(Confirm.daemonLive(install: true).action == "Install live")
        #expect(Confirm.daemonLive(install: false).action == "Go live")
    }
}

/// The menu-bar item must never be empty: a symbol the system lacks falls
/// back, and so does the whole label (lane 14 review: no status item seen).
@Suite struct MenuBarIconTests {
    @Test func testTheSymbolFollowsTheLevel() {
        let ok = MenuTitle(text: "work-1 41%", level: .ok, problem: false)
        #expect(MenuBarIcon.symbol(ok, available: { _ in true }) == "gauge.with.dots.needle.33percent")
        #expect(MenuBarIcon.symbol(MenuTitle(text: "", level: .over, problem: false), available: { _ in true })
                == "exclamationmark.triangle.fill")
        #expect(MenuBarIcon.symbol(MenuTitle(text: "cs", level: .ok, problem: true), available: { _ in true })
                == "questionmark.circle")
    }

    @Test func testAMissingSymbolFallsBack() {
        let ok = MenuTitle(text: "", level: .near, problem: false)
        #expect(MenuBarIcon.symbol(ok, available: { $0 == "gauge" }) == "gauge")
        #expect(MenuBarIcon.symbol(ok, available: { _ in false }) == nil, "nil: draw text instead")
    }

    @Test func testCompactWithNoSymbolStillShowsText() {
        let compact = MenuTitle(text: "", level: .ok, problem: false)
        #expect(MenuBarIcon.text(compact, symbol: nil) == "cs")
        #expect(MenuBarIcon.text(compact, symbol: "gauge") == "")
        #expect(MenuBarIcon.text(MenuTitle(text: "work-1 41%", level: .ok, problem: false), symbol: nil) == "work-1 41%")
    }
}
