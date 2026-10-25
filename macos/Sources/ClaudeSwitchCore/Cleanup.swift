import Foundation

// Cleaning up after Claude (owner decisions 2026-10-25): the answers of
// `clean --json`, `clean --apply --json`, `clean runs --json` and
// `clean undo --json` (docs/cli/clean.md#json), read leniently like every
// answer here.

/// One candidate of a plan: removable, or kept with the reason.
public struct CleanItem: Equatable, Identifiable {
    public var id: String { kind + "|" + path + "|" + (branch ?? "") }
    public var kind: String
    public var path: String
    public var repo: String?
    public var branch: String?
    public var remove: Bool
    public var why: String
    public var bytes: Int64

    public init(kind: String, path: String, repo: String? = nil, branch: String? = nil, remove: Bool,
                why: String, bytes: Int64 = 0) {
        self.kind = kind
        self.path = path
        self.repo = repo
        self.branch = branch
        self.remove = remove
        self.why = why
        self.bytes = bytes
    }

    init?(_ j: JSON) {
        guard let kind = j["kind"].string, let path = j["path"].string else { return nil }
        self.init(kind: kind, path: path, repo: j["repo"].string, branch: j["branch"].string,
                  remove: j["remove"].bool ?? false, why: j["why"].string ?? "",
                  bytes: Int64(j["bytes"].double ?? 0))
    }

    /// Where it is, as a person reads it: a branch by its repository.
    public func place(home: String = NSHomeDirectory()) -> String {
        if kind == "branch" || kind == "remote", let b = branch {
            return CleanItem.tilde(repo ?? path, home: home) + ": " + b
        }
        return CleanItem.tilde(path, home: home) + (branch.map { " [\($0)]" } ?? "")
    }

    static func tilde(_ p: String, home: String) -> String {
        p.hasPrefix(home + "/") ? "~" + p.dropFirst(home.count) : p
    }
}

/// The kinds, in the CLI's order, with their names.
public enum CleanKind {
    public static let order = ["worktree", "prune", "branch", "remote", "clone", "scratch", "plugin", "transcript"]
    public static let titles = [
        "worktree": "Worktrees", "prune": "Records of worktrees whose folder is gone", "branch": "Local branches",
        "remote": "Branches on GitHub", "clone": "Lane clones", "scratch": "Ended sessions' scratch",
        "plugin": "Old plugin versions", "transcript": "Old subagent transcripts",
    ]
    public static func title(_ kind: String) -> String { titles[kind] ?? kind }
}

/// A kind's removable items.
public struct CleanGroup: Equatable, Identifiable {
    public var id: String { kind }
    public var kind: String
    public var items: [CleanItem]
    public var bytes: Int64 { items.reduce(0) { $0 + $1.bytes } }
    public var title: String { CleanKind.title(kind) }
}

/// `clean --json`.
public struct CleanPlan: Equatable {
    public var items: [CleanItem]
    public var kinds: [String: Bool]

    public init(items: [CleanItem], kinds: [String: Bool] = [:]) {
        self.items = items
        self.kinds = kinds
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    init?(_ j: JSON) {
        guard j["plan"].raw is [String: Any] else { return nil }
        items = j["plan"]["items"].array.compactMap(CleanItem.init)
        var k: [String: Bool] = [:]
        for (name, v) in j["kinds"].object { if let b = v.bool { k[name] = b } }
        kinds = k
    }

    public var removable: [CleanItem] { items.filter(\.remove) }
    public var kept: Int { items.count - removable.count }
    public var bytes: Int64 { removable.reduce(0) { $0 + $1.bytes } }

    /// The removable items by kind, in the CLI's order.
    public var groups: [CleanGroup] {
        CleanKind.order.compactMap { kind in
            let items = removable.filter { $0.kind == kind }
            return items.isEmpty ? nil : CleanGroup(kind: kind, items: items)
        }
    }
}

/// `clean --apply --json` and `clean undo --json`: one run.
public struct CleanRun: Equatable {
    public var id: String
    public var at: Date?
    /// What it removed, and of those what has been put back.
    public var done: [CleanItem]
    public var undone: Int
    public var skipped: [CleanItem]
    public var failed: [String]

    init?(_ j: JSON) {
        let r = j["run"]
        guard let id = r["id"].string else { return nil }
        self.id = id
        at = r["at"].date
        done = r["done"].array.compactMap(CleanItem.init)
        undone = r["done"].array.filter { $0["undone"].bool == true }.count
        skipped = r["skipped"].array.compactMap(CleanItem.init)
        failed = r["failed"].array.compactMap { f in
            f["path"].string.map { "\(f["kind"].string ?? "") \($0): \(f["error"].string ?? "")" }
        }
    }

    public var bytes: Int64 { done.reduce(0) { $0 + $1.bytes } }

    /// What it did, in one line.
    public var summary: String {
        if done.isEmpty { return "Run \(id) removed nothing." }
        var counts: [String: Int] = [:]
        for d in done { counts[d.kind, default: 0] += 1 }
        let parts = CleanKind.order.compactMap { k in counts[k].map { "\($0) \(CleanKind.title(k).lowercased())" } }
        var s = "Run \(id) moved \(Format.bytes(bytes)) to the Trash: " + parts.joined(separator: ", ") + "."
        if !skipped.isEmpty { s += " \(skipped.count) changed since the plan and stayed." }
        if !failed.isEmpty { s += " \(failed.count) failed." }
        return s
    }
}

/// One line of `clean runs --json`.
public struct CleanRunSummary: Equatable, Identifiable {
    public var id: String
    public var at: Date?
    public var removed: Int
    public var undone: Int
    public var bytes: Int64

    init?(_ j: JSON) {
        guard let id = j["id"].string else { return nil }
        self.id = id
        at = j["at"].date
        removed = Int(j["removed"].double ?? 0)
        undone = Int(j["undone"].double ?? 0)
        bytes = Int64(j["bytes"].double ?? 0)
    }

    public static func list(data: Data) -> [CleanRunSummary]? {
        JSON(data: data).flatMap(list)
    }

    public static func list(_ j: JSON) -> [CleanRunSummary]? {
        guard j["runs"].raw is [Any] else { return nil }
        return j["runs"].array.compactMap(CleanRunSummary.init)
    }

    /// Whether undo has something to put back.
    public var undoable: Bool { removed > undone }
}

public extension Confirm {
    /// Clean now: what moves, and that it can be put back.
    static func cleanApply(_ plan: CleanPlan) -> Confirmation {
        let n = plan.removable.count
        let parts = plan.groups.map { "\($0.items.count) \($0.title.lowercased())" }
        return Confirmation(title: "Move \(n) item\(n == 1 ? "" : "s") (\(Format.bytes(plan.bytes))) to the Trash?",
                            message: parts.joined(separator: ", ") + ". Each is judged again just before it moves; "
                                + "branch tips are kept as refs/archive, and Undo puts the run back.",
                            action: "Move to the Trash", destructive: true)
    }
}
