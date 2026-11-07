import CryptoKit
import Foundation

// Settings → Claude Code (the owner's picks, 2026-10-28): CC1 A, one page
// with a folding row per kind of thing Claude Code reads; CC2 B, files open
// in their editor or in the app's own (Edit…), folders reveal in Finder,
// plugins get on/off switches; CC3 B, a "Showing" picker scopes the pane to
// one profile, with its Shared / Its own badges and actions there, and
// Settings → Profiles says it in one line. Never shown: settings.json `env`
// values, MCP servers' arguments, headers and URL paths. The models below
// carry none of them, so no view can show one.

/// The five things a profile can share with ~/.claude, in the CLI's order.
public enum SetupItemName: String, CaseIterable, Equatable {
    case instructions = "CLAUDE.md", settings = "settings.json", skills, commands, agents

    /// A file the app opens and edits; the others are folders.
    public var isFile: Bool { self == .instructions || self == .settings }

    public var kind: SetupKind {
        switch self {
        case .instructions: return .instructions
        case .settings: return .settings
        case .skills: return .skills
        case .commands: return .commands
        case .agents: return .agents
        }
    }
}

/// How a profile has an item: a link to ~/.claude's, a link elsewhere, its
/// own file or folder, or none.
public enum ShareState: String, Equatable {
    case shared, elsewhere, own, missing

    public var badge: String? {
        switch self {
        case .shared: return "Shared"
        case .own: return "Its own"
        case .elsewhere: return "Linked elsewhere"
        case .missing: return nil
        }
    }

    /// What its row offers: a shared or elsewhere link becomes a copy, an own
    /// item goes back to the shared one (kept as a backup), a missing one
    /// links to ~/.claude's.
    public var action: ShareAction {
        switch self {
        case .shared, .elsewhere: return .makeOwn
        case .own: return .useShared
        case .missing: return .share
        }
    }
}

public enum ShareAction: Equatable {
    case makeOwn, useShared, share

    public var title: String {
        switch self {
        case .makeOwn: return "Make Own Copy"
        case .useShared: return "Use Shared…"
        case .share: return "Share"
        }
    }
}

/// The pane's rows, in its order.
public enum SetupKind: String, CaseIterable, Identifiable {
    case instructions, settings, skills, agents, commands, plugins, servers

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .instructions: return "Instructions"
        case .settings: return "Settings"
        case .skills: return "Skills"
        case .agents: return "Agents"
        case .commands: return "Commands"
        case .plugins: return "Plugins"
        case .servers: return "MCP Servers"
        }
    }

    public var symbol: String {
        switch self {
        case .instructions: return "doc.text"
        case .settings: return "gearshape"
        case .skills: return "wand.and.stars"
        case .agents: return "person.crop.square"
        case .commands: return "command"
        case .plugins: return "puzzlepiece.extension"
        case .servers: return "server.rack"
        }
    }

    /// The item a profile shares for it; plugins and servers are never links.
    public var item: SetupItemName? {
        switch self {
        case .instructions: return .instructions
        case .settings: return .settings
        case .skills: return .skills
        case .agents: return .agents
        case .commands: return .commands
        case .plugins, .servers: return nil
        }
    }
}

/// A skill, agent or command: its name and its description's first line.
public struct SetupEntry: Equatable, Identifiable {
    public var name: String
    public var description: String
    public var id: String { name }

    init?(_ j: JSON) {
        guard let n = j["name"].string, !n.isEmpty else { return nil }
        name = n
        description = j["description"].string ?? ""
    }
}

public struct PluginEntry: Equatable, Identifiable {
    /// `name@marketplace`.
    public var id: String
    public var name: String
    public var marketplace: String
    public var version: String?
    public var enabled: Bool

    init?(_ j: JSON) {
        guard let id = j["id"].string, !id.isEmpty else { return nil }
        self.id = id
        let parts = id.split(separator: "@", maxSplits: 1).map(String.init)
        name = j["name"].string ?? parts.first ?? id
        marketplace = j["marketplace"].string ?? (parts.count > 1 ? parts[1] : "")
        version = j["version"].string.flatMap { $0.isEmpty ? nil : $0 }
        enabled = j["enabled"].bool ?? false
    }
}

/// An MCP server: its name, transport and where it runs, and nothing else.
public struct ServerEntry: Equatable, Identifiable {
    public var name: String
    public var type: String
    /// A URL's host or a command's name (`mcp.linear.example`, `npx`).
    public var target: String
    public var id: String { name }

    init?(_ j: JSON) {
        guard let n = j["name"].string, !n.isEmpty else { return nil }
        name = n
        type = j["type"].string ?? "stdio"
        target = Self.safeTarget(j["target"].string ?? "")
    }

    /// Only a host or a command's name, whatever the CLI sent: a URL's path,
    /// query and credentials, and a command's arguments, can hold secrets.
    public static func safeTarget(_ t: String) -> String {
        let s = t.trimmingCharacters(in: .whitespaces)
        if s.contains("://"), let u = URLComponents(string: s), let h = u.host, !h.isEmpty { return h }
        let word = s.split(separator: " ").first.map(String.init) ?? ""
        let bare = word.split(separator: "?").first.map(String.init) ?? ""
        return bare.isEmpty ? "" : (bare as NSString).lastPathComponent
    }
}

/// CLAUDE.md as a profile reads it.
public struct InstructionsFile: Equatable {
    /// The path in the profile's dir (a link, when shared).
    public var path: String
    /// What it resolves to: the file the editor reads and `claude write` writes.
    public var target: String
    public var lines: Int?
    public var modified: Date?

    init?(_ j: JSON) {
        guard let p = j["path"].string else { return nil }
        path = p
        target = j["target"].string ?? p
        lines = j["lines"].double.map { Int($0) }
        modified = j["modified"].date
    }
}

/// settings.json in figures: never a value from `env`, only its names.
public struct SettingsFile: Equatable {
    public var path: String
    public var target: String
    public var model: String?
    public var allow: Int
    public var deny: Int
    public var hooks: [String]
    public var statusLine: Bool
    /// The names under `env`; their values are never read.
    public var envNames: [String]
    /// The parse error's sentence, when it is not valid JSON.
    public var error: String?

    init?(_ j: JSON) {
        guard let p = j["path"].string else { return nil }
        path = p
        target = j["target"].string ?? p
        model = j["model"].string.flatMap { $0.isEmpty ? nil : $0 }
        allow = Int(j["allow"].double ?? 0)
        deny = Int(j["deny"].double ?? 0)
        hooks = j["hooks"].array.compactMap(\.string)
        statusLine = j["status_line"].bool ?? false
        // The contract sends names. Sent an object, only its keys are kept.
        envNames = j["env"].raw is [Any] ? j["env"].array.compactMap(\.string) : j["env"].keys
        error = j["error"].string.flatMap { $0.isEmpty ? nil : $0 }
    }
}

/// One of a profile's five items, as `claude show` reports it.
public struct ProfileItem: Equatable {
    public var item: SetupItemName
    /// nil for a state this app does not know: shown, never acted on.
    public var state: ShareState?
    public var summary: String

    init?(_ j: JSON) {
        guard let i = j["item"].string.flatMap(SetupItemName.init(rawValue:)) else { return nil }
        item = i
        state = j["state"].string.flatMap(ShareState.init(rawValue:))
        summary = j["summary"].string ?? ""
    }
}

/// A profile in `claude show`'s `profiles`.
public struct ProfileSetup: Equatable, Identifiable {
    public var name: String
    public var dir: String?
    /// Its dir is ~/.claude.
    public var base: Bool
    public var items: [ProfileItem]
    public var plugins: [PluginEntry]
    public var servers: Int?
    public var id: String { name }

    init?(_ j: JSON) {
        guard let n = j["name"].string else { return nil }
        name = n
        dir = j["dir"].string
        base = j["base"].bool ?? false
        items = j["items"].array.compactMap(ProfileItem.init)
        plugins = j["plugins"].array.compactMap(PluginEntry.init)
        servers = j["servers"].double.map { Int($0) }
    }

    public func state(_ item: SetupItemName) -> ShareState? { items.first { $0.item == item }?.state }
}

/// `claude show --json` (~/.claude and every profile), or `claude show
/// --profile <p> --json` (what one profile reads).
public struct ClaudeSetup: Equatable {
    public var dir: String
    /// The profile shown; nil for ~/.claude with every profile.
    public var profile: String?
    public var base: Bool
    public var shares: [SetupItemName: ShareState]
    public var instructions: InstructionsFile?
    public var settings: SettingsFile?
    public var skills: [SetupEntry]
    public var agents: [SetupEntry]
    public var commands: [SetupEntry]
    public var plugins: [PluginEntry]
    public var servers: [ServerEntry]
    public var profiles: [ProfileSetup]

    init?(_ j: JSON) {
        guard let d = j["dir"].string, !d.isEmpty else { return nil }
        dir = d
        profile = j["profile"].string.flatMap { $0.isEmpty ? nil : $0 }
        base = j["base"].bool ?? (profile == nil)
        var s: [SetupItemName: ShareState] = [:]
        for e in j["shares"].array {
            if let i = e["item"].string.flatMap(SetupItemName.init(rawValue:)),
               let st = e["state"].string.flatMap(ShareState.init(rawValue:)) { s[i] = st }
        }
        shares = s
        instructions = j["instructions"].isNull ? nil : InstructionsFile(j["instructions"])
        settings = j["settings"].isNull ? nil : SettingsFile(j["settings"])
        skills = j["skills"].array.compactMap(SetupEntry.init)
        agents = j["agents"].array.compactMap(SetupEntry.init)
        commands = j["commands"].array.compactMap(SetupEntry.init)
        plugins = j["plugins"].array.compactMap(PluginEntry.init)
        servers = j["servers"].array.compactMap(ServerEntry.init)
        profiles = j["profiles"].array.compactMap(ProfileSetup.init)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    /// The profile whose dir is ~/.claude.
    public var baseProfile: ProfileSetup? { profiles.first(where: \.base) }

    /// How the profile shown has an item; nil for ~/.claude itself.
    public func share(_ item: SetupItemName) -> ShareState? {
        guard profile != nil, !base else { return nil }
        return shares[item]
    }
}

// MARK: the pane's rows

/// One item under an unfolded row.
public struct SetupLine: Equatable, Identifiable {
    public var name: String
    public var detail: String
    /// A plugin's row carries its switch.
    public var plugin: PluginEntry?
    public var id: String { name + "\u{0}" + detail }
}

/// Open a file in its editor, or reveal a folder in Finder.
public enum SetupFileAction: Equatable {
    case open(String)
    case reveal(String)

    public var title: String {
        if case .open = self { return "Open" }
        return "Reveal"
    }
}

public struct SetupRow: Equatable, Identifiable {
    public var kind: SetupKind
    public var summary: String
    public var items: [SetupLine]
    /// Shown one profile: how it has this item (its badge and action).
    public var share: ShareState?
    /// CLAUDE.md and settings.json: Edit… opens the app's editor.
    public var edit: SetupItemName?
    public var file: SetupFileAction?
    public var id: String { kind.rawValue }
}

public enum SetupRows {
    /// The rows for a report: ~/.claude's with every profile, or one
    /// profile's, with its badges and actions.
    public static func build(_ s: ClaudeSetup, now: Date) -> [SetupRow] {
        let scoped = s.profile != nil
        let other = scoped && !s.base
        func path(_ name: String) -> String { (s.dir as NSString).appendingPathComponent(name) }
        func folder(_ kind: SetupKind, _ name: String, _ list: [SetupEntry], slash: Bool = false) -> SetupRow {
            let share = kind.item.flatMap(s.share)
            return SetupRow(kind: kind,
                            summary: share == .missing ? SetupText.notSetUp : SetupText.count(list.count, kind),
                            items: list.map { SetupLine(name: (slash ? "/" : "") + $0.name,
                                                        detail: $0.description.isEmpty ? SetupText.noDescription
                                                            : $0.description) },
                            share: share, edit: nil,
                            file: share == .missing ? nil : .reveal(path(name)))
        }
        var rows: [SetupRow] = []
        rows.append(SetupRow(kind: .instructions,
                             summary: SetupText.instructions(s.instructions, edited: !scoped, now: now),
                             items: [], share: s.share(.instructions), edit: .instructions,
                             file: s.instructions.map { .open($0.path) }))
        rows.append(SetupRow(kind: .settings, summary: SetupText.settings(s.settings),
                             items: SetupText.settingsLines(s.settings), share: s.share(.settings), edit: .settings,
                             file: s.settings.map { .open($0.path) }))
        rows.append(folder(.skills, "skills", s.skills))
        rows.append(folder(.agents, "agents", s.agents))
        rows.append(folder(.commands, "commands", s.commands, slash: true))
        rows.append(SetupRow(kind: .plugins, summary: SetupText.plugins(s.plugins),
                             items: s.plugins.map { p in
                                 SetupLine(name: p.name, detail: [p.marketplace, p.version].compactMap { $0 }
                                     .filter { !$0.isEmpty }.joined(separator: " · "), plugin: p)
                             },
                             share: nil, edit: nil, file: .reveal(path("plugins"))))
        rows.append(SetupRow(kind: .servers, summary: SetupText.servers(s.servers.count, copiedInto: other ? s.profile : nil),
                             items: s.servers.map { SetupLine(name: $0.name,
                                                              detail: $0.target.isEmpty ? $0.type : $0.type + " · " + $0.target) },
                             share: nil, edit: nil, file: nil))
        return rows
    }
}

// MARK: words

public enum SetupText {
    /// With a claudeswitch from before `claude show`.
    public static let minimumVersion = "0.8.20"
    public static let needsNewer = "This needs claudeswitch \(minimumVersion) or later."
    public static let notSetUp = "not set up"
    public static let noDescription = "No description"
    public static let everyProfile = "Every profile (~/.claude)"

    public static func subtitle(scope: String?) -> String {
        scope.map { "What Claude Code reads in the profile \($0)." }
            ?? "What Claude Code reads from ~/.claude, in every profile that shares it."
    }

    /// "7 skills", "1 agent", "none".
    public static func count(_ n: Int, _ kind: SetupKind) -> String {
        let word: String
        switch kind {
        case .skills: word = "skill"
        case .agents: word = "agent"
        case .commands: word = "command"
        case .servers: word = "server"
        default: word = "item"
        }
        return n == 0 ? "none" : "\(n) \(word)\(n == 1 ? "" : "s")"
    }

    /// "CLAUDE.md · 42 lines · edited 2 days ago" (one profile's: without
    /// the age, as its row has more buttons).
    public static func instructions(_ f: InstructionsFile?, edited: Bool, now: Date) -> String {
        guard let f else { return notSetUp }
        var parts = ["CLAUDE.md"]
        if let n = f.lines { parts.append("\(n) line\(n == 1 ? "" : "s")") }
        if edited, let m = f.modified { parts.append(editedAgo(m, now: now)) }
        return parts.joined(separator: " · ")
    }

    /// "edited 2 days ago".
    public static func editedAgo(_ at: Date, now: Date) -> String {
        let s = max(0, Int(now.timeIntervalSince(at)))
        func n(_ v: Int, _ unit: String) -> String { "edited \(v) \(unit)\(v == 1 ? "" : "s") ago" }
        if s < 60 { return "edited just now" }
        if s < 3600 { return n(s / 60, "minute") }
        if s < 86_400 { return n(s / 3600, "hour") }
        let d = s / 86_400
        if d < 14 { return n(d, "day") }
        if d < 60 { return n(d / 7, "week") }
        return n(d / 30, "month")
    }

    /// "model opus · 12 allowed, 2 denied · 2 hooks".
    public static func settings(_ f: SettingsFile?) -> String {
        guard let f else { return notSetUp }
        if let e = f.error { return "not valid JSON: " + e }
        var parts: [String] = []
        if let m = f.model { parts.append("model " + m) }
        let perms = [f.allow > 0 ? "\(f.allow) allowed" : nil, f.deny > 0 ? "\(f.deny) denied" : nil].compactMap { $0 }
        if !perms.isEmpty { parts.append(perms.joined(separator: ", ")) }
        if !f.hooks.isEmpty { parts.append("\(f.hooks.count) hook\(f.hooks.count == 1 ? "" : "s")") }
        return parts.isEmpty ? "no settings" : parts.joined(separator: " · ")
    }

    /// Settings unfolded: what it sets, the environment by count alone.
    public static func settingsLines(_ f: SettingsFile?) -> [SetupLine] {
        guard let f, f.error == nil else { return [] }
        let perms = [f.allow > 0 ? "\(f.allow) allowed" : nil, f.deny > 0 ? "\(f.deny) denied" : nil].compactMap { $0 }
        return [SetupLine(name: "Model", detail: f.model ?? "Claude Code's default"),
                SetupLine(name: "Permissions", detail: perms.isEmpty ? "none" : perms.joined(separator: ", ")),
                SetupLine(name: "Hooks", detail: f.hooks.isEmpty ? "none" : f.hooks.joined(separator: ", ")),
                SetupLine(name: "Status line", detail: f.statusLine ? "set" : "not set"),
                SetupLine(name: "Environment", detail: environment(f.envNames.count))]
    }

    /// "3 variables, values hidden": never a value, nor even a name.
    public static func environment(_ n: Int) -> String {
        n == 0 ? "none" : n == 1 ? "1 variable, value hidden" : "\(n) variables, values hidden"
    }

    /// "4 installed, 3 on".
    public static func plugins(_ p: [PluginEntry]) -> String {
        p.isEmpty ? "none installed" : "\(p.count) installed, \(p.filter(\.enabled).count) on"
    }

    /// "3 servers"; one profile's: "3 servers, copied when work was made".
    public static func servers(_ n: Int, copiedInto profile: String?) -> String {
        guard n > 0 else { return "none" }
        return count(n, .servers) + (profile.map { ", copied when \($0) was made" } ?? "")
    }

    /// "a", "a and b", "a, b and c" (or "a, b or c").
    public static func list(_ names: [String], _ conj: String = "and") -> String {
        switch names.count {
        case 0: return ""
        case 1: return names[0]
        default: return names.dropLast().joined(separator: ", ") + " \(conj) " + names[names.count - 1]
        }
    }

    /// Under the list, with every profile shown: who reads ~/.claude and
    /// what each other profile keeps of its own. `picker`: the Showing
    /// picker is there to name.
    public static func footer(_ profiles: [ProfileSetup], picker: Bool) -> String? {
        guard !profiles.isEmpty else { return nil }
        let sharers = profiles.filter { p in p.base || p.items.contains { $0.state == .shared } }
            .map { $0.base ? "\($0.name) (it is ~/.claude)" : $0.name }
        var first = sharers.isEmpty ? "No profile shares it"
            : (sharers.count == 1 ? "Read by " : "Shared by ") + list(sharers)
        let owns = profiles.filter { !$0.base }.compactMap { p -> String? in
            let own = p.items.filter { $0.state == .own }.map(\.item.rawValue)
            return own.isEmpty ? nil : "\(p.name) keeps its own \(list(own))"
        }
        if !owns.isEmpty { first += "; " + owns.joined(separator: "; ") }
        return first + "." + (picker ? " Showing picks one profile's setup." : "")
    }

    /// Settings → Profiles' one line for a profile: "Shares CLAUDE.md,
    /// skills and commands; its own settings.json; no agents."
    public static func profileLine(_ p: ProfileSetup) -> String {
        if p.base { return "This profile is ~/.claude: the setup every other profile can share." }
        func names(_ s: ShareState) -> [String] { p.items.filter { $0.state == s }.map(\.item.rawValue) }
        var parts: [String] = []
        if !names(.shared).isEmpty { parts.append("shares " + list(names(.shared))) }
        if !names(.own).isEmpty { parts.append("its own " + list(names(.own))) }
        if !names(.elsewhere).isEmpty { parts.append(list(names(.elsewhere)) + " linked elsewhere") }
        if !names(.missing).isEmpty { parts.append("no " + list(names(.missing), "or")) }
        guard !parts.isEmpty else { return "Nothing reported yet." }
        let s = parts.joined(separator: "; ") + "."
        return s.prefix(1).uppercased() + s.dropFirst()
    }

    /// The Showing picker's line for a profile: "work (~/.claude-work)";
    /// the base profile by its name alone. A long name or directory is cut
    /// in its middle: the picker is as wide as its widest line, and one
    /// long directory pushed the pane past the window's edge.
    public static func pickerLabel(_ p: ProfileSetup, home: String) -> String {
        let name = Format.middle(p.name, 28)
        guard !p.base, let d = p.dir else { return name }
        return "\(name) (\(Format.middle(FolderText.abbreviate(d, home: home), 32)))"
    }

    // MARK: the editor (CC2 B)

    public static func editorTitle(_ item: SetupItemName) -> String { "Edit " + item.rawValue }

    /// Who reads the file the editor writes: ~/.claude's is read by the
    /// profile that is ~/.claude and every one sharing it; a profile's own
    /// (or one linked elsewhere) by that profile.
    public static func readers(_ item: SetupItemName, scope: String?, profiles: [ProfileSetup]) -> [String] {
        let state = scope.flatMap { s in profiles.first { $0.name == s } }.flatMap { p in p.base ? nil : p.state(item) }
        if let scope, let state, state != .shared { return [scope] }
        let all = profiles.filter { $0.base || $0.state(item) == .shared }.map(\.name)
        return all.isEmpty ? [scope ?? "default"] : all
    }

    /// The editor's caption: "~/.claude/CLAUDE.md, read by default and
    /// work". A profile's link to it says saving changes it for them all.
    public static func editorCaption(target: String, home: String, readers: [String], linked: Bool) -> String {
        let p = FolderText.abbreviate(target, home: home)
        if linked && readers.count > 1 { return "\(p), shared: saving changes it for \(list(readers))" }
        return "\(p), read by \(list(readers))"
    }

    public static func editorFoot(_ item: SetupItemName) -> String {
        item == .settings ? "Claude Code reads it at the start of each session. It must be valid JSON."
            : "Claude Code reads it at the start of each session."
    }

    /// The note after a save: "Saved ~/.claude/CLAUDE.md; work reads it too".
    public static func saved(_ r: SetupWriteResult, home: String) -> String {
        "Saved " + FolderText.abbreviate(r.path, home: home)
            + (r.sharedWith.isEmpty ? "" : "; \(list(r.sharedWith)) read\(r.sharedWith.count == 1 ? "s" : "") it too")
    }

    // MARK: sharing (CC3 B)

    public static func useSharedConfirmation(profile: String, item: SetupItemName) -> Confirmation {
        Confirmation(title: "Use the shared \(item.rawValue) in \(profile)?",
                     message: "\(profile)'s own \(item.rawValue) is kept beside it as a backup, "
                        + "\(item.rawValue).claudeswitch-<date>-<time>, and \(profile) reads ~/.claude/\(item.rawValue) "
                        + "from then on.",
                     action: "Use Shared", destructive: false)
    }

    public static func shareFailed(_ action: ShareAction, profile: String, item: SetupItemName) -> String {
        switch action {
        case .makeOwn: return "Could not give \(profile) its own \(item.rawValue)"
        case .useShared, .share: return "Could not share \(item.rawValue) with \(profile)"
        }
    }

    /// "work now shares settings.json with ~/.claude; its own is kept as
    /// settings.json.claudeswitch-20261028-101500".
    public static func shareNote(_ r: ShareResult) -> String {
        if r.state == .own { return "\(r.profile) now has its own copy of \(r.item.rawValue)" }
        return "\(r.profile) now shares \(r.item.rawValue) with ~/.claude"
            + (r.backup.map { "; its own is kept as " + ($0 as NSString).lastPathComponent } ?? "")
    }

    public static func pluginNote(_ r: PluginToggle, name: String) -> String {
        "\(name) is now \(r.enabled ? "on" : "off")" + (r.profile == StateFile.defaultProfile ? "" : " in \(r.profile)")
    }

    public static func pluginFailed(_ name: String, on: Bool) -> String { "Could not turn \(name) \(on ? "on" : "off")" }
}

/// Where a write or a plugin switch goes: the profile shown, or, with every
/// profile shown, the one that is ~/.claude (no --profile when that is
/// default). Unavailable when no profile is ~/.claude: nothing here would
/// write ~/.claude's file then.
public enum SetupTarget: Equatable {
    case profile(String?)
    case unavailable

    public static func of(scope: String?, profiles: [ProfileSetup]) -> SetupTarget {
        if let scope { return .profile(scope == StateFile.defaultProfile ? nil : scope) }
        guard let b = profiles.first(where: \.base) else { return profiles.isEmpty ? .profile(nil) : .unavailable }
        return .profile(b.name == StateFile.defaultProfile ? nil : b.name)
    }
}

/// What the editor opened: the text, and the hash of its bytes, which the
/// save sends back (`--if-unchanged`).
public struct EditorBuffer: Equatable {
    public var text: String
    public var sha256: String

    public init(text: String, sha256: String) {
        self.text = text
        self.sha256 = sha256
    }

    /// More than `claude write` takes: the editor does not open it.
    public static let limit = 1 << 20

    public enum LoadError: Error, Equatable {
        case tooLarge, notText
    }

    /// From the file's bytes; nil bytes (no file) is an empty one, whose
    /// hash is the empty string's.
    public static func load(_ data: Data?) -> Result<EditorBuffer, LoadError> {
        let d = data ?? Data()
        guard d.count <= limit else { return .failure(.tooLarge) }
        guard let t = String(data: d, encoding: .utf8) else { return .failure(.notText) }
        return .success(EditorBuffer(text: t, sha256: hash(d)))
    }

    public static func hash(_ d: Data) -> String {
        SHA256.hash(data: d).map { String(format: "%02x", $0) }.joined()
    }

    public static func loadMessage(_ e: LoadError, item: SetupItemName) -> String {
        switch e {
        case .tooLarge: return "\(item.rawValue) is larger than 1 MiB, too large to edit here: use Open."
        case .notText: return "\(item.rawValue) is not UTF-8 text, so it cannot be edited here: use Open."
        }
    }
}

// MARK: the CLI

/// `claude write --json`: what was written.
public struct SetupWriteResult: Equatable {
    public var path: String
    public var bytes: Int
    public var sha256: String
    /// The other profiles whose same item is this file.
    public var sharedWith: [String]

    init?(_ j: JSON) {
        guard let p = j["path"].string else { return nil }
        path = p
        bytes = Int(j["bytes"].double ?? 0)
        sha256 = j["sha256"].string ?? ""
        sharedWith = j["shared_with"].array.compactMap(\.string)
    }
}

/// `profile share|own --json`.
public struct ShareResult: Equatable {
    public var profile: String
    public var item: SetupItemName
    public var state: ShareState
    /// Where an own item was moved; nil when nothing was.
    public var backup: String?

    init?(_ j: JSON) {
        guard let p = j["profile"].string, let i = j["item"].string.flatMap(SetupItemName.init(rawValue:)),
              let s = j["state"].string.flatMap(ShareState.init(rawValue:)) else { return nil }
        profile = p
        item = i
        state = s
        backup = j["backup"].string
    }
}

/// `claude plugin enable|disable --json`.
public struct PluginToggle: Equatable {
    public var plugin: String
    public var profile: String
    public var enabled: Bool

    init?(_ j: JSON) {
        guard let id = j["plugin"].string, let e = j["enabled"].bool else { return nil }
        plugin = id
        profile = j["profile"].string ?? StateFile.defaultProfile
        enabled = e
    }
}

public enum SetupSupport {
    /// A binary from before these commands prints its usage (read as too
    /// old for this call) or answers `usage`; the rest of the app works.
    public static func unsupported(_ e: CallError) -> Bool { PoolSupport.unsupported(e) }
}

public extension CLI {
    /// `profile recent --json`: every profile's recent folders.
    func profileRecent() -> Result<RecentFolders, CallError> {
        setupShaped(call(["profile", "recent", "--json"], timeout: 30), "profile recent", RecentFolders.init)
    }

    /// `claude show [--profile <p>] --json`.
    func claudeShow(profile: String? = nil) -> Result<ClaudeSetup, CallError> {
        var a = ["claude", "show"]
        if let p = profile {
            guard CLI.validID(p) else { return .failure(.refused("usage", "\"\(p)\" is not a valid profile name.")) }
            a.append("--profile=" + p)
        }
        return setupShaped(call(a + ["--json"], timeout: 30), "claude show", ClaudeSetup.init)
    }

    /// `claude write <item> [--profile <p>] --if-unchanged <sha> --json`,
    /// the text on stdin.
    func claudeWrite(_ item: SetupItemName, profile: String?, ifUnchanged sha: String,
                     text: String) -> Result<SetupWriteResult, CallError> {
        guard item.isFile else { return .failure(.refused("usage", "Only CLAUDE.md and settings.json are edited here.")) }
        guard sha.count == 64, sha.allSatisfy(\.isHexDigit) else {
            return .failure(.refused("usage", "Open the file again before saving."))
        }
        let data = Data(text.utf8)
        guard data.count <= EditorBuffer.limit else {
            return .failure(.refused("invalid_value", "\(item.rawValue) is larger than 1 MiB: edit it with Open."))
        }
        var a = ["claude", "write", item.rawValue]
        if let p = profile {
            guard CLI.validID(p) else { return .failure(.refused("usage", "\"\(p)\" is not a valid profile name.")) }
            a.append("--profile=" + p)
        }
        return setupShaped(call(a + ["--if-unchanged=" + sha, "--json"], timeout: 30, input: data), "claude write",
                           SetupWriteResult.init)
    }

    /// `claude plugin enable|disable <id> [--profile <p>] --json`: Claude
    /// Code's own command, which takes up to 30 s.
    func claudePlugin(_ id: String, enable: Bool, profile: String?) -> Result<PluginToggle, CallError> {
        guard CLI.validID(id) else { return .failure(.refused("usage", "\"\(id)\" is not a plugin name.")) }
        var a = ["claude", "plugin", enable ? "enable" : "disable", id]
        if let p = profile {
            guard CLI.validID(p) else { return .failure(.refused("usage", "\"\(p)\" is not a valid profile name.")) }
            a.append("--profile=" + p)
        }
        return setupShaped(call(a + ["--json"], timeout: 45), "claude plugin", PluginToggle.init)
    }

    /// `profile share <p> <item> --json`: the item becomes a link to
    /// ~/.claude's; an own one is kept as a backup first.
    func profileShare(_ profile: String, _ item: SetupItemName) -> Result<ShareResult, CallError> {
        guard CLI.validID(profile) else { return .failure(.refused("usage", "\"\(profile)\" is not a valid profile name.")) }
        return setupShaped(call(["profile", "share", profile, item.rawValue, "--json"], timeout: 30), "profile share",
                           ShareResult.init)
    }

    /// `profile own <p> <item> --json`: a link becomes a copy of what it
    /// points to.
    func profileOwn(_ profile: String, _ item: SetupItemName) -> Result<ShareResult, CallError> {
        guard CLI.validID(profile) else { return .failure(.refused("usage", "\"\(profile)\" is not a valid profile name.")) }
        return setupShaped(call(["profile", "own", profile, item.rawValue, "--json"], timeout: 60), "profile own",
                           ShareResult.init)
    }

    private func setupShaped<T>(_ r: Result<JSON, CallError>, _ what: String, _ make: (JSON) -> T?) -> Result<T, CallError> {
        r.flatMap { j in
            guard let v = make(j) else {
                return .failure(.cli(.unreadable("`claudeswitch \(what)` answered in a shape this app cannot read.")))
            }
            return .success(v)
        }
    }
}
