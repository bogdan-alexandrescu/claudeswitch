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
    case state, why, settings, profiles, chrome, accounts
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

    public init(full: Bool, settings: Bool = false, profiles: Bool = false) {
        self.full = full
        self.settings = settings
        self.profiles = profiles
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
    /// The rotation order just asked for, shown until a full refresh reads
    /// the account list again (B6: kept longer, it brought back renamed and
    /// deleted ids).
    public private(set) var priorityOrder: [String]?
    private var fresh = Freshness()

    public init(binaryPath: String? = nil, binaryVersion: String? = nil, problem: CLIError? = nil,
                state: StateFile? = nil, why: WhyReport? = nil, settings: ConfigSettings? = nil,
                profiles: ProfileList? = nil, chrome: ChromeList? = nil, accountList: AccountList? = nil) {
        self.binaryPath = binaryPath
        self.binaryVersion = binaryVersion
        self.problem = problem
        self.state = state
        self.why = why
        self.settings = settings
        self.profiles = profiles
        self.chrome = chrome
        self.accountList = accountList
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
        return applied
    }

    /// A pool edit's answer, stamped with the token taken when the edit
    /// started: edits run concurrently, so an older edit's answer arriving
    /// after a newer one (or after a read begun later) is dropped, like a
    /// read (review). Returns whether it was applied.
    @discardableResult
    public mutating func applyPools(_ r: PoolResult, token: Int) -> Bool {
        guard profiles != nil, fresh.accept(.profiles, token) else { return false }
        applyPools(r)
        return true
    }

    /// A pool edit's answer, applied at once: every effective pool, and the
    /// account listed where it now is.
    public mutating func applyPools(_ r: PoolResult) {
        guard var list = profiles else { return }
        for i in list.profiles.indices {
            let name = list.profiles[i].name
            if let pool = r.pools[name] { list.profiles[i].pool = pool }
            if var listed = list.profiles[i].listed {
                listed.removeAll { $0 == r.account }
                // Default holds an account no pool lists by D6 alone; the
                // next read says whether it is listed there.
                if name == r.profile && name != StateFile.defaultProfile { listed.append(r.account) }
                list.profiles[i].listed = listed
            }
        }
        profiles = list
        fresh.changed(.profiles)
        if var accounts = accountList, let i = accounts.accounts.firstIndex(where: { $0.id == r.account }) {
            accounts.accounts[i].profile = r.profile
            accountList = accounts
            fresh.changed(.accounts)
        }
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

    /// The profile whose effective pool holds an account.
    public func pool(of account: String) -> String? {
        profiles?.profiles.first { $0.pool.contains(account) }?.name
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
}
