import Foundation

/// The text of a confirmation the app asks before an action that moves an
/// account, drops a guard, overwrites a credential or turns real swaps on.
public struct Confirmation: Equatable {
    public var title: String
    public var message: String
    /// The confirming button's label.
    public var action: String
    public var destructive: Bool
}

public enum Confirm {
    public static func poolRemove(account: String, from: String) -> Confirmation {
        Confirmation(title: "Remove \(account) from \(from)?",
                     message: "\(account) goes back to \(StateFile.defaultProfile)'s accounts, and "
                        + "\(from) stops rotating to it.",
                     action: "Remove", destructive: true)
    }

    public static func poolMove(account: String, from: String, to: String) -> Confirmation {
        Confirmation(title: "Move \(account) to \(to)?",
                     message: "\(account) leaves \(from)'s accounts and joins \(to)'s: only \(to) rotates to it from now on.",
                     action: "Move", destructive: false)
    }

    public static func forgetGhost(profile: String, account: String) -> Confirmation {
        Confirmation(title: "Forget \(profile)?",
                     message: "The last login of the old profile \(profile) (\(account)) is no longer guarded: "
                        + "claudeswitch stops treating \(account) as possibly live there.",
                     action: "Forget", destructive: true)
    }

    public static func recoveryClear(slot: String, account: String?) -> Confirmation {
        Confirmation(title: "Clear recovery copy \(slot)?",
                     message: "The kept credential in \(slot)" + (account.map { " (\($0))" } ?? "")
                        + " is deleted. It cannot be restored afterwards.",
                     action: "Clear", destructive: true)
    }

    public static func recoveryRestore(slot: String, to account: String, itsAccount: String?) -> Confirmation {
        var m = "The credential kept in \(slot) replaces \(account)'s stored credential."
        var destructive = false
        if let its = itsAccount, its != account {
            m += " The copy belongs to \(its): restoring it to another account stores \(its)'s login under \(account)."
            destructive = true
        }
        return Confirmation(title: "Restore \(slot) to \(account)?", message: m, action: "Restore",
                            destructive: destructive)
    }

    public static func daemonLive(install: Bool) -> Confirmation {
        Confirmation(title: install ? "Install the daemon live?" : "Turn the daemon live?",
                     message: "Live, the daemon switches accounts for real when one nears its limit, "
                        + "in every profile. Dry run only reports what it would do.",
                     action: install ? "Install live" : "Go live", destructive: false)
    }
}

/// The menu-bar item's content, chosen so it is never empty.
public enum MenuBarIcon {
    /// Fallbacks, in order, when a symbol is missing on this system.
    static let fallbacks = ["gauge", "circle.fill"]

    public static func preferred(_ t: MenuTitle) -> String {
        if t.problem { return "questionmark.circle" }
        switch t.level {
        case .ok: return "gauge.with.dots.needle.33percent"
        case .near: return "gauge.with.dots.needle.67percent"
        case .over: return "exclamationmark.triangle.fill"
        }
    }

    /// The first symbol this system has, or nil (then text is drawn).
    public static func symbol(_ t: MenuTitle, available: (String) -> Bool) -> String? {
        ([preferred(t)] + fallbacks).first(where: available)
    }

    /// The text beside the symbol; "cs" when there would be nothing else.
    public static func text(_ t: MenuTitle, symbol: String?) -> String {
        if symbol == nil && t.text.isEmpty { return "cs" }
        return t.text
    }
}
