import Foundation

// Settings → Overview (owner decision 2026-10-26: status tiles and
// needs-you), the Settings window's first page: what needs the person, a
// tile per area with its live figure and the pane it opens, and the last
// few things that happened. Pure: the Bar passes what it has read.

/// Where a tile or a need leads.
public enum OverviewTarget: String, Equatable, Sendable {
    case daemon, profiles, accounts, rotation, cleanup, update
}

public struct OverviewClean: Equatable {
    public var on: Bool
    public var every: TimeInterval?
    public var lastAt: Date?
    public var lastBytes: Int64
    public var lastRemoved: Int

    public init(on: Bool, every: TimeInterval?, lastAt: Date?, lastBytes: Int64, lastRemoved: Int) {
        self.on = on
        self.every = every
        self.lastAt = lastAt
        self.lastBytes = lastBytes
        self.lastRemoved = lastRemoved
    }
}

public struct OverviewUpdate: Equatable {
    public var current: String?
    public var available: String?
    public var lastCheck: Date?
    public var enabled: Bool

    public init(current: String?, available: String?, lastCheck: Date?, enabled: Bool) {
        self.current = current
        self.available = available
        self.lastCheck = lastCheck
        self.enabled = enabled
    }
}

public struct OverviewSwitch: Equatable {
    public var at: Date
    public var profile: String?
    public var from: String?
    public var to: String?
    public var reason: String?

    public init(at: Date, profile: String?, from: String?, to: String?, reason: String?) {
        self.at = at
        self.profile = profile
        self.from = from
        self.to = to
        self.reason = reason
    }
}

/// What the page is built from.
public struct OverviewInput {
    public var daemon: DaemonView?
    public var cards: [ProfileCard]
    public var accounts: [AccountView]
    public var pools: Int?
    /// Claude in Chrome sign-in notices, as the popover words them.
    public var chrome: [String]
    public var clean: OverviewClean?
    public var update: OverviewUpdate
    public var switches: [OverviewSwitch]
    public var now: Date

    public init(daemon: DaemonView?, cards: [ProfileCard], accounts: [AccountView], pools: Int?, chrome: [String],
                clean: OverviewClean?, update: OverviewUpdate, switches: [OverviewSwitch], now: Date) {
        self.daemon = daemon
        self.cards = cards
        self.accounts = accounts
        self.pools = pools
        self.chrome = chrome
        self.clean = clean
        self.update = update
        self.switches = switches
        self.now = now
    }
}

public struct OverviewNeed: Equatable, Identifiable {
    public var id: String { text }
    public var text: String
    public var action: String
    public var target: OverviewTarget
}

public struct OverviewTile: Equatable, Identifiable {
    public var id: String { title }
    public var title: String
    public var symbol: String
    public var primary: String
    public var secondary: String
    /// Drawn in the warning colour: something here is wrong.
    public var attention: Bool
    public var target: OverviewTarget
}

public struct OverviewEvent: Equatable, Identifiable {
    public var id: String { when + text }
    public var at: Date
    public var when: String
    public var text: String
}

public struct Overview: Equatable {
    public var needs: [OverviewNeed]
    public var tiles: [OverviewTile]
    public var recent: [OverviewEvent]

    public static func build(_ i: OverviewInput) -> Overview {
        var needs: [OverviewNeed] = []
        switch i.daemon?.health {
        case .never?:
            needs.append(OverviewNeed(text: "The daemon is not installed: nothing switches by itself",
                                      action: "Open Daemon", target: .daemon))
        case .stale(let since)?:
            needs.append(OverviewNeed(text: "The daemon has not polled for " + Format.duration(i.now.timeIntervalSince(since)),
                                      action: "Open Daemon", target: .daemon))
        default:
            break
        }
        for a in i.accounts where a.status == .needsLogin {
            needs.append(OverviewNeed(text: "\(a.id) needs signing in again", action: "Sign in…", target: .accounts))
        }
        for c in i.chrome {
            needs.append(OverviewNeed(text: c, action: "Open Accounts", target: .accounts))
        }
        if i.update.enabled, let v = i.update.available {
            needs.append(OverviewNeed(text: "ClaudeSwitch \(v) is available", action: "Update…", target: .update))
        }
        return Overview(needs: needs, tiles: [daemon(i), profiles(i), accounts(i), rotation(i), cleanup(i), update(i)],
                        recent: recent(i))
    }

    static func daemon(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Daemon", symbol: "gearshape.2", primary: "Unknown", secondary: "",
                             attention: false, target: .daemon)
        guard let d = i.daemon else { return t }
        switch d.health {
        case .never:
            t.primary = "Not installed"
            t.secondary = "nothing switches by itself"
            t.attention = true
        case .stale(let since):
            t.primary = "Not polling"
            t.secondary = "for " + Format.duration(i.now.timeIntervalSince(since))
            t.attention = true
        case .polling:
            t.primary = d.live ? "Live" : "Dry run"
            t.secondary = "polled " + Format.age(d.lastPoll, now: i.now)
        }
        return t
    }

    static func usage(_ a: AccountView?) -> String {
        guard let a else { return "no account" }
        return "\(a.id) · \(Format.pct(a.fiveHour?.utilization)) · week \(Format.pct(a.sevenDay?.utilization))"
    }

    static func profiles(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Profiles", symbol: "person.2", primary: "No profile", secondary: "",
                             attention: false, target: .profiles)
        switch i.cards.count {
        case 0:
            break
        case 1:
            let c = i.cards[0]
            t.primary = c.name
            t.secondary = usage(c.active)
        default:
            t.primary = "\(i.cards.count) profiles"
            t.secondary = i.cards.map { "\($0.name): \($0.active?.id ?? "none")" }.joined(separator: " · ")
        }
        return t
    }

    static func accounts(_ i: OverviewInput) -> OverviewTile {
        let n = i.accounts.count
        var t = OverviewTile(title: "Accounts", symbol: "person.crop.circle",
                             primary: "\(n) account\(n == 1 ? "" : "s")", secondary: "", attention: false,
                             target: .accounts)
        let login = i.accounts.filter { $0.status == .needsLogin }.count
        let full = i.accounts.filter { $0.status == .noHeadroom }.count
        let refused = i.accounts.filter { if case .refused = $0.status { return true } else { return false } }.count
        var parts: [String] = []
        if login > 0 { parts.append("\(login) need\(login == 1 ? "s" : "") sign-in") }
        if full > 0 { parts.append("\(full) no headroom") }
        if refused > 0 { parts.append("\(refused) refused") }
        if parts.isEmpty && n > 0 { parts.append("all available") }
        if let p = i.pools, p > 0 { parts.append("\(p) pool\(p == 1 ? "" : "s")") }
        t.secondary = parts.joined(separator: " · ")
        t.attention = login > 0
        return t
    }

    static func rotation(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Rotation", symbol: "arrow.triangle.2.circlepath", primary: "Next: unknown",
                             secondary: "", attention: false, target: .rotation)
        guard let c = i.cards.first else { return t }
        t.primary = "Next: " + Format.next(c.decision)
        if let dry = c.poolDryAt, dry > i.now {
            t.secondary = "pool runs dry in " + Format.duration(dry.timeIntervalSince(i.now))
        } else {
            t.secondary = "switches at \(Int(c.switchAt))% · week \(Int(c.switchAtWeekly))%"
        }
        return t
    }

    static func cleanup(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Cleanup", symbol: "trash", primary: "Unknown",
                             secondary: "its settings are not read yet", attention: false, target: .cleanup)
        guard let c = i.clean else { return t }
        if !c.on {
            t.primary = "Off"
        } else if let every = c.every, every != 24 * 3600 {
            t.primary = "Every " + Format.duration(every)
        } else {
            t.primary = "Daily"
        }
        t.secondary = c.lastAt.map { "last run " + Format.age($0, now: i.now) + " · " + Format.bytes(c.lastBytes) }
            ?? "no run yet"
        return t
    }

    static func update(_ i: OverviewInput) -> OverviewTile {
        let u = i.update
        var t = OverviewTile(title: "Updates", symbol: "arrow.down.circle", primary: "Up to date", secondary: "",
                             attention: false, target: .update)
        if !u.enabled {
            t.primary = "Check is off"
        } else if let v = u.available {
            t.primary = "\(v) available"
            t.attention = true
        } else if u.lastCheck == nil {
            t.primary = "Not checked yet"
        }
        var s = u.current.map { "This is \($0)" } ?? "Version unknown"
        if u.enabled, u.lastCheck != nil { s += " · checked " + Format.age(u.lastCheck, now: i.now) }
        t.secondary = s
        return t
    }

    /// The last few switches and the last cleanup, newest first.
    static func recent(_ i: OverviewInput) -> [OverviewEvent] {
        var events: [OverviewEvent] = []
        for s in i.switches {
            var text = "\(s.from ?? "?") → \(s.to ?? "?")"
            if let p = s.profile { text = p + ": " + text }
            if let r = s.reason, !r.isEmpty { text += " (\(r))" }
            events.append(OverviewEvent(at: s.at, when: when(s.at, now: i.now), text: text))
        }
        if let c = i.clean, let at = c.lastAt {
            events.append(OverviewEvent(at: at, when: when(at, now: i.now),
                                        text: "cleanup moved \(Format.bytes(c.lastBytes)) (\(c.lastRemoved) items)"))
        }
        return Array(events.sorted { $0.at > $1.at }.prefix(4))
    }

    /// "16:53" today, "Oct 25" before.
    public static func when(_ at: Date, now: Date) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = Calendar.current.isDate(at, inSameDayAs: now) ? "HH:mm" : "MMM d"
        return f.string(from: at)
    }
}
