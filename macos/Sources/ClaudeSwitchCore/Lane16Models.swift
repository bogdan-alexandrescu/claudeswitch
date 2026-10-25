import Foundation

// Lane 16: the add-account sheet's source and target, the new-profile
// sheet (shared pools: the pools it uses), and the wording the QA pass asked
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

    /// The profile an existing account is added for: the first that may
    /// use it. The sheet shows where it is, not a choice, because signing an
    /// existing account in never moves it (the CLI ignores --profile for
    /// it). Nil for a new name.
    public static func lockedProfile(account: String, accounts: AccountList?, list: ProfileList?) -> String? {
        if let a = accounts?.account(account) { return a.profile ?? StateFile.defaultProfile }
        return list?.profiles.first { $0.pool.contains(account) }?.name
    }

    /// Where an existing account is: in a pool, which several profiles may
    /// use (shared pools), or in none (D6). With no [[profile]] blocks
    /// (`grouped` false) there are no pools to move it between, and the note
    /// says only where it is.
    public static func lockedNote(account: String, info: AccountInfo?, profile: String, grouped: Bool = true) -> String {
        guard grouped, let i = info, let profiles = i.profiles else { return "\(account) is already in \(profile)." }
        if let pool = i.pool {
            let who = profiles.isEmpty ? "which no profile uses"
                : "which \(spoken(profiles)) \(profiles.count == 1 ? "uses" : "use")"
            return "\(account) is in the pool \(pool), \(who). To change that, move it to another pool in Accounts."
        }
        let who = profiles.isEmpty ? "no profile uses it" : "\(spoken(profiles)) uses it"
        return "\(account) is in no pool: \(who). To change that, move it into a pool in Accounts."
    }

    /// The profiles an existing account is used by, as the sheet shows them.
    public static func usedBy(info: AccountInfo?, profile: String) -> String {
        guard let p = info?.profiles, !p.isEmpty else { return profile }
        return p.joined(separator: ", ")
    }

    /// The profile `add` names for an existing account: the source when it
    /// may use it (then the source keeps it until its own rotation, D34),
    /// else the first that may.
    public static func existingTarget(info: AccountInfo?, source: String, fallback: String) -> String {
        guard let p = info?.profiles, !p.isEmpty else { return fallback }
        return p.contains(source) ? source : p[0]
    }

    /// Saving one profile's login into another's accounts (owner decision):
    /// the CLI's own wording, shown before saving; nil for the same profile.
    /// When the source may use the account too (a pool both list), it keeps
    /// it until its own rotation moves on (D34).
    public static func crossProfileNote(source: String, target: String, sourceUsesIt: Bool = false) -> String? {
        guard source != target else { return nil }
        if sourceUsesIt {
            return "still signed in in \(source), which uses it too — \(target) can use it once \(source) rotates off it"
        }
        return "still signed in in \(source) — \(source) will move off it; \(target) can use it after"
    }

    /// Whether a new account added for `target` lands in a pool `source`
    /// uses too: it joins the target's own pool, or the first it lists (D20).
    public static func newAccountShared(source: String, target: String, list: ProfileList?, pools: PoolList?) -> Bool {
        guard let first = list?.profile(target)?.pools?.first else { return source == target }
        if let p = pools?.pool(first) { return p.profiles.contains(source) }
        return first == target ? source == target : false
    }

    /// Where a new account goes, in the sheet's words: the profile (its own
    /// pool), or the first pool it lists.
    public static func destination(target: String, list: ProfileList?, pools: PoolList?) -> String {
        guard let first = list?.profile(target)?.pools?.first, first != target else { return target }
        return "the pool \(first) (\(target)'s first)"
    }

    /// What to tell the person when the CLI keeps the account elsewhere
    /// than the profile chosen; nil when they agree. The sheet then shows
    /// the CLI's profile, never the chosen one.
    public static func mismatch(chosen: String, start: LoginStart) -> String? {
        guard let p = start.profile, p != chosen else { return nil }
        return "\(start.account) already exists: it stays in its pool, which \(p) uses. Move it to another pool in Accounts."
    }

    // MARK: organization (GROUND_TRUTH §48)

    /// The organizations the accounts already here are in, each named by
    /// its accounts: the CLI knows no organization names without reading
    /// the keychain. In the order of their first account by name.
    public static func organizations(_ accounts: AccountList?) -> [OrgChoice] {
        var ids: [String: [String]] = [:]
        var order: [String] = []
        for a in (accounts?.accounts ?? []).sorted(by: { $0.id < $1.id }) {
            guard let seat = a.seat, let at = seat.lastIndex(of: "@") else { continue }
            let org = String(seat[seat.index(after: at)...])
            guard !org.isEmpty else { continue }
            if ids[org] == nil { order.append(org) }
            ids[org, default: []].append(a.id)
        }
        return order.map { OrgChoice(id: $0, label: "Same as " + ids[$0]!.joined(separator: ", ")) }
    }

    /// The organization to ask for; nil leaves it to the browser.
    public static func org(_ pick: OrgPick, typed: String) -> String? {
        switch pick {
        case .browser: return nil
        case .known(let id): return id
        case .other:
            let t = typed.trimmingCharacters(in: .whitespacesAndNewlines)
            return validOrg(t) ? t : nil
        }
    }

    /// Why the typed organization cannot be asked for; nil when it can.
    public static func orgProblem(_ pick: OrgPick, typed: String) -> String? {
        guard case .other = pick, org(pick, typed: typed) == nil else { return nil }
        return "Paste the organization's id: letters, digits and dashes."
    }

    /// The CLI's own rule for --org: nothing that could carry another
    /// parameter into the sign-in URL.
    static func validOrg(_ s: String) -> Bool {
        !s.isEmpty && s.count <= 64 && s.unicodeScalars.allSatisfy {
            ("0"..."9").contains($0) || ("a"..."z").contains($0) || ("A"..."Z").contains($0) || $0 == "-"
        }
    }

    /// The sheet's own next step for a refusal whose CLI hint names a
    /// flag; nil keeps the CLI's. A duplicate with no organization asked
    /// for landed in the browser's current one.
    public static func appHint(code: String, asked: String?) -> String? {
        guard code == "already_vaulted", asked == nil else { return nil }
        return "The sign-in landed in the organization your browser is in. To add this person in "
            + "another organization, choose it under Organization and sign in again."
    }

    /// Whether a failed Finish spent the attempt, so the sheet opens again
    /// for another try. A refusal comes after the code was exchanged, and
    /// the CLI clears the attempt; `failed` may be a mistyped code.
    public static func startsOver(after code: String) -> Bool {
        ["already_vaulted", "wrong_account", "no_pending_login"].contains(code)
    }
}

/// An organization the sheet offers: its id and how it is shown.
public struct OrgChoice: Equatable, Hashable {
    public var id: String
    public var label: String

    public init(id: String, label: String) {
        self.id = id
        self.label = label
    }
}

/// What the sheet's Organization picker holds.
public enum OrgPick: Hashable {
    /// Whichever the browser is in: nothing asked for.
    case browser
    case known(String)
    /// One pasted by id.
    case other
}

// MARK: new profile (B4, shared pools)

/// What creating a profile takes: `profile create` with its own accounts
/// (`--pool`: accounts in no pool, which default stops using; with no
/// [[profile]] blocks yet, every account), then `profile pools` with the
/// pools chosen, in order (sharing them, nothing moves; its own pool
/// leads), then `profile seed` with one of their accounts.
public struct NewProfilePlan: Equatable {
    public var name: String
    public var pools: [String]
    public var own: [String] = []
    public var seed: String?

    public static func make(name: String, pools: [String], own: [String] = [], seed: String?, candidates: [String],
                            unlisted: [String]? = nil) -> NewProfilePlan {
        NewProfilePlan(name: name, pools: pools, own: own.filter { unlisted?.contains($0) ?? true },
                       seed: seed.flatMap { candidates.contains($0) ? $0 : nil })
    }

    /// The accounts it can sign in with: its own, then those of the pools
    /// chosen, in order, but one live in another profile (§3: one profile
    /// at a time).
    public static func seedCandidates(pools: [String], own: [String] = [], list: PoolList?,
                                      liveElsewhere: (String) -> String?) -> [String] {
        var out: [String] = []
        for id in own + pools.flatMap({ list?.pool($0)?.accounts ?? [] }) where !out.contains(id) && liveElsewhere(id) == nil {
            out.append(id)
        }
        return out
    }

    /// `profile seed` changes nothing of the profile but its login, so a
    /// timeout waiting for the daemon (daemon_not_loaded) leaves it created.
    public func notLoadedMessage(_ cli: String) -> String {
        "\(name) was created but not signed in: " + cli
    }
}

/// Success notes.
public enum Notes {}

public extension ProfileInfo {
    /// The profile a config with no [[profile]] blocks has.
    var implicit: Bool { !declared }
}

public enum ProfileText {
    /// The note beside a profile's name.
    public static func badge(_ p: ProfileInfo) -> String? {
        if p.implicit { return "No profiles yet" }
        return p.fromEnv ? "from CLAUDE_CONFIG_DIR" : nil
    }

    public static let createHint = "Create a profile to split accounts"
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
        "hold_for_claude_ai": "Hold for claude.ai",
        "clean": "Clean up on a schedule",
        "clean_every": "Every",
        "clean_after": "Only once unchanged for",
        "clean_worktrees": "Worktrees and branches",
        "clean_clones": "Lane clones",
        "clean_temp": "Temp files",
        "clean_transcripts_after": "Subagent transcripts after",
        "clean_remote_branches": "Branches on GitHub",
        "clean_keep": "Never remove",
        "clean_roots": "Also look in",
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
        case "bool": return v == "true" ? "on" : v == "false" ? "off" : v
        default: return v
        }
    }
}
