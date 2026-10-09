import Foundation

// The answers of the JSON CLI (docs/APP_CLI.md), one type per shape. As
// everywhere in this app, readers are lenient: a key that is missing or of
// another type is unknown, and keys this app does not know are ignored. An
// initialiser fails only when the key that identifies the answer is absent.

private func nonEmpty(_ j: JSON) -> String? {
    j.string.flatMap { $0.isEmpty ? nil : $0 }
}

// MARK: reading saved answers (the app's --render mode reads fixtures)

public extension ConfigValues {
    init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }
}

public extension DaemonStatus {
    init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }
}

public extension RecoveryItem {
    /// The items of a `recovery --json` answer.
    static func list(data: Data) -> [RecoveryItem]? {
        guard let j = JSON(data: data), j["items"].raw is [Any] else { return nil }
        return j["items"].array.compactMap(RecoveryItem.init)
    }
}

// MARK: config

/// One entry of `config schema --json`.
public struct SettingSchema: Equatable, Identifiable {
    public var id: String { key }
    public var key: String
    /// percent, number, int, duration, enum or list.
    public var type: String
    public var defaultValue: String
    /// Seconds for a duration; nil when unbounded.
    public var min: Double?
    public var max: Double?
    public var minExclusive: Bool
    public var enumValues: [String]
    public var scopes: [String]
    public var description: String

    public init(key: String, type: String, defaultValue: String, min: Double? = nil, max: Double? = nil,
                minExclusive: Bool = false, enumValues: [String] = [], scopes: [String] = ["global"],
                description: String = "") {
        self.key = key
        self.type = type
        self.defaultValue = defaultValue
        self.min = min
        self.max = max
        self.minExclusive = minExclusive
        self.enumValues = enumValues
        self.scopes = scopes
        self.description = description
    }

    init?(_ j: JSON) {
        guard let key = j["key"].string else { return nil }
        self.init(key: key, type: j["type"].string ?? "", defaultValue: j["default"].string ?? "",
                  min: j["min"].double, max: j["max"].double, minExclusive: j["min_exclusive"].bool ?? false,
                  enumValues: j["enum"].array.compactMap(\.string), scopes: j["scopes"].array.compactMap(\.string),
                  description: j["description"].string ?? "")
    }

    /// Whether `profile set` takes it.
    public var profileScoped: Bool { scopes.contains("profile") }

    /// The inline check, before the CLI's own: nil when the value looks
    /// acceptable, else what is wrong. The CLI stays the authority (cross-
    /// setting rules are only there).
    public func validate(_ raw: String) -> String? {
        let v = raw.trimmingCharacters(in: .whitespaces)
        switch type {
        case "list":
            return nil
        case "enum":
            return enumValues.contains(v) ? nil : "one of: " + enumValues.joined(separator: ", ")
        case "duration":
            guard let secs = GoDuration.parse(v) else { return "a duration such as 90s, 5m or 2h" }
            return rangeError(secs, unit: { Format.duration($0) })
        case "int":
            guard let n = Int(v) else { return "a whole number" }
            return rangeError(Double(n), unit: { String(format: "%g", $0) })
        case "percent", "number":
            guard let n = Double(v), n.isFinite else { return type == "percent" ? "a percentage" : "a number" }
            return rangeError(n, unit: { String(format: "%g", $0) })
        default:
            return v.isEmpty ? "a value" : nil
        }
    }

    private func rangeError(_ n: Double, unit: (Double) -> String) -> String? {
        if let lo = min, minExclusive ? n <= lo : n < lo {
            if let hi = max { return "between \(unit(lo)) and \(unit(hi))" + (minExclusive ? ", above \(unit(lo))" : "") }
            return minExclusive ? "more than \(unit(lo))" : "at least \(unit(lo))"
        }
        if let hi = max, n > hi {
            if let lo = min { return "between \(unit(lo)) and \(unit(hi))" }
            return "at most \(unit(hi))"
        }
        return nil
    }
}

/// The Settings window's panes for the global settings. The schema carries
/// no grouping, so the app assigns one; a key it does not know goes to
/// Advanced, so a new setting is never hidden.
public enum SettingsPane: String, CaseIterable {
    case rotation, polling, advanced

    static let rotationKeys = ["switch_at", "switch_at_weekly", "hard_floor", "landing_margin", "switch_when",
                               "max_switch_wait", "cooldown", "models", "prefer"]
    static let pollingKeys = ["hot_threshold", "poll_active", "poll_hot", "poll_idle", "api_budget",
                              "blind_failover_polls"]

    public static func of(_ s: SettingSchema) -> SettingsPane {
        if rotationKeys.contains(s.key) { return .rotation }
        if pollingKeys.contains(s.key) { return .polling }
        return .advanced
    }
}

/// `config schema --json`.
public struct ConfigSchema: Equatable {
    public var settings: [SettingSchema]

    init?(_ j: JSON) {
        guard j["settings"].raw is [Any] else { return nil }
        settings = j["settings"].array.compactMap(SettingSchema.init)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func setting(_ key: String) -> SettingSchema? { settings.first { $0.key == key } }

    /// A pane's settings, in the schema's order.
    public func settings(in pane: SettingsPane) -> [SettingSchema] {
        settings.filter { SettingsPane.of($0) == pane }
    }
}

/// `config --json`: every setting's value as a string, and the path.
public struct ConfigValues: Equatable {
    public var values: [String: String]
    public var path: String?

    init?(_ j: JSON) {
        guard j.raw is [String: Any] else { return nil }
        var v: [String: String] = [:]
        for (k, x) in j.object where k != "path" {
            if let s = x.string { v[k] = s }
        }
        values = v
        path = j["path"].string
    }
}

public struct ConfigGetResult: Equatable {
    public var key: String
    public var value: String
    public var scope: String
    public var profile: String?
    public var override: String?

    init?(_ j: JSON) {
        guard let key = j["key"].string else { return nil }
        self.key = key
        value = j["value"].string ?? ""
        scope = j["scope"].string ?? "global"
        profile = j["profile"].string
        override = j["override"].string
    }
}

public struct ConfigSetResult: Equatable {
    public var key: String
    public var previous: String
    public var value: String
    public var path: String?
    public var daemonRunning: Bool
    /// Not in the contract today; read when the CLI adds it.
    public var warnings: [String]

    init?(_ j: JSON) {
        guard let key = j["key"].string else { return nil }
        self.key = key
        previous = j["previous"].string ?? ""
        value = j["value"].string ?? ""
        path = j["path"].string
        daemonRunning = j["daemon_running"].bool ?? false
        warnings = j["warnings"].array.compactMap(\.string)
    }
}

// MARK: profiles

public struct Thresholds: Equatable {
    public var switchAt: Double?
    public var switchAtWeekly: Double?
    public var hardFloor: Double?
    public var landingMargin: Double?

    init(_ j: JSON) {
        switchAt = j["switch_at"].double
        switchAtWeekly = j["switch_at_weekly"].double
        hardFloor = j["hard_floor"].double
        landingMargin = j["landing_margin"].double
    }
}

/// One profile as `profile list --json` reports it.
public struct ProfileInfo: Equatable, Identifiable {
    public var id: String { name }
    public var name: String
    /// As written; nil is Claude Code's default directory.
    public var dir: String?
    public var fromEnv: Bool
    public var declared: Bool
    public var pool: [String]
    /// What the config's pool lists: `pool` without the accounts default
    /// holds by D6 alone. Nil from a CLI older than lane 15.
    public var listed: [String]?
    public var live: String?
    public var pinned: String?
    /// yes, no or unknown.
    public var signedIn: String
    public var overrides: [String: String]
    public var thresholds: Thresholds
    /// The Chrome profile folder this profile names (C2); nil is Chrome's
    /// last-used profile. Its display name when Chrome lists it.
    public var chrome: String?
    public var chromeName: String?
    /// F4: the folders that pick this profile (globs); nil from a CLI that
    /// predates them, and the Folders field is hidden.
    public var paths: [String]?

    init?(_ j: JSON) {
        guard let name = j["name"].string else { return nil }
        self.name = name
        dir = nonEmpty(j["dir"])
        fromEnv = j["from_env"].bool ?? false
        declared = j["declared"].bool ?? false
        pool = j["pool"].array.compactMap(\.string)
        listed = j["listed"].raw is [Any] ? j["listed"].array.compactMap(\.string) : nil
        live = nonEmpty(j["live"])
        pinned = nonEmpty(j["pinned"])
        signedIn = j["signed_in"].string ?? "unknown"
        var o: [String: String] = [:]
        for (k, v) in j["overrides"].object {
            if let s = v.string { o[k] = s } else if let d = v.double { o[k] = String(format: "%g", d) }
        }
        overrides = o
        thresholds = Thresholds(j["thresholds"])
        chrome = nonEmpty(j["chrome"])
        chromeName = nonEmpty(j["chrome_name"])
        // Go writes an empty list as null, so the key alone says it is known.
        let keys = (j.raw as? [String: Any])?.keys
        paths = keys?.contains("paths") == true ? j["paths"].array.compactMap(\.string) : nil
    }
}

/// A removed or repointed profile's old credential the guard still holds.
public struct Ghost: Equatable, Identifiable {
    public var id: String { profile + "/" + account }
    public var profile: String
    public var account: String
    public var why: String
    public var since: Date?

    init?(_ j: JSON) {
        guard let p = j["profile"].string else { return nil }
        profile = p
        account = j["account"].string ?? ""
        why = j["why"].string ?? ""
        since = j["since"].date
    }
}

/// `profile list --json`.
public struct ProfileList: Equatable {
    public var profiles: [ProfileInfo]
    public var ghosts: [Ghost]

    init?(_ j: JSON) {
        // Go writes a nil list as null, so either key identifies the answer.
        guard j["profiles"].raw is [Any] || j["ghosts"].raw is [Any] else { return nil }
        profiles = j["profiles"].array.compactMap(ProfileInfo.init)
        ghosts = j["ghosts"].array.compactMap(Ghost.init)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func profile(_ name: String) -> ProfileInfo? { profiles.first { $0.name == name } }
}

/// `profile create --json`: the new profile, and the account seeded.
public struct ProfileCreated: Equatable {
    public var profile: ProfileInfo
    public var seeded: String?

    init?(_ j: JSON) {
        guard let p = ProfileInfo(j) else { return nil }
        profile = p
        seeded = nonEmpty(j["seeded"])
    }
}

public struct SeedResult: Equatable {
    public var profile: String
    public var seeded: String

    init?(_ j: JSON) {
        guard let p = j["profile"].string else { return nil }
        profile = p
        seeded = j["seeded"].string ?? ""
    }
}

public struct ForgetResult: Equatable {
    public var profile: String
    public var released: Int

    init?(_ j: JSON) {
        guard let p = j["profile"].string else { return nil }
        profile = p
        released = Int(j["released"].double ?? 0)
    }
}

public struct PoolResult: Equatable {
    public var account: String
    /// Where the account is now.
    public var profile: String
    public var changed: Bool
    public var pools: [String: [String]]

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profile = j["profile"].string ?? ""
        changed = j["changed"].bool ?? false
        pools = j["pools"].object.mapValues { $0.array.compactMap(\.string) }
    }
}

public struct ProfileSetResult: Equatable {
    public var profile: String
    public var key: String
    /// nil: the profile inherits the global value.
    public var override: String?
    public var effective: String

    init?(_ j: JSON) {
        guard let p = j["profile"].string, let k = j["key"].string else { return nil }
        profile = p
        key = k
        override = j["override"].string
        effective = j["effective"].string ?? ""
    }
}

// MARK: accounts

public struct RenameResult: Equatable {
    public var account: String
    public var previous: String

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        previous = j["previous"].string ?? ""
    }
}

public struct DeleteResult: Equatable {
    public var account: String
    public var seat: String?
    public var orgID: String?
    public var email: String?
    public var removedCredential: Bool
    public var removedConfig: Bool
    public var removedPriority: Bool
    public var removedPool: String?
    public var removedState: Bool
    public var twin: String?
    public var daemonRunning: Bool

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        seat = nonEmpty(j["seat"])
        orgID = nonEmpty(j["org_id"])
        email = nonEmpty(j["email"])
        let r = j["removed"]
        removedCredential = r["credential"].bool ?? false
        removedConfig = r["config"].bool ?? false
        removedPriority = r["priority"].bool ?? false
        removedPool = nonEmpty(r["pool"])
        removedState = r["state"].bool ?? false
        twin = nonEmpty(j["twin"])
        daemonRunning = j["daemon_running"].bool ?? false
    }
}

public struct PinResult: Equatable {
    public var profile: String
    public var pinned: String

    init?(_ j: JSON) {
        guard let p = j["profile"].string, let a = j["pinned"].string else { return nil }
        profile = p
        pinned = a
    }
}

public struct PriorityResult: Equatable {
    public var priority: [String]
    /// The enabled accounts in the order rotation spends them.
    public var order: [String]

    init?(_ j: JSON) {
        guard j["order"].raw is [Any] else { return nil }
        priority = j["priority"].array.compactMap(\.string)
        order = j["order"].array.compactMap(\.string)
    }
}

// MARK: recovery

/// A kept copy of a credential (`recovery --json`).
public struct RecoveryItem: Equatable, Identifiable {
    public var id: String { slot }
    public var slot: String
    public var profile: String?
    public var keptAt: Date?
    public var seat: String?
    public var account: String?
    public var who: String?
    public var accessExpiresAt: Date?
    public var refreshExpiresAt: Date?
    public var renewable: Bool
    public var error: String?

    init?(_ j: JSON) {
        guard let s = j["slot"].string else { return nil }
        slot = s
        profile = nonEmpty(j["profile"])
        keptAt = j["kept_at"].date
        seat = nonEmpty(j["seat"])
        account = nonEmpty(j["account"])
        who = nonEmpty(j["who"])
        accessExpiresAt = j["access_expires_at"].date
        refreshExpiresAt = j["refresh_expires_at"].date
        renewable = j["renewable"].bool ?? false
        error = nonEmpty(j["error"])
    }
}

public struct RestoreResult: Equatable {
    public var slot: String
    public var account: String
    public var seat: String?
    public var renewable: Bool
    public var cleared: Bool

    init?(_ j: JSON) {
        guard let s = j["slot"].string else { return nil }
        slot = s
        account = j["account"].string ?? ""
        seat = nonEmpty(j["seat"])
        renewable = j["renewable"].bool ?? false
        cleared = j["cleared"].bool ?? false
    }
}

// MARK: use

public struct UseResult: Equatable {
    public var account: String
    public var profile: String
    public var from: String?
    public var dryRun: Bool
    public var orgID: String?
    /// False when the usage read confirming the swap was rate limited (the
    /// swap itself succeeded).
    public var verified: Bool
    public var fiveHour: Double?
    public var sevenDay: Double?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profile = j["profile"].string ?? StateFile.defaultProfile
        from = nonEmpty(j["from"])
        dryRun = j["dry_run"].bool ?? false
        orgID = nonEmpty(j["org_id"])
        verified = j["verified"].bool ?? false
        fiveHour = j["five_hour"].double
        sevenDay = j["seven_day"].double
    }
}

// MARK: adding an account

/// `login <id> --direct --json --no-open`.
public struct LoginStart: Equatable {
    public var url: URL
    public var expiresAt: Date?
    public var account: String
    /// The profile the account will be in: the one asked for when it is
    /// new, its own when it exists.
    public var profile: String?
    public var newAccount: Bool

    /// Only an https URL is ever opened.
    init?(_ j: JSON) {
        guard let s = j["url"].string, let u = URL(string: s), u.scheme?.lowercased() == "https", u.host != nil else {
            return nil
        }
        url = u
        expiresAt = j["expires_at"].date
        let p = j["pending"]
        account = p["account"].string ?? ""
        profile = nonEmpty(p["profile"])
        newAccount = p["new_account"].bool ?? false
    }
}

/// The verified account, from `login --code` and from `add`.
public struct AccountAdded: Equatable {
    public var account: String
    public var seat: String?
    public var orgID: String?
    public var email: String?
    public var plan: String?
    public var profile: String?
    public var pool: [String]
    public var configured: Bool
    public var newAccount: Bool
    public var renewable: Bool
    public var accessExpiresAt: Date?
    public var refreshExpiresAt: Date?
    /// `add`: the profile whose login was saved, and what happens next when
    /// it is not the one the account joined (lane 16).
    public var from: String?
    public var note: String?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        from = nonEmpty(j["from"])
        note = nonEmpty(j["note"])
        seat = nonEmpty(j["seat"])
        orgID = nonEmpty(j["org_id"])
        email = nonEmpty(j["email"])
        plan = nonEmpty(j["plan"])
        profile = nonEmpty(j["profile"])
        pool = j["pool"].array.compactMap(\.string)
        configured = j["configured"].bool ?? false
        newAccount = j["new_account"].bool ?? false
        renewable = j["renewable"].bool ?? false
        accessExpiresAt = j["access_expires_at"].date
        refreshExpiresAt = j["refresh_expires_at"].date
    }
}

// MARK: daemon

/// `daemon <verb> --json`: the service's status after the verb.
public struct DaemonStatus: Equatable {
    public var action: String
    public var platform: String
    public var file: String?
    public var log: String?
    public var installed: Bool
    public var loaded: Bool
    public var running: Bool
    /// The installed file's mode: live or dry-run.
    public var mode: String?
    public var binary: String?
    public var thisBinary: String?
    public var binaryIsThis: Bool
    /// What the running daemon recorded; nil unless one runs.
    public var daemonLive: Bool?
    public var daemonVersion: String?
    public var since: Date?

    init?(_ j: JSON) {
        guard j["installed"].bool != nil else { return nil }
        action = j["action"].string ?? ""
        platform = j["platform"].string ?? ""
        file = nonEmpty(j["file"])
        log = nonEmpty(j["log"])
        installed = j["installed"].bool ?? false
        loaded = j["loaded"].bool ?? false
        running = j["running"].bool ?? false
        mode = nonEmpty(j["mode"])
        binary = nonEmpty(j["binary"])
        thisBinary = nonEmpty(j["this_binary"])
        binaryIsThis = j["binary_is_this"].bool ?? false
        daemonLive = j["daemon_live"].bool
        daemonVersion = nonEmpty(j["daemon_version"])
        since = j["since"].date
    }
}
