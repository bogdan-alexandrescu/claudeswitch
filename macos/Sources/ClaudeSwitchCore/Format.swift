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

    /// How long ago: "12s ago", "3m ago", "never".
    public static func age(_ at: Date?, now: Date) -> String {
        guard let at = at else { return "never" }
        let ago = now.timeIntervalSince(at)
        if ago < 5 { return "just now" }
        return duration(ago) + " ago"
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

    public static func status(_ s: AccountStatus, now: Date) -> String {
        switch s {
        case .refused(let w, let until):
            var t = "refused"
            if let w = w, !w.isEmpty { t += " · " + window(w) }
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
    /// A collapsed card's line: "work-1 · session 3% · week 41%"; trouble
    /// first, "needs login · work-1", since figures mean nothing then.
    static func cardSummary(_ c: ProfileCard) -> String {
        guard let a = c.active else { return "no account live" }
        switch a.status {
        case .needsLogin, .refused: return "\(a.status.label) · \(a.id)"
        default: return "\(a.id) · session \(pct(a.fiveHour?.utilization)) · week \(pct(a.sevenDay?.utilization))"
        }
    }
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
        let prefix = s.cards.count > 1 ? card.name + " · " : ""
        guard let a = card.active else {
            return MenuTitle(text: compact ? "" : prefix + "–", level: .ok, problem: false)
        }
        var level = a.level
        if a.status == .needsLogin { level = .over }
        let marker = level == .over ? "!" : ""
        if compact { return MenuTitle(text: marker, level: level, problem: false) }
        return MenuTitle(text: "\(prefix)\(a.id) \(Format.pct(a.bindingPct))\(marker)", level: level, problem: false)
    }
}
