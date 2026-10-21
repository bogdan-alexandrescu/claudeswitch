import Foundation

// Shared pools (docs/PROFILES.md §10, D31-D36): accounts are grouped into
// named pools, one pool per account; each profile lists the pools it uses,
// in order, and profiles that list one pool share its accounts, one profile
// at a time. A profile's own pool (`pool = [...]`, D35) goes by its name.
// Accounts no pool lists are default's, after its pools (D6).
//
// `pool list --json` is the picture; every pool edit answers with it. Pure,
// so the panes stay thin and this is tested.

/// One pool of `pool list --json`.
public struct PoolInfo: Equatable, Identifiable {
    public var id: String { name }
    public var name: String
    public var accounts: [String]
    /// The profiles that list it, in config order; a profile's own pool,
    /// that profile alone.
    public var profiles: [String]
    /// A profile's own pool (D35), under the profile's name.
    public var own: Bool

    public init(name: String, accounts: [String], profiles: [String], own: Bool) {
        self.name = name
        self.accounts = accounts
        self.profiles = profiles
        self.own = own
    }

    init?(_ j: JSON) {
        guard let n = j["name"].string, !n.isEmpty else { return nil }
        self.init(name: n, accounts: j["accounts"].array.compactMap(\.string),
                  profiles: j["profiles"].array.compactMap(\.string), own: j["own"].bool ?? false)
    }
}

/// The sheet's own next step for a pool refusal whose CLI hint names a
/// command; nil keeps the CLI's.
public enum PoolHint {
    public static func app(code: String?) -> String? {
        guard code == "pool_in_use" else { return nil }
        return "Uncheck it in each of those profiles first: Profiles, the profile, then Change… beside its pools."
    }
}

/// `pool list --json`, and the answer of every pool edit.
public struct PoolList: Equatable {
    /// The [[pool]] blocks in config order, then each profile's own pool.
    public var pools: [PoolInfo]
    /// The accounts no pool lists, in config order.
    public var unlisted: [String]

    public init(pools: [PoolInfo], unlisted: [String]) {
        self.pools = pools
        self.unlisted = unlisted
    }

    init?(_ j: JSON) {
        // Go writes a nil list as null, so the key alone identifies it.
        guard let o = j.raw as? [String: Any], o.keys.contains("pools"), o["error"] == nil else { return nil }
        pools = j["pools"].array.compactMap(PoolInfo.init)
        unlisted = j["unlisted"].array.compactMap(\.string)
    }

    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }

    public func pool(_ name: String) -> PoolInfo? { pools.first { $0.name == name } }

    /// The pool that lists an account; nil when none does.
    public func pool(of account: String) -> PoolInfo? { pools.first { $0.accounts.contains(account) } }

    /// Every profile that may use an account: its pool's, or default for
    /// one in no pool (D6) when there is a default.
    public func profiles(of account: String, defaultExists: Bool) -> [String] {
        if let p = pool(of: account) { return p.profiles }
        return defaultExists ? [StateFile.defaultProfile] : []
    }
}

/// Whether the binary knows pools at all.
public enum PoolSupport {
    /// A binary from before shared pools has no `pool` command: it prints
    /// its usage (the app reads that as too old for this call), or answers
    /// `usage`. The rest of the app keeps working with it.
    public static func unsupported(_ e: CallError) -> Bool {
        switch e {
        case .cli(.tooOld): return true
        case .app(let a): return a.code == "usage"
        default: return false
        }
    }
}

// MARK: the Accounts pane

/// One section of the Accounts pane: a pool, or the accounts in no pool.
public struct PoolSection: Equatable, Identifiable {
    public enum Kind: Equatable, Hashable {
        case pool(String)
        case unlisted
    }

    public var kind: Kind
    public var own: Bool
    /// "TEAM", or "In no pool".
    public var title: String
    /// "used by default (2nd), work (2nd)".
    public var subtitle: String
    /// In rotation order.
    public var accounts: [String]

    public var id: String {
        switch kind {
        case .pool(let n): return "pool:" + n
        case .unlisted: return "unlisted"
        }
    }

    /// The pool's name; nil for the accounts in no pool.
    public var name: String? {
        if case .pool(let n) = kind { return n }
        return nil
    }

    /// An empty [[pool]]: it shows where to drop an account, and Delete.
    public var deletable: Bool { name != nil && !own && accounts.isEmpty }

    /// The accounts in no pool, as a place to move one to ("Move to pool →
    /// In no pool") when no account is there yet.
    public static let noPool = PoolSection(kind: .unlisted, own: false, title: "In no pool", subtitle: "", accounts: [])
}

/// What dropping an account on a section (or "Move to pool") does.
public enum PoolDrop: Equatable {
    /// `pool move <account> --to <to>`; `from` is its pool now, nil for
    /// one in no pool.
    case move(account: String, from: String?, to: String)
    /// `pool remove <from> <account>`: it is then in no pool (D6).
    case remove(account: String, from: String)

    public var account: String {
        switch self {
        case .move(let a, _, _), .remove(let a, _): return a
        }
    }
}

public enum PoolLayout {
    /// Whether the pane groups the accounts by pool: with a binary that
    /// knows pools, and something to group. With no [[profile]] blocks every
    /// account is the one implicit profile's, and the pane is one list.
    public static func grouped(_ pools: PoolList?, profiles: ProfileList?) -> Bool {
        guard let p = pools else { return false }
        if !p.pools.isEmpty { return true }
        return profiles?.profiles.contains(where: \.declared) == true
    }

    /// The sections, in pool list's order ([[pool]] blocks, then profiles'
    /// own pools; an empty own pool is none), then the accounts in no pool
    /// when there are any. Within a section, `order` (the rotation order)
    /// decides; an account it does not name comes last, in the pool's order.
    public static func sections(_ pools: PoolList, order: [String], profiles: ProfileList?) -> [PoolSection] {
        func ordered(_ ids: [String]) -> [String] {
            order.filter(ids.contains) + ids.filter { !order.contains($0) }
        }
        var out: [PoolSection] = []
        for p in pools.pools where !(p.own && p.accounts.isEmpty) {
            out.append(PoolSection(kind: .pool(p.name), own: p.own, title: p.name.uppercased(),
                                   subtitle: usedBy(p, pools: pools, profiles: profiles), accounts: ordered(p.accounts)))
        }
        if !pools.unlisted.isEmpty {
            out.append(PoolSection(kind: .unlisted, own: false, title: "In no pool",
                                   subtitle: unlistedSubtitle(pools, profiles: profiles),
                                   accounts: ordered(pools.unlisted)))
        }
        return out
    }

    /// "used by default (2nd), work (2nd)": the ordinal is the pool's place
    /// in that profile's pools, shown when the profile uses something else
    /// too (another pool, or for default the accounts in no pool).
    static func usedBy(_ p: PoolInfo, pools: PoolList, profiles: ProfileList?) -> String {
        guard !p.profiles.isEmpty else { return "no profile uses it" }
        let names = p.profiles.map { name -> String in
            guard let info = profiles?.profile(name), let listed = info.pools else { return name }
            let used = listed.filter { n in pools.pool(n).map { !$0.accounts.isEmpty || !$0.own } ?? false }
            let more = used.count > 1 || (name == StateFile.defaultProfile && !pools.unlisted.isEmpty)
            guard more, let i = used.firstIndex(of: p.name) else { return name }
            return "\(name) (\(ordinal(i + 1)))"
        }
        return "used by " + names.joined(separator: ", ")
    }

    static func unlistedSubtitle(_ pools: PoolList, profiles: ProfileList?) -> String {
        guard let list = profiles else { return "default uses them last" }
        guard let d = list.profile(StateFile.defaultProfile) else { return "no profile uses them" }
        let before = (d.pools ?? []).contains { n in pools.pool(n).map { !$0.accounts.isEmpty } ?? false }
        return before ? "default uses them last" : "default uses them"
    }

    /// 1st, 2nd, 3rd, 4th, 11th, 21st.
    public static func ordinal(_ n: Int) -> String {
        let suffix: String
        switch (n % 10, n % 100) {
        case (_, 11...13): suffix = "th"
        case (1, _): suffix = "st"
        case (2, _): suffix = "nd"
        case (3, _): suffix = "rd"
        default: suffix = "th"
        }
        return "\(n)\(suffix)"
    }

    /// What dropping `account` on `section` does; nil within its own
    /// section (there a drop reorders).
    public static func drop(_ account: String, onto section: PoolSection, pools: PoolList) -> PoolDrop? {
        let from = pools.pool(of: account)?.name
        switch section.kind {
        case .pool(let to):
            return from == to ? nil : .move(account: account, from: from, to: to)
        case .unlisted:
            return from.map { .remove(account: account, from: $0) }
        }
    }

    /// "Move to pool": every pool section but the account's own.
    public static func targets(for account: String, sections: [PoolSection]) -> [PoolSection] {
        sections.filter { $0.name != nil && !$0.accounts.contains(account) }
    }
}

/// "a", "a and b", "a, b and c".
func spoken(_ names: [String]) -> String {
    switch names.count {
    case 0: return ""
    case 1: return names[0]
    default: return names.dropLast().joined(separator: ", ") + " and " + names.last!
    }
}

public extension Confirm {
    /// Asked before a drop or a "Move to pool" runs: where the account goes,
    /// and which profiles gain or lose it.
    static func poolDrop(_ d: PoolDrop, pools: PoolList, defaultExists: Bool = true) -> Confirmation {
        let before = pools.profiles(of: d.account, defaultExists: defaultExists)
        switch d {
        case .move(let a, let from, let to):
            let after = pools.pool(to)?.profiles ?? []
            var m = from.map { "\(a) leaves \($0) and joins \(to)." } ?? "\(a) joins \(to)."
            if after.isEmpty {
                m += " No profile uses \(to), so \(a) rotates nowhere until one does."
            } else {
                m += " From now on \(spoken(after)) may use it"
                let lost = before.filter { !after.contains($0) }
                m += lost.isEmpty ? "." : "; \(spoken(lost)) no longer can."
            }
            return Confirmation(title: "Move \(a) to \(to)?", message: m, action: "Move", destructive: false)
        case .remove(let a, let from):
            var m = "\(a) is then in no pool"
            m += defaultExists ? ": \(StateFile.defaultProfile) uses it, after its pools." : ", and no profile uses it."
            let after = defaultExists ? [StateFile.defaultProfile] : []
            let lost = before.filter { !after.contains($0) }
            if !lost.isEmpty { m += " \(spoken(lost)) no longer can." }
            return Confirmation(title: "Take \(a) out of \(from)?", message: m, action: "Take out", destructive: false)
        }
    }
}

public extension Notes {
    /// The success note of a drop or a move.
    static func poolDrop(_ d: PoolDrop) -> String {
        switch d {
        case .move(let a, _, let to): return "\(a) is now in \(to)"
        case .remove(let a, _): return "\(a) is in no pool now"
        }
    }
}

public extension RotationOrder {
    /// A drag within one pool's section: the section's accounts take their
    /// new order in the places they hold in the whole rotation order, and
    /// every other account stays where it is (within a pool the global
    /// priority decides, D33).
    static func reorder(_ global: [String], section: [String], from: IndexSet, to: Int) -> [String] {
        var moved = section
        let picked = from.sorted().filter { $0 < section.count }
        let items = picked.map { section[$0] }
        var dest = min(max(to, 0), section.count)
        for i in picked.reversed() {
            moved.remove(at: i)
            if i < dest { dest -= 1 }
        }
        moved.insert(contentsOf: items, at: dest)
        var next = moved.makeIterator()
        var out = global.map { section.contains($0) ? (next.next() ?? $0) : $0 }
        while let rest = next.next() { out.append(rest) }
        return out
    }
}

// MARK: a new pool

/// What "New pool" takes: the accounts in no pool join at create (`pool
/// create` refuses one already in a pool); the others are moved in after it
/// exists (`pool move`), confirmed first.
public struct NewPoolPlan: Equatable {
    public var name: String
    public var create: [String]
    public var moves: [PoolDrop]

    public static func make(name: String, selected: [String], pools: PoolList?) -> NewPoolPlan {
        var create: [String] = []
        var moves: [PoolDrop] = []
        for id in selected {
            if let from = pools?.pool(of: id)?.name {
                moves.append(.move(account: id, from: from, to: name))
            } else {
                create.append(id)
            }
        }
        return NewPoolPlan(name: name, create: create, moves: moves)
    }

    /// Beside an account in the sheet: the pool it would leave.
    public static func note(for account: String, pools: PoolList?) -> String? {
        pools?.pool(of: account).map { "leaves pool \($0.name)" }
    }

    /// Asked before creating when accounts leave their pools.
    public var confirmation: Confirmation? {
        let moved = moves.compactMap { d -> (String, String)? in
            if case .move(let a, let from?, _) = d { return (a, from) }
            return nil
        }
        guard !moved.isEmpty else { return nil }
        return Confirmation(title: "Create \(name) and move \(moved.map(\.0).joined(separator: ", "))?",
                            message: moved.map { "\($0.0) leaves \($0.1)" }.joined(separator: "; ") + ".",
                            action: "Create and move", destructive: false)
    }
}

// MARK: a profile's pools

public enum ProfilePools {
    /// The pools a profile's card names, in the order it uses them: its
    /// `pools` that pool list holds (an empty own pool is none). Nil from a
    /// binary that does not report them.
    public static func shown(_ p: ProfileInfo, pools: PoolList?) -> [String]? {
        guard let listed = p.pools, let l = pools else { return nil }
        return listed.filter { l.pool($0) != nil }
    }

    /// Default uses the accounts in no pool, after its pools (D6).
    public static func usesUnlisted(_ p: ProfileInfo, pools: PoolList?) -> Bool {
        p.name == StateFile.defaultProfile && p.declared && !(pools?.unlisted.isEmpty ?? true)
    }

    /// The collapsed card's words for them: "pools personal, team, then 1
    /// in no pool". Nil from a binary that does not report them.
    public static func summary(_ p: ProfileInfo, pools: PoolList?) -> String? {
        guard let shown = shown(p, pools: pools) else { return nil }
        let n = usesUnlisted(p, pools: pools) ? pools?.unlisted.count ?? 0 : 0
        if shown.isEmpty { return n > 0 ? "\(n) \(n == 1 ? "account" : "accounts") in no pool" : "no pools" }
        var s = (shown.count == 1 ? "pool " : "pools ") + shown.joined(separator: ", ")
        if n > 0 { s += ", then \(n) in no pool" }
        return s
    }

    /// The `profile pools` calls that leave the profile using `chosen`, in
    /// order. The CLI converts a profile's own pool that holds accounts and
    /// keeps it first unless the list names it, so dropping it takes a
    /// second call.
    public static func steps(profile: String, pools: PoolList?, chosen: [String]) -> [[String]] {
        guard let own = pools?.pool(profile), own.own, !own.accounts.isEmpty, !chosen.contains(profile) else {
            return [chosen]
        }
        return [[profile] + chosen, chosen]
    }

    /// What the change does beyond the order, said before saving; nil when
    /// nothing else happens.
    public static func note(profile: String, pools: PoolList?, chosen: [String]) -> String? {
        var notes: [String] = []
        if let own = pools?.pool(profile), own.own, !own.accounts.isEmpty, !chosen.contains(profile) {
            notes.append("\(profile)'s own accounts (\(own.accounts.joined(separator: ", "))) become the pool "
                + "\(profile), which no profile uses then: they rotate nowhere until a profile lists it or you move them.")
        }
        for name in chosen where name != profile {
            if let p = pools?.pool(name), p.own {
                notes.append("\(name)'s own pool becomes shared: \(name) keeps using it.")
            }
        }
        return notes.isEmpty ? nil : notes.joined(separator: " ")
    }
}

/// The pools checked in Change… (and New profile), in the order the
/// profile uses them, with ↑/↓.
public struct PoolChoice: Equatable {
    /// Every pool offered, in pool list's order.
    public let all: [String]
    /// The ones checked, in order.
    public private(set) var order: [String]

    public init(all: [String], chosen: [String]) {
        self.all = all
        order = chosen.filter(all.contains)
    }

    public var unchecked: [String] { all.filter { !order.contains($0) } }

    public func isChecked(_ name: String) -> Bool { order.contains(name) }

    /// 1 for the first pool used; nil when unchecked.
    public func position(of name: String) -> Int? { order.firstIndex(of: name).map { $0 + 1 } }

    /// Checks a pool (it joins last) or unchecks one.
    public mutating func toggle(_ name: String) {
        if let i = order.firstIndex(of: name) { order.remove(at: i) } else if all.contains(name) { order.append(name) }
    }

    /// Moves a checked pool `by` places; false when it cannot.
    @discardableResult
    public mutating func move(_ name: String, by: Int) -> Bool {
        guard let next = RotationOrder.move(order, name, by: by) else { return false }
        order = next
        return true
    }
}

// MARK: a new account's pool (D37)

/// Where a new account goes: a pool by name (a [[pool]], or a profile's own
/// under the profile's name), or none, which default uses last (D6).
public enum PoolPick: Hashable {
    case pool(String)
    case none
}

/// One choice in the Add account sheet's Pool row.
public struct PoolOption: Equatable, Hashable {
    public var pick: PoolPick
    public var label: String
}

public enum NewAccountPool {
    /// Every pool, labelled with who uses it, then "No pool" when there is a
    /// profile named default to use it.
    public static func options(pools: PoolList?, profiles: ProfileList?) -> [PoolOption] {
        var out = (pools?.pools ?? []).map { p in
            PoolOption(pick: .pool(p.name), label: p.name + " — " + (p.own
                ? "\(p.profiles.first ?? p.name)'s own"
                : p.profiles.isEmpty ? "no profile uses it" : "used by " + p.profiles.joined(separator: ", ")))
        }
        let defaultExists = profiles.map { $0.profile(StateFile.defaultProfile) != nil }
            ?? (pools?.pools.contains { $0.profiles.contains(StateFile.defaultProfile) } ?? true)
        if defaultExists {
            out.append(PoolOption(pick: .none, label: "No pool — \(StateFile.defaultProfile) uses it last"))
        }
        return out
    }

    /// Where the sheet starts: the last pool the profile it was opened from
    /// uses alone, so nothing is shared unless chosen; for default with no
    /// such pool, No pool; else the profile's last pool.
    public static func initial(profile: String?, pools: PoolList?, profiles: ProfileList?) -> PoolPick? {
        let options = Self.options(pools: pools, profiles: profiles)
        guard let name = profile else { return options.first?.pick }
        let listed = profiles?.profile(name)?.pools
            ?? (pools?.pool(name).map { $0.own ? [$0.name] : [] } ?? [])
        if let alone = listed.last(where: { pools?.pool($0)?.profiles == [name] }) { return .pool(alone) }
        if name == StateFile.defaultProfile, options.contains(where: { $0.pick == .none }) { return PoolPick.none }
        if let last = listed.last { return .pool(last) }
        return options.first?.pick
    }

    /// The CLI's words for it: --pool=NAME or --no-pool.
    public static func args(_ p: PoolPick) throws -> [String] {
        switch p {
        case .pool(let n): return [try Argv.flag("pool", Argv.name(n, "pool"), "pool")]
        case .none: return ["--no-pool"]
        }
    }

    /// The sheet's words for where it goes.
    public static func describe(_ p: PoolPick, pools: PoolList?) -> String {
        switch p {
        case .none: return "no pool (\(StateFile.defaultProfile) uses it last)"
        case .pool(let n):
            guard let info = pools?.pool(n), !info.profiles.isEmpty else { return "the pool \(n)" }
            return "the pool \(n), which " + info.profiles.joined(separator: " and ") + (info.profiles.count == 1 ? " uses" : " use")
        }
    }
}

// MARK: the grouped pane as one list

/// One row of the grouped Accounts pane. On macOS a row dragged in a List
/// drops only within the ForEach it came from, so with one ForEach per pool
/// no other pool offered a drop spot (owner, 2026-10-20): the pane is one
/// ForEach, its headers rows too.
public enum PoolRow: Equatable, Hashable, Identifiable {
    /// A section's header; the section's id.
    case header(String)
    /// An account, and the id of the section it is in.
    case account(String, section: String)
    /// An empty pool's one row, which says to drop an account there.
    case empty(String)

    public var id: String {
        switch self {
        case .header(let s): return "h:" + s
        case .account(let a, _): return "a:" + a
        case .empty(let s): return "e:" + s
        }
    }

    /// The account, for an account row.
    public var account: String? {
        if case .account(let a, _) = self { return a }
        return nil
    }

    /// Only an account moves.
    public var movable: Bool { account != nil }
}

/// What a move in the one list means.
public enum PoolRowMove: Equatable {
    /// Within a pool: the rotation order, in that section's own indices.
    case reorder(section: String, from: IndexSet, to: Int)
    /// Across a header: the account into that section's pool.
    case drop(account: String, onto: String)
    case none
}

public extension PoolLayout {
    /// Each section's header, then its accounts, or its empty row.
    static func rows(_ sections: [PoolSection]) -> [PoolRow] {
        sections.flatMap { s -> [PoolRow] in
            [.header(s.id)] + (s.accounts.isEmpty ? [.empty(s.id)] : s.accounts.map { .account($0, section: s.id) })
        }
    }

    /// A List move (SwiftUI's onMove: `to` counts rows before the move) read
    /// as a reorder within the account's section, a move into the section
    /// whose header it now sits under, or nothing.
    static func move(_ rows: [PoolRow], from: IndexSet, to: Int) -> PoolRowMove {
        guard from.count == 1, let i = from.first, i < rows.count,
              case .account(let id, let src) = rows[i], to != i, to != i + 1 else { return .none }
        guard let h = rows[..<min(to, rows.count)].lastIndex(where: { if case .header = $0 { return true }; return false }),
              case .header(let dst) = rows[h] else { return .none }
        guard dst == src else { return .drop(account: id, onto: dst) }
        let mine = rows.indices.filter { if case .account(_, let s) = rows[$0] { return s == src }; return false }
        guard let local = mine.firstIndex(of: i) else { return .none }
        return .reorder(section: src, from: [local], to: mine.filter { $0 < to }.count)
    }
}

// MARK: deleting a pool (D38)

/// What deleting a pool does: where its accounts can go, and what each
/// profile that listed it is left using.
public struct PoolDeletePlan: Equatable {
    public var name: String
    public var accounts: [String]
    /// The profiles that list it.
    public var users: [String]
    /// Every other pool, then No pool when a profile named default uses it.
    public var destinations: [PoolOption]
    /// Where the sheet starts; nil when there is nothing to move.
    public var initial: PoolPick?
    /// The profiles' pools, each in its order, for the summary.
    var listed: [String: [String]]

    /// "default and work stop using team. default then uses only personal;
    /// work then uses only ops.", with its accounts going where it starts.
    public var summary: String { summary(to: initial) }

    /// The same, with its accounts going to `to`: the accounts in no pool
    /// are default's, last (D6).
    public func summary(to: PoolPick?) -> String {
        guard !users.isEmpty else { return "No profile uses it." }
        let left = users.map { u -> String in
            var rest = (listed[u] ?? [name]).filter { $0 != name }
            if to == PoolPick.none, u == StateFile.defaultProfile, !accounts.isEmpty {
                rest.append("the accounts in no pool")
            }
            switch rest.count {
            case 0: return "\(u) then uses no pool"
            case 1: return "\(u) then uses only \(rest[0])"
            default: return "\(u) then uses " + rest.joined(separator: ", then ")
            }
        }
        return PoolDeletePlan.joined(users) + " stop" + (users.count == 1 ? "s" : "")
            + " using \(name). " + left.joined(separator: "; ") + "."
    }

    public static func make(_ name: String, pools: PoolList, profiles: ProfileList?) -> PoolDeletePlan {
        let info = pools.pool(name)
        let accounts = info?.accounts ?? []
        let users = info?.profiles ?? []
        let destinations = NewAccountPool.options(pools: pools, profiles: profiles)
            .filter { $0.pick != .pool(name) }
        var listed: [String: [String]] = [:]
        for u in users { listed[u] = profiles?.profile(u)?.pools ?? [name] }
        // Where the same profiles use the accounts, so a delete shares them
        // with no one new by default (No pool is default's); else the first.
        func usersOf(_ p: PoolPick) -> [String] {
            switch p {
            case .none: return [StateFile.defaultProfile]
            case .pool(let n): return pools.pool(n)?.profiles ?? []
            }
        }
        let same = destinations.first { Set(usersOf($0.pick)) == Set(users) && !users.isEmpty }
        return PoolDeletePlan(name: name, accounts: accounts, users: users, destinations: destinations,
                              initial: accounts.isEmpty ? nil : (same ?? destinations.first)?.pick, listed: listed)
    }

    /// "a", "a and b", "a, b and c".
    static func joined(_ v: [String]) -> String {
        v.count <= 2 ? v.joined(separator: " and ") : v.dropLast().joined(separator: ", ") + " and " + v.last!
    }
}

