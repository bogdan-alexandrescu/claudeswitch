import Foundation

/// The app's heartbeat (0.6.1). While the app runs and posts actionable
/// rotation notifications, it runs `app heartbeat --json` every minute; the
/// CLI records app_notifies_until two minutes ahead in state.json, and the
/// daemon skips its own plain rotation notice while that is in the future.
/// When the app quits, or its notifications are off, the beats stop and the
/// daemon notifies again within two minutes. The app never writes
/// state.json itself.
public final class AppHeartbeat {
    /// How often the app beats.
    public static let interval: TimeInterval = 60
    /// How far ahead each beat sets app_notifies_until (the CLI's appLease).
    public static let lease: TimeInterval = 120

    /// Schedules `tick` every `every` seconds; returns what cancels it.
    public typealias Schedule = (_ every: TimeInterval, _ tick: @escaping () -> Void) -> (() -> Void)

    private let shouldBeat: () -> Bool
    private let beat: () -> Void
    private let schedule: Schedule
    private var cancel: (() -> Void)?

    /// shouldBeat: whether the app posts rotation notifications right now.
    /// beat: runs the command (off the main thread is the caller's to do).
    public init(shouldBeat: @escaping () -> Bool, beat: @escaping () -> Void,
                schedule: @escaping Schedule = AppHeartbeat.timer) {
        self.shouldBeat = shouldBeat
        self.beat = beat
        self.schedule = schedule
    }

    /// Beats now, then every interval. Starting again replaces the timer.
    public func start() {
        stop()
        tick()
        cancel = schedule(Self.interval) { [weak self] in self?.tick() }
    }

    public func stop() {
        cancel?()
        cancel = nil
    }

    private func tick() {
        if shouldBeat() { beat() }
    }

    /// A repeating main-run-loop Timer.
    public static func timer(_ every: TimeInterval, _ tick: @escaping () -> Void) -> (() -> Void) {
        let t = Timer(timeInterval: every, repeats: true) { _ in tick() }
        t.tolerance = 5
        RunLoop.main.add(t, forMode: .common)
        return { t.invalidate() }
    }
}
