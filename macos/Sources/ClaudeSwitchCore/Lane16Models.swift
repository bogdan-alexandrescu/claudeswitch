import Foundation

// Lane 16: the add-account sheet's source and target, the new-profile
// sheet's moves, one verb for pool moves, and the wording the QA pass asked
// for. Pure, so the views stay thin and this is tested.

// MARK: adding an account (B2, B3, B9, B10)

/// The name `add` suggests for the login in a source profile. The sheet
/// asks again whenever the source changes; an answer for a source no longer
/// chosen is dropped, and a name the person typed is never replaced.
public struct NameSuggestion: Equatable {
    /// The source asked about last.
    public private(set) var source: String?
    /// The current source's suggested name, once it has answered.
    public private(set) var suggested: String?
    /// The name this put in the field, so it can be replaced while the
    /// person has not changed it.
    private var filled: String?
    private var token = 0

    public init() {}

    /// A probe of `source` starting; its answer carries the token.
    public mutating func probe(_ source: String) -> Int {
        token += 1
        self.source = source
        suggested = nil
        return token
    }

    /// An answer; returns what the name field should hold.
    public mutating func answer(token t: Int, hint: String?, name: String) -> String {
        guard t == token else { return name }
        let hint = hint.flatMap { $0.isEmpty ? nil : $0 }
        suggested = hint
        guard name.isEmpty || name == filled else { return name }
        filled = hint
        return hint ?? ""
    }
}

public enum AddForm {
    /// The profiles an account can join, in `profile list`'s order.
    public static func targets(_ list: ProfileList?) -> [String] {
        let names = list?.profiles.map(\.name) ?? []
        return names.isEmpty ? [StateFile.defaultProfile] : names
    }

    /// The profile the sheet starts on: the one asked for while it exists,
    /// else the first declared profile, else the implicit one. Never a
    /// profile that does not exist (QA: "default" with no default declared).
    public static func initialTarget(requested: String?, list: ProfileList?) -> String {
        let all = targets(list)
        if let r = requested, all.contains(r) { return r }
        return list?.profiles.first(where: \.declared)?.name ?? all[0]
    }

    /// The profile an existing account is in: the sheet shows it, locked,
    /// because signing an existing account in never moves it (the CLI
    /// ignores --profile for it). Nil for a new name.
    public static func lockedProfile(account: String, accounts: AccountList?, list: ProfileList?) -> String? {
        if let a = accounts?.account(account) { return a.profile ?? StateFile.defaultProfile }
        return list?.profiles.first { $0.pool.contains(account) }?.name
    }

    public static func lockedNote(account: String, profile: String) -> String {
        "\(account) is already in \(profile). Move it in Profiles."
    }

    /// Saving one profile's login into another's accounts (owner decision):
    /// the CLI's own wording, shown before saving; nil for the same profile.
    public static func crossProfileNote(source: String, target: String) -> String? {
        guard source != target else { return nil }
        return "still signed in in \(source) — \(source) will move off it; \(target) can use it after"
    }

    /// What to tell the person when the CLI keeps the account elsewhere
    /// than the profile chosen; nil when they agree. The sheet then shows
    /// the CLI's profile, never the chosen one.
    public static func mismatch(chosen: String, start: LoginStart) -> String? {
        guard let p = start.profile, p != chosen else { return nil }
        return "\(start.account) stays in \(p): an account that exists keeps its profile. Move it in Profiles."
    }
}

// MARK: new profile (B4)

/// What creating a profile with some accounts takes: those no pool lists
/// join at create (`--pool`); those another pool lists are moved there
/// after it exists (`pool remove <from> <id> --to <new>`), confirmed first.
public struct NewProfilePlan: Equatable {
    public var name: String
    public var pool: [String]
    public var moves: [PoolStep]
    public var seedAtCreate: String?
    public var seedAfter: String?

    public static func make(name: String, selected: [String], seed: String?, list: ProfileList?) -> NewProfilePlan {
        var pool: [String] = []
        var moves: [PoolStep] = []
        for id in selected {
            if let from = list?.lister(of: id), from != name {
                moves.append(.move(account: id, from: from, to: name))
            } else {
                pool.append(id)
            }
        }
        let s = seed.flatMap { selected.contains($0) ? $0 : nil }
        return NewProfilePlan(name: name, pool: pool, moves: moves,
                              seedAtCreate: s.flatMap { pool.contains($0) ? $0 : nil },
                              seedAfter: s.flatMap { pool.contains($0) ? nil : $0 })
    }

    /// Beside an account in the sheet: where it is now, when it has to be
    /// moved; nil when it is free to take.
    public static func note(for account: String, list: ProfileList?) -> String? {
        list?.lister(of: account).map { "in \($0) — moves here when created" }
    }

    /// `profile create` made the profile but timed out waiting for the
    /// daemon to seed it (daemon_not_loaded): nothing after it ran, so the
    /// moves did not either (review).
    public func notLoadedMessage(_ cli: String) -> String {
        var m = "\(name) was created but not signed in: " + cli
        let moved = moves.compactMap { s -> String? in
            if case .move(let a, _, _) = s { return a }
            return nil
        }
        if !moved.isEmpty {
            m += " " + moved.joined(separator: ", ") + (moved.count == 1 ? " was" : " were")
                + " not moved in: move " + (moved.count == 1 ? "it" : "them") + " from its row in Profiles."
        }
        return m
    }

    /// Asked before creating when accounts move.
    public var confirmation: Confirmation? {
        let moved = moves.compactMap { step -> (String, String)? in
            if case .move(let a, let from, _) = step { return (a, from) }
            return nil
        }
        guard !moved.isEmpty else { return nil }
        let names = moved.map(\.0).joined(separator: ", ")
        let detail = moved.map { "\($0.0) leaves \($0.1)" }.joined(separator: "; ")
        return Confirmation(title: "Create \(name) and move \(names)?",
                            message: detail + ". Only \(name) rotates to "
                                + (moved.count == 1 ? "it" : "them") + " from now on.",
                            action: "Create and move", destructive: false)
    }
}

// MARK: pool edits (B5, C)

public enum Notes {
    /// The success note of a pool edit.
    public static func pool(_ r: PoolResult) -> String {
        r.changed ? "\(r.account) is now in \(r.profile)" : "\(r.account) is already in \(r.profile)"
    }
}

public extension Confirm {
    /// One verb for every way into a profile: "Move a to work?", whether a
    /// pool lists the account (a move) or default holds it by D6 alone (an
    /// add). Nil when it is there already.
    static func poolStep(_ step: PoolStep) -> Confirmation? {
        switch step {
        case .already: return nil
        case .move(let a, let from, let to): return poolMove(account: a, from: from, to: to)
        case .add(let a, let to):
            return Confirmation(title: "Move \(a) to \(to)?",
                                message: "\(a) is in \(StateFile.defaultProfile) because no profile lists it. "
                                    + "It joins \(to)'s accounts: only \(to) rotates to it from now on.",
                                action: "Move", destructive: false)
        }
    }
}

public extension ProfileList {
    /// The profiles an account can be moved to.
    func moveTargets(for account: String) -> [String] {
        profiles.map(\.name).filter { step(adding: account, to: $0) != .already }
    }
}

public extension ProfileInfo {
    /// The profile a config with no [[profile]] blocks has.
    var implicit: Bool { !declared }

    /// In default's pool only because no profile lists it (D6).
    func isImplicitMember(_ account: String) -> Bool {
        guard let l = listed else { return false }
        return pool.contains(account) && !l.contains(account)
    }
}

public enum ProfileText {
    /// The note beside a profile's name.
    public static func badge(_ p: ProfileInfo) -> String? {
        if p.implicit { return "No profiles yet" }
        return p.fromEnv ? "from CLAUDE_CONFIG_DIR" : nil
    }

    public static let createHint = "Create a profile to split accounts"
}

/// A pool chip's text: trouble before the figure.
public enum Chip {
    public static func text(_ id: String, status: AccountStatus?, pct: Double?, error: String?) -> String {
        switch status {
        case .needsLogin?: return id + " · needs login"
        case .refused?: return id + " · refused"
        default: break
        }
        if error != nil { return id + " · error" }
        return pct.map { id + " · " + Format.pct($0) } ?? id
    }
}

// MARK: popover height

public enum PopoverLayout {
    /// Room kept free below the menu bar's window.
    static let margin: CGFloat = 40
    static let minimum: CGFloat = 320

    /// The tallest the popover may be on a screen this tall (its visible
    /// frame: menu bar and Dock excluded).
    public static func maxHeight(visible: CGFloat) -> CGFloat {
        max(minimum, visible - margin)
    }

    /// Whether content of this measured height needs a scroll view.
    public static func scrolls(content: CGFloat, visible: CGFloat) -> Bool {
        content > maxHeight(visible: visible)
    }
}

// MARK: settings wording

public enum SettingNames {
    static let labels = [
        "switch_at": "Switch at session",
        "switch_at_weekly": "Switch at week",
        "hard_floor": "Hard floor",
        "landing_margin": "Landing margin",
        "models": "Count model limits",
        "prefer": "Spend first",
    ]

    /// An enum setting's choices as a person reads them (F1); a choice not
    /// listed reads as itself.
    static let choices = [
        "prefer": ["room": "Most room", "expiring": "Expiring quota"],
    ]

    /// A setting's name for a person.
    public static func label(_ key: String) -> String {
        if let l = labels[key] { return l }
        let words = key.split(separator: "_").map(String.init)
        guard let first = words.first else { return key }
        return ([first.prefix(1).uppercased() + first.dropFirst()] + words.dropFirst()).joined(separator: " ")
    }

    /// The schema's description without its "advanced:" prefix, as a sentence.
    public static func description(_ s: SettingSchema) -> String {
        var d = s.description.trimmingCharacters(in: .whitespaces)
        if d.lowercased().hasPrefix("advanced:") { d = String(d.dropFirst("advanced:".count)).trimmingCharacters(in: .whitespaces) }
        return d.prefix(1).uppercased() + d.dropFirst()
    }

    /// A value to edit: a Go duration without its zero units, still in the
    /// syntax `config set` takes ("3m0s" → "3m", "1h0m0s" → "1h").
    public static func editable(_ s: SettingSchema, _ raw: String) -> String {
        guard s.type == "duration" else { return raw }
        var v = raw
        if v.hasSuffix("m0s") { v.removeLast(2) }
        if v.hasSuffix("h0m") { v.removeLast(2) }
        return v
    }

    /// A value as a person reads it: durations as "3m", percents with "%",
    /// an empty model list as "none" (the CLI: empty counts no model).
    public static func value(_ s: SettingSchema, _ raw: String) -> String {
        let v = raw.trimmingCharacters(in: .whitespaces)
        switch s.type {
        case "duration": return GoDuration.parse(v).map(Format.duration) ?? v
        case "percent": return v.isEmpty ? v : v + "%"
        case "list": return v.isEmpty ? "none" : v
        case "enum": return choices[s.key]?[v] ?? v
        default: return v
        }
    }
}
