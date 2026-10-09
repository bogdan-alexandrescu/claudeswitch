import Foundation

// F6: Shortcuts and a hotkey. App Intents need Xcode's metadata processor
// (appintentsmetadataprocessor), which the Command Line Tools do not ship,
// so the app registers a URL scheme instead — claudeswitch://switch-best,
// pin, unpin, status, each with an optional ?profile= — usable from
// Shortcuts ("Open URL"), Raycast and `open` — and a global hotkey for
// Switch to best on the followed profile.

public enum ShortcutCommand: Equatable {
    case switchBest(profile: String?)
    case pin(profile: String?)
    case unpin(profile: String?)
    case status(profile: String?)

    public var verb: String {
        switch self {
        case .switchBest: return "switch-best"
        case .pin: return "pin"
        case .unpin: return "unpin"
        case .status: return "status"
        }
    }

    /// The profile named, or nil for the followed one.
    public var profile: String? {
        switch self {
        case .switchBest(let p), .pin(let p), .unpin(let p), .status(let p): return p
        }
    }
}

public enum ShortcutURL {
    public static let scheme = "claudeswitch"

    /// The command a claudeswitch:// URL asks for; nil for any other URL,
    /// an unknown verb, or a profile that is not a name.
    public static func parse(_ url: URL) -> ShortcutCommand? {
        guard url.scheme?.lowercased() == scheme,
              let parts = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let host = parts.host?.lowercased() else { return nil }
        var profile: String?
        if let p = parts.queryItems?.first(where: { $0.name == "profile" })?.value, !p.isEmpty {
            guard CLI.validID(p) else { return nil }
            profile = p
        }
        switch host {
        case "switch-best": return .switchBest(profile: profile)
        case "pin": return .pin(profile: profile)
        case "unpin": return .unpin(profile: profile)
        case "status": return .status(profile: profile)
        default: return nil
        }
    }

    public static func url(_ c: ShortcutCommand) -> URL {
        var parts = URLComponents()
        parts.scheme = scheme
        parts.host = c.verb
        if let p = c.profile { parts.queryItems = [URLQueryItem(name: "profile", value: p)] }
        return parts.url!
    }
}

/// The modifier keys of a hotkey.
public struct HotKeyModifiers: OptionSet, Hashable {
    public let rawValue: UInt32
    public init(rawValue: UInt32) { self.rawValue = rawValue }

    // Carbon's values (Events.h), so rawValue is RegisterEventHotKey's.
    public static let command = HotKeyModifiers(rawValue: 0x0100)
    public static let shift = HotKeyModifiers(rawValue: 0x0200)
    public static let option = HotKeyModifiers(rawValue: 0x0800)
    public static let control = HotKeyModifiers(rawValue: 0x1000)

    /// In the order macOS writes them: ⌃⌥⇧⌘.
    public var symbols: String {
        var s = ""
        if contains(.control) { s += "⌃" }
        if contains(.option) { s += "⌥" }
        if contains(.shift) { s += "⇧" }
        if contains(.command) { s += "⌘" }
        return s
    }
}

/// A global hotkey: a virtual key code and its modifiers.
public struct HotKey: Equatable {
    public var keyCode: UInt32
    public var modifiers: HotKeyModifiers

    public init(keyCode: UInt32, modifiers: HotKeyModifiers) {
        self.keyCode = keyCode
        self.modifiers = modifiers.intersection([.command, .shift, .option, .control])
    }

    /// ⌥⌘S, suggested for Switch to best.
    public static let suggested = HotKey(keyCode: 1, modifiers: [.option, .command])

    public var display: String { modifiers.symbols + (KeyNames.name(keyCode) ?? "?") }
    public var carbonModifiers: UInt32 { modifiers.rawValue }

    /// A hotkey needs ⌘, ⌥ or ⌃ (shift alone would take a typed letter) and
    /// a key with a name.
    public var isUsable: Bool {
        !modifiers.intersection([.command, .option, .control]).isEmpty && KeyNames.name(keyCode) != nil
    }

    /// "<keyCode>:<modifiers>", for the app's defaults.
    public var stored: String { "\(keyCode):\(modifiers.rawValue)" }

    public init?(stored: String) {
        let parts = stored.split(separator: ":")
        guard parts.count == 2, let k = UInt32(parts[0]), let m = UInt32(parts[1]) else { return nil }
        self.init(keyCode: k, modifiers: HotKeyModifiers(rawValue: m))
    }
}

/// Names of the ANSI virtual key codes (Carbon's kVK_*), for display.
public enum KeyNames {
    static let table: [UInt32: String] = {
        var t: [UInt32: String] = [:]
        let letters: [(UInt32, String)] = [
            (0, "A"), (1, "S"), (2, "D"), (3, "F"), (4, "H"), (5, "G"), (6, "Z"), (7, "X"), (8, "C"), (9, "V"),
            (11, "B"), (12, "Q"), (13, "W"), (14, "E"), (15, "R"), (16, "Y"), (17, "T"), (31, "O"), (32, "U"),
            (34, "I"), (35, "P"), (37, "L"), (38, "J"), (40, "K"), (45, "N"), (46, "M"),
            (18, "1"), (19, "2"), (20, "3"), (21, "4"), (23, "5"), (22, "6"), (26, "7"), (28, "8"), (25, "9"), (29, "0"),
            (24, "="), (27, "-"), (30, "]"), (33, "["), (39, "'"), (41, ";"), (42, "\\"), (43, ","), (44, "/"),
            (47, "."), (50, "`"),
            (36, "Return"), (48, "Tab"), (49, "Space"), (51, "Delete"), (53, "Escape"),
            (123, "←"), (124, "→"), (125, "↓"), (126, "↑"),
            (122, "F1"), (120, "F2"), (99, "F3"), (118, "F4"), (96, "F5"), (97, "F6"),
            (98, "F7"), (100, "F8"), (101, "F9"), (109, "F10"), (103, "F11"), (111, "F12"),
        ]
        for (k, n) in letters { t[k] = n }
        return t
    }()

    public static func name(_ keyCode: UInt32) -> String? { table[keyCode] }
}

/// One line for a profile's status: what the Status shortcut shows.
public enum StatusLine {
    public static func text(_ card: ProfileCard?) -> String {
        guard let c = card else { return "No profile to report on yet." }
        guard let a = c.active else { return "\(c.name) · no live account" }
        func pct(_ w: WindowReading?) -> String { w?.utilization.map { "\(Int($0.rounded()))%" } ?? "unknown" }
        var s = "\(c.name) · \(a.id) · session \(pct(a.fiveHour)), week \(pct(a.sevenDay))"
        if a.status != .available && a.status != .unknown { s += " · \(a.status.label)" }
        if c.pinned != nil { s += " · pinned" }
        return s
    }
}
