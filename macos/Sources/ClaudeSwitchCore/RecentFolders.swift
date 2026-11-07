import Foundation

// OC1, where Open Claude Code starts (the owner's pick, 2026-10-28): in the
// profile's last folder, named on the button ("in claudeswitch ⌄"); the
// folder line, or a right-click, opens the profile's recent folders,
// Choose Folder… and Home Folder.

/// One folder Claude Code was used in under a profile
/// (`profile recent --json`): its absolute path, and when a session there
/// last wrote its transcript (nil when none is left).
public struct RecentFolder: Equatable, Identifiable {
    public var path: String
    public var lastUsed: Date?
    public var id: String { path }

    public init(path: String, lastUsed: Date?) {
        self.path = path
        self.lastUsed = lastUsed
    }

    init?(_ j: JSON) {
        // Paths are absolute in the contract; anything else cannot be a cd.
        guard let p = j["path"].string, p.hasPrefix("/") else { return nil }
        path = p
        lastUsed = j["last_used"].date
    }
}

/// `profile recent --json`: each profile's folders, newest first.
public struct RecentFolders: Equatable {
    public var profiles: [String: [RecentFolder]]

    public init(profiles: [String: [RecentFolder]] = [:]) {
        self.profiles = profiles
    }

    init?(_ j: JSON) {
        guard j["profiles"].raw is [Any] else { return nil }
        var out: [String: [RecentFolder]] = [:]
        for p in j["profiles"].array {
            guard let name = p["profile"].string else { continue }
            out[name] = p["folders"].array.compactMap(RecentFolder.init)
        }
        profiles = out
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func folders(_ profile: String) -> [RecentFolder] { profiles[profile] ?? [] }
}

/// A folder chosen in the app for a profile, and when: kept in the app's
/// defaults, so a choice outlives the next `profile recent` read.
public struct RememberedFolder: Equatable {
    public var path: String
    public var at: Date

    public init(path: String, at: Date) {
        self.path = path
        self.at = at
    }

    /// From the defaults' dictionary (profile → {path, at}); anything
    /// malformed is left out.
    public static func load(_ plist: Any?) -> [String: RememberedFolder] {
        guard let d = plist as? [String: Any] else { return [:] }
        var out: [String: RememberedFolder] = [:]
        for (profile, v) in d {
            guard let e = v as? [String: Any], let p = e["path"] as? String, p.hasPrefix("/"),
                  let t = e["at"] as? Double else { continue }
            out[profile] = RememberedFolder(path: p, at: Date(timeIntervalSince1970: t))
        }
        return out
    }

    public static func plist(_ m: [String: RememberedFolder]) -> [String: Any] {
        m.mapValues { ["path": $0.path, "at": $0.at.timeIntervalSince1970] as [String: Any] }
    }
}

/// The folder Open Claude Code starts in, and since when it is the one.
public struct FolderPick: Equatable {
    public var path: String
    public var at: Date?

    public init(path: String, at: Date?) {
        self.path = path
        self.at = at
    }
}

public enum LastFolder {
    /// A profile's last folder: the newer of the one chosen in the app and
    /// the profile's newest recent folder. A chosen folder that no longer
    /// exists is passed over; a recent one with no recorded use is never
    /// the last (its place in the list is only alphabetical). Nil: the
    /// home folder.
    public static func pick(remembered: RememberedFolder?, recent: [RecentFolder],
                            exists: (String) -> Bool) -> FolderPick? {
        let kept = remembered.flatMap { exists($0.path) ? FolderPick(path: $0.path, at: $0.at) : nil }
        let newest = recent.compactMap { f in f.lastUsed.map { (f.path, $0) } }
            .max { $0.1 < $1.1 }
            .map { FolderPick(path: $0.0, at: $0.1) }
        switch (kept, newest) {
        case let (k?, n?): return (k.at ?? .distantPast) >= (n.at ?? .distantPast) ? k : n
        case let (k?, nil): return k
        case let (nil, n?): return n
        case (nil, nil): return nil
        }
    }
}

/// The folders macOS asks the person about before an app reads in them:
/// Desktop, Documents, Downloads, iCloud Drive, and other volumes. Even a
/// check that one exists can prompt, so the app does not make it there.
public enum ProtectedFolders {
    public static func contains(_ path: String, home: String) -> Bool {
        let roots = ["Desktop", "Documents", "Downloads", "Library/Mobile Documents"].map { home + "/" + $0 }
        return path.hasPrefix("/Volumes/") || roots.contains { path == $0 || path.hasPrefix($0 + "/") }
    }
}

/// The words on the button and in the folder menu.
public enum FolderText {
    /// "~/code/website" for a path in the home folder, "~" for it.
    public static func abbreviate(_ path: String, home: String) -> String {
        if path == home { return "~" }
        if !home.isEmpty, path.hasPrefix(home + "/") { return "~" + path.dropFirst(home.count) }
        return path
    }

    public static func name(_ path: String) -> String {
        let n = (path as NSString).lastPathComponent
        return n.isEmpty ? path : n
    }

    /// The pill's second line: "in claudeswitch", or "in your home folder".
    public static func buttonLine(_ pick: FolderPick?, home: String) -> String {
        guard let p = pick, p.path != home else { return "in your home folder" }
        return "in " + name(p.path)
    }

    /// The pill's tooltip.
    public static func help(profile: String, pick: FolderPick?, home: String) -> String {
        let place = pick.flatMap { $0.path == home ? nil : abbreviate($0.path, home: home) } ?? "your home folder"
        return "Open Claude Code for \(profile) in \(place). The folder line, or a right-click, chooses another."
    }

    /// An age for the menu's column: "5m", "2h", "3d", "1w"; "" when unknown.
    public static func age(_ at: Date?, now: Date) -> String {
        guard let at else { return "" }
        let s = max(0, Int(now.timeIntervalSince(at)))
        if s < 60 { return "now" }
        if s < 3600 { return "\(s / 60)m" }
        if s < 86_400 { return "\(s / 3600)h" }
        let d = s / 86_400
        return d < 7 ? "\(d)d" : "\(d / 7)w"
    }

    /// One recent folder as a menu item: its name, then where it is and
    /// how long ago, "claudeswitch  ~/code · 2h". One line, as a menu item
    /// is; a long name is cut in its middle.
    public static func menuLine(_ path: String, at: Date?, home: String, now: Date) -> String {
        let parent = abbreviate((path as NSString).deletingLastPathComponent, home: home)
        let a = age(at, now: now)
        return Format.middle(name(path), 36) + "  " + Format.middle(parent, 36) + (a.isEmpty ? "" : " · " + a)
    }

    public static func header(_ profile: String) -> String { "Recent in \(profile)" }

    public static func empty(_ profile: String) -> String { "No folders yet: \(profile) has not been opened in one" }

    public static let chooseItem = "Choose Folder…"
    public static let homeItem = "Home Folder"
}

/// One recent folder in the menu.
public struct FolderMenuEntry: Equatable, Identifiable {
    public var path: String
    public var line: String
    /// The folder the button names.
    public var checked: Bool
    public var id: String { path }
}

public enum FolderMenu {
    /// The recent folders as menu items, the current one first and checked.
    /// A current folder the list lacks (chosen here, not yet recorded by
    /// Claude Code) is added, so the check always marks what the button
    /// says. The home folder has its own item and is left out.
    public static func entries(recent: [RecentFolder], current: FolderPick?, home: String,
                               now: Date) -> [FolderMenuEntry] {
        var list = recent.filter { $0.path != home }
        if let c = current, c.path != home {
            let was = list.firstIndex { $0.path == c.path }
            let at = [c.at, was.flatMap { list[$0].lastUsed }].compactMap { $0 }.max()
            if let i = was { list.remove(at: i) }
            list.insert(RecentFolder(path: c.path, lastUsed: at), at: 0)
        }
        return list.map { f in
            FolderMenuEntry(path: f.path, line: FolderText.menuLine(f.path, at: f.lastUsed, home: home, now: now),
                            checked: f.path == current?.path)
        }
    }
}
