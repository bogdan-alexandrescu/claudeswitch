import Foundation
import Testing
@testable import ClaudeSwitchCore

/// M11: no modal alerts in the popover. A refused action's error shows inline
/// in the card that caused it, with the CLI's message verbatim, a dismiss
/// button and, when the CLI gives retry_at (R1), the time to try again.
@Suite struct InlineErrorTests {
    // MARK: retry_at in the error object

    @Test func testRetryAtIsDecodedFromTheErrorObject() throws {
        let e = try #require(AppError(data: try fixture("cli-error-live-rate-limited.json")))
        #expect(e.code == "live")
        #expect(e.message.hasPrefix("can't confirm \"work-1\" isn't signed in under profile \"default\""))
        let at = try #require(e.retryAt)
        #expect(at == GoTime.parse("2026-10-08T14:03:07-07:00"))
        #expect(CallError.app(e).retryAt == at)
    }

    @Test func testNoRetryAtWhenTheCLIGivesNone() throws {
        let plain = try #require(AppError(data: try fixture("cli-error-live.json")))
        #expect(plain.retryAt == nil)
        let null = try #require(AppError(data: Data(#"{"error":{"code":"live","message":"m","hint":"","retry_at":null}}"#.utf8)))
        #expect(null.retryAt == nil)
        #expect(CallError.cli(.missing).retryAt == nil)
    }

    // MARK: the error model per card

    @Test func testAnErrorShowsOnTheCardThatCausedItOnly() {
        var errs = CardErrors()
        errs.post(CardError(title: "Could not switch work", .app(AppError(code: "live", message: "m1"))), card: "work")
        #expect(errs.error(for: "work")?.message == "m1")
        #expect(errs.error(for: "default") == nil)
        errs.post(CardError(title: "Could not pin default", .app(AppError(code: "failed", message: "m2"))), card: "default")
        #expect(errs.error(for: "work")?.message == "m1", "one card's error does not replace another's")
        #expect(errs.error(for: "default")?.message == "m2")
    }

    @Test func testTheMessageIsTheCLIsVerbatim() throws {
        let app = try #require(AppError(data: try fixture("cli-error-live-rate-limited.json")))
        let e = CardError(title: "Could not switch default to work-1", .app(app))
        #expect(e.message == app.message)
        #expect(e.hint == app.hint)
        #expect(e.retryAt == app.retryAt)
    }

    @Test func testDismissClearsOnlyThatCard() {
        var errs = CardErrors()
        errs.post(CardError(title: "t", .app(AppError(code: "live", message: "a"))), card: "work")
        errs.post(CardError(title: "t", .app(AppError(code: "live", message: "b"))), card: "default")
        errs.dismiss(card: "work")
        #expect(errs.error(for: "work") == nil)
        #expect(errs.error(for: "default")?.message == "b")
    }

    @Test func testANewActionOnTheCardClearsItsError() {
        var errs = CardErrors()
        errs.post(CardError(title: "t", .app(AppError(code: "live", message: "a"))), card: "work")
        errs.begin(card: "default")
        #expect(errs.error(for: "work") != nil, "an action elsewhere leaves it")
        errs.begin(card: "work")
        #expect(errs.error(for: "work") == nil)
    }

    @Test func testTheSameRefusalAgainIsANewError() {
        var errs = CardErrors()
        let app = AppError(code: "live", message: "a")
        errs.post(CardError(title: "t", .app(app)), card: "work")
        let first = errs.error(for: "work")
        errs.begin(card: "work")
        errs.post(CardError(title: "t", .app(app)), card: "work")
        #expect(errs.error(for: "work") != first, "a repeat refusal shows afresh")
    }

    // MARK: the retry line

    @Test func testRetryLineNamesTheLocalTime() throws {
        let la = try #require(TimeZone(identifier: "America/Los_Angeles"))
        let at = try #require(GoTime.parse("2026-10-08T21:03:07Z"))
        var e = CardError(title: "t", .app(AppError(code: "live", message: "m")))
        #expect(e.retryLine(timeZone: la) == nil, "no retry_at, no line")
        e.retryAt = at
        #expect(e.retryLine(timeZone: la) == "Try again at 14:03:07")
        let utc = try #require(TimeZone(identifier: "UTC"))
        #expect(e.retryLine(timeZone: utc) == "Try again at 21:03:07")
    }
}
