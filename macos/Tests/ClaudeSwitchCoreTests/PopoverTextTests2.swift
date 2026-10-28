import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The popover after the 2026-10-28 visual QA.
@Suite struct PopoverQATests {
    func card(kind: String?, target: String? = nil, status: AccountStatus = .available) -> ProfileCard {
        let a = AccountView(id: "w01", isActive: true, isPinned: false, status: status,
                            fiveHour: WindowReading(utilization: 62, resetsAt: nil),
                            sevenDay: WindowReading(utilization: 18, resetsAt: nil),
                            bindingWindow: "five_hour", bindingPct: 62, level: .ok, scopedWeekly: [])
        var j: [String: Any] = ["kind": kind ?? "", "reason": "", "forced": false]
        if let target { j["target"] = target }
        return ProfileCard(name: "work", dir: nil, signedIn: "yes", active: a, pinned: nil,
                           decision: kind.flatMap { _ in Decision(JSON(j)) },
                           switchAt: 85, switchAtWeekly: 98, pool: ["w01"], accounts: [a])
    }

    /// A collapsed card leads with a pending switch or wait, and its figures
    /// are a part of their own, so a long id truncates before they do.
    @Test func testACollapsedCardLeadsWithWhatTheDaemonIsAboutToDo() {
        let figures = "session 62% · week 18%"
        #expect(Format.cardSummaryParts(card(kind: "stay")) == CardSummary(lead: "w01", figures: figures))
        #expect(Format.cardSummaryParts(card(kind: "switch", target: "w02")) == CardSummary(lead: "w01 → w02", figures: figures))
        #expect(Format.cardSummaryParts(card(kind: "wait")) == CardSummary(lead: "waiting · w01", figures: figures))
        #expect(Format.cardSummaryParts(card(kind: "stay", status: .needsLogin))
            == CardSummary(lead: "needs sign-in · w01", figures: nil))
        #expect(Format.cardSummary(card(kind: "switch", target: "w02")) == "w01 → w02 · session 62% · week 18%")
    }

    /// The menu-bar title stays short: each name is cut in the middle past
    /// 16 characters, so the figure and its "!" always show.
    @Test func testTheMenuBarTitleCutsLongNamesInTheMiddle() {
        #expect(Format.middle("client-example-enterprise-integration", 16) == "client-e…gration")
        #expect(Format.middle("work", 16) == "work")
    }

    /// The retry time is said once: not again when the CLI's message has
    /// it, and not at all once it has passed.
    @Test func testTheRetryTimeIsSaidOnceAndOnlyWhileAhead() {
        let tz = TimeZone(identifier: "UTC")!
        let at = Date(timeIntervalSince1970: 1_800_000_000)
        let clock = CardError.clock(at, timeZone: tz)
        let quiet = CardError(title: "t", message: "rate limited", retryAt: at)
        #expect(quiet.retryLine(now: at.addingTimeInterval(-60), timeZone: tz) == "Try again at " + clock)
        #expect(quiet.retryLine(now: at.addingTimeInterval(1), timeZone: tz) == nil)
        let said = CardError(title: "t", message: "rate limited until \(clock); try again then", retryAt: at)
        #expect(said.retryLine(now: at.addingTimeInterval(-60), timeZone: tz) == nil)
    }

    /// Switch to best never offers an account that is live in another
    /// profile or needs sign-in (the base fixture's why names personal, live
    /// in review and needing sign-in, for default).
    @Test func testSwitchToBestNeverOffersAnAccountItCannotUse() throws {
        let s = try cardsSnapshot()
        let d = try #require(s.card("default"))
        #expect(d.best?.id != "personal")
        #expect(d.best == nil ? d.bestUnavailable?.contains("personal") == true : true)
    }
}
