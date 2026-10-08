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
    /// The weekly pace, from `why --json` (IMPROVEMENTS I8).
    public var pace: WeeklyPace?
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

/// One profile's card in the popover (IMPROVEMENTS M7): its live account,
/// its pool for the account picker, measured against its own thresholds.
public struct ProfileCard: Equatable, Identifiable {
    public var id: String { name }
    public var name: String
    /// As written in the config; nil is Claude Code's default directory.
    public var dir: String?
    /// yes, no or unknown, from `profile list`; nil when it was not asked.
    public var signedIn: String?
    public var active: AccountView?
    public var pinned: String?
    public var decision: Decision?
    public var switchAt: Double
    public var switchAtWeekly: Double
    public var pool: [String]
    /// The pool's accounts, in the order rotation considers them.
    public var accounts: [AccountView]
    /// Where "Switch to best" goes (why's `best`), and why nowhere.
    public var best: AccountView?
    public var bestWhy: String?
    /// M12: why's `on_best`, the policy's "already on the best"; nil from an
    /// older binary, or before `why` has loaded.
    public var onBest: Bool?
    /// The account `why` judged live; when it is not `active` (state moved
    /// on since), why's decision is about another account.
    public var whyActive: String?

    public init(name: String, dir: String?, signedIn: String?, active: AccountView?, pinned: String?,
                decision: Decision?, switchAt: Double, switchAtWeekly: Double, pool: [String],
                accounts: [AccountView]) {
        self.name = name
        self.dir = dir
        self.signedIn = signedIn
        self.active = active
        self.pinned = pinned
        self.decision = decision
        self.switchAt = switchAt
        self.switchAtWeekly = switchAtWeekly
        self.pool = pool
        self.accounts = accounts
    }

    public var isPinned: Bool { pinned != nil }
    public var dirLabel: String { dir ?? "~/.claude" }

    public func trigger(for window: String) -> Double {
        window == "seven_day" ? switchAtWeekly : switchAt
    }

    /// "Switch to best: w02 12%", or the bare verb when there is none.
    public var bestLabel: String {
        guard let b = best else { return "Switch to best" }
        return "Switch to best: \(b.id) \(Format.pct(b.bindingPct))"
    }

    /// M12: the best other account has no more room than the active one.
    /// The button is then off. Room is the policy's (owner, 2026-10-08):
    /// points short of each window's own trigger, which the CLI reports as
    /// `on_best`, so the button and the CLI never disagree. Without it (an
    /// older binary, or not yet loaded) the card falls back to the higher of
    /// session and week; an unknown reading on either side claims nothing.
    public var alreadyOnBest: Bool {
        guard best != nil else { return false }
        if let on = onBest { return on }
        guard let b = best?.bindingPct, let a = active?.bindingPct, a.isFinite, b.isFinite else { return false }
        return b >= a
    }

    /// The button's first line.
    public var bestTitle: String { alreadyOnBest ? "Already on the best" : "Switch to best" }

    /// The button's second line: the best account and its utilization,
    /// "next: …" when the card is already on the best (M12).
    public var bestSubtitle: String? {
        guard let b = best else { return nil }
        let line = "\(b.id) · \(Format.pct(b.bindingPct))"
        return alreadyOnBest ? "next: " + line : line
    }

    /// Why the button is off; nil when there is a best account.
    public var bestUnavailable: String? {
        guard best == nil else { return nil }
        return bestWhy ?? "No account to switch to"
    }

    /// Owner decision: with nothing clear of the landing margin, why still
    /// names the best account and says how little room it has; the card
    /// shows that beside the button.
    public var bestWarning: String? { best == nil ? nil : bestWhy }

    /// Whether "Next:" is about the account shown: hidden when why judged
    /// another account live than the one state records.
    public var nextVisible: Bool {
        guard let w = whyActive, let a = active?.id else { return true }
        return w == a
    }

    /// Claude Code opens signed in only where the profile has a login.
    public var canOpen: Bool { signedIn != "no" }

    public var openBlocked: String? {
        canOpen ? nil : "\(name) is not signed in. Sign it in from Settings → Profiles."
    }

    /// How close a per-model weekly limit is to the weekly trigger; nil
    /// when its figure is unknown.
    public func level(of l: LimitReading) -> Level? {
        l.percent.map { Level.of($0, trigger: switchAtWeekly) }
    }
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
    /// A card per profile, in `profile list`'s order when it was read.
    public var cards: [ProfileCard] = []

    public var active: AccountView? { accounts.first { $0.id == activeID } }

    public func card(_ name: String) -> ProfileCard? { cards.first { $0.name == name } }

    /// The profile the menu-bar title follows: the one chosen while it
    /// exists, else default, else the first.
    public func followed(_ chosen: String?) -> String {
        if let c = chosen, card(c) != nil { return c }
        if card(StateFile.defaultProfile) != nil || cards.isEmpty { return StateFile.defaultProfile }
        return cards[0].name
    }

    public static func build(state: StateFile?, why: WhyReport?, settings: ConfigSettings?,
                             profiles list: ProfileList? = nil, now: Date) -> Snapshot {
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
        let cards = Self.cards(state: state, why: why, cfg: cfg, list: list, order: order, now: now)
        return Snapshot(accounts: views, activeID: activeID, decision: primary?.decision,
                        daemon: daemon, profiles: profiles, settings: switchCfg, now: now, cards: cards)
    }

    static func cards(state: StateFile?, why: WhyReport?, cfg: ConfigSettings, list: ProfileList?,
                      order: [String], now: Date) -> [ProfileCard] {
        var names: [String] = []
        if let l = list {
            names = l.profiles.map(\.name)
        } else {
            for n in (state?.profiles.map(\.name) ?? []) + (why?.profiles.map(\.profile) ?? []) where !names.contains(n) {
                names.append(n)
            }
        }
        return names.map { name in
            let info = list?.profile(name)
            let pw = why?.profiles.first { $0.profile == name }
            let rec = state?.profiles.first { $0.name == name }
            var c = cfg
            c.switchAt = info?.thresholds.switchAt ?? pw?.switchAt ?? cfg.switchAt
            c.switchAtWeekly = info?.thresholds.switchAtWeekly ?? pw?.switchAtWeekly ?? cfg.switchAtWeekly
            let pool: [String]
            if let i = info {
                pool = i.pool
            } else if let p = pw?.pool, !p.isEmpty {
                pool = p
            } else {
                pool = name == StateFile.defaultProfile ? order : []
            }
            let verdictOrder = (pw?.accounts ?? []).map(\.id)
            let ordered = verdictOrder.filter(pool.contains) + pool.filter { !verdictOrder.contains($0) }
            let activeID = rec?.active ?? info?.live ?? pw?.accounts.first { $0.active }?.id
            let pinned = rec?.pinned ?? info?.pinned
            func view(_ id: String) -> AccountView {
                account(id: id, record: state?.accounts[id], verdict: pw?.accounts.first { $0.id == id },
                        activeID: activeID, pinned: pinned, cfg: c, now: now)
            }
            let accounts = ordered.map(view)
            let active = activeID.map { id in accounts.first { $0.id == id } ?? view(id) }
            var card = ProfileCard(name: name, dir: info?.dir, signedIn: info?.signedIn, active: active, pinned: pinned,
                                   decision: pw?.decision, switchAt: c.trigger(for: "five_hour"),
                                   switchAtWeekly: c.trigger(for: "seven_day"), pool: pool, accounts: accounts)
            card.best = pw?.best.flatMap { id in id == activeID ? nil : (accounts.first { $0.id == id } ?? view(id)) }
            card.bestWhy = pw?.bestWhy
            card.onBest = pw?.onBest
            card.whyActive = pw?.accounts.first { $0.active }?.id
            return card
        }
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
            scopedWeekly: last?.scopedWeekly ?? [], pace: verdict?.pace,
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
