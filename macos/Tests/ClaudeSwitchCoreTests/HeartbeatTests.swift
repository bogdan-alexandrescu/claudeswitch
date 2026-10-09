import Foundation
import Testing
@testable import ClaudeSwitchCore

/// 0.6.1 (decided 2026-10-08): while the app posts actionable rotation
/// notifications it records a heartbeat through the CLI (`app heartbeat
/// --json`, app_notifies_until = now + 2 minutes) every minute, and the
/// daemon skips its own rotation notice. The app never writes state.json.
@Suite final class HeartbeatTests {
    let dir: URL
    let cli: CLI

    init() throws {
        dir = try FakeBinary.directory()
        cli = CLI(path: try FakeAppCLI.make(in: dir))
    }
    deinit { try? FileManager.default.removeItem(at: dir) }

    @Test func testTheCommandAndItsAnswer() throws {
        try FakeAppCLI.reply(dir, text: #"{"app_notifies_until": "2026-10-08T12:02:00Z"}"#, exit: 0)
        let until = try cli.appHeartbeat().get()
        #expect(FakeAppCLI.calls(dir).last == ["app", "heartbeat", "--json"])
        #expect(until == ISO8601DateFormatter().date(from: "2026-10-08T12:02:00Z"))
    }

    @Test func testAnOlderBinaryIsTooOld() throws {
        try FakeAppCLI.reply(dir, text: "", stderr: "claudeswitch: unknown command \"app\"\n", exit: 2)
        guard case .failure(.cli(.tooOld)) = cli.appHeartbeat() else {
            Issue.record("expected tooOld")
            return
        }
    }

    @Test func testTheTimingIsAMinuteForATwoMinuteLease() {
        #expect(AppHeartbeat.interval == 60)
        #expect(AppHeartbeat.lease == 120)
        #expect(AppHeartbeat.interval < AppHeartbeat.lease, "a beat lands before the last one lapses")
    }

    /// The timer: one beat at start, one each time it fires, none while the
    /// app is not posting rotation notifications, none after stop.
    @Test func testTheTimerBeatsEveryMinuteWhileTheAppNotifies() {
        var beats = 0
        var notifying = true
        var scheduled: [TimeInterval] = []
        var fire: (() -> Void)?
        var cancelled = 0
        let hb = AppHeartbeat(shouldBeat: { notifying }, beat: { beats += 1 }, schedule: { every, tick in
            scheduled.append(every)
            fire = tick
            return { cancelled += 1 }
        })
        hb.start()
        #expect(beats == 1, "beats at once, so the daemon is told before the first rotation")
        #expect(scheduled == [60])
        fire?()
        fire?()
        #expect(beats == 3)
        notifying = false
        fire?()
        #expect(beats == 3, "notifications off: no beat, so the lease lapses and the daemon notifies")
        notifying = true
        hb.stop()
        #expect(cancelled == 1)
        hb.start()
        #expect(scheduled == [60, 60], "a restart schedules one timer")
        hb.start()
        #expect(cancelled == 2, "starting again replaces the timer rather than adding one")
    }
}
