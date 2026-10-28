import Foundation
import Testing
@testable import ClaudeSwitchCore

/// An Accounts row's second line (visual QA, Settings → Accounts).
@Suite struct AccountsRowTests {
    let now = Date(timeIntervalSince1970: 1_790_000_000)

    /// A row said "needs login" and "login renews until Nov 7" at once: the
    /// refresh date says nothing once only a sign-in helps.
    @Test func testAnAccountThatNeedsSignInSaysNothingOfRenewing() {
        let later = now.addingTimeInterval(9 * 86_400)
        #expect(Format.login(refresh: later, token: nil, now: now)?.hasPrefix("login renews until ") == true)
        #expect(Format.login(refresh: later, token: nil, now: now, needsSignIn: true) == nil)
        #expect(Format.login(refresh: nil, token: later, now: now, needsSignIn: true) == nil)
    }

    /// Whole segments give way, the seat first, then Chrome, then the login,
    /// then who uses it (named, then counted); the figure goes last.
    @Test func testTheFigureOutlastsEverythingElse() {
        let login = "login renews until Nov 7, 2026 at 6:45 PM"
        #expect(AccountLine.variants(usedBy: ["default", "work"], figure: "12% 7d", chrome: "Chrome Work",
                                     seat: "seat abc@def", login: login) == [
            "used by default, work · 12% 7d · Chrome Work · seat abc@def · " + login,
            "used by default, work · 12% 7d · Chrome Work · " + login,
            "used by default, work · 12% 7d · " + login,
            "used by default, work · 12% 7d",
            "used by 2 profiles · 12% 7d",
            "12% 7d",
        ])
        #expect(AccountLine.variants(usedBy: ["client-example-enterprise"], figure: "12% 7d", chrome: nil,
                                     seat: nil, login: login) == [
            "used by client-example-enterprise · 12% 7d · " + login,
            "used by client-example-enterprise · 12% 7d",
            "12% 7d",
        ], "one profile is not counted: its name, or nothing")
    }

    @Test func testWhatIsUnknownIsLeftOut() {
        #expect(AccountLine.variants(usedBy: [], figure: nil, chrome: nil, seat: nil, login: nil) == ["used by no profile"])
        #expect(AccountLine.variants(usedBy: nil, figure: nil, chrome: nil, seat: nil, login: "token valid until x")
                == ["token valid until x"])
        #expect(AccountLine.variants(usedBy: nil, figure: nil, chrome: nil, seat: nil, login: nil) == [""])
    }
}
