import Foundation
import Testing
@testable import ClaudeSwitchCore

// Visual QA of the update window (2026-10-28): what the progress bar's
// figures say, why Cancel is greyed, what a failure says and where it
// leads, and how What's new wraps.

@Suite struct UpdateWindowTextTests {
    /// "Downloading… 4 MB of 11 MB" sat beside 33%, the whole install's
    /// share, where 4 of 11 reads as 36-39%; and whole megabytes hid the
    /// progress. The figure is now the download's own, in tenths of a
    /// megabyte under 100 MB.
    @Test func testADownloadShowsItsOwnShareInTenthsOfAMegabyte() {
        let step = UpdateStep.downloading(received: 4_400_000, total: 11_300_000)
        #expect(step.label == "Downloading… 4.2 MB of 10.8 MB")
        #expect(step.percent == "38%", "4.2 of 10.8")
        #expect(Int(step.fraction * 100) == 38, "the bar agrees with it")
        #expect(UpdateStep.downloading(received: 11_300_000, total: 11_300_000).percent == "100%")
        #expect(UpdateStep.downloading(received: 11_299_999, total: 11_300_000).percent == "99%",
                "100% only once it is all there")
        #expect(UpdateStep.downloading(received: 0, total: 0).percent == nil)
        #expect(UpdateStep.verifying.percent == nil, "the quick steps after the downloads have no figure")
        #expect(UpdateStep.installingApp.percent == nil)
        #expect(UpdateStep.downloading(received: 300 << 10, total: 150 << 20).label == "Downloading… 300 KB of 150 MB")
        let offer = UpdateDialog.available(version: "0.5.6", current: "0.5.5", notes: nil, bytes: 11_300_000, page: nil)
        #expect(offer.detail == "Downloads 10.8 MB from GitHub")
    }

    /// Cancel was greyed with the reason only in its tooltip.
    @Test func testTooLateToCancelIsSaidUnderTheBar() {
        #expect(UpdateStep.downloading(received: 1, total: 10).note == nil)
        #expect(UpdateStep.verifying.note == nil)
        #expect(UpdateStep.installingCLI.note == "Too late to cancel: the new files are going in.")
        #expect(UpdateStep.installingApp.note == "Too late to cancel: the new files are going in.")
        #expect(UpdateStep.restartingDaemon.note == "Too late to cancel: the new files are in.")
        #expect(UpdateStep.relaunching.note == "Too late to cancel: the new files are in.")
    }

    /// "ClaudeSwitch and claudeswitch are as they were" named the two by
    /// case alone.
    @Test func testAFailureSaysTheAppAndTheCommandWereNotChanged() {
        let kept = "The app and the claudeswitch command were not changed."
        let errors: [UpdateError] = [.missingAsset("checksums.txt"), .checksumMissing("a.zip"),
                                     .checksumMismatch("a.zip"), .download("x: 404"), .install("y"), .unsigned("z"),
                                     .cancelled]
        for e in errors {
            #expect(e.message.contains(" " + kept), "\(e)")
            #expect(!e.message.contains("ClaudeSwitch and claudeswitch"))
        }
        #expect(UpdateError.checksumMismatch("ClaudeSwitch-0.5.6-macos.zip").message
            == "ClaudeSwitch-0.5.6-macos.zip does not match its published checksum. " + kept
                + " Try again later, or download it from the release page.")
        #expect(UpdateError.daemon("exit 1").message == "The daemon did not restart on the new claudeswitch (exit 1), "
            + "so the app and the claudeswitch command were put back as they were.")
    }

    /// "…the latest release: The Internet connection appears to be
    /// offline.." read as one sentence with a capital after the colon and
    /// two full stops.
    @Test func testACheckThatFailedSaysWhyInASentenceOfItsOwn() {
        #expect(UpdateError.noRelease("The Internet connection appears to be offline.").message
            == "Could not read the latest release. The Internet connection appears to be offline.")
        #expect(UpdateError.noRelease("HTTP 404").message == "Could not read the latest release. HTTP 404.")
        #expect(UpdateError.noRelease("offline").message == "Could not read the latest release. Offline.")
    }

    /// On a checksum mismatch Try Again fails the same way: the release page
    /// is a way out, as on the offer.
    @Test func testAFailedInstallLeadsToTheReleasePage() {
        #expect(UpdateDialog.failed(version: "0.5.6", message: "x").page
            == URL(string: UpdateSource.home + "/releases/tag/v0.5.6"))
        let page = URL(string: "https://github.com/bogdan-alexandrescu/claudeswitch/releases/tag/v0.5.6")
        #expect(UpdateDialog.available(version: "0.5.6", current: "0.5.5", notes: nil, bytes: 0, page: page).page == page)
        #expect(UpdateDialog.checking.page == nil)
        #expect(UpdateDialog.checkFailed("x").page == nil)
    }

    /// What's new was one text, so a bullet's wrapped lines ran back under
    /// the bullet; each line now says whether it is one, for the window to
    /// hang the rest under its text.
    @Test func testWhatsNewSaysWhichLinesAreBullets() throws {
        let lines = UpdateNotes.lines("**Cleanup**\n• `cs clean --apply` removes only what you read\n• two\n\nplain")
        #expect(lines == [UpdateNoteLine(text: "**Cleanup**", bullet: false),
                          UpdateNoteLine(text: "`cs clean --apply` removes only what you read", bullet: true),
                          UpdateNoteLine(text: "two", bullet: true),
                          UpdateNoteLine(text: "", bullet: false),
                          UpdateNoteLine(text: "plain", bullet: false)])
        var r = try fixtureRelease()
        r.notes = "## Cleanup\n- one\n* two"
        #expect(UpdateNotes.lines(try #require(r.whatsNew)).map(\.bullet) == [false, true, true],
                "the release's own list items, as whatsNew writes them")
    }
}
