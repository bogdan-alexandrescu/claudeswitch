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
    public static func forgetGhost(profile: String, account: String) -> Confirmation {
        let (p, a) = (inQuotes(profile), inQuotes(account))
        return Confirmation(title: "Forget \(p)?",
                            message: "The last login of the old profile \(p) (\(a)) is no longer guarded: "
                                + "claudeswitch stops treating \(a) as possibly live there.",
                            action: "Forget", destructive: true)
    }

    public static func recoveryClear(slot: String, account: String?) -> Confirmation {
        Confirmation(title: "Clear the recovery copy \(inQuotes(slot))?",
                     message: "The kept credential in \(inQuotes(slot))" + (account.map { " (\(inQuotes($0)))" } ?? "")
                        + " is deleted. It cannot be restored afterwards.",
                     action: "Clear", destructive: true)
    }

    public static func recoveryRestore(slot: String, to account: String, itsAccount: String?) -> Confirmation {
        let (s, a) = (inQuotes(slot), inQuotes(account))
        var m = "The credential kept in \(s) replaces the stored credential of \(a)."
        var destructive = false
        if let its = itsAccount, its != account {
            m += " The copy belongs to \(inQuotes(its)): restoring it to another account stores the login of "
                + "\(inQuotes(its)) under \(a)."
            destructive = true
        }
        return Confirmation(title: "Restore \(s) to \(a)?", message: m, action: "Restore",
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
