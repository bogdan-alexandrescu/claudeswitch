import Foundation
import Testing
@testable import ClaudeSwitchCore

// 0.8.9 (owner request 2026-10-25): Check for updates answers in a window of
// its own: checking, up to date, a check that failed, an offer with what is
// new and how much it downloads, a progress bar through the install, a
// failure, and the version it became once it reopened. And every build asks
// the public repository: one from the private tree asked a placeholder
// address (HTTP 404) and could never offer an update.

@Suite struct UpdateDialogTests {
    @Test func testEveryBuildChecksThePublicRepository() throws {
        #expect(UpdateSource.home == "https://github.com/bogdan-alexandrescu/claudeswitch")
        let path = try #require(URL(string: UpdateSource.home)).path
        #expect(UpdateSource.latestURL().absoluteString == "https://api.github.com/repos" + path + "/releases/latest")
    }

    @Test func testACheckBecomesTheWindowsState() throws {
        let r = try fixtureRelease()
        let failed = UpdateDialog.checked(.failure(.noRelease("latest: HTTP 404")), current: "0.5.5", arch: .arm64)
        #expect(failed == .checkFailed(UpdateError.noRelease("latest: HTTP 404").message))
        #expect(failed.title == "Could not check for updates" && failed.message.contains("404"))

        let same = UpdateDialog.checked(.success(r), current: "0.5.6", arch: .arm64)
        #expect(same.title == "ClaudeSwitch is up to date")
        #expect(same.message == "0.5.6 is the latest version.")
        let ahead = UpdateDialog.checked(.success(r), current: "0.6.0", arch: .arm64)
        #expect(ahead.message == "This is 0.6.0, newer than the latest release, 0.5.6.")

        let offer = UpdateDialog.checked(.success(r), current: "0.5.5", arch: .arm64)
        #expect(offer == .available(version: "0.5.6", current: "0.5.5",
                                    notes: "Fixture release for the app's update checker.", bytes: 45,
                                    page: URL(string: "https://github.com/bogdan-alexandrescu/claudeswitch/releases/tag/v0.5.6")))
        #expect(offer.title == "ClaudeSwitch 0.5.6 is available")
        #expect(offer.message.hasPrefix("You have 0.5.5."))
        #expect(offer.detail == "Downloads 45 B from GitHub")
    }

    @Test func testWhatsNewLeavesOutGitHubsChangelogLineAndTheBareTag() throws {
        var r = try fixtureRelease()
        r.notes = "**Full Changelog**: https://github.com/o/claudeswitch/compare/v0.5.5...v0.5.6"
        #expect(r.whatsNew == nil, "a body with only the changelog link has nothing to show")
        r.notes = "v0.5.6\n\n## Cleanup\n- one\n* two\n\n**Full Changelog**: https://github.com/o/claudeswitch/compare/v0.5.5...v0.5.6\n"
        #expect(r.whatsNew == "**Cleanup**\n• one\n• two")
        r.notes = "  \n"
        #expect(r.whatsNew == nil)
    }

    @Test func testTheProgressBarOnlyMovesForward() {
        let steps: [UpdateStep] = [.downloading(received: 0, total: 100), .downloading(received: 50, total: 100),
                                   .downloading(received: 100, total: 100), .verifying, .installingCLI,
                                   .installingApp, .restartingDaemon, .relaunching]
        let f = steps.map(\.fraction)
        #expect(f == f.sorted() && Set(f).count == f.count, "each step further along than the one before: \(f)")
        #expect(f.first == 0 && f.last == 1)
        #expect(UpdateStep.downloading(received: 1 << 20, total: 11 << 20).label == "Downloading… 1 MB of 11 MB")
        #expect(UpdateStep.downloading(received: 0, total: 0).fraction == 0)
        #expect(steps.filter(\.cancellable) == Array(steps.prefix(4)), "Cancel only before anything is replaced")
        let installing = UpdateDialog.installing(version: "0.5.6", step: .installingApp)
        #expect(installing.title == "Updating to 0.5.6" && installing.message == "Installing ClaudeSwitch…")
    }

    @Test func testTheGatePassesNewStepsAndVisibleProgressOnly() {
        let gate = UpdateProgressGate()
        #expect(gate.pass(.downloading(received: 0, total: 1000)))
        #expect(!gate.pass(.downloading(received: 3, total: 1000)), "0.3% is not worth a redraw")
        #expect(gate.pass(.downloading(received: 20, total: 1000)))
        #expect(gate.pass(.downloading(received: 1000, total: 1000)), "the end of a download always shows")
        #expect(gate.pass(.verifying))
        #expect(!gate.pass(.verifying))
        #expect(gate.pass(.installingCLI))
    }

    @Test func testTheOtherStatesSayWhatHappened() {
        let failed = UpdateDialog.failed(version: "0.5.6", message: UpdateError.install("x").message)
        #expect(failed.title == "The update did not install" && failed.message.contains("as they were"))
        let done = UpdateDialog.installed(version: "0.5.6")
        #expect(done.title == "ClaudeSwitch is now 0.5.6")
        #expect(done.message.contains("claudeswitch") && done.message.contains("daemon"))
        #expect(UpdateDialog.checking.title == "Checking for updates…")
        #expect(UpdateError.cancelled.message.contains("as they were"))
    }
}

extension UpdaterTests {
    @Test func testAFailedCheckSaysWhyOnce() throws {
        let (net, _, up) = try setUp()
        net.failing = ["latest"]
        #expect(up.latest().failure == .noRelease("offline"), "not \"latest: offline\"")
    }

    @Test func testAnInstallReportsEachStepInOrder() throws {
        let (_, _, up) = try setUp()
        var steps: [UpdateStep] = []
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app,
                           restartDaemon: { _ in .success(()) }, progress: { steps.append($0) })
        #expect(r.failure == nil)
        #expect(steps.first == .downloading(received: 0, total: 45))
        #expect(steps.contains(.downloading(received: 29, total: 45)), "the archive first")
        #expect(steps.contains(.downloading(received: 45, total: 45)), "then the app zip")
        let after = steps.filter { if case .downloading = $0 { return false } else { return true } }
        #expect(after == [.verifying, .installingCLI, .installingApp, .restartingDaemon])
        let f = steps.map(\.fraction)
        #expect(f == f.sorted(), "the bar never goes back")
    }

    @Test func testCancelWhileDownloadingChangesNothing() throws {
        let (_, sys, up) = try setUp()
        var cancelled = false
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app,
                           restartDaemon: { _ in
                               Issue.record("the daemon must not restart")
                               return .success(())
                           },
                           progress: { if case .downloading(let got, _) = $0, got > 0 { cancelled = true } },
                           isCancelled: { cancelled })
        #expect(r.failure == .cancelled)
        expectUntouched(sys)
    }

    @Test func testCancelIsTooLateOnceInstallingBegins() throws {
        let (_, sys, up) = try setUp()
        var cancelled = false
        let r = up.install(try fixtureRelease(), binary: Self.binary, app: Self.app,
                           restartDaemon: { _ in .success(()) },
                           progress: { if $0 == .installingCLI { cancelled = true } },
                           isCancelled: { cancelled })
        #expect(r.failure == nil, "half an update is worse than a whole one")
        #expect(sys.files[Self.binary] == FakeSystem.newBinary)
    }
}
