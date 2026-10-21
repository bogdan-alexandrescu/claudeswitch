import Foundation

// Lane 15: `account list --json` and `profile remove --json`
// (docs/APP_CLI.md). Lenient like every reader here: unknown keys are
// ignored, a missing or mistyped value is unknown.

private func text(_ j: JSON) -> String? {
    j.string.flatMap { $0.isEmpty ? nil : $0 }
}

/// The last reading of an account, in brief.
public struct AccountReading: Equatable {
    public var fiveHour: Double?
    public var sevenDay: Double?
    /// five_hour or seven_day: the window in the most trouble.
    public var binding: String?
    public var at: Date?
    public var error: String?
    public var refusedUntil: Date?
    public var refusedWindow: String?

    init?(_ j: JSON) {
        guard j.raw is [String: Any] else { return nil }
        fiveHour = j["five_hour"].double
        sevenDay = j["seven_day"].double
        binding = text(j["binding"])
        at = j["at"].date
        error = text(j["error"])
        refusedUntil = j["refused_until"].date
        refusedWindow = text(j["refused_window"])
    }
}

/// One account of `account list --json`: config and state alone, no
/// keychain, so the app reads it on every refresh.
public struct AccountInfo: Equatable, Identifiable {
    public var id: String
    public var email: String?
    /// "Max 20x"; nil until the CLI or the daemon recorded one.
    public var plan: String?
    public var seat: String?
    public var enabled: Bool
    /// The first profile that may use it.
    public var profile: String?
    /// The pool that lists it, a [[pool]] or a profile's own (shared
    /// pools); nil in none (D6), and from an older CLI.
    public var pool: String?
    /// Every profile that may use it; nil from a CLI that predates shared
    /// pools.
    public var profiles: [String]?
    /// The profile it is live in.
    public var activeIn: String?
    public var pinned: Bool
    /// F3: pinned with --hard (stays even when refused); nil from a CLI
    /// that predates the pin safety valve.
    public var pinHard: Bool?
    public var refreshExpiresAt: Date?
    public var accessExpiresAt: Date?
    /// available, reserved, refused, needs_login or unknown (or a word a
    /// newer CLI adds).
    public var state: String
    public var reading: AccountReading?

    init?(_ j: JSON) {
        guard let id = j["id"].string else { return nil }
        self.id = id
        email = text(j["email"])
        plan = text(j["plan"])
        seat = text(j["seat"])
        enabled = j["enabled"].bool ?? true
        profile = text(j["profile"])
        pool = text(j["pool"])
        profiles = (j.raw as? [String: Any])?.keys.contains("profiles") == true
            ? j["profiles"].array.compactMap(\.string) : nil
        activeIn = text(j["active_in"])
        pinned = j["pinned"].bool ?? false
        pinHard = j["pin_hard"].bool
        refreshExpiresAt = j["refresh_expires_at"].date
        accessExpiresAt = j["access_expires_at"].date
        state = j["state"].string ?? "unknown"
        reading = AccountReading(j["reading"])
    }

    /// The Plan column: the plan, or a dash when none was recorded.
    public var planLabel: String { plan ?? "—" }
}

/// `account list --json`.
public struct AccountList: Equatable {
    public var accounts: [AccountInfo]

    init?(_ j: JSON) {
        // Go writes an empty list as null, so the key alone identifies it.
        guard let o = j.raw as? [String: Any], o.keys.contains("accounts") else { return nil }
        accounts = j["accounts"].array.compactMap(AccountInfo.init)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func account(_ id: String) -> AccountInfo? { accounts.first { $0.id == id } }
}

/// `profile remove --json`.
public struct ProfileRemoved: Equatable {
    public var profile: String
    /// Where its accounts went; nil when it had none to move.
    public var to: String?
    public var moved: [String]
    /// The guard on its old credential, when an account was live there.
    public var ghost: Ghost?
    /// Not deleted: the folder (as written; nil is ~/.claude) and the
    /// keychain item the daemon last resolved for it.
    public var keptDir: String?
    public var keptCredential: String?
    public var daemonRunning: Bool
    /// Whether a running daemon loaded the change in time; nil with none.
    public var daemonLoaded: Bool?
    public var pools: [String: [String]]

    init?(_ j: JSON) {
        guard let p = j["profile"].string else { return nil }
        profile = p
        to = text(j["to"])
        moved = j["moved"].array.compactMap(\.string)
        ghost = Ghost(j["ghost"])
        keptDir = text(j["kept"]["dir"])
        keptCredential = text(j["kept"]["credential"])
        daemonRunning = j["daemon_running"].bool ?? false
        daemonLoaded = j["daemon_loaded"].bool
        pools = j["pools"].object.mapValues { $0.array.compactMap(\.string) }
    }
}

/// What the Profiles pane's "Remove profile…" sheet offers.
public enum ProfileRemoval {
    /// A declared profile can go while another remains.
    public static func canRemove(_ list: ProfileList, _ name: String) -> Bool {
        list.profile(name)?.declared == true && list.profiles.filter(\.declared).count > 1
    }

    /// Where its accounts may go: every other declared profile.
    public static func destinations(_ list: ProfileList, removing name: String) -> [String] {
        list.profiles.filter { $0.declared && $0.name != name }.map(\.name)
    }

    /// The choice the sheet starts on: `default` (where the CLI sends them
    /// without --to, D6), except for `default` itself, whose accounts need a
    /// choice.
    public static func suggested(_ list: ProfileList, removing name: String) -> String? {
        guard name != "default" else { return nil }
        return destinations(list, removing: name).contains("default") ? "default" : nil
    }

    /// The accounts that move when it goes (`profile remove`'s `moved`):
    /// its own pool's, and for default the accounts in no pool (D6). The
    /// [[pool]] blocks it lists stay, for any other profile that lists them.
    /// Without the pools (an older CLI), every account it uses.
    public static func ownAccounts(_ p: ProfileInfo, pools: PoolList?) -> [String] {
        guard let l = pools else { return p.pool }
        var out = l.pool(p.name).flatMap { $0.own ? $0.accounts : nil } ?? []
        if p.name == StateFile.defaultProfile { out += l.unlisted }
        return out
    }

    /// Whether the sheet asks where its accounts go: not when it has none.
    public static func needsDestination(_ p: ProfileInfo, pools: PoolList? = nil) -> Bool {
        !ownAccounts(p, pools: pools).isEmpty
    }

    /// The sheet's explanation, before the CLI's own confirmation.
    public static func summary(_ p: ProfileInfo, to: String, pools: PoolList? = nil) -> String {
        let own = ownAccounts(p, pools: pools)
        let listed = pools.flatMap { l in ProfilePools.shown(p, pools: l)?.filter { l.pool($0)?.own == false } } ?? []
        let stay = "the pools it uses (\(listed.joined(separator: ", "))) stay"
        var s: String
        switch (own.isEmpty, listed.isEmpty) {
        case (true, true): s = "It has no accounts."
        case (true, false): s = "It has no accounts of its own; \(stay)."
        case (false, true): s = "Its accounts (\(own.joined(separator: ", "))) move to \(to)."
        case (false, false): s = "Its own accounts (\(own.joined(separator: ", "))) move to \(to); \(stay)."
        }
        s += " Its folder \(p.dir ?? "~/.claude") and its saved login stay on disk; nothing is deleted."
        if let live = p.live {
            s += " \(live) may still be signed in there, so it stays guarded: no other profile uses it"
                + " until that login is gone, or you forget the old profile."
        }
        return s
    }
}

// MARK: alerts and banners, per surface

/// Where an action was started: the menu-bar popover or the Settings
/// window. Its alert or banner shows there only, never in both.
public enum Surface: String, CaseIterable {
    case popover, settings
}

/// One pending item (an alert, a banner) per surface.
public struct Inbox<Item: Equatable>: Equatable {
    private var items: [Surface: Item] = [:]

    public init() {}

    /// Shows item on that surface; nil clears it.
    public mutating func post(_ item: Item?, to surface: Surface) {
        items[surface] = item
    }

    public func item(for surface: Surface) -> Item? { items[surface] }

    /// How many surfaces show something.
    public var count: Int { items.count }
}
