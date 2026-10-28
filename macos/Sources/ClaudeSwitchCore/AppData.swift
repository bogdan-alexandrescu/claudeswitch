import Foundation

// What the app has read through the CLI, and how a refresh's answers are
// applied (lane 16, B1). The owner's report: a pool edit succeeded and then
// disappeared, because a refresh copied the profile list when it started
// and wrote that copy back when it ended, over the newer list the edit had
// produced. Now a refresh carries only what it read, each part is applied
// only when no newer reading or edit of it has been applied since the
// refresh began, and an edit's answer is applied as soon as it arrives.

/// The parts of the app's data that are read separately.
public enum DataPart: Hashable, CaseIterable {
    case state, why, settings, profiles, chrome, accounts, pools
}

/// Which reading of each part is the newest applied. Reads and edits are
/// numbered in the order they began (a read) or were applied (an edit); a
/// read is applied only over older ones.
public struct Freshness: Equatable {
    private var counter = 0
    private var stamps: [DataPart: Int] = [:]

    public init() {}

    /// A read starting now.
    public mutating func begin() -> Int {
        counter += 1
        return counter
    }

    /// Whether a read begun at `token` may replace `part`; it is recorded
    /// as the newest when so.
    public mutating func accept(_ part: DataPart, _ token: Int) -> Bool {
        guard token > (stamps[part] ?? 0) else { return false }
        stamps[part] = token
        return true
    }

    /// An edit's answer has been applied to `part`: every read begun
    /// before now is older than it.
    public mutating func changed(_ part: DataPart) {
        counter += 1
        stamps[part] = counter
    }
}

/// What one refresh reads.
public struct ReadRequest: Equatable {
    /// Locate the binary, check it, run `why`, `chrome list` and
    /// `account list`; without it, state.json alone.
    public var full: Bool
    public var settings: Bool
    public var profiles: Bool
    /// `pool list` (config only, so every full read asks), unless the
    /// binary said lately that it has no pools.
    public var pools: Bool

    public init(full: Bool, settings: Bool = false, profiles: Bool = false, pools: Bool = true) {
        self.full = full
        self.settings = settings
        self.profiles = profiles
        self.pools = pools
    }
}

/// One refresh's answers: only the parts in `fetched` were read, and only
/// those are applied. Nothing here is a copy of what the app already had.
public struct Reading {
    public var token: Int
    public var fetched: Set<DataPart> = []
    /// With a full read: the binary found, its version, and what is wrong.
    public var located = false
    public var path: String?
    public var version: String?
    public var problem: CLIError?
    public var state: StateFile?
    public var why: WhyReport?
    public var settings: ConfigSettings?
    public var profiles: ProfileList?
    /// `profile list` answered with an error object (the config does not
    /// load, say): shown where the profiles are, the last good list kept.
    public var profilesError: CallError?
    public var chrome: ChromeList?
    public var accountList: AccountList?
    /// `pool list --json` (shared pools).
    public var pools: PoolList?
    /// The binary has no pools (one from before them): the panes fall back.
    public var poolsUnsupported = false

    public init(token: Int) { self.token = token }
}

/// The reads of one refresh, off the main thread.
public enum RefreshReader {
    public static func read(path: String?, request: ReadRequest, token: Int, stateFile: String,
                            version: (CLI) -> Result<String, CLIError>) -> Reading {
        var r = Reading(token: token)
        if request.full {
            r.located = true
            r.path = path
            r.fetched.insert(.why)
            if let p = path {
                let cli = CLI(path: p)
                switch version(cli) {
                case .success(let v): r.version = v
                case .failure(let e): r.problem = e
                }
                if r.problem == nil {
                    switch cli.why() {
                    case .success(let w): r.why = w
                    case .failure(let e): r.problem = e
                    }
                }
                if r.problem == nil && request.settings, case .success(let c) = cli.settings() {
                    r.settings = c
                    r.fetched.insert(.settings)
                }
                if r.problem == nil && request.profiles {
                    switch cli.profileList() {
                    case .success(let l):
                        r.profiles = l
                        r.fetched.insert(.profiles)
                    case .failure(.cli(let e)):
                        r.problem = e
                    case .failure(let e):
                        r.profilesError = e
                        r.fetched.insert(.profiles)
                    }
                }
                if r.problem == nil, case .success(let c) = cli.chromeList() {
                    r.chrome = c
                    r.fetched.insert(.chrome)
                }
                if r.problem == nil, case .success(let l) = cli.accountList() {
                    r.accountList = l
                    r.fetched.insert(.accounts)
                }
                if r.problem == nil && request.pools {
                    switch cli.poolList() {
                    case .success(let l):
                        r.pools = l
                        r.fetched.insert(.pools)
                    case .failure(let e) where PoolSupport.unsupported(e):
                        r.poolsUnsupported = true
                        r.fetched.insert(.pools)
                    case .failure:
                        break // kept as it was; the next refresh asks again
                    }
                }
            } else {
                r.problem = .missing
            }
        }
        r.state = (try? Data(contentsOf: URL(fileURLWithPath: stateFile))).flatMap(StateFile.init(data:))
        r.fetched.insert(.state)
        return r
    }
}

/// Everything the app has read, applied in order.
public struct AppData: Equatable {
    public private(set) var binaryPath: String?
    public private(set) var binaryVersion: String?
    public private(set) var problem: CLIError?
    public private(set) var state: StateFile?
    public private(set) var why: WhyReport?
    public private(set) var settings: ConfigSettings?
    public private(set) var profiles: ProfileList?
    public private(set) var profilesError: CallError?
    public private(set) var chrome: ChromeList?
    public private(set) var accountList: AccountList?
    /// Every pool and the accounts in no pool (shared pools); nil until
    /// read, and with a binary that has none.
    public private(set) var pools: PoolList?
    /// The binary answered `pool list` as one from before pools.
    public private(set) var poolsUnsupported = false
    /// The rotation order just asked for, shown until a full refresh reads
    /// the account list again (B6: kept longer, it brought back renamed and
    /// deleted ids).
    public private(set) var priorityOrder: [String]?
    private var fresh = Freshness()

    public init(binaryPath: String? = nil, binaryVersion: String? = nil, problem: CLIError? = nil,
                state: StateFile? = nil, why: WhyReport? = nil, settings: ConfigSettings? = nil,
                profiles: ProfileList? = nil, chrome: ChromeList? = nil, accountList: AccountList? = nil,
                pools: PoolList? = nil, poolsUnsupported: Bool = false) {
        self.binaryPath = binaryPath
        self.binaryVersion = binaryVersion
        self.problem = problem
        self.state = state
        self.why = why
        self.settings = settings
        self.profiles = profiles
        self.chrome = chrome
        self.accountList = accountList
        self.pools = pools
        self.poolsUnsupported = poolsUnsupported
    }

    /// A refresh starting now; its reading carries this token.
    public mutating func begin() -> Int { fresh.begin() }

    /// Applies what a refresh read, part by part, unless something newer
    /// was applied since it began. Returns the parts applied.
    @discardableResult
    public mutating func apply(_ r: Reading) -> Set<DataPart> {
        var applied: Set<DataPart> = []
        if r.located, fresh.accept(.why, r.token) {
            binaryPath = r.path
            binaryVersion = r.version
            problem = r.problem
            why = r.why
            applied.insert(.why)
        }
        if r.fetched.contains(.state), fresh.accept(.state, r.token) {
            state = r.state
            applied.insert(.state)
        }
        if r.fetched.contains(.settings), fresh.accept(.settings, r.token) {
            settings = r.settings
            applied.insert(.settings)
        }
        if r.fetched.contains(.profiles), fresh.accept(.profiles, r.token) {
            if let l = r.profiles { profiles = l }
            profilesError = r.profilesError
            applied.insert(.profiles)
        }
        if r.fetched.contains(.chrome), fresh.accept(.chrome, r.token) {
            chrome = r.chrome
            applied.insert(.chrome)
        }
        if r.fetched.contains(.accounts), fresh.accept(.accounts, r.token) {
            accountList = r.accountList
            priorityOrder = nil
            applied.insert(.accounts)
        }
        if r.fetched.contains(.pools), fresh.accept(.pools, r.token) {
            if r.poolsUnsupported {
                pools = nil
                poolsUnsupported = true
            } else if let l = r.pools {
                pools = l
                poolsUnsupported = false
            }
            applied.insert(.pools)
        }
        return applied
    }

    /// A pool edit's answer (the pool list), stamped with the token taken
    /// when the edit started: edits run concurrently, so an older edit's
    /// answer arriving after a newer one's is dropped, like a read. Applied
    /// at once to the pools and to every account's pool and profiles; a
    /// read begun before now is older than it. Returns whether it was.
    @discardableResult
    public mutating func applyPools(_ l: PoolList, token: Int) -> Bool {
        guard fresh.accept(.pools, token) else { return false }
        pools = l
        poolsUnsupported = false
        fresh.changed(.pools)
        if var accounts = accountList {
            let defaultExists = profiles.map { $0.profile(StateFile.defaultProfile) != nil } ?? true
            for i in accounts.accounts.indices {
                let id = accounts.accounts[i].id
                let users = l.profiles(of: id, defaultExists: defaultExists)
                accounts.accounts[i].pool = l.pool(of: id)?.name
                accounts.accounts[i].profiles = users
                accounts.accounts[i].profile = users.first
            }
            accountList = accounts
            fresh.changed(.accounts)
        }
        return true
    }

    /// `profile pools`'s answer: that profile as `profile list` reports it,
    /// in place of the one read before. Stamped like a pool edit.
    @discardableResult
    public mutating func applyProfile(_ p: ProfileInfo, token: Int) -> Bool {
        guard var list = profiles, fresh.accept(.profiles, token) else { return false }
        if let i = list.profiles.firstIndex(where: { $0.name == p.name }) {
            list.profiles[i] = p
        } else {
            list.profiles.append(p)
        }
        profiles = list
        fresh.changed(.profiles)
        return true
    }

    /// The order just asked for, shown at once.
    public mutating func reorder(_ ids: [String]) {
        priorityOrder = ids
        fresh.changed(.accounts)
    }

    /// The CLI's answer to a reorder.
    public mutating func applyPriority(_ r: PriorityResult) {
        priorityOrder = r.order
        fresh.changed(.accounts)
    }

    /// The configured accounts, in rotation order as far as it is known:
    /// the order just asked for, else `account list`'s (rotation order),
    /// then any other id the snapshot or the pools name.
    public func accountIDs(snapshot: Snapshot?) -> [String] {
        let listed = accountList?.accounts.map(\.id)
        var out = priorityOrder ?? listed ?? []
        if let known = listed { out = out.filter(known.contains) }
        for id in (listed ?? []) + (snapshot?.accounts.map(\.id) ?? []) + (profiles?.profiles.flatMap(\.pool) ?? [])
            where !out.contains(id) {
            out.append(id)
        }
        return out
    }

    /// The first profile that may use an account (shared pools: several
    /// may).
    public func profile(of account: String) -> String? {
        if let a = accountList?.account(account), a.profiles != nil { return a.profile }
        return profiles?.profiles.first { $0.pool.contains(account) }?.name ?? accountList?.account(account)?.profile
    }

    /// The menu's picture of it all, at `now`.
    public func snapshot(now: Date) -> Snapshot? {
        guard state != nil || why != nil else { return nil }
        return Snapshot.build(state: state, why: why, settings: settings, profiles: profiles, now: now)
    }
}

/// Moving one account up or down the rotation order, for the keyboard and
/// VoiceOver (dragging needs a pointer).
public enum RotationOrder {
    /// The order with `id` moved `by` places; nil when it cannot move.
    public static func move(_ ids: [String], _ id: String, by: Int) -> [String]? {
        guard let i = ids.firstIndex(of: id) else { return nil }
        let j = i + by
        guard j >= 0, j < ids.count, j != i else { return nil }
        var out = ids
        out.remove(at: i)
        out.insert(id, at: j)
        return out
    }

    /// The whole order with `id` moved `by` places within its section (a
    /// pool's accounts): it swaps with the section's previous or next
    /// account, and every other account stays where it is, as a drag there
    /// does (D33). Nil at the section's ends.
    public static func move(_ global: [String], section: [String], _ id: String, by: Int) -> [String]? {
        let s = global.filter(section.contains)
        guard let i = s.firstIndex(of: id) else { return nil }
        let j = i + by
        guard j >= 0, j < s.count, j != i else { return nil }
        return reorder(global, section: s, from: IndexSet([i]), to: j > i ? j + 1 : j)
    }
}
