import Foundation
import Testing
@testable import ClaudeSwitchCore

// Second-pass QA of the update window (2026-10-28): a failure Try Again
// cannot fix said nothing about what to do instead, the window's off state
// repeated its title, the bar was a third full beside "38%", and the
// window's apostrophes were straight.

@Suite struct Update2QATests {
    static let advice = "Try again later, or download it from the release page."
    static let kept = "The app and the claudeswitch command were not changed."

    /// A release missing a file, or a download that does not match its
    /// checksum, fails the same way on Try Again; "Release Notes on
    /// GitHub" did not read as the way out.
    @Test func testAFailureTheReleaseCausesSaysWhatToDoInstead() {
        let theRelease: [UpdateError] = [.missingAsset("checksums.txt"), .checksumMissing("a.zip"),
                                         .checksumMismatch("a.zip")]
        for e in theRelease {
            #expect(e.message.hasSuffix(" " + Self.kept + " " + Self.advice), "\(e)")
        }
        #expect(UpdateError.checksumMismatch("ClaudeSwitch-0.5.6-macos.zip").message
            == "ClaudeSwitch-0.5.6-macos.zip does not match its published checksum. " + Self.kept + " " + Self.advice)
        // Try Again can work for these: a download cut off, a folder made
        // writable, a cancel.
        let passing: [UpdateError] = [.download("x: offline"), .install("y"), .cancelled, .daemon("exit 1"),
                                      .noRelease("offline")]
        for e in passing {
            #expect(!e.message.contains("release page"), "\(e)")
        }

        let failed = UpdateDialog.failed(version: "0.5.6",
                                         message: UpdateError.checksumMismatch("ClaudeSwitch-0.5.6-macos.zip").message)
        #expect(failed.message.hasSuffix(Self.advice))
        #expect(failed.pageLabel == "Release Page on GitHub", "the page it is downloaded from, by its name")
        #expect(failed.page == URL(string: UpdateSource.home + "/releases/tag/v0.5.6"))
        let offer = UpdateDialog.available(version: "0.5.6", current: "0.5.5", notes: nil, bytes: 0,
                                           page: URL(string: UpdateSource.home + "/releases/tag/v0.5.6"))
        #expect(offer.pageLabel == "Release Notes on GitHub")
        #expect(UpdateDialog.checking.pageLabel == nil && UpdateDialog.checkFailed("x").pageLabel == nil)
    }

    /// "Checking for updates is off" over "Settings → Advanced → Check for
    /// updates is off, and off makes no call at all."
    @Test func testTheOffStateSaysWhatOffMeansWithoutRepeatingItsTitle() {
        let off = UpdateDialog.checkOff
        #expect(off.message == "While it is off, the app never contacts GitHub. Turn On checks now, then once a day.")
        #expect(!off.message.contains(off.title) && !off.message.contains("Check for updates is off"),
                "the title says that")
    }

    /// The bar was the whole install, 33% full beside the download's 38%.
    /// It now fills by the download's own share while downloading, then
    /// by step.
    @Test func testTheBarAgreesWithItsFigureWhileDownloading() throws {
        let step = UpdateStep.downloading(received: 4_400_000, total: 11_300_000)
        #expect(step.percent == "38%")
        #expect(step.fraction > 0.38 && step.fraction < 0.39, "\(step.fraction)")
        for got in stride(from: Int64(0), through: 1000, by: 7) {
            let s = UpdateStep.downloading(received: got, total: 1000)
            let text = try #require(s.percent)
            let figure = try #require(Double(text.dropLast()))
            #expect(abs(s.fraction * 100 - figure) < 1, "\(got): bar \(s.fraction), figure \(figure)")
        }
        #expect(UpdateStep.downloading(received: 1000, total: 1000).fraction == 1)
        #expect(UpdateStep.downloading(received: 2000, total: 1000).fraction == 1)
        #expect(UpdateStep.downloading(received: -5, total: 1000).fraction == 0)
        #expect(UpdateStep.downloading(received: 0, total: 0).fraction == 0)

        let after: [UpdateStep] = [.verifying, .installingCLI, .installingApp, .restartingDaemon, .relaunching]
        #expect(after.map(\.fraction) == [0.2, 0.4, 0.6, 0.8, 1], "a fifth a step")
        #expect(after.allSatisfy { $0.percent == nil }, "the quick steps have no figure")
    }

    /// The spinner sits on the line it waits with.
    @Test func testTheCheckSaysItIsUnderWay() {
        #expect(UpdateDialog.checking.message == "Asking GitHub for the latest release…")
    }

    /// "What’s new", "release’s checksums", "This copy’s version".
    @Test func testTheWindowsApostrophesAreCurly() {
        #expect(UpdateDialog.versionUnknown(latest: "0.5.6").title == "This copy’s version is unknown")
        #expect(UpdateStep.verifying.label == "Checking the downloads against the release’s checksums…")
        let offer = UpdateDialog.available(version: "0.5.6", current: "0.5.5", notes: nil, bytes: 0, page: nil)
        #expect(offer.message.contains("release’s checksums"))
        let states: [UpdateDialog] = [
            .checking, .checkOff, .upToDate(current: "0.5.6", latest: "0.5.6"),
            .upToDate(current: "0.6.0", latest: "0.5.6"), .versionUnknown(latest: "0.5.6"), .checkFailed("x"),
            offer, .installed(version: "0.5.6"),
            .failed(version: "0.5.6", message: UpdateError.checksumMismatch("a.zip").message),
        ] + [UpdateStep.downloading(received: 1, total: 10), .verifying, .installingCLI, .installingApp,
             .restartingDaemon, .relaunching].map { .installing(version: "0.5.6", step: $0) }
        for d in states {
            let words = [d.title, d.message, d.detail ?? "", d.pageLabel ?? ""]
            #expect(!words.joined().contains("'"), "\(d)")
        }
        let errors: [UpdateError] = [.noRelease("x"), .missingAsset("x"), .checksumMissing("x"), .checksumMismatch("x"),
                                     .download("x"), .install("x"), .daemon("x"), .unsigned("x"), .cancelled]
        #expect(!errors.map(\.message).joined().contains("'"))
    }
}
