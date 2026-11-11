import Foundation

// The drain forecast (docs/IMPROVEMENTS.md, "Drain forecast"): how many hours
// of use an account, or a profile's pool, has before its switch point at the
// speed the daemon measured. Read from `account list --json`'s `drain` and
// `why --json`'s `pool_drain`; an older CLI has neither and every line is
// hidden, and unknown is never said as 0.

/// The forecast's state, as the CLI words it; a word a newer CLI adds reads
/// as unknown.
public enum DrainState: String, Equatable {
    /// Hours of use left before the switch point.
    case hours
    /// Its week clears before it reaches the switch point.
    case lastsToReset = "lasts_to_reset"
    /// Not being spent (∞).
    case idle
    /// At or past its switch point.
    case drained
    case unknown

    init(_ j: JSON) { self = j.string.flatMap(DrainState.init(rawValue:)) ?? .unknown }
}

/// One account's forecast (`account list --json`'s `drain`).
public struct AccountDrain: Equatable {
    public var state: DrainState
    /// Hours of use before the switch point; nil unless hours or
    /// lasts to reset.
    public var hoursLeft: Double?
    /// Week points an hour of use; nil unless measured lately.
    public var speedPerHour: Double?
    /// It climbs with no profile here using it (the CLI's ↑).
    public var usedElsewhere: Bool

    public init(state: DrainState, hoursLeft: Double?, speedPerHour: Double?, usedElsewhere: Bool) {
        self.state = state
        self.hoursLeft = hoursLeft
        self.speedPerHour = speedPerHour
        self.usedElsewhere = usedElsewhere
    }

    /// Nil when the CLI did not say.
    init?(_ j: JSON) {
        guard j.raw is [String: Any] else { return nil }
        self.init(state: DrainState(j["state"]), hoursLeft: j["hours_left"].double,
                  speedPerHour: j["speed_per_hour"].double, usedElsewhere: j["used_elsewhere"].bool ?? false)
    }
}

/// A profile's pool forecast (`why --json`'s `pool_drain`): hours, idle,
/// drained or unknown.
public struct PoolDrain: Equatable {
    public var state: DrainState
    /// How many of its accounts have room.
    public var accounts: Int
    /// Nil unless hours.
    public var hoursLeft: Double?
    public var speedPerHour: Double?

    public init(state: DrainState, accounts: Int, hoursLeft: Double?, speedPerHour: Double?) {
        self.state = state
        self.accounts = accounts
        self.hoursLeft = hoursLeft
        self.speedPerHour = speedPerHour
    }

    /// Nil when the CLI did not say.
    init?(_ j: JSON) {
        guard j.raw is [String: Any] else { return nil }
        self.init(state: DrainState(j["state"]), accounts: j["accounts"].double.map { Int($0) } ?? 0,
                  hoursLeft: j["hours_left"].double, speedPerHour: j["speed_per_hour"].double)
    }
}

/// The forecast in the app's words, as the CLI says it (render.HoursOfUse,
/// LastsWord, PoolLine). Nil wherever the forecast is unknown.
public enum DrainText {
    /// Under this many hours of use a pool, or the live account in the menu
    /// bar, is low.
    public static let lowHours = 2.0

    /// "~21h"; under an hour and a half, minutes to the nearest five, "~45m"
    /// (never under 5).
    public static func hours(_ h: Double) -> String {
        let minutes = h * 60
        if minutes < 90 { return "~\(max(5, Int((minutes / 5).rounded()) * 5))m" }
        return "~\(Int(h.rounded()))h"
    }

    /// "2.6% an hour", "10% an hour": one decimal, dropped when it is 0.
    public static func speed(_ v: Double) -> String {
        let r = (v * 10).rounded() / 10
        let n = r == r.rounded() ? String(Int(r)) : String(format: "%.1f", r)
        return n + "% an hour"
    }

    /// The switch menu's and Settings → Accounts' word: "~21h left", "lasts
    /// to reset", "∞" or "drained", then " · used elsewhere" when someone
    /// elsewhere spends it.
    public static func short(_ d: AccountDrain?) -> String? {
        guard let d = d else { return nil }
        let word: String
        switch d.state {
        case .hours:
            guard let h = d.hoursLeft else { return nil }
            word = hours(h) + " left"
        case .lastsToReset: word = "lasts to reset"
        case .idle: word = "∞"
        case .drained: word = "drained"
        case .unknown: return nil
        }
        return d.usedElsewhere ? word + " · used elsewhere" : word
    }

    /// The line under the live account's dials: "~21h of use left at 2.6%
    /// an hour", "lasts until the week clears at …", "∞, not being spent" or
    /// "drained: at its switch point".
    public static func live(_ d: AccountDrain?) -> String? {
        guard let d = d else { return nil }
        let at = d.speedPerHour.map { " at " + speed($0) } ?? ""
        switch d.state {
        case .hours: return d.hoursLeft.map { hours($0) + " of use left" + at }
        case .lastsToReset: return "lasts until the week clears" + at
        case .idle: return "∞, not being spent"
        case .drained: return "drained: at its switch point"
        case .unknown: return nil
        }
    }

    /// The popover card's "work pool: ~83h of use left across 6 accounts".
    public static func poolLine(profile: String, _ p: PoolDrain?) -> String? {
        guard let p = p else { return nil }
        let head = profile + " pool: "
        let accounts = "\(p.accounts) account\(p.accounts == 1 ? "" : "s")"
        switch p.state {
        case .hours:
            guard let h = p.hoursLeft else { return nil }
            return head + hours(h) + " of use left " + (p.accounts == 1 ? "in " : "across ") + accounts
        case .idle: return head + "∞, not being spent (\(accounts) with room)"
        case .drained: return head + "drained, no account under its switch point"
        case .lastsToReset, .unknown: return nil
        }
    }

    /// Overview's Rotation tile: "~83h of use left", "∞, not being spent"
    /// or "drained".
    public static func poolShort(_ p: PoolDrain?) -> String? {
        guard let p = p else { return nil }
        switch p.state {
        case .hours: return p.hoursLeft.map { hours($0) + " of use left" }
        case .idle: return "∞, not being spent"
        case .drained: return "drained"
        case .lastsToReset, .unknown: return nil
        }
    }

    /// Under two hours of use, or drained: the pool line turns amber and
    /// Overview lists it as a need.
    public static func low(_ p: PoolDrain?) -> Bool {
        guard let p = p else { return false }
        if p.state == .drained { return true }
        guard p.state == .hours, let h = p.hoursLeft else { return false }
        return h < lowHours
    }

    /// The menu bar's "~45m": the live account's hours, only while under two.
    public static func menu(_ d: AccountDrain?) -> String? {
        guard let d = d, d.state == .hours, let h = d.hoursLeft, h < lowHours else { return nil }
        return hours(h)
    }
}
