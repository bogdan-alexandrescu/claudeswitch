import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Design calls of 2026-10-28 (owner: "CLD builds its recommendations"):
/// the popover's Next line (D2) and how loud Switch to best is (D3).
@Suite struct DesignCallsTests {
    func card(kind: String?, reason: String = "", session: Double = 85, week: Double = 98,
              used: (Double, Double) = (18, 41)) -> ProfileCard {
        let a = AccountView(id: "a1", isActive: true, isPinned: false, status: .available,
                            fiveHour: WindowReading(utilization: used.0, resetsAt: nil),
                            sevenDay: WindowReading(utilization: used.1, resetsAt: nil),
                            bindingWindow: used.1 - week > used.0 - session ? "seven_day" : "five_hour",
                            bindingPct: used.1 - week > used.0 - session ? used.1 : used.0, level: .ok, scopedWeekly: [])
        let b = AccountView(id: "b1", isActive: false, isPinned: false, status: .available,
                            fiveHour: WindowReading(utilization: 5, resetsAt: nil),
                            sevenDay: WindowReading(utilization: 10, resetsAt: nil),
                            bindingWindow: "seven_day", bindingPct: 10, level: .ok, scopedWeekly: [])
        let d = kind.flatMap { Decision(JSON(["kind": $0, "reason": reason, "forced": false])) }
        var c = ProfileCard(name: "work", dir: nil, signedIn: "yes", active: a, pinned: nil, decision: d,
                            switchAt: session, switchAtWeekly: week, pool: ["a1", "b1"], accounts: [a, b])
        c.best = b
        c.onBest = false
        return c
    }

    /// D2: a routine stay says how far the account is from rotating, as
    /// `cs status` does; it read "stay — active account at 41%, under the
    /// 98% trigger". The window is the one the daemon judged (its reason's trigger); the
    /// figure is the reading the dial above shows, so the two agree (the
    /// daemon's projected 42% beside a 41% dial read "53 points"). Short
    /// enough not to leave "it rotates" alone on a second line.
    @Test func testARoutineStaySaysHowFarTheAccountIsFromRotating() {
        #expect(card(kind: "stay", reason: "active account at 42%, under the 98% trigger").nextLine
            == "stay · 57 points left on the week")
        #expect(card(kind: "stay", reason: "active account at 80%, under the 85% trigger", used: (80, 41)).nextLine
            == "stay · 5 points left on the session")
        #expect(card(kind: "stay", reason: "active account at 84%, under the 85% trigger", used: (84, 41)).nextLine
            == "stay · 1 point left on the session")
        #expect(card(kind: "stay", reason: "active account at 80%, under the 85% trigger", week: 85,
                     used: (80, 41)).nextLine == "stay · 5 points left", "one line for both windows names neither")
        // The daemon's reason names its window since 0.8.18; older ones did not.
        #expect(card(kind: "stay", reason: "active account at 41% of its week, under the 98% trigger").nextLine
            == "stay · 57 points left on the week")
        #expect(card(kind: "stay", reason: "active account at 41% of its Modelthree week, under the 98% trigger").nextLine
            == "stay · 57 points left on the week")
    }

    /// The room is the card's binding window's, the one its label and picker
    /// show: a reason naming the week beside a session nearer its line read
    /// "86 points left on the week" under a label of "work-team 6%" (QA pass 2).
    @Test func testTheRoomIsTheBindingWindows() {
        #expect(card(kind: "stay", reason: "active account at 12% of its week, under the 98% trigger",
                     used: (6, 12)).nextLine == "stay · 79 points left on the session")
    }

    @Test func testAnyOtherDecisionKeepsTheDaemonsReason() {
        #expect(card(kind: "stay", reason: "active account's usage is unknown; holding until it can be read").nextLine
            == "stay — active account's usage is unknown; holding until it can be read")
        #expect(card(kind: "wait", reason: "every account is out").nextLine == "wait — every account is out")
        #expect(card(kind: "stay").nextLine == "stay")
        #expect(card(kind: nil).nextLine == "unknown")
    }

    /// D3: Switch to best is the filled button only when the daemon would
    /// move anyway; while it stays, the loudest control was an action nobody
    /// needed.
    @Test func testSwitchToBestIsFilledOnlyWhenTheDaemonWouldMove() {
        #expect(!card(kind: "stay", reason: "active account at 41%, under the 98% trigger").bestIsPrimary)
        #expect(card(kind: "switch", reason: "active account at 86%, over the 85% trigger").bestIsPrimary)
        #expect(card(kind: "wait", reason: "every account is out").bestIsPrimary)
        #expect(card(kind: nil).bestIsPrimary, "with no decision yet it stays as it was")
        var none = card(kind: "switch")
        none.best = nil
        #expect(!none.bestIsPrimary, "nothing to switch to")
        var on = card(kind: "switch")
        on.onBest = true
        #expect(!on.bestIsPrimary, "already on the best")
    }
}
