import Foundation

// Claude in Chrome (lane 13, C2; docs/APP_CLI.md "chrome"): an account's own
// Chrome profile, a profile's Chrome profile, or Chrome's last used, opened
// by the CLI. The app never reads Chrome's files itself.

private func text(_ j: JSON) -> String? { j.string.flatMap { $0.isEmpty ? nil : $0 } }

/// One account's own Chrome profile (`chrome list --json`).
public struct ChromeProfile: Equatable, Identifiable {
    public var id: String { account }
    public var account: String
    public var profileDir: String
    public var added: Date?
    /// The Claude Code profiles the account is live in.
    public var liveIn: [String]
    /// C2: a Chrome profile the person already had (`add --existing`).
    public var existing: Bool
    /// Chrome's display name for it, when Chrome lists it.
    public var name: String?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profileDir = j["profile_dir"].string ?? ""
        added = j["added"].date
        liveIn = j["live_in"].array.compactMap(\.string)
        existing = j["existing"].bool ?? false
        name = text(j["name"])
    }

    /// How it is named to a person: Chrome's name, else the folder.
    public var label: String { name ?? profileDir }
}

/// A profile's live account and the Chrome profile it resolves to (C2):
/// its own mapping (`account`), the profile's (`profile`), or Chrome's last
/// used (`last_used`); `none` when nothing is known.
public struct ChromeLive: Equatable {
    public var profile: String
    public var account: String
    public var profileDir: String?
    public var name: String?
    public var rule: String
    /// The account the last rotation in this profile came from, and when.
    public var lastFrom: String?
    public var lastSwitch: Date?

    public init(profile: String, account: String, profileDir: String?, name: String?, rule: String,
                lastFrom: String?, lastSwitch: Date?) {
        self.profile = profile
        self.account = account
        self.profileDir = profileDir
        self.name = name
        self.rule = rule
        self.lastFrom = lastFrom
        self.lastSwitch = lastSwitch
    }

    init?(_ j: JSON) {
        guard let p = j["profile"].string, let a = j["account"].string else { return nil }
        self.init(profile: p, account: a, profileDir: text(j["profile_dir"]), name: text(j["name"]),
                  rule: j["rule"].string ?? "none", lastFrom: text(j["last_from"]), lastSwitch: j["last_switch"].date)
    }

    public var label: String { name ?? profileDir ?? "Chrome" }

    /// The card's notice (C2): the live account's Chrome profile comes from
    /// its profile, not a mapping of its own, and the last rotation changed
    /// the account, so Claude in Chrome there is still signed in as the one
    /// before. Nil once dismissed for this rotation.
    public func signInNotice(dismissed: Set<String>) -> ChromeSignInNotice? {
        guard rule == "profile", let old = lastFrom, old != account else { return nil }
        let n = ChromeSignInNotice(profile: profile, chromeName: label, old: old, account: account,
                                   key: [profile, account, old, lastSwitch.map { String(Int($0.timeIntervalSince1970)) } ?? ""]
                                       .joined(separator: "|"))
        return dismissed.contains(n.key) ? nil : n
    }
}

/// "Claude in Chrome in "Work" is still signed in as work-1", with its button.
public struct ChromeSignInNotice: Equatable {
    public var profile: String
    public var chromeName: String
    public var old: String
    public var account: String
    /// One rotation's identity, for dismissing it.
    public var key: String

    public var text: String { "Claude in Chrome in \"\(chromeName)\" is still signed in as \(old)" }
    public var button: String { "Sign in as \(account)" }
}

/// An account's Chrome choice (Settings → Accounts).
public enum AccountChrome: Equatable {
    /// No mapping: the profile's Chrome profile, or Chrome's last used.
    case sameAsProfile
    /// One of the person's own Chrome profiles.
    case existing(folder: String, name: String?)
    /// One claudeswitch created (`chrome add`).
    case created(folder: String)
}

public struct ChromeList: Equatable {
    public var profiles: [ChromeProfile]
    /// Whether this platform can open Chrome.
    public var supported: Bool
    /// C2: per profile with a live account, its resolved Chrome profile.
    public var live: [ChromeLive]

    init?(_ j: JSON) {
        guard j["supported"].bool != nil || j["chrome_profiles"].raw is [Any] else { return nil }
        profiles = j["chrome_profiles"].array.compactMap(ChromeProfile.init)
        supported = j["supported"].bool ?? false
        live = j["live"].array.compactMap(ChromeLive.init)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func has(_ account: String) -> Bool { profiles.contains { $0.account == account } }

    public func mapping(_ account: String) -> ChromeProfile? { profiles.first { $0.account == account } }

    public func live(in profile: String) -> ChromeLive? { live.first { $0.profile == profile } }

    public func choice(for account: String) -> AccountChrome {
        guard let m = mapping(account) else { return .sameAsProfile }
        return m.existing ? .existing(folder: m.profileDir, name: m.name) : .created(folder: m.profileDir)
    }
}

/// One Chrome profile from Chrome's Local State (`chrome profiles --json`).
public struct ChromeLocalProfile: Equatable, Identifiable {
    public var id: String { folder }
    public var folder: String
    public var name: String?
    public var lastUsed: Bool
    /// False for a folder claudeswitch names that Chrome does not list.
    public var inChrome: Bool
    public var usedByProfiles: [String]
    public var usedByAccounts: [String]

    init?(_ j: JSON) {
        guard let f = j["folder"].string else { return nil }
        folder = f
        name = text(j["name"])
        lastUsed = j["last_used"].bool ?? false
        inChrome = j["in_chrome"].bool ?? false
        usedByProfiles = j["used_by_profiles"].array.compactMap(\.string)
        usedByAccounts = j["used_by_accounts"].array.compactMap(\.string)
    }

    public var label: String { name ?? folder }
}

/// `chrome profiles --json`.
public struct ChromeProfiles: Equatable {
    public var profiles: [ChromeLocalProfile]
    public var lastUsed: String?
    /// Whether Chrome's Local State could be read.
    public var localState: Bool
    public var supported: Bool

    init?(_ j: JSON) {
        guard j["chrome_profiles"].raw is [Any] else { return nil }
        profiles = j["chrome_profiles"].array.compactMap(ChromeLocalProfile.init)
        lastUsed = text(j["last_used"])
        localState = j["local_state"].bool ?? false
        supported = j["supported"].bool ?? false
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    /// The ones a picker offers: Chrome's own.
    public var pickable: [ChromeLocalProfile] { profiles.filter(\.inChrome) }

    public func label(_ folder: String) -> String {
        profiles.first { $0.folder == folder }?.label ?? folder
    }

    /// Chrome's last-used profile by name, for "Chrome's last used (Work 2)".
    public var lastUsedLabel: String? { lastUsed.map(label) }
}

public struct ChromeAdded: Equatable {
    public var account: String
    public var profileDir: String
    /// False when the account already had a profile, opened again, and for
    /// `--existing`.
    public var created: Bool
    public var opened: Bool
    public var email: String?
    /// C2: mapped to one of the person's own Chrome profiles.
    public var existing: Bool
    public var name: String?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profileDir = j["profile_dir"].string ?? ""
        created = j["created"].bool ?? false
        opened = j["opened"].bool ?? false
        email = text(j["email"])
        existing = j["existing"].bool ?? false
        name = text(j["name"])
    }
}

public struct ChromeOpened: Equatable {
    public var account: String
    public var profileDir: String
    public var opened: Bool

    public init(account: String, profileDir: String, opened: Bool) {
        self.account = account
        self.profileDir = profileDir
        self.opened = opened
    }

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        self.init(account: a, profileDir: j["profile_dir"].string ?? "", opened: j["opened"].bool ?? false)
    }
}

/// `chrome signin --json`: the Chrome profile opened at the sign-in pages.
public struct ChromeSignedIn: Equatable {
    public var account: String
    public var profileDir: String
    public var name: String?
    public var rule: String
    public var opened: Bool
    public var email: String?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profileDir = j["profile_dir"].string ?? ""
        name = text(j["name"])
        rule = j["rule"].string ?? ""
        opened = j["opened"].bool ?? false
        email = text(j["email"])
    }

    public var label: String { name ?? profileDir }
}
