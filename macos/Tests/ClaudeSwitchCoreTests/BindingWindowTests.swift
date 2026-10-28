import Foundation
import Testing
@testable import ClaudeSwitchCore

/// An account is judged by the window nearest its own trigger, as the daemon
/// judges it (internal/policy, fixed 2026-10-26), not by the higher figure.
/// At 86% of the session and 90% of the week, against 85 and 98, the session
/// is past its line; judged by the week (90 < 98) the app said "available".
@Suite struct BindingWindowTests {
    let cfg = ConfigSettings(switchAt: 85, switchAtWeekly: 98)
    let now = Date(timeIntervalSince1970: 1_800_000_000)

    func view(session: Double?, week: Double?) -> AccountView {
        var usage: [String: Any] = [:]
        if let session { usage["five_hour"] = ["utilization": session] }
        if let week { usage["seven_day"] = ["utilization": week] }
        let record = AccountRecord(id: "a1", JSON(["last_usage": usage]))
        return Snapshot.account(id: "a1", record: record, verdict: nil, activeID: "a1", pinned: nil, cfg: cfg, now: now)
    }

    @Test func testTheWindowNearestItsOwnTriggerBinds() {
        let a = view(session: 80, week: 90)
        #expect(a.bindingWindow == "five_hour" && a.bindingPct == 80,
                "5 points from the session's 85 is nearer than 8 from the week's 98")
        #expect(a.level == Level.of(80, trigger: 85))
        let b = view(session: 10, week: 90)
        #expect(b.bindingWindow == "seven_day" && b.bindingPct == 90)
        let c = view(session: nil, week: 40)
        #expect(c.bindingWindow == "seven_day" && c.bindingPct == 40, "an unknown window never binds")
    }

    @Test func testAnAccountPastItsSessionLineHasNoHeadroomWhateverItsWeek() {
        #expect(view(session: 86, week: 90).status == .noHeadroom)
        #expect(view(session: 80, week: 90).status == .available)
    }
}
