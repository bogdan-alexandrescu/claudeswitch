import Foundation
import Testing
@testable import ClaudeSwitchCore

/// QA 2026-10-28, second pass: step 4 was done only once `daemon status`
/// said installed, so while it had failed or not answered the step stayed
/// open on Install (dry run). `daemon install --dry-run` over an installed
/// live service rewrites it as dry run. Install is offered only when the
/// CLI said the service is not installed.
@Suite struct SetupDaemonTests {
    func status(installed: Bool) -> DaemonStatus {
        let j = #"{"installed": \#(installed), "loaded": \#(installed), "running": \#(installed), "mode": "live"}"#
        return DaemonStatus(JSON(data: Data(j.utf8))!)!
    }

    @Test func testTheServiceIsInstalledOnlyAsTheCLISays() {
        #expect(SetupFacts.daemonInstalled(status: nil, missing: true) == false, "the CLI said not installed")
        #expect(SetupFacts.daemonInstalled(status: nil, missing: false) == nil, "not read yet, or the read failed")
        #expect(SetupFacts.daemonInstalled(status: status(installed: true), missing: false) == true)
        #expect(SetupFacts.daemonInstalled(status: status(installed: false), missing: false) == false)
        #expect(SetupFacts().daemonInstalled == nil, "unknown until read")
    }

    @Test func testAnUnreadServiceKeepsTheStepOpenWithoutInstall() {
        var f = SetupFlow(facts: SetupFacts(accounts: 1, profiles: 1))
        f.complete(.addAccounts)
        #expect(f.current == .daemon)
        #expect(f.daemonOffer() == .reading, "no answer yet: no Install")
        let e = CallError.cli(.failed("daemon status timed out"))
        #expect(f.daemonOffer(readError: e) == .readFailed(e), "the read failed: no Install")
        f.facts.daemonInstalled = false
        #expect(f.daemonOffer(readError: e) == .install, "only once the CLI said not installed")
        f.facts.daemonInstalled = true
        #expect(f.status(.daemon) == .done)
        #expect(f.daemonOffer() == .settings, "never Install over a service that is there")
    }

    @Test func testTheReadingLines() {
        #expect(FirstRun.daemonReading == "Reading the service…")
        #expect(FirstRun.tryAgain == "Try Again")
        #expect(FirstRun.daemonSettings == "Open Settings → Daemon")
    }
}
