import Foundation

/// Text formatting, kept apart from the views so it can be tested.
public enum Format {
    public static func pct(_ v: Double?) -> String {
        guard let v = v, v.isFinite else { return "–" }
        if v > 0 && v < 1 { return "<1%" }
        return String(format: "%.0f%%", v)
    }

    /// "5h" / "7d" for Go's window keys.
    public static func window(_ key: String?) -> String {
        switch key {
        case "five_hour": return "5h"
        case "seven_day": return "7d"
        case let k?: return k
        case nil: return ""
        }
    }

    /// A duration as its two largest units: "3d 4h", "2h 14m", "45m", "30s".
    /// A size as the CLI prints it (internal/clean's human): "1.4 GB",
    /// "512 MB", "12 KB".
    public static func bytes(_ n: Int64) -> String {
        let gb: Int64 = 1 << 30, mb: Int64 = 1 << 20, kb: Int64 = 1 << 10
        switch n {
        case gb...: return String(format: "%.1f GB", Double(n) / Double(gb))
        case mb...: return String(format: "%.0f MB", Double(n) / Double(mb))
        case kb...: return String(format: "%.0f KB", Double(n) / Double(kb))
        default: return "\(n) B"
        }
    }

    public static func duration(_ seconds: TimeInterval) -> String {
        let s = max(0, Int(seconds.rounded()))
        let d = s / 86_400, h = (s % 86_400) / 3600, m = (s % 3600) / 60
        if d > 0 { return h > 0 ? "\(d)d \(h)h" : "\(d)d" }
        if h > 0 { return m > 0 ? "\(h)h \(m)m" : "\(h)h" }
        if m > 0 { return "\(m)m" }
        return "\(s)s"
    }

    /// Time until a reset: "resets in 2h 14m", or "reset" once it has passed.
    public static func resets(_ at: Date?, now: Date) -> String {
        guard let at = at else { return "" }
        let left = at.timeIntervalSince(now)
        return left <= 0 ? "reset" : "resets in " + duration(left)
    }

    /// How long something has been running: "2h 14m".
    public static func uptime(since: Date, now: Date) -> String {
        duration(now.timeIntervalSince(since))
    }

    /// An account's sign-in, judged at now: when its login renews or expired,
    /// or, without a refresh date, its token's. Nil once it needs signing in
    /// (its state says so): a renewal date would contradict that.
    public static func login(refresh: Date?, token: Date?, now: Date, needsSignIn: Bool = false) -> String? {
        guard !needsSignIn else { return nil }
        func at(_ d: Date) -> String { d.formatted(date: .abbreviated, time: .shortened) }
        if let r = refresh { return (r > now ? "login renews until " : "login expired ") + at(r) }
        if let t = token { return "token " + (t > now ? "valid until " : "expired ") + at(t) }
        return nil
    }

    /// How long ago, in its largest unit: "12s ago", "3m ago", "18h ago",
    /// "2d ago", "never". A countdown keeps two units (resets); an age
    /// needs one.
    public static func age(_ at: Date?, now: Date) -> String {
        guard let at = at else { return "never" }
        let ago = now.timeIntervalSince(at)
        if ago < 5 { return "just now" }
        let s = Int(ago)
        let one = s >= 86_400 ? "\(s / 86_400)d" : s >= 3600 ? "\(s / 3600)h" : s >= 60 ? "\(s / 60)m" : "\(s)s"
        return one + " ago"
    }

    /// The decision as one line: "stay", "switch → work-2", "wait".
    public static func next(_ d: Decision?) -> String {
        guard let d = d else { return "unknown" }
        switch d.kind {
        case "switch": return "switch → " + (d.target ?? "?")
        case "stay", "wait": return d.kind
        default: return d.kind
        }
    }

    /// A window in the app's words: "session" (five_hour) or "week"
    /// (seven_day); nil for no window, the key itself for any other.
    public static func windowName(_ key: String?) -> String? {
        switch key {
        case "five_hour": return "session"
        case "seven_day": return "week"
        case let k? where !k.isEmpty: return k
        default: return nil
        }
    }

    /// "refused · session · clears in 36m", "needs sign-in", "available".
    public static func status(_ s: AccountStatus, now: Date) -> String {
        switch s {
        case .refused(let w, let until):
            var t = "refused"
            if let w = windowName(w) { t += " · " + w }
            if let u = until, u > now { t += " · clears in " + duration(u.timeIntervalSince(now)) }
            return t
        default:
            return s.label
        }
    }

    /// The weekly pace (Go: render.PaceText): "20% used · 57% of the week gone".
    public static func pace(_ p: WeeklyPace) -> String {
        String(format: "%.0f%% used · %.0f%% of the week gone", p.actual, p.expected)
    }

    /// The expiry estimate (Go: render.ExpiryText), or nil when it is too
    /// early in the week to make one.
    public static func paceExpiry(_ p: WeeklyPace) -> String? {
        guard let unused = p.unusedAtReset else { return nil }
        if unused >= 0.5 { return String(format: "~%.0f%% would expire unused at reset", unused) }
        return "on pace to use it all"
    }

    /// A per-model or otherwise scoped limit's label.
    public static func limitName(_ l: LimitReading) -> String {
        if let m = l.model, !m.isEmpty { return m + " weekly" }
        switch l.kind {
        case "weekly_scoped": return "scoped weekly"
        default: return l.kind.replacingOccurrences(of: "_", with: " ")
        }
    }
}

public extension Format {
    /// A card's account picker line: "work-1  41% week · available ·
    /// trigger Thu 09:30" (F2: the ETA only when the CLI knows it), "w03
    /// 100% · refused · session · clears in 36m", or "team-2  12% session ·
    /// in use by work" for one live in another profile (D34). The figure
    /// names its window, since one line's may be a session and the next's a
    /// week; a refusal names its own, once.
    static func pickerLine(_ a: AccountView, inUseBy: String?, now: Date) -> String {
        // The live account's check is the menu's own (its gutter), so the
        // ids line up.
        var refusalNamesWindow = false
        if case .refused(let w, _) = a.status, windowName(w) != nil { refusalNamesWindow = true }
        var s: String = a.id + "  " + pct(a.bindingPct)
        if a.bindingPct != nil, !refusalNamesWindow, let w = windowName(a.bindingWindow) { s += " " + w }
        if let p = inUseBy { return s + " · in use by " + p }
        s += " · " + status(a.status, now: now)
        if let eta = Runway.accountETA(a.triggerAt, now: now) { s += " · " + eta }
        return s
    }

    /// A collapsed card's line: "work-1 · session 3% · week 41%"; trouble
    /// first, "needs sign-in · work-1", since figures mean nothing then.
    static func cardSummary(_ c: ProfileCard) -> String {
        let p = cardSummaryParts(c)
        return p.figures.map { p.lead + " · " + $0 } ?? p.lead
    }

    /// The line in two parts, so the view truncates the lead (a long id)
    /// and never the figures. A pending switch or wait leads: "w01 → w02",
    /// "waiting · w01".
    static func cardSummaryParts(_ c: ProfileCard) -> CardSummary {
        guard let a = c.active else { return CardSummary(lead: "no account live", figures: nil) }
        switch a.status {
        case .needsLogin, .refused: return CardSummary(lead: "\(a.status.label) · \(a.id)", figures: nil)
        default: break
        }
        var lead = a.id
        switch c.decision?.kind {
        case "switch": if let t = c.decision?.target { lead = "\(a.id) → \(t)" }
        case "wait": lead = "waiting · \(a.id)"
        default: break
        }
        return CardSummary(lead: lead, figures: "session \(pct(a.fiveHour?.utilization)) · week \(pct(a.sevenDay?.utilization))")
    }

    /// s cut in the middle past n characters: "client-e…gration".
    static func middle(_ s: String, _ n: Int) -> String {
        guard s.count > n, n > 1 else { return s }
        let head = (n - 1) / 2 + (n - 1) % 2, tail = (n - 1) / 2
        return String(s.prefix(head)) + "…" + String(s.suffix(tail))
    }
}

/// A collapsed card's line in parts: the lead may truncate, the figures not.
public struct CardSummary: Equatable {
    public var lead: String
    public var figures: String?
}

/// What the menu bar shows.
public struct MenuTitle: Equatable {
    public var text: String
    public var level: Level
    /// True when there is something to fix rather than a figure to read.
    public var problem: Bool
    /// True before the first data load: neither a figure nor a problem.
    public var loading: Bool

    public init(text: String, level: Level, problem: Bool, loading: Bool = false) {
        self.text = text
        self.level = level
        self.problem = problem
        self.loading = loading
    }

    /// The label while `--render` draws the screens to files. SwiftUI puts
    /// the menu bar item up for those seconds whatever the app does, beside
    /// the real app's; "cs …" read as a second ClaudeSwitch that never loads
    /// (2026-10-21), so it says what it is.
    public static let rendering = MenuTitle(text: "cs render", level: .ok, problem: false)

    /// The label, told what went wrong reaching the binary. Only a binary
    /// that is missing or too old is a problem (the question mark); with no
    /// data yet and nothing wrong it is loading, which shows the ordinary
    /// gauge — before the first load is not an error. Data, when there is
    /// any, wins over a later failure.
    public static func of(_ s: Snapshot?, compact: Bool, profile: String?, problem: CLIError?) -> MenuTitle {
        if let s = s { return of(s, compact: compact, profile: profile) }
        switch problem {
        case .missing?, .tooOld?, .unsupportedContract?:
            return MenuTitle(text: compact ? "" : "cs", level: .ok, problem: true)
        case nil:
            return MenuTitle(text: compact ? "" : "cs …", level: .ok, problem: false, loading: true)
        default:
            return MenuTitle(text: compact ? "" : "cs", level: .ok, problem: false)
        }
    }

    public static func of(_ s: Snapshot?, compact: Bool) -> MenuTitle {
        guard let s = s else { return MenuTitle(text: compact ? "" : "cs", level: .ok, problem: true) }
        guard let a = s.active else {
            return MenuTitle(text: compact ? "" : "cs –", level: .ok, problem: false)
        }
        var level = a.level
        if a.status == .needsLogin { level = .over }
        let marker = level == .over ? "!" : ""
        if compact { return MenuTitle(text: marker, level: level, problem: false) }
        return MenuTitle(text: "\(a.id) \(Format.pct(a.bindingPct))\(marker)", level: level, problem: false)
    }

    /// The title following one profile (IMPROVEMENTS M7): its live account,
    /// prefixed with the profile's name when there is more than one.
    public static func of(_ s: Snapshot?, compact: Bool, profile: String?) -> MenuTitle {
        guard let s = s else { return MenuTitle(text: compact ? "" : "cs", level: .ok, problem: true) }
        guard let card = s.card(s.followed(profile)) else { return of(s, compact: compact) }
        // Names cut in the middle past 16, so the figure and its "!" show
        // and a long title does not hide the item behind the notch.
        let prefix = s.cards.count > 1 ? Format.middle(card.name, 16) + " · " : ""
        guard let a = card.active else {
            // Said, not drawn as a dash: "scratch · no account".
            return MenuTitle(text: compact ? "" : prefix + "no account", level: .ok, problem: false)
        }
        var level = a.level
        if a.status == .needsLogin { level = .over }
        // A stopped daemon is trouble too: the figure is old and nothing
        // rotates.
        if case .stale = s.daemon.health { level = .over }
        let marker = level == .over ? "!" : ""
        if compact { return MenuTitle(text: marker, level: level, problem: false) }
        return MenuTitle(text: "\(prefix)\(Format.middle(a.id, 16)) \(Format.pct(a.bindingPct))\(marker)",
                         level: level, problem: false)
    }
}
