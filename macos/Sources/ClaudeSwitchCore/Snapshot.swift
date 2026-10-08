import Foundation

/// How close a figure is to the line it is held to.
public enum Level: Int, Comparable {
    case ok, near, over
    public static func < (a: Level, b: Level) -> Bool { a.rawValue < b.rawValue }

    /// "near" starts this many points below the trigger.
    public static let nearMargin: Double = 10

    public static func of(_ pct: Double, trigger: Double) -> Level {
        if pct >= trigger { return .over }
        if pct >= trigger - nearMargin { return .near }
        return .ok
    }
}

/// The one-phrase state of an account, in the order Go's render.stateOf
/// checks them: a dead credential first, because nothing else in the row can
/// be trusted then.
public enum AccountStatus: Equatable {
    case needsLogin
    case refused(window: String?, until: Date?)
    case windowReset
    case noHeadroom
    case available
    case unknown

    public var label: String {
        switch self {
        case .needsLogin: return "needs login"
        case .refused: return "refused"
        case .windowReset: return "window reset"
        case .noHeadroom: return "no headroom"
        case .available: return "available"
        case .unknown: return "unknown"
        }
    }
}

public struct AccountView: Equatable, Identifiable {
    public var id: String
    public var isActive: Bool
    public var isPinned: Bool
    public var status: AccountStatus
    public var fiveHour: WindowReading?
    public var sevenDay: WindowReading?
    /// The binding window and its utilization: the higher of the two.
    public var bindingWindow: String?
    public var bindingPct: Double?
    public var level: Level
    public var scopedWeekly: [LimitReading]
    public var eligible: Bool?
    public var why: String?
    public var lastAt: Date?
    public var lastError: String?
    public var refreshExpiry: Date?
}

/// Whether the daemon is doing its job, judged from state.json alone.
public enum DaemonHealth: Equatable {
    /// No daemon has ever started (state.json has no daemon_since).
    case never
    /// state.json has been saved recently enough.
    case polling
    /// Nothing has been saved since this moment, longer than the allowance.
    case stale(since: Date)
}

public struct DaemonView: Equatable {
    public var health: DaemonHealth
    public var live: Bool
    public var since: Date?
    /// The newest reading of any account.
    public var lastPoll: Date?
    public var savedAt: Date?
    public var now: Date

    /// The daemon saves state.json on a two-minute ticker whatever else it is
    /// doing (cmd/claudeswitch runDaemon), so a save older than three of those
    /// (or three active polls, when that interval is longer) means it has
    /// stopped. The lock file is deliberately not probed: the daemon takes it
    /// once at startup without retrying, so a probe that overlapped startup
    /// would make it exit.
    public static let saveTick: TimeInterval = 120

    public static func staleAfter(pollActive: TimeInterval?) -> TimeInterval {
        3 * max(saveTick, pollActive ?? 0)
    }

    public static func health(daemonSince: Date?, savedAt: Date?, lastPoll: Date?,
                              pollActive: TimeInterval?, now: Date) -> DaemonHealth {
        guard let started = daemonSince else { return .never }
        // A CLI command can save too, so a fresh save is evidence the daemon
        // may be fine, never proof; a stale one is proof that it is not.
        let last = [savedAt, lastPoll].compactMap { $0 }.max() ?? started
        return now.timeIntervalSince(last) > staleAfter(pollActive: pollActive) ? .stale(since: last) : .polling
    }

    public var mode: String {
        switch health {
        case .never: return "no daemon"
        case .polling: return live ? "live" : "dry-run"
        case .stale(let since): return "not polling for " + Format.duration(now.timeIntervalSince(since))
        }
    }
}

public struct ProfileView: Equatable, Identifiable {
    public var id: String { name }
    public var name: String
    public var active: String?
    public var pinned: String?
    public var decision: Decision?
}

/// Everything the menu shows, built from state.json, `why --json` and
/// `config --json`. Pure: the clock is passed in.
public struct Snapshot: Equatable {
    public var accounts: [AccountView]
    public var activeID: String?
    public var decision: Decision?
    public var daemon: DaemonView
    /// Every profile; the menu shows a section for them only when there is
    /// more than one.
    public var profiles: [ProfileView]
    public var settings: ConfigSettings
    public var now: Date

    public var active: AccountView? { accounts.first { $0.id == activeID } }

    public static func build(state: StateFile?, why: WhyReport?, settings: ConfigSettings?,
                             now: Date) -> Snapshot {
        let cfg = settings ?? ConfigSettings()
        let primary = why?.primary
        var switchCfg = cfg
        // A profile's effective thresholds win where the binary reports them.
        if let s = primary?.switchAt { switchCfg.switchAt = s }
        if let s = primary?.switchAtWeekly { switchCfg.switchAtWeekly = s }

        let def = state?.defaultProfile
        let verdicts = primary?.accounts ?? []
        let activeID = def?.active ?? verdicts.first { $0.active }?.id

        var order = verdicts.map(\.id)
        let rest = (state?.accounts.keys.sorted() ?? [])
            .filter { $0 != StateFile.unattributed && !order.contains($0) }
        order += rest

        let views = order.map { id -> AccountView in
            let rec = state?.accounts[id]
            let v = verdicts.first { $0.id == id }
            return account(id: id, record: rec, verdict: v, activeID: activeID,
                           pinned: def?.pinned, cfg: switchCfg, now: now)
        }

        let lastPoll = state?.accounts.values.compactMap(\.lastAt).max()
        let health = DaemonView.health(daemonSince: state?.daemonSince, savedAt: state?.savedAt,
                                       lastPoll: lastPoll, pollActive: cfg.pollActive, now: now)
        let daemon = DaemonView(health: health, live: state?.daemonLive ?? false, since: state?.daemonSince,
                                lastPoll: lastPoll, savedAt: state?.savedAt, now: now)

        let profiles = (state?.profiles ?? []).map { rec in
            ProfileView(name: rec.name, active: rec.active, pinned: rec.pinned,
                         decision: why?.profiles.first { $0.profile == rec.name }?.decision)
        }
        return Snapshot(accounts: views, activeID: activeID, decision: primary?.decision,
                        daemon: daemon, profiles: profiles, settings: switchCfg, now: now)
    }

    static func account(id: String, record: AccountRecord?, verdict: Verdict?, activeID: String?,
                        pinned: String?, cfg: ConfigSettings, now: Date) -> AccountView {
        let last = record?.last
        let worst = last?.worst
        let window = worst?.window ?? verdict?.window
        let pct = worst?.pct ?? verdict?.utilization
        let trigger = cfg.trigger(for: window ?? "five_hour")
        let status = statusOf(record: record, trigger: trigger, now: now)
        var level = pct.map { Level.of($0, trigger: trigger) } ?? .ok
        if case .refused = status { level = .over }
        return AccountView(
            id: id, isActive: id == activeID, isPinned: id == pinned, status: status,
            fiveHour: last?.fiveHour, sevenDay: last?.sevenDay,
            bindingWindow: window, bindingPct: pct, level: level,
            scopedWeekly: last?.scopedWeekly ?? [],
            eligible: verdict?.eligible, why: verdict?.why,
            lastAt: record?.lastAt, lastError: record?.lastError, refreshExpiry: record?.refreshExpiry)
    }

    static func statusOf(record: AccountRecord?, trigger: Double, now: Date) -> AccountStatus {
        guard let r = record else { return .unknown }
        if let e = r.lastError, needsLogin(e) { return .needsLogin }
        if let until = r.burntUntil, now < until { return .refused(window: r.burntWindow, until: until) }
        guard let last = r.last, let worst = last.worst else { return .unknown }
        let w = worst.window == "seven_day" ? last.sevenDay : last.fiveHour
        if let reset = w.resetsAt, now > reset, let at = r.lastAt, at < reset { return .windowReset }
        if worst.pct >= trigger { return .noHeadroom }
        return .available
    }

    /// Go's render.loginFix: errors only an interactive sign-in cures.
    public static func needsLogin(_ lastError: String) -> Bool {
        ["no stored credential", "not in the vault", "401", "re-login", "invalid_grant",
         "needs an interactive login"].contains { lastError.contains($0) }
    }
}
