import Foundation

// Settings → Overview (owner decision 2026-10-26: status tiles and
// needs-you), the Settings window's first page: what needs the person, a
// tile per area with its live figure and the pane it opens, and the last
// few things that happened. Pure: the Bar passes what it has read.

/// Where a tile or a need leads.
public enum OverviewTarget: String, Equatable, Sendable {
    case daemon, profiles, accounts, rotation, cleanup, update, health, advanced
    /// Starts the sign-in for the need's account, as the reminder does.
    case signIn
    /// Gives the need's account a Chrome profile of its own, as the popover's
    /// notice does.
    case chromeOwn
}

public struct OverviewClean: Equatable {
    public var on: Bool
    public var every: TimeInterval?
    public var lastAt: Date?
    public var lastBytes: Int64
    public var lastRemoved: Int
    /// What an Undo of that run put back.
    public var lastPutBack: Int = 0

    public init(on: Bool, every: TimeInterval?, lastAt: Date?, lastBytes: Int64, lastRemoved: Int, lastPutBack: Int = 0) {
        self.on = on
        self.every = every
        self.lastAt = lastAt
        self.lastBytes = lastBytes
        self.lastRemoved = lastRemoved
        self.lastPutBack = lastPutBack
    }
}

public struct OverviewUpdate: Equatable {
    public var current: String?
    public var available: String?
    /// The last good check.
    public var lastCheck: Date?
    public var enabled: Bool
    /// The last check, when it failed.
    public var failure: UpdateCheckFailure?

    public init(current: String?, available: String?, lastCheck: Date?, enabled: Bool,
                failure: UpdateCheckFailure? = nil) {
        self.current = current
        self.available = available
        self.lastCheck = lastCheck
        self.enabled = enabled
        self.failure = failure
    }
}

/// What the Updates tile and its need do.
public enum OverviewUpdateAction: Equatable, Sendable {
    /// Settings → Advanced, where the check is turned on.
    case advanced
    /// The update window on the offer.
    case offer
    /// Check for updates.
    case check
}

extension OverviewUpdate {
    /// With the check off, Advanced: off makes no call, so the tile does not
    /// start one (it opened a window that waited for an answer forever).
    public var action: OverviewUpdateAction {
        guard enabled else { return .advanced }
        return available != nil ? .offer : .check
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
    /// The notices themselves, so the need can act; `chrome` is their text
    /// alone, for a caller without them.
    public var chromeNotices: [ChromeSignInNotice] = []
    /// Health checks failing in the last `doctor` run, nil when not run, and
    /// when it ran (the need says its age once it is old).
    public var healthFailing: Int?
    public var healthAt: Date?
    /// Why the app cannot act: claudeswitch missing or too old.
    public var problem: String?
    /// `daemon status`'s answer once read: it knows the mode and whether the
    /// service is installed before state.json says so.
    public var daemonMode: String?
    public var daemonInstalled: Bool?
    /// The settings read failed, so the Cleanup tile cannot say.
    public var settingsUnreadable = false

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
    /// The account a `.signIn` or `.chromeOwn` need acts on.
    public var account: String?
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
    public var id: String { "\(at.timeIntervalSince1970) " + text }
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
        if let p = i.problem {
            needs.append(OverviewNeed(text: p, action: "Open Advanced", target: .advanced))
        }
        switch i.daemonInstalled == false ? .never : i.daemon?.health {
        case .never? where i.daemonInstalled == true:
            needs.append(OverviewNeed(text: "The daemon is installed but has not polled yet",
                                      action: "Open Daemon", target: .daemon))
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
            needs.append(OverviewNeed(text: "\(a.id) needs sign-in", action: "Sign In…", target: .signIn, account: a.id))
        }
        if i.chromeNotices.isEmpty {
            for c in i.chrome {
                needs.append(OverviewNeed(text: c, action: "Open Accounts", target: .accounts))
            }
        }
        for n in i.chromeNotices {
            needs.append(OverviewNeed(text: n.text, action: n.ownButton, target: .chromeOwn, account: n.account))
        }
        // A pool about to run dry is the person's to act on (add an account,
        // or plan around it).
        for c in i.cards where Runway.urgent(dryAt: c.poolDryAt, now: i.now) {
            if let dry = c.poolDryAt, dry > i.now {
                needs.append(OverviewNeed(text: "\(inQuotes(c.name))’s pool runs dry in "
                                            + Format.duration(dry.timeIntervalSince(i.now)),
                                          action: "Open Rotation", target: .rotation))
            }
        }
        if let f = i.healthFailing, f > 0 {
            var text = "\(f) health check\(f == 1 ? "" : "s") fail\(f == 1 ? "s" : "")"
            if let at = i.healthAt, i.now.timeIntervalSince(at) > 600 {
                text += " (checked " + Format.age(at, now: i.now) + ")"
            }
            needs.append(OverviewNeed(text: text, action: "Open Health", target: .health))
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
        if i.daemonInstalled == false {
            t.primary = "Not installed"
            t.secondary = "nothing switches by itself"
            t.attention = true
            return t
        }
        switch d.health {
        case .never where i.daemonInstalled == true:
            t.primary = "Not polling yet"
            t.secondary = "installed; no reading saved"
            t.attention = true
        case .never:
            t.primary = "Not installed"
            t.secondary = "nothing switches by itself"
            t.attention = true
        case .stale(let since):
            t.primary = "Not polling"
            t.secondary = "for " + Format.duration(i.now.timeIntervalSince(since))
            t.attention = true
        case .polling:
            t.primary = (i.daemonMode.map { $0 == "live" } ?? d.live) ? "Live" : "Dry run"
            t.secondary = "polled " + Format.age(d.lastPoll, now: i.now)
        }
        return t
    }

    static func usage(_ a: AccountView?) -> String {
        guard let a else { return "no account" }
        return "\(a.id) · session \(Format.pct(a.fiveHour?.utilization)) · week \(Format.pct(a.sevenDay?.utilization))"
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
        // A profile not signed in cannot open Claude Code: lead with it.
        let unsigned = i.cards.filter { $0.signedIn == "no" }.count
        if unsigned > 0 {
            t.secondary = "\(unsigned) need\(unsigned == 1 ? "s" : "") sign-in" + (t.secondary.isEmpty ? "" : " · " + t.secondary)
            t.attention = true
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
        let unknown = i.accounts.filter { $0.status == .unknown }.count
        let reset = i.accounts.filter { $0.status == .windowReset }.count
        var parts: [String] = []
        if login > 0 { parts.append("\(login) need\(login == 1 ? "s" : "") sign-in") }
        if full > 0 { parts.append("\(full) no headroom") }
        if refused > 0 { parts.append("\(refused) refused") }
        if unknown > 0 { parts.append("\(unknown) unknown") }
        if reset > 0 { parts.append("\(reset) read before a reset") }
        // Only when every account is: unknown is not fine.
        if parts.isEmpty && n > 0 { parts.append("all available") }
        if let p = i.pools, p > 0 { parts.append("\(p) pool\(p == 1 ? "" : "s")") }
        t.secondary = parts.joined(separator: " · ")
        t.attention = login > 0
        return t
    }

    static func rotation(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Rotation", symbol: "arrow.triangle.2.circlepath", primary: "Next: unknown",
                             secondary: "", attention: false, target: .rotation)
        // The profile that most needs a look speaks for the tile: a pool
        // running dry soon, then a pending switch or wait, then a pin; else
        // the first. With one, its figures are named as its own.
        let pressing = i.cards.first { Runway.urgent(dryAt: $0.poolDryAt, now: i.now) }
            ?? i.cards.first { $0.decision?.kind == "switch" || $0.decision?.kind == "wait" }
            ?? i.cards.first { $0.isPinned }
        guard let c = pressing ?? i.cards.first else { return t }
        t.attention = Runway.urgent(dryAt: c.poolDryAt, now: i.now)
        t.primary = "Next: " + Format.next(c.decision)
        // With several profiles the figures are one profile's: name it.
        let whose = i.cards.count > 1 ? c.name + ": " : ""
        if let dry = c.poolDryAt, dry > i.now {
            t.secondary = whose + "pool runs dry in " + Format.duration(dry.timeIntervalSince(i.now))
        } else {
            t.secondary = whose + "session \(Int(c.switchAt))% · week \(Int(c.switchAtWeekly))%"
        }
        return t
    }

    static func cleanup(_ i: OverviewInput) -> OverviewTile {
        var t = OverviewTile(title: "Cleanup", symbol: "trash", primary: "Unknown",
                             secondary: "its settings are not read yet", attention: false, target: .cleanup)
        guard let c = i.clean else {
            if i.settingsUnreadable { (t.primary, t.secondary) = ("Could not read", "its settings; open to try again") }
            return t
        }
        if !c.on {
            t.primary = "Schedule off"
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
        var version: String? = u.current.map { "This is \($0)" } ?? "Version unknown"
        var when = u.lastCheck.map { "checked " + Format.age($0, now: i.now) }
        if !u.enabled {
            t.primary = "Check is off"
            when = nil
        } else if let v = u.available {
            t.primary = "\(v) available"
            t.attention = true
        } else if let f = u.failure {
            // Not "Up to date": the last check did not read the release.
            t.primary = "Last check failed"
            when = "failed " + Format.age(f.at, now: i.now)
        } else if u.lastCheck == nil {
            t.primary = "Not checked yet"
        } else if u.current.flatMap(SemVer.init) == nil {
            // Nor is a copy with nothing to compare the release with (swift run).
            t.primary = "Version unknown"
            version = u.current.map { "This is \($0)" }
        }
        t.secondary = [version, when].compactMap { $0 }.joined(separator: " · ")
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
                                        text: "cleanup removed \(c.lastRemoved) item\(c.lastRemoved == 1 ? "" : "s") · "
                                            + Format.bytes(c.lastBytes)
                                            + (c.lastPutBack > 0 ? " · \(c.lastPutBack) put back" : "")))
        }
        return Array(events.sorted { $0.at > $1.at }.prefix(4))
    }

    /// The time today, the date before, in the person's own locale ("5:46 PM"
    /// or "17:46", "Oct 25" or "25 Oct"), as the panes write times.
    public static func when(_ at: Date, now: Date) -> String {
        let f = DateFormatter()
        f.setLocalizedDateFormatFromTemplate(Calendar.current.isDate(at, inSameDayAs: now) ? "jmm" : "MMMd")
        return f.string(from: at)
    }
}
