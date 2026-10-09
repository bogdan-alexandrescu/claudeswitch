import Foundation

// F11: notification actions. The daemon notifies through osascript
// (`display notification`), which shows under Script Editor and cannot carry
// actions, so the app posts its own actionable notifications when it sees
// the change in state.json: a rotation carries Undo and Pin here; an account
// that needs a sign-in, or the live account being refused, carries Sign in.

/// An account's state as far as notifications care.
public enum NoticeStatus: Equatable {
    case ok, refused, needsLogin

    public init(_ s: AccountStatus) {
        switch s {
        case .needsLogin: self = .needsLogin
        case .refused: self = .refused
        default: self = .ok
        }
    }
}

public struct NoticeProfile: Equatable {
    public var name: String
    public var active: String?
    /// Each pool account's status.
    public var status: [String: NoticeStatus]

    public init(name: String, active: String?, status: [String: NoticeStatus]) {
        self.name = name
        self.active = active
        self.status = status
    }
}

/// What the notifications compare from one read to the next.
public struct NoticeState: Equatable {
    public var profiles: [NoticeProfile]

    public init(profiles: [NoticeProfile]) { self.profiles = profiles }

    public init(_ s: Snapshot) {
        profiles = s.cards.map { c in
            var st: [String: NoticeStatus] = [:]
            for a in c.accounts { st[a.id] = NoticeStatus(a.status) }
            if let a = c.active { st[a.id] = NoticeStatus(a.status) }
            return NoticeProfile(name: c.name, active: c.active?.id, status: st)
        }
    }

    func profile(_ name: String) -> NoticeProfile? { profiles.first { $0.name == name } }
}

/// Switches the app made itself (`use` from the popover, a shortcut, an
/// Undo): the state change they cause is not announced back.
public struct AppSwitches: Equatable {
    public static let window: TimeInterval = 120
    private var made: [String: (to: String, at: Date)] = [:]

    public init() {}

    public mutating func record(profile: String, to account: String, at: Date) {
        made[profile] = (account, at)
    }

    public func made(profile: String, to account: String, now: Date) -> Bool {
        guard let m = made[profile], m.to == account else { return false }
        return abs(now.timeIntervalSince(m.at)) <= Self.window
    }

    public static func == (a: AppSwitches, b: AppSwitches) -> Bool {
        a.made.mapValues { "\($0.to)@\($0.at.timeIntervalSince1970)" } == b.made.mapValues { "\($0.to)@\($0.at.timeIntervalSince1970)" }
    }
}

public enum NoticeCategory {
    public static let rotation = "xyz.claudeswitch.rotation"
    public static let signIn = "xyz.claudeswitch.signin"
    /// F5's re-login reminder (ReloginReminder); its notification
    /// identifier names the account.
    public static let relogin = "xyz.claudeswitch.relogin"

    public struct Action: Equatable {
        public var id: String
        public var title: String
    }

    /// Every category the app registers: one registration, one delegate.
    public static let all = [rotation, signIn, relogin]

    public static func actions(_ category: String) -> [Action] {
        switch category {
        case rotation: return [Action(id: NoticeAction.undo, title: "Undo"), Action(id: NoticeAction.pinHere, title: "Pin here")]
        case signIn: return [Action(id: NoticeAction.signIn, title: "Sign in")]
        case relogin: return [Action(id: NoticeAction.reloginSignIn, title: "Sign in…")]
        default: return []
        }
    }
}

public enum NoticeAction {
    public static let undo = "xyz.claudeswitch.undo"
    public static let pinHere = "xyz.claudeswitch.pin-here"
    public static let signIn = "xyz.claudeswitch.sign-in"
    /// F5's reminder action (its identifier as that lane shipped it).
    public static let reloginSignIn = "signin"
    /// UNNotificationDefaultActionIdentifier: the notification itself clicked.
    public static let defaultTap = "com.apple.UNNotificationDefaultActionIdentifier"
}

/// The app's one notification router: every category's actions, and the
/// clicked notification itself, to the command it runs.
public enum NoticeRouter {
    public static func command(category: String, action: String, identifier: String,
                               userInfo: [AnyHashable: Any]) -> NoticeCommand? {
        switch category {
        case NoticeCategory.relogin:
            guard action == NoticeAction.reloginSignIn || action == NoticeAction.defaultTap,
                  let a = ReloginReminder.account(fromIdentifier: identifier) else { return nil }
            return .signIn(account: a, profile: nil)
        case NoticeCategory.signIn:
            return Notices.command(action: action == NoticeAction.defaultTap ? NoticeAction.signIn : action,
                                   userInfo: userInfo)
        case NoticeCategory.rotation:
            return Notices.command(action: action, userInfo: userInfo)
        default:
            return nil
        }
    }
}

/// What an action runs.
public enum NoticeCommand: Equatable {
    /// `use <account> --profile <profile> --json`
    case use(account: String, profile: String)
    /// `account pin <account> --json`
    case pin(account: String)
    /// Add account → sign in again, for that account.
    case signIn(account: String, profile: String?)
}

public struct Notice: Equatable {
    public var id: String
    public var category: String
    public var title: String
    public var body: String
    /// The notification's userInfo: plain strings, read back by `command`.
    public var info: [String: String]
}

public enum Notices {
    /// The notices for what changed between two reads. None on the first
    /// read, none for a switch the app made, and only on a transition.
    public static func diff(old: NoticeState?, new: NoticeState, appSwitches: AppSwitches, now: Date) -> [Notice] {
        guard let old else { return [] }
        var out: [Notice] = []
        for p in new.profiles {
            guard let was = old.profile(p.name) else { continue }
            if let from = was.active, let to = p.active, from != to,
               !appSwitches.made(profile: p.name, to: to, now: now) {
                out.append(Notice(id: "rotation:\(p.name):\(to):\(Int(now.timeIntervalSince1970))",
                                  category: NoticeCategory.rotation, title: "\(p.name) → \(to)",
                                  body: "Rotated from \(from). Undo switches back; Pin here keeps \(to).",
                                  info: ["kind": "rotation", "profile": p.name, "from": from, "to": to]))
            }
            for (account, status) in p.status.sorted(by: { $0.key < $1.key }) {
                guard let before = was.status[account], before != status, status != .ok else { continue }
                let live = account == p.active
                // An idle account's refusal is rotation's business; a
                // credential that can no longer sign in is the person's.
                if status == .refused && !live { continue }
                let title = status == .needsLogin ? "\(account) needs a sign-in" : "\(account) was refused"
                let body = status == .needsLogin
                    ? "Its credential no longer signs in" + (live ? ", and it is live in \(p.name)." : ".")
                    : "It is live in \(p.name). Rotation moves off it; sign in again if it keeps happening."
                out.append(Notice(id: "signin:\(account):\(Int(now.timeIntervalSince1970))",
                                  category: NoticeCategory.signIn, title: title, body: body,
                                  info: ["kind": "signin", "profile": p.name, "account": account]))
            }
        }
        return out
    }

    /// The command an action runs, from the notification's userInfo; nil
    /// for an action the category does not carry, or ids that are not names.
    public static func command(action: String, info: [String: String]) -> NoticeCommand? {
        func id(_ k: String) -> String? { info[k].flatMap { CLI.validID($0) ? $0 : nil } }
        switch (info["kind"], action) {
        case ("rotation"?, NoticeAction.undo):
            guard let from = id("from"), let p = id("profile") else { return nil }
            return .use(account: from, profile: p)
        case ("rotation"?, NoticeAction.pinHere):
            return id("to").map { .pin(account: $0) }
        case ("signin"?, NoticeAction.signIn):
            return id("account").map { .signIn(account: $0, profile: id("profile")) }
        default:
            return nil
        }
    }

    /// `command`, from a UNNotificationResponse's userInfo.
    public static func command(action: String, userInfo: [AnyHashable: Any]) -> NoticeCommand? {
        var info: [String: String] = [:]
        for (k, v) in userInfo { if let k = k as? String, let v = v as? String { info[k] = v } }
        return command(action: action, info: info)
    }
}
