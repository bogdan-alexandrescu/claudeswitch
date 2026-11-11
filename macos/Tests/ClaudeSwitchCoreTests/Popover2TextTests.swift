import Foundation
import Testing
@testable import ClaudeSwitchCore

/// The popover's text after the second visual QA (2026-10-28): the picker
/// says which window its figure is from and when a refusal clears, a
/// refusal names its window in the app's words.
@Suite struct Popover2TextTests {
    let now = GoTime.parse("2026-10-08T02:46:41Z")!

    func account(_ id: String, _ window: String?, _ pct: Double?, status: AccountStatus = .available) -> AccountView {
        AccountView(id: id, isActive: false, isPinned: false, status: status,
                    fiveHour: nil, sevenDay: nil, bindingWindow: window, bindingPct: pct, level: .ok,
                    scopedWeekly: [])
    }

    /// "w06 25%" beside "w09 56%" did not say that one is a session and the
    /// other a week.
    @Test func testThePickerNamesTheWindowOfEachFigure() {
        #expect(Format.pickerLine(account("w06", "five_hour", 25), inUseBy: nil, now: now)
            == "w06  25% session · available")
        #expect(Format.pickerLine(account("w09", "seven_day", 56), inUseBy: nil, now: now)
            == "w09  56% week · available")
        #expect(Format.pickerLine(account("team-2", "seven_day", 41), inUseBy: "work", now: now)
            == "team-2  41% week · in use by work")
        #expect(Format.pickerLine(account("team-2", nil, nil), inUseBy: "work", now: now)
            == "team-2  – · in use by work", "an unknown figure names no window")
    }

    /// "w03 100% · refused" did not say when it clears; the refusal names
    /// its window once.
    @Test func testThePickerSaysWhenARefusalClears() {
        let refused = AccountStatus.refused(window: "five_hour", until: now.addingTimeInterval(36 * 60))
        #expect(Format.pickerLine(account("w03", "five_hour", 100, status: refused), inUseBy: nil, now: now)
            == "w03  100% · refused · session · clears in 36m")
        let unnamed = AccountStatus.refused(window: nil, until: nil)
        #expect(Format.pickerLine(account("w03", "seven_day", 100, status: unnamed), inUseBy: nil, now: now)
            == "w03  100% week · refused")
    }

    /// A refusal is said in the app's words: session and week, not 5h/7d.
    @Test func testARefusalNamesItsWindowAsSessionOrWeek() {
        let until = now.addingTimeInterval(40 * 60)
        #expect(Format.status(.refused(window: "five_hour", until: until), now: now) == "refused · session · clears in 40m")
        #expect(Format.status(.refused(window: "seven_day", until: nil), now: now) == "refused · week")
        #expect(Format.status(.refused(window: "five_hour", until: now.addingTimeInterval(-1)), now: now)
            == "refused · session", "a time already passed is not said")
    }
}
