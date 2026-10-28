import Foundation
import Testing
@testable import ClaudeSwitchCore

/// QA 2026-10-28, second pass: with the binary's banner closed by its ×
/// every step button was grey and nothing said why; the window's words
/// used straight apostrophes ("Don't show this again").
@Suite struct SetupTextTests {
    /// The line kept by the steps once the banner is closed.
    @Test func testAClosedBannerLeavesALineSayingWhy() {
        #expect(FirstRun.banner(.missing)?.line == "claudeswitch is missing: install it first")
        #expect(FirstRun.banner(.tooOld(version: "0.5.0"))?.line == "claudeswitch is too old: update it first")
        #expect(FirstRun.banner(.unsupportedContract(version: "0.9.0", contract: 3))?.line
                == "claudeswitch is newer than this app: update the app first")
    }

    @Test func testTheWindowsWordsUseCurlyApostrophes() {
        #expect(FirstRun.dontShowLabel == "Don’t show this again")
        var words = [FirstRun.reopenHint, FirstRun.dontShowHelp, FirstRun.dontShowLabel, FirstRun.initTooOld,
                     FirstRun.daemonReading, FirstRun.tryAgain, FirstRun.daemonSettings]
        for s in SetupStep.allCases {
            words += [s.title, s.detail, s.action, s.reopen(.done), s.reopen(.skipped)]
        }
        let problems: [CLIError] = [.missing, .tooOld(version: "0.5.0"),
                                    .unsupportedContract(version: "0.9.0", contract: 3)]
        for p in problems {
            if let b = FirstRun.banner(p) { words += [b.title, b.line] }
        }
        for w in words { #expect(!w.contains("'"), "a straight apostrophe in \(w)") }
    }
}
