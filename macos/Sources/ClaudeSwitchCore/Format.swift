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

    /// A per-model or otherwise scoped limit's label.
    public static func limitName(_ l: LimitReading) -> String {
        if let m = l.model, !m.isEmpty { return m + " weekly" }
        switch l.kind {
        case "weekly_scoped": return "scoped weekly"
        default: return l.kind.replacingOccurrences(of: "_", with: " ")
        }
    }
}

/// What the menu bar shows.
public struct MenuTitle: Equatable {
    public var text: String
    public var level: Level
    /// True when there is something to fix rather than a figure to read.
    public var problem: Bool

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
}
