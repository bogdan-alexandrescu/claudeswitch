import Foundation
import Testing
@testable import ClaudeSwitchCore

/// Renaming needs the daemon stopped (it owns the state file), so the app's
/// Rename stops a running daemon service, renames, and starts it again
/// (owner decision, 2026-10-21). Start loads the same service file, so live
/// stays live and dry run stays dry run.
@Suite struct RenameFlowTests {
    func status(installed: Bool = true, running: Bool) -> DaemonStatus {
        let j = #"{"installed": \#(installed), "loaded": \#(running), "running": \#(running), "mode": "dry-run"}"#
        return DaemonStatus(JSON(data: Data(j.utf8))!)!
    }

    let renamed = RenameResult(JSON(data: Data(#"{"account": "AI-Eng-1", "previous": "lab-1"}"#.utf8))!)!
    let denied = CallError.refused("service_failed", "launchctl unload failed")

    /// Runs the flow against stubs, recording which steps ran, in order.
    func run(status st: Result<DaemonStatus, CallError>,
             stop: Result<DaemonStatus, CallError>? = nil,
             rename: Result<RenameResult, CallError>? = nil,
             start: Result<DaemonStatus, CallError>? = nil) -> (Result<PausedRename, CallError>, [String]) {
        var steps: [String] = []
        let r = RenameFlow.run(
            status: { steps.append("status"); return st },
            stop: { steps.append("stop"); return stop ?? .success(status(running: false)) },
            rename: { steps.append("rename"); return rename ?? .success(renamed) },
            start: { steps.append("start"); return start ?? .success(status(running: true)) })
        return (r, steps)
    }

    @Test func testARunningDaemonIsStoppedForTheRenameAndStartedAgain() throws {
        let (r, steps) = run(status: .success(status(running: true)))
        #expect(steps == ["status", "stop", "rename", "start"])
        let p = try r.get()
        #expect(p.renamed.account == "AI-Eng-1" && p.paused && p.restartError == nil)
    }

    @Test func testADaemonNotRunningIsLeftAlone() throws {
        let (r, steps) = run(status: .success(status(running: false)))
        #expect(steps == ["status", "rename"])
        #expect(try !r.get().paused)
    }

    @Test func testWithNoServiceTheRenameRunsAsAsked() throws {
        let (r, steps) = run(status: .failure(.refused("not_installed", "the daemon service is not installed")))
        #expect(steps == ["status", "rename"])
        #expect(try r.get().renamed.account == "AI-Eng-1")
    }

    /// A stop that failed changed nothing, so nothing else runs.
    @Test func testAStopThatFailsRenamesNothing() {
        let (r, steps) = run(status: .success(status(running: true)), stop: .failure(denied))
        #expect(steps == ["status", "stop"])
        #expect(r == .failure(denied))
    }

    /// The daemon is started again whatever the rename did.
    @Test func testARefusedRenameStillStartsTheDaemonAgain() {
        let refused = CallError.refused("invalid_value", "\"x y\" is not a valid account id")
        let (r, steps) = run(status: .success(status(running: true)), rename: .failure(refused))
        #expect(steps == ["status", "stop", "rename", "start"])
        #expect(r == .failure(refused))
    }

    /// The account is renamed; the daemon being down is said, not hidden.
    @Test func testAStartThatFailsIsReportedWithTheRename() throws {
        let (r, steps) = run(status: .success(status(running: true)), start: .failure(denied))
        #expect(steps == ["status", "stop", "rename", "start"])
        let p = try r.get()
        #expect(p.renamed.account == "AI-Eng-1" && p.paused && p.restartError == denied)
    }

    /// Both failed: the rename's refusal is shown, and says the daemon is down.
    @Test func testARefusedRenameAndAFailedStartAreBothSaid() {
        let refused = CallError.refused("invalid_value", "not a valid account id")
        let (r, _) = run(status: .success(status(running: true)), rename: .failure(refused), start: .failure(denied))
        guard case .failure(let e) = r else { Issue.record("succeeded: \(r)"); return }
        #expect(e.code == "invalid_value")
        #expect(e.message.contains("not a valid account id") && e.message.contains("not started again"))
    }

    /// The service stopped and a daemon still runs: one started outside it
    /// (`cs watch` in a terminal). Nothing is renamed and the service is put back.
    @Test func testADaemonOutsideTheServiceIsNotRenamedUnder() {
        let (r, steps) = run(status: .success(status(running: true)), stop: .success(status(running: true)))
        #expect(steps == ["status", "stop", "start"])
        #expect(r.code == "daemon_running")
    }
}

private extension Result where Failure == CallError {
    var code: String? {
        if case .failure(let e) = self { return e.code }
        return nil
    }
}
