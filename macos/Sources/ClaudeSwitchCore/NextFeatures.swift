import Foundation

// The app side of F1-F5, F7 and F12 (docs/IMPROVEMENTS.md, "Next
// features"). Each reads the CLI's JSON, leniently as everywhere here; a
// key or command an older binary lacks hides its feature, and an unknown
// figure is never shown as 0.

private func text(_ j: JSON) -> String? {
    j.string.flatMap { $0.isEmpty ? nil : $0 }
}

private func clock(_ at: Date, _ format: String, _ tz: TimeZone) -> String {
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = tz
    f.dateFormat = format
    return f.string(from: at)
}

/// Whether a failure means the binary predates the command or flag (an
/// older claudeswitch): the feature then says it needs a newer one.
public enum FeatureSupport {
    public static func needsNewerCLI(_ e: CallError?) -> Bool {
        if case .cli(.tooOld)? = e { return true }
        return false
    }

    /// The line a pane shows in place of its feature.
    public static func newerCLI(_ what: String) -> String {
        "\(what) needs a newer claudeswitch. Update it (./install.sh, or a release binary) and reopen this pane."
    }
}

// MARK: F1 spend expiring quota first

/// `prefer`: which eligible account rotation spends first.
public enum RotationPrefer: String, CaseIterable {
    case room, expiring

    public var label: String { SettingNames.choices["prefer"]?[rawValue] ?? rawValue }
}

/// A per-profile override's choices for an enum setting: inherit (the
/// global value, named), then every value.
public enum OverrideChoices {
    public struct Choice: Equatable, Hashable {
        /// "" inherits (`profile set <p> <key> inherit`).
        public var value: String
        public var label: String
    }

    public static func options(_ s: SettingSchema, global: String) -> [Choice] {
        [Choice(value: "", label: "Inherit (\(SettingNames.value(s, global)))")]
            + s.enumValues.map { Choice(value: $0, label: SettingNames.value(s, $0)) }
    }
}

// MARK: F2 pool runway

public enum Runway {
    /// Within this of the pool running dry the line is a warning (the
    /// daemon notifies then too).
    public static let urgentWithin: TimeInterval = 2 * 3600

    /// "Thu 14:00" within the week, "16 Oct 08:00" beyond it, "now" once
    /// it has passed.
    public static func when(_ at: Date, now: Date, timeZone: TimeZone = .current) -> String {
        if at <= now { return "now" }
        if at.timeIntervalSince(now) < 6.5 * 86400 { return clock(at, "EEE HH:mm", timeZone) }
        return clock(at, "d MMM HH:mm", timeZone)
    }

    /// "work pool: runs dry Thu 14:00 at this pace"; nil when unknown.
    public static func poolLine(profile: String, dryAt: Date?, now: Date, timeZone: TimeZone = .current) -> String? {
        guard let at = dryAt else { return nil }
        return "\(profile) pool: runs dry \(when(at, now: now, timeZone: timeZone)) at this pace"
    }

    public static func urgent(dryAt: Date?, now: Date) -> Bool {
        guard let at = dryAt else { return false }
        return at.timeIntervalSince(now) <= urgentWithin
    }

    /// The account picker's "trigger Thu 09:30"; nil when unknown.
    public static func accountETA(_ triggerAt: Date?, now: Date, timeZone: TimeZone = .current) -> String? {
        triggerAt.map { "trigger " + when($0, now: now, timeZone: timeZone) }
    }
}

// MARK: F3 pin safety valve

public extension AccountList {
    /// Whether this CLI knows `account pin --hard` (it reports pin_hard).
    var knowsHardPin: Bool { accounts.contains { $0.pinHard != nil } }
}

/// The daemon lifting a pin (audit `kind: unpin`).
public struct PinLift: Equatable {
    public var profile: String
    public var account: String?
    public var reason: String?
    public var at: Date

    /// How long a card says so.
    public static let shownFor: TimeInterval = 24 * 3600

    /// Identifies this lift, for dismissing its notice.
    public var key: String { profile + "@" + String(Int(at.timeIntervalSince1970)) }

    /// "Pin on work-2 lifted: it was refused".
    public var message: String {
        let head = "Pin on \(account ?? profile) lifted"
        guard let r = reason else { return head + " by the daemon" }
        return head + ": " + r
    }

    init?(_ j: JSON) {
        guard j["kind"].string == "unpin", let p = text(j["profile"]), let at = j["at"].date else { return nil }
        profile = p
        account = text(j["account"]) ?? text(j["from"])
        reason = text(j["reason"])
        self.at = at
    }

    /// The newest lift per profile in audit.jsonl's bytes (a tail of it);
    /// a line that is not JSON, or cut short, is skipped.
    public static func latest(jsonl: Data) -> [String: PinLift] {
        var out: [String: PinLift] = [:]
        for line in jsonl.split(separator: UInt8(ascii: "\n")) {
            guard let j = JSON(data: Data(line)), let l = PinLift(j) else { continue }
            if let have = out[l.profile], have.at > l.at { continue }
            out[l.profile] = l
        }
        return out
    }

    /// The notice a card shows: its profile's last lift, for a day, while
    /// it is not pinned again and the notice was not dismissed.
    public static func notice(profile: String, lifts: [String: PinLift], pinned: Bool,
                              dismissed: Set<String>, now: Date) -> PinLift? {
        guard !pinned, let l = lifts[profile], !dismissed.contains(l.key) else { return nil }
        let age = now.timeIntervalSince(l.at)
        return age >= -60 && age < shownFor ? l : nil
    }

    /// Reads the last `bytes` of the audit log; nil when there is none.
    public static func readTail(path: String, bytes: Int = 256 * 1024) -> Data? {
        guard let h = FileHandle(forReadingAtPath: path) else { return nil }
        defer { try? h.close() }
        let size = (try? h.seekToEnd()) ?? 0
        let start = size > UInt64(bytes) ? size - UInt64(bytes) : 0
        try? h.seek(toOffset: start)
        return try? h.readToEnd()
    }
}

public extension Paths {
    var auditFile: String { stateDir + "/audit.jsonl" }
}

// MARK: F4 profile folders

/// The Folders field: globs, comma- or line-separated.
public enum FolderList {
    public static func parse(_ s: String) -> [String] {
        var out: [String] = []
        for part in s.split(whereSeparator: { $0 == "," || $0 == "\n" }) {
            let p = part.trimmingCharacters(in: .whitespaces)
            if !p.isEmpty && !out.contains(p) { out.append(p) }
        }
        return out
    }

    public static func text(_ paths: [String]) -> String { paths.joined(separator: ", ") }

    /// What `profile set <p> paths` is given: one comma-separated value
    /// ("" clears them).
    public static func value(_ paths: [String]) -> String { paths.joined(separator: ",") }

    /// nil when the field can be saved, else what is wrong.
    public static func validate(_ s: String) -> String? {
        for p in parse(s) {
            if p.hasPrefix("-") { return "\"\(p)\" is not a folder" }
            if p.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) {
                return "a folder cannot hold a control character"
            }
        }
        return nil
    }
}

// MARK: F5 re-login reminders

public struct ReloginReminder: Equatable {
    public var account: String
    public var expiresAt: Date
    /// Whole days left, rounded up; 0 once it has expired.
    public var days: Int

    public init(account: String, expiresAt: Date, days: Int) {
        self.account = account
        self.expiresAt = expiresAt
        self.days = days
    }

    static let prefix = "xyz.claudeswitch.relogin."

    /// The notification's identifier, which names the account.
    public var identifier: String { Self.prefix + account }

    public static func account(fromIdentifier id: String) -> String? {
        guard id.hasPrefix(prefix) else { return nil }
        let a = String(id.dropFirst(prefix.count))
        return CLI.validID(a) ? a : nil
    }

    public var title: String { "Sign \(account) in again" }

    /// "personal needs signing in within 5 days".
    public var body: String {
        if days <= 0 { return "\(account) needs signing in: its login has expired" }
        return "\(account) needs signing in within \(days) day\(days == 1 ? "" : "s")"
    }
}

/// F5: one notification per account per day while its refresh token
/// expires within 5 days. The days sent are kept in UserDefaults.
public enum ReloginReminders {
    public static let within: TimeInterval = 5 * 86400
    public static let defaultsKey = "reloginRemindersSent"

    public static func dayKey(_ now: Date, calendar: Calendar) -> String {
        let c = calendar.dateComponents([.year, .month, .day], from: now)
        return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
    }

    /// The reminders due now: an enabled account whose refresh_expires_at is
    /// known and within 5 days (or past), not yet reminded today.
    public static func due(_ list: AccountList?, now: Date, sent: [String: String],
                           calendar: Calendar = .current) -> [ReloginReminder] {
        let today = dayKey(now, calendar: calendar)
        return (list?.accounts ?? []).compactMap { a in
            guard a.enabled, let exp = a.refreshExpiresAt, exp.timeIntervalSince(now) <= within,
                  sent[a.id] != today else { return nil }
            let left = exp.timeIntervalSince(now)
            return ReloginReminder(account: a.id, expiresAt: exp, days: left <= 0 ? 0 : Int((left / 86400).rounded(.up)))
        }
    }

    /// The record after reminding `accounts` today, keeping only the last
    /// week's entries so it stays small.
    public static func record(_ sent: [String: String], accounts: [String], now: Date,
                              calendar: Calendar = .current) -> [String: String] {
        let today = dayKey(now, calendar: calendar)
        let cutoff = dayKey(now.addingTimeInterval(-7 * 86400), calendar: calendar)
        var out = sent.filter { $0.value >= cutoff }
        for a in accounts { out[a] = today }
        return out
    }
}

// MARK: F7 usage history

/// One reading: either figure may be unknown.
public struct UsagePoint: Equatable {
    public var at: Date
    public var fiveHour: Double?
    public var sevenDay: Double?

    init?(_ j: JSON) {
        guard let at = j["at"].date else { return nil }
        self.at = at
        fiveHour = j["five_hour"].double
        sevenDay = j["seven_day"].double
    }
}

public struct UsageSwitch: Equatable {
    public var at: Date
    public var profile: String?
    public var from: String?
    public var to: String?
    public var reason: String?

    init?(_ j: JSON) {
        guard let at = j["at"].date else { return nil }
        self.at = at
        profile = text(j["profile"])
        from = text(j["from"])
        to = text(j["to"])
        reason = text(j["reason"])
    }
}

/// `history --usage --days N --json`: a series per account and the switches.
public struct UsageHistory: Equatable {
    public var accountIDs: [String]
    public var series: [String: [UsagePoint]]
    public var switches: [UsageSwitch]

    init?(_ j: JSON) {
        guard let o = j.raw as? [String: Any] else { return nil }
        let key = o.keys.contains("accounts") ? "accounts" : o.keys.contains("series") ? "series" : nil
        guard let k = key else { return nil }
        var ids: [String] = []
        var series: [String: [UsagePoint]] = [:]
        let body = j[k]
        if body.raw is [String: Any] {
            // {"work-1": [{at, five_hour, seven_day}], ...}
            for id in body.keys {
                ids.append(id)
                series[id] = body[id].array.compactMap(UsagePoint.init)
            }
        } else {
            // [{"id": "work-1", "series": [...]}, ...]
            for a in body.array {
                guard let id = text(a["id"]) ?? text(a["account"]), series[id] == nil else { continue }
                ids.append(id)
                let pts = a["series"].raw != nil ? a["series"] : a["readings"].raw != nil ? a["readings"] : a["points"]
                series[id] = pts.array.compactMap(UsagePoint.init)
            }
        }
        accountIDs = ids
        self.series = series
        switches = j["switches"].array.compactMap(UsageSwitch.init).sorted { $0.at < $1.at }
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }
}

public enum HistoryWindow: String, CaseIterable, Identifiable {
    case session, week

    public var id: String { rawValue }
    public var label: String { self == .session ? "Session" : "Week" }
}

/// One point on a chart: a bucket's highest reading.
public struct ChartPoint: Equatable, Identifiable {
    public var at: Date
    public var value: Double
    public var window: HistoryWindow
    /// Consecutive points share a segment; a gap in the readings starts a
    /// new one, so the line breaks there instead of bridging it.
    public var segment: Int

    public var id: String { "\(window.rawValue)-\(segment)-\(at.timeIntervalSince1970)" }
}

/// A switch drawn on an account's chart.
public struct SwitchMark: Equatable, Identifiable {
    public var at: Date
    public var from: String?
    public var to: String?
    public var profile: String?
    /// To this account (else away from it).
    public var into: Bool

    public var id: String { "\(at.timeIntervalSince1970)-\(from ?? "")-\(to ?? "")" }
}

public enum HistoryChart {
    public static let days = 30
    /// One point per hour: a 30-day chart of every poll would be thousands
    /// of points per account.
    public static let bucket: TimeInterval = 3600
    /// No reading for this long breaks the line.
    public static let gap: TimeInterval = 6 * 3600

    public static func domain(now: Date, days: Int = HistoryChart.days) -> ClosedRange<Date> {
        now.addingTimeInterval(-Double(days) * 86400)...now
    }

    /// An account's points for one window over the last `days`: bucketed to
    /// the hour (its highest reading), sorted, clamped to 0-100, unknown
    /// readings left out (never drawn as 0), split where readings stop.
    public static func points(_ h: UsageHistory, account: String, window: HistoryWindow, now: Date,
                              days: Int = HistoryChart.days) -> [ChartPoint] {
        let range = domain(now: now, days: days)
        var buckets: [Double: Double] = [:]
        for p in h.series[account] ?? [] where range.contains(p.at) {
            guard let v = window == .session ? p.fiveHour : p.sevenDay, v.isFinite else { continue }
            let b = (p.at.timeIntervalSince1970 / bucket).rounded(.down) * bucket
            buckets[b] = max(buckets[b] ?? -.infinity, min(max(v, 0), 100))
        }
        var out: [ChartPoint] = []
        var segment = 0
        var last: Double?
        for b in buckets.keys.sorted() {
            if let l = last, b - l > gap { segment += 1 }
            out.append(ChartPoint(at: Date(timeIntervalSince1970: b), value: buckets[b]!, window: window, segment: segment))
            last = b
        }
        return out
    }

    /// The switches to or away from an account in the last `days`.
    public static func switches(_ h: UsageHistory, account: String, now: Date, days: Int = HistoryChart.days) -> [SwitchMark] {
        let range = domain(now: now, days: days)
        return h.switches.filter { range.contains($0.at) && ($0.from == account || $0.to == account) }
            .map { SwitchMark(at: $0.at, from: $0.from, to: $0.to, profile: $0.profile, into: $0.to == account) }
    }

    /// The accounts with anything to draw, in the CLI's order.
    public static func accounts(_ h: UsageHistory, now: Date, days: Int = HistoryChart.days) -> [String] {
        h.accountIDs.filter { id in
            !points(h, account: id, window: .week, now: now, days: days).isEmpty
                || !points(h, account: id, window: .session, now: now, days: days).isEmpty
        }
    }
}

// MARK: F12 health

public enum CheckStatus: String {
    case ok, warn, fail, unknown
}

/// A fix `doctor --json` names that this app can run.
public enum FixAction: Equatable, Hashable {
    case signin(String)
    case daemonRestart, statuslineInstall, keychainAllow

    public init?(_ raw: String) {
        let words = raw.split(separator: " ").map(String.init)
        switch words {
        case ["daemon", "restart"]: self = .daemonRestart
        case ["statusline", "install"]: self = .statuslineInstall
        case ["keychain", "allow"]: self = .keychainAllow
        default:
            guard words.count == 2, words[0] == "signin", CLI.validID(words[1]) else { return nil }
            self = .signin(words[1])
        }
    }

    /// The button.
    public var title: String {
        switch self {
        case .signin(let a): return "Sign \(a) in…"
        case .daemonRestart: return "Restart the daemon"
        case .statuslineInstall: return "Install the status line"
        case .keychainAllow: return "Allow keychain access"
        }
    }

    /// The command it runs; nil for sign-in, which opens Add account.
    public var argv: [String]? {
        switch self {
        case .signin: return nil
        case .daemonRestart: return ["daemon", "restart", "--json"]
        case .statuslineInstall: return ["statusline", "install", "--json"]
        case .keychainAllow: return ["keychain", "allow", "--json"]
        }
    }
}

public struct DoctorCheck: Equatable, Identifiable {
    public var id: String
    public var name: String
    public var status: CheckStatus
    public var message: String
    /// As the CLI wrote it; `fix` is set only when this app knows it.
    public var fixText: String?
    public var fix: FixAction?

    /// A Fix button: a failure or a warning (an info note is a warning,
    /// "status line not set" among them) whose fix this app can run
    /// (0.6.1; it was failures only).
    public var offersFix: Bool { (status == .fail || status == .warn) && fix != nil }
}

/// `doctor --json`: one object per check.
public struct DoctorReport: Equatable {
    public var checks: [DoctorCheck]

    init?(_ j: JSON) {
        guard j["checks"].raw is [Any] else { return nil }
        checks = j["checks"].array.enumerated().compactMap { i, c in
            guard let name = text(c["name"]) else { return nil }
            let fixText = text(c["fix"])
            return DoctorCheck(id: "\(i)-\(name)", name: name,
                               status: c["status"].string.flatMap(CheckStatus.init(rawValue:)) ?? .unknown,
                               message: c["message"].string ?? "", fixText: fixText, fix: fixText.flatMap(FixAction.init))
        }
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func count(_ s: CheckStatus) -> Int { checks.filter { $0.status == s }.count }

    /// "4 failing · 2 warnings · 3 ok".
    public var summary: String {
        var parts: [String] = []
        let f = count(.fail), w = count(.warn), o = count(.ok)
        if f > 0 { parts.append("\(f) failing") }
        if w > 0 { parts.append("\(w) warning\(w == 1 ? "" : "s")") }
        if o > 0 { parts.append("\(o) ok") }
        return parts.isEmpty ? "no checks" : parts.joined(separator: " · ")
    }
}

// MARK: the CLI forms

public extension CLI {
    /// `account pin <id> [--hard] --json` (F3: --hard stays even when the
    /// account is refused or needs a login).
    func pin(_ id: String, hard: Bool) -> Result<PinResult, CallError> {
        guard CLI.validID(id) else { return .failure(.refused("usage", "\"\(id)\" is not a valid account name.")) }
        return shaped(call(["account", "pin", id] + (hard ? ["--hard"] : []) + ["--json"]), "account pin", PinResult.init)
    }

    /// F4: a profile's folders, as one comma-separated value ("" clears).
    func profilePaths(_ profile: String, _ paths: [String]) -> Result<ProfileSetResult, CallError> {
        profileSet(profile, "paths", FolderList.value(paths))
    }

    /// F7: `history --usage --days N --json`.
    func usageHistory(days: Int = HistoryChart.days) -> Result<UsageHistory, CallError> {
        shaped(call(["history", "--usage", "--days=\(days)", "--json"]), "history --usage", UsageHistory.init)
    }

    /// F12: `doctor --json`. It exits non-zero when a check fails, so its
    /// answer is read whatever the exit code.
    func doctor() -> Result<DoctorReport, CallError> {
        let r = runner.run(path, ["doctor", "--json"], timeout: 90)
        if r.timedOut { return .failure(.cli(.failed("`claudeswitch doctor` did not finish in time."))) }
        if let j = JSON(data: r.stdout) {
            if let d = DoctorReport(j) { return .success(d) }
            if let e = AppError(j) { return .failure(.app(e)) }
        }
        let err = r.stderrText
        if err.contains("flag provided but not defined") || err.contains("unknown command")
            || (r.exitCode == 2 && err.contains("usage")) {
            return .failure(.cli(.tooOld(version: version() ?? "this version")))
        }
        if r.exitCode == 0 {
            return .failure(.cli(.unreadable("`claudeswitch doctor --json` printed something this app cannot read.")))
        }
        return .failure(.cli(.failed(Self.tidy(err, fallback: "`claudeswitch doctor` failed (exit \(r.exitCode))."))))
    }

    /// F12: runs a fix that is a command; its answer is any JSON object.
    func runFix(_ fix: FixAction) -> Result<JSON, CallError> {
        guard let argv = fix.argv else { return .failure(.refused("usage", "That fix is not a command.")) }
        return call(argv, timeout: 90)
    }

    private func shaped<T>(_ r: Result<JSON, CallError>, _ what: String, _ make: (JSON) -> T?) -> Result<T, CallError> {
        r.flatMap { j in
            guard let v = make(j) else {
                return .failure(.cli(.unreadable("`claudeswitch \(what)` answered in a shape this app cannot read.")))
            }
            return .success(v)
        }
    }
}
