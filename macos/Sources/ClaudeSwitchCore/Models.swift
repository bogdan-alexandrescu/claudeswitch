import Foundation

// The shapes below mirror the Go code, field for field, and name where each
// comes from. Every field is optional: see JSON for why.

/// One quota window (Go: usage.Window).
public struct WindowReading: Equatable {
    public var utilization: Double?
    public var resetsAt: Date?

    init(_ j: JSON) {
        utilization = j["utilization"].double
        resetsAt = j["resets_at"].date
    }

    public init(utilization: Double?, resetsAt: Date?) {
        self.utilization = utilization
        self.resetsAt = resetsAt
    }

    public var known: Bool { utilization != nil }
}

/// One entry of the API's limits[] array (Go: usage.Limit), stored in the
/// API's own shape: a per-model weekly limit is kind "weekly_scoped" with the
/// model's name in scope.model.display_name. A null percent is unknown.
public struct LimitReading: Equatable {
    public var kind: String
    public var group: String
    public var percent: Double?
    public var severity: String
    public var isActive: Bool
    public var resetsAt: Date?
    public var model: String?

    init(_ j: JSON) {
        kind = j["kind"].string ?? ""
        group = j["group"].string ?? ""
        percent = j["percent"].double
        severity = j["severity"].string ?? ""
        isActive = j["is_active"].bool ?? false
        resetsAt = j["resets_at"].date
        model = j["scope"]["model"]["display_name"].string
    }
}

/// A usage reading (Go: usage.Usage, as persisted in state.json).
public struct UsageReading: Equatable {
    public var fiveHour: WindowReading
    public var sevenDay: WindowReading
    public var limits: [LimitReading]
    public var extraUsageEnabled: Bool

    init(_ j: JSON) {
        fiveHour = WindowReading(j["five_hour"])
        sevenDay = WindowReading(j["seven_day"])
        limits = j["limits"].array.map(LimitReading.init)
        extraUsageEnabled = j["extra_usage"]["is_enabled"].bool ?? false
    }

    /// The higher of the two windows, as Go's Usage.Worst: ("five_hour" |
    /// "seven_day", percent), or nil when neither window is known.
    public var worst: (window: String, pct: Double)? {
        if !fiveHour.known && !sevenDay.known { return nil }
        let f = fiveHour.utilization ?? 0, s = sevenDay.utilization ?? 0
        return s > f ? ("seven_day", s) : ("five_hour", f)
    }

    /// Weekly limits narrower than the whole account — per model, today.
    public var scopedWeekly: [LimitReading] {
        limits.filter { $0.group == "weekly" && $0.kind != "weekly_all" }
    }
}

/// One account's durable record (Go: state.Account).
public struct AccountRecord: Equatable {
    public var id: String
    public var last: UsageReading?
    public var lastAt: Date?
    public var lastError: String?
    public var burntUntil: Date?
    public var burntWindow: String?
    public var refreshExpiry: Date?
    /// "account-uuid@org-uuid": who the stored credential signs in as.
    public var seat: String?
    public var orgID: String?
    /// When the access token runs out (renewed by refresh while renewable).
    public var tokenExpiry: Date?

    init(id: String, _ j: JSON) {
        seat = j["seat"].string.flatMap { $0.isEmpty ? nil : $0 }
        orgID = j["org_id"].string.flatMap { $0.isEmpty ? nil : $0 }
        tokenExpiry = j["token_expiry"].date
        self.id = j["id"].string ?? id
        last = j["last_usage"].isNull ? nil : UsageReading(j["last_usage"])
        lastAt = j["last_at"].date
        lastError = j["last_error"].string.flatMap { $0.isEmpty ? nil : $0 }
        burntUntil = j["burnt_until"].date
        burntWindow = j["burnt_window"].string
        refreshExpiry = j["refresh_expiry"].date
    }
}

/// One Claude Code profile's live-credential state (Go: state.ProfileState).
public struct ProfileRecord: Equatable {
    public var name: String
    public var active: String?
    public var pinned: String?
    public var lastSwitch: Date?

    init(name: String, _ j: JSON) {
        self.name = name
        active = j["active_account"].string.flatMap { $0.isEmpty ? nil : $0 }
        pinned = j["pinned"].string.flatMap { $0.isEmpty ? nil : $0 }
        lastSwitch = j["last_switch"].date
    }
}

/// ~/.local/state/claudeswitch/state.json (Go: state.State).
public struct StateFile: Equatable {
    /// The id Go files readings under when the live credential matches no
    /// configured account (state.Unattributed). It is not an account.
    public static let unattributed = "active"
    public static let defaultProfile = "default"

    public var accounts: [String: AccountRecord]
    /// Every profile, "default" first. A file written before profiles
    /// existed has only top-level fields, which are the default profile.
    public var profiles: [ProfileRecord]
    public var daemonLive: Bool
    public var daemonSince: Date?
    public var savedAt: Date?
    /// Each account's email, recorded by the CLI when it was vaulted or
    /// identified (lane 13), so it is known without the keychain.
    public var emails: [String: String]

    public init?(data: Data) {
        guard let j = JSON(data: data), j.raw is [String: Any] else { return nil }
        self.init(j)
    }

    init(_ j: JSON) {
        var accts: [String: AccountRecord] = [:]
        for (id, v) in j["accounts"].object { accts[id] = AccountRecord(id: id, v) }
        accounts = accts
        // Dev builds before D19 wrote the same map as "instances".
        let map = j["profiles"].isNull ? j["instances"] : j["profiles"]
        var list = map.keys.map { ProfileRecord(name: $0, map[$0]) }
        if !list.contains(where: { $0.name == Self.defaultProfile }) {
            list.append(ProfileRecord(name: Self.defaultProfile, j))
        }
        list.sort { a, b in
            a.name == Self.defaultProfile ? b.name != Self.defaultProfile
                : (b.name == Self.defaultProfile ? false : a.name < b.name)
        }
        profiles = list
        daemonLive = j["daemon_live"].bool ?? false
        daemonSince = j["daemon_since"].date
        savedAt = j["saved_at"].date
        emails = j["emails"].object.compactMapValues { $0.string }
    }

    public var defaultProfile: ProfileRecord {
        profiles.first { $0.name == Self.defaultProfile } ?? profiles[0]
    }
}

/// What rotation will do next (Go: decisionJSON).
public struct Decision: Equatable {
    public var kind: String          // "stay" | "switch" | "wait"
    public var reason: String
    public var target: String?
    public var forced: Bool
    public var recoversAccount: String?
    public var recoversAt: Date?

    init?(_ j: JSON) {
        guard let kind = j["kind"].string else { return nil }
        self.kind = kind
        reason = j["reason"].string ?? ""
        target = j["target"].string
        forced = j["forced"].bool ?? false
        recoversAccount = j["recovers_account"].string
        recoversAt = j["recovers_at"].date
    }
}

/// How an account's weekly window is being spent against the clock (Go:
/// state.Pace, as addWeeklyJSON writes it). Display only. atReset and
/// unusedAtReset are absent until the week is far enough along to estimate.
public struct WeeklyPace: Equatable {
    /// Share of the week elapsed, in percent.
    public var expected: Double
    /// Weekly utilization now.
    public var actual: Double
    public var atReset: Double?
    public var unusedAtReset: Double?
    public var resetsAt: Date?

    public init(expected: Double, actual: Double, atReset: Double?, unusedAtReset: Double?, resetsAt: Date?) {
        self.expected = expected
        self.actual = actual
        self.atReset = atReset
        self.unusedAtReset = unusedAtReset
        self.resetsAt = resetsAt
    }

    init?(_ j: JSON) {
        guard let e = j["expected"].double, let a = j["actual"].double else { return nil }
        self.init(expected: e, actual: a, atReset: j["at_reset"].double,
                  unusedAtReset: j["unused_at_reset"].double, resetsAt: j["resets_at"].date)
    }
}

/// The policy's verdict on one account (Go: verdictsJSON).
public struct Verdict: Equatable {
    public var id: String
    public var eligible: Bool
    public var why: String
    public var utilization: Double?
    public var window: String?
    public var active: Bool
    public var clearsAt: Date?
    public var pace: WeeklyPace?
    /// F2: when this account reaches its trigger at its recent pace; nil
    /// when unknown (too few readings, an older binary).
    public var triggerAt: Date?

    init(_ j: JSON) {
        id = j["id"].string ?? ""
        eligible = j["eligible"].bool ?? false
        why = j["why"].string ?? ""
        utilization = j["utilization"].double
        window = j["window"].string
        active = j["active"].bool ?? false
        clearsAt = j["clears_at"].date
        pace = WeeklyPace(j["weekly_pace"])
        triggerAt = j["trigger_at"].date
    }
}

/// One profile's block of `why --json`.
public struct ProfileWhy: Equatable {
    public var profile: String
    public var current: Bool
    public var decision: Decision?
    public var accounts: [Verdict]
    public var switchAt: Double?
    public var switchAtWeekly: Double?
    /// The profile's effective pool, when the binary reports it.
    public var pool: [String] = []
    /// Where "Switch to best" would go now (lane 16): the best eligible
    /// account other than the live one, by the policy's own ordering.
    public var best: String?
    /// Why there is none, when `best` is nil.
    public var bestWhy: String?
    /// M12: the CLI's "Already on the best", by the policy's room. Nil from
    /// a binary older than contract 2's on_best.
    public var onBest: Bool? = nil
    /// Points the live account and `best` sit below their binding window's
    /// trigger, as the policy ranks them; nil when unknown.
    public var activeRoom: Double? = nil
    public var bestRoom: Double? = nil
    /// F2: when no account of the pool is eligible any more at this pace;
    /// nil when unknown (or from an older binary).
    public var poolDryAt: Date? = nil
}

/// `claudeswitch why --json`. With one profile it is
/// {"decision", "accounts", "dir"}; with several (multi-profile work in
/// flight) {"profiles": [{"profile", "current", "decision", "accounts",
/// "thresholds", "pool"}], "dir"}. Both read into `profiles`.
public struct WhyReport: Equatable {
    public var profiles: [ProfileWhy]

    public init?(data: Data) {
        guard let j = JSON(data: data), j.raw is [String: Any] else { return nil }
        var list: [ProfileWhy] = []
        for b in j["profiles"].array {
            list.append(ProfileWhy(
                profile: b["profile"].string ?? StateFile.defaultProfile,
                current: b["current"].bool ?? false,
                decision: Decision(b["decision"]),
                accounts: b["accounts"].array.map(Verdict.init),
                switchAt: b["thresholds"]["switch_at"].double,
                switchAtWeekly: b["thresholds"]["switch_at_weekly"].double,
                pool: b["pool"].array.compactMap(\.string),
                best: Self.text(b["best"]), bestWhy: Self.text(b["best_why"]),
                onBest: b["on_best"].bool, activeRoom: b["active_room"].double, bestRoom: b["best_room"].double,
                poolDryAt: b["pool_dry_at"].date))
        }
        if list.isEmpty {
            let d = Decision(j["decision"])
            let a = j["accounts"].array.map(Verdict.init)
            guard d != nil || !a.isEmpty else { return nil }
            list.append(ProfileWhy(profile: StateFile.defaultProfile, current: true,
                                    decision: d, accounts: a, switchAt: nil, switchAtWeekly: nil,
                                    best: Self.text(j["best"]), bestWhy: Self.text(j["best_why"]),
                                    onBest: j["on_best"].bool, activeRoom: j["active_room"].double,
                                    bestRoom: j["best_room"].double, poolDryAt: j["pool_dry_at"].date))
        }
        profiles = list
    }

    static func text(_ j: JSON) -> String? { j.string.flatMap { $0.isEmpty ? nil : $0 } }

    /// The block this app reports on: the default profile, which is the one
    /// `claudeswitch use` acts on when run without CLAUDE_CONFIG_DIR.
    public var primary: ProfileWhy? {
        profiles.first { $0.profile == StateFile.defaultProfile }
            ?? profiles.first { $0.current } ?? profiles.first
    }
}

/// `claudeswitch config --json`: every setting as a string, plus "path".
public struct ConfigSettings: Equatable {
    public var switchAt: Double?
    public var switchAtWeekly: Double?
    public var hardFloor: Double?
    public var pollActive: TimeInterval?
    public var path: String?

    public init(switchAt: Double? = nil, switchAtWeekly: Double? = nil, hardFloor: Double? = nil,
                pollActive: TimeInterval? = nil, path: String? = nil) {
        self.switchAt = switchAt
        self.switchAtWeekly = switchAtWeekly
        self.hardFloor = hardFloor
        self.pollActive = pollActive
        self.path = path
    }

    public init?(data: Data) {
        guard let j = JSON(data: data), j.raw is [String: Any] else { return nil }
        switchAt = j["switch_at"].double
        switchAtWeekly = j["switch_at_weekly"].double
        hardFloor = j["hard_floor"].double
        pollActive = j["poll_active"].string.flatMap(GoDuration.parse)
        path = j["path"].string
    }

    /// The trigger a window is held to (Go: Config.TriggerFor). A weekly
    /// trigger of zero means "same as the 5-hour one".
    public func trigger(for window: String) -> Double {
        let five = switchAt ?? 90
        if window == "seven_day", let w = switchAtWeekly, w > 0 { return w }
        return five
    }
}

/// Parses Go's time.Duration.String() form ("1h2m3.5s", "30s", "0s").
public enum GoDuration {
    public static func parse(_ s: String) -> TimeInterval? {
        var total: TimeInterval = 0
        var num = ""
        var unit = ""
        var any = false
        func flush() -> Bool {
            guard !num.isEmpty, let n = Double(num) else { return num.isEmpty && unit.isEmpty }
            let mult: Double
            switch unit {
            case "h": mult = 3600
            case "m": mult = 60
            case "s": mult = 1
            case "ms": mult = 0.001
            case "µs", "us": mult = 0.000_001
            case "ns": mult = 0.000_000_001
            default: return false
            }
            total += n * mult
            any = true
            num = ""; unit = ""
            return true
        }
        for ch in s {
            if ch.isNumber || ch == "." {
                if !unit.isEmpty { guard flush() else { return nil } }
                num.append(ch)
            } else {
                unit.append(ch)
            }
        }
        guard flush(), any else { return nil }
        return total
    }
}
