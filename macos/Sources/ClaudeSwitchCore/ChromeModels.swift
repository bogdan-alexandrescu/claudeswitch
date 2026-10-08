import Foundation

// Claude in Chrome (lane 13, docs/APP_CLI.md "chrome"): a Chrome profile per
// account, opened by the CLI.

/// One account's Chrome profile (`chrome list --json`).
public struct ChromeProfile: Equatable, Identifiable {
    public var id: String { account }
    public var account: String
    public var profileDir: String
    public var added: Date?
    /// The Claude Code profiles the account is live in.
    public var liveIn: [String]

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profileDir = j["profile_dir"].string ?? ""
        added = j["added"].date
        liveIn = j["live_in"].array.compactMap(\.string)
    }
}

public struct ChromeList: Equatable {
    public var profiles: [ChromeProfile]
    /// Whether this platform can open Chrome.
    public var supported: Bool

    init?(_ j: JSON) {
        guard j["supported"].bool != nil || j["chrome_profiles"].raw is [Any] else { return nil }
        profiles = j["chrome_profiles"].array.compactMap(ChromeProfile.init)
        supported = j["supported"].bool ?? false
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func has(_ account: String) -> Bool { profiles.contains { $0.account == account } }
}

public struct ChromeAdded: Equatable {
    public var account: String
    public var profileDir: String
    /// False when the account already had a profile, opened again.
    public var created: Bool
    public var opened: Bool
    public var email: String?

    init?(_ j: JSON) {
        guard let a = j["account"].string else { return nil }
        account = a
        profileDir = j["profile_dir"].string ?? ""
        created = j["created"].bool ?? false
        opened = j["opened"].bool ?? false
        email = j["email"].string.flatMap { $0.isEmpty ? nil : $0 }
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
