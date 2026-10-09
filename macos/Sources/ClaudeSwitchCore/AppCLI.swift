import Foundation

// The app's side of docs/APP_CLI.md: every action is a `claudeswitch …
// --json` command answering with one JSON object, or with an error object
// {"error": {"code", "message", "hint"}}. Nothing here parses human text.

/// The CLI's error object.
public struct AppError: Error, Equatable {
    public var code: String
    public var message: String
    public var hint: String
    /// When the refusal is expected to clear (`retry_at`, R1: a §3 check
    /// that was rate limited); nil when the CLI gives none.
    public var retryAt: Date?

    public init(code: String, message: String, hint: String = "", retryAt: Date? = nil) {
        self.code = code
        self.message = message
        self.hint = hint
        self.retryAt = retryAt
    }

    init?(_ j: JSON) {
        let e = j["error"]
        guard let code = e["code"].string else { return nil }
        self.init(code: code, message: e["message"].string ?? code, hint: e["hint"].string ?? "",
                  retryAt: e["retry_at"].date)
    }

    /// The error object in bytes, or nil when they hold something else.
    public init?(data: Data) {
        guard let j = JSON(data: data) else { return nil }
        self.init(j)
    }
}

/// Why a call did not succeed: the CLI said no (its error object), or the
/// binary could not be run or read.
public enum CallError: Error, Equatable {
    case app(AppError)
    case cli(CLIError)

    public var code: String? {
        if case .app(let e) = self { return e.code }
        return nil
    }

    public var message: String {
        switch self {
        case .app(let e): return e.message
        case .cli(let e): return e.message
        }
    }

    public var hint: String {
        switch self {
        case .app(let e): return e.hint
        case .cli(.missing), .cli(.tooOld): return CLI.installSteps
        case .cli: return ""
        }
    }

    /// When the CLI said the refusal may clear (retry_at), if it did.
    public var retryAt: Date? {
        if case .app(let e) = self { return e.retryAt }
        return nil
    }

    /// A refusal made here, before anything ran.
    static func refused(_ code: String, _ message: String, hint: String = "") -> CallError {
        .app(AppError(code: code, message: message, hint: hint))
    }
}

public extension Result where Failure == CallError {
    var failure: CallError? {
        if case .failure(let e) = self { return e }
        return nil
    }
    var failureCode: String? { failure?.code }
    var failureMessage: String? { failure?.message }
    var failureHint: String? { failure?.hint }
}

public enum PoolVerb: String { case add, remove }

public enum DaemonVerb: Equatable {
    case status, start, stop, restart, live, dryRun, uninstall
    case install(live: Bool?)

    var args: [String] {
        switch self {
        case .status: return ["status"]
        case .start: return ["start"]
        case .stop: return ["stop"]
        case .restart: return ["restart"]
        case .live: return ["live"]
        case .dryRun: return ["dry-run"]
        case .uninstall: return ["uninstall"]
        case .install(let live):
            switch live {
            case true?: return ["install", "--live"]
            case false?: return ["install", "--dry-run"]
            case nil: return ["install"]
            }
        }
    }
}

/// Argument vectors, checked before anything runs. Names (accounts,
/// profiles, slots, setting keys) must be ids; a free value never starts
/// with "-" and holds no control character, and a valued flag is passed as
/// one `--flag=value` argument, so nothing can be read as another flag.
enum Argv {
    static func name(_ s: String, _ what: String) throws -> String {
        guard CLI.validID(s) else { throw CallError.refused("usage", "\"\(s)\" is not a valid \(what) name.") }
        return s
    }

    static func hasControl(_ s: String) -> Bool {
        s.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) }
    }

    /// A positional value: a setting's value.
    static func value(_ s: String, _ key: String) throws -> String {
        if s.hasPrefix("-") || hasControl(s) {
            throw CallError.refused("invalid_value", "\(key): \"\(s)\" is not a value this setting takes.")
        }
        return s
    }

    /// `--flag=value`, for a value that may be anything printable.
    static func flag(_ name: String, _ v: String, _ what: String) throws -> String {
        guard !hasControl(v), !v.isEmpty else { throw CallError.refused("usage", "That \(what) cannot be used.") }
        return "--\(name)=\(v)"
    }
}

/// The JSON CLI. Every method blocks: call it off the main thread.
public extension CLI {
    /// Runs one command and reads its one JSON object.
    func call(_ args: [String], timeout: TimeInterval = 60) -> Result<JSON, CallError> {
        let r = runner.run(path, args, timeout: timeout)
        let what = args.prefix(while: { !$0.hasPrefix("-") }).joined(separator: " ")
        if r.timedOut { return .failure(.cli(.failed("`claudeswitch \(what)` did not finish in time."))) }
        if let j = JSON(data: r.stdout), j.raw is [String: Any] {
            if let e = AppError(j) { return .failure(.app(e)) }
            if r.exitCode == 0 { return .success(j) }
        }
        if r.exitCode == 0 {
            return .failure(.cli(.unreadable("`claudeswitch \(what)` printed something this app cannot read.")))
        }
        let err = r.stderrText
        if err.contains("flag provided but not defined") || err.contains("unknown command")
            || (r.exitCode == 2 && err.contains("usage")) {
            return .failure(.cli(.tooOld(version: version() ?? "this version")))
        }
        return .failure(.cli(.failed(Self.tidy(err, fallback: "`claudeswitch \(what)` failed (exit \(r.exitCode))."))))
    }

    /// Builds the argument vector, runs it and decodes the answer.
    private func decode<T>(timeout: TimeInterval = 60, _ build: () throws -> [String],
                           _ make: (JSON) -> T?) -> Result<T, CallError> {
        let args: [String]
        do { args = try build() } catch let e as CallError { return .failure(e) } catch {
            return .failure(.cli(.failed(error.localizedDescription)))
        }
        return call(args, timeout: timeout).flatMap { j in
            guard let v = make(j) else {
                return .failure(.cli(.unreadable("`claudeswitch \(args.prefix(2).joined(separator: " "))` answered in a shape this app cannot read.")))
            }
            return .success(v)
        }
    }

    // MARK: app

    /// `app heartbeat --json` (0.6.1): tells the daemon this app is posting
    /// rotation notices, so it skips its own until the answer's time.
    func appHeartbeat() -> Result<Date, CallError> {
        decode(timeout: 20, { ["app", "heartbeat", "--json"] }) { $0["app_notifies_until"].date }
    }

    // MARK: config

    /// `init --empty --json`: a comments-only config at `configPath` (the
    /// CLI's default when nil). Answers the path written; `exists` when
    /// one is already there.
    func initEmpty(configPath: String?) -> Result<String, CallError> {
        decode({
            var a = ["init", "--empty", "--json"]
            if let p = configPath { a.append(try Argv.flag("config", p, "config path")) }
            return a
        }) { $0["path"].string }
    }

    func configSchema() -> Result<ConfigSchema, CallError> {
        decode({ ["config", "schema", "--json"] }, ConfigSchema.init)
    }

    func configValues() -> Result<ConfigValues, CallError> {
        decode({ ["config", "--json"] }, ConfigValues.init)
    }

    func configGet(_ key: String, profile: String?) -> Result<ConfigGetResult, CallError> {
        decode({
            var a = ["config", "get", try Argv.name(key, "setting")]
            if let p = profile { a.append(try Argv.flag("profile", Argv.name(p, "profile"), "profile")) }
            return a + ["--json"]
        }, ConfigGetResult.init)
    }

    func configSet(_ key: String, _ value: String) -> Result<ConfigSetResult, CallError> {
        decode({ ["config", "set", try Argv.name(key, "setting"), try Argv.value(value, key), "--json"] },
               ConfigSetResult.init)
    }

    // MARK: profiles

    func profileList() -> Result<ProfileList, CallError> {
        decode({ ["profile", "list", "--json"] }, ProfileList.init)
    }

    /// Waits for a running daemon to load the config when seeding (up to
    /// 10 s in the CLI), hence the longer timeout.
    func profileCreate(name: String, dir: String?, pool: [String], seed: String?) -> Result<ProfileCreated, CallError> {
        decode(timeout: 90, {
            var a = ["profile", "create", try Argv.name(name, "profile")]
            if let d = dir, !d.isEmpty { a.append(try Argv.flag("dir", d, "directory")) }
            if !pool.isEmpty {
                a.append(try Argv.flag("pool", try pool.map { try Argv.name($0, "account") }.joined(separator: ","), "pool"))
            }
            if let s = seed { a.append(try Argv.flag("seed", Argv.name(s, "account"), "account")) }
            return a + ["--json"]
        }, ProfileCreated.init)
    }

    func profileSeed(_ profile: String, account: String) -> Result<SeedResult, CallError> {
        decode(timeout: 90, { ["profile", "seed", try Argv.name(profile, "profile"), try Argv.name(account, "account"), "--json"] },
               SeedResult.init)
    }

    /// Removes a profile's [[profile]] block; its accounts join `to`, or
    /// `default` when nil. Without `confirmed` the CLI refuses with
    /// confirmation_required, whose message (where the accounts go, what
    /// stays on disk, which account stays guarded) is the app's confirmation
    /// text. Waits for a running daemon (up to 10 s).
    func profileRemove(_ profile: String, to: String?, confirmed: Bool) -> Result<ProfileRemoved, CallError> {
        decode(timeout: 90, {
            var a = ["profile", "remove", try Argv.name(profile, "profile")]
            if let t = to { a.append(try Argv.flag("to", Argv.name(t, "profile"), "profile")) }
            return a + (confirmed ? ["--yes"] : []) + ["--json"]
        }, ProfileRemoved.init)
    }

    func profileForget(_ profile: String) -> Result<ForgetResult, CallError> {
        decode({ ["profile", "forget", try Argv.name(profile, "profile"), "--json"] }, ForgetResult.init)
    }

    func profilePool(_ profile: String, _ verb: PoolVerb, account: String, to: String?) -> Result<PoolResult, CallError> {
        decode({
            var a = ["profile", "pool", try Argv.name(profile, "profile"), verb.rawValue, try Argv.name(account, "account")]
            if let t = to { a.append(try Argv.flag("to", Argv.name(t, "profile"), "profile")) }
            return a + ["--json"]
        }, PoolResult.init)
    }

    /// A per-profile override; "" (or "inherit") removes it.
    func profileSet(_ profile: String, _ key: String, _ value: String) -> Result<ProfileSetResult, CallError> {
        decode({
            let v = value.trimmingCharacters(in: .whitespaces)
            return ["profile", "set", try Argv.name(profile, "profile"), try Argv.name(key, "setting"),
                    v.isEmpty ? "inherit" : try Argv.value(v, key), "--json"]
        }, ProfileSetResult.init)
    }

    // MARK: accounts

    /// Every configured account from config and state alone: never the
    /// keychain, so it is read on every refresh.
    func accountList() -> Result<AccountList, CallError> {
        decode({ ["account", "list", "--json"] }, AccountList.init)
    }

    func accountRename(_ old: String, to new: String) -> Result<RenameResult, CallError> {
        decode({ ["account", "rename", try Argv.name(old, "account"), try Argv.name(new, "account"), "--json"] },
               RenameResult.init)
    }

    /// Without `confirmed` the CLI refuses with confirmation_required, whose
    /// message names the account and seat: that is the app's confirmation
    /// text. With it, `--yes`. Waits for a running daemon (up to 10 s).
    func accountDelete(_ id: String, confirmed: Bool) -> Result<DeleteResult, CallError> {
        decode(timeout: 90, {
            ["account", "delete", try Argv.name(id, "account")] + (confirmed ? ["--yes"] : []) + ["--json"]
        }, DeleteResult.init)
    }

    func pin(_ id: String) -> Result<PinResult, CallError> {
        decode({ ["account", "pin", try Argv.name(id, "account"), "--json"] }, PinResult.init)
    }

    /// The profiles unpinned.
    func unpin(profile: String) -> Result<[String], CallError> {
        decode({ ["account", "unpin", try Argv.flag("profile", Argv.name(profile, "profile"), "profile"), "--json"] }) {
            $0["unpinned"].raw == nil ? nil : $0["unpinned"].array.compactMap(\.string)
        }
    }

    func priority(_ ids: [String]) -> Result<PriorityResult, CallError> {
        decode({
            guard !ids.isEmpty else { throw CallError.refused("usage", "Name the accounts in order.") }
            return ["priority"] + (try ids.map { try Argv.name($0, "account") }) + ["--json"]
        }, PriorityResult.init)
    }

    // MARK: recovery

    func recovery(identify: Bool) -> Result<[RecoveryItem], CallError> {
        decode(timeout: identify ? 120 : 60, { ["recovery"] + (identify ? ["--identify"] : []) + ["--json"] }) {
            $0["items"].raw == nil ? nil : $0["items"].array.compactMap(RecoveryItem.init)
        }
    }

    func recoveryRestore(_ slot: String, account: String, force: Bool) -> Result<RestoreResult, CallError> {
        decode({
            ["recovery", "restore", try Argv.name(slot, "slot"), try Argv.name(account, "account")]
                + (force ? ["--force"] : []) + ["--json"]
        }, RestoreResult.init)
    }

    /// Whether the slot was cleared.
    func recoveryClear(_ slot: String) -> Result<Bool, CallError> {
        decode({ ["recovery", "clear", try Argv.name(slot, "slot"), "--yes", "--json"] }) { $0["cleared"].bool }
    }

    // MARK: use

    func use(_ id: String, profile: String?) -> Result<UseResult, CallError> {
        decode(timeout: 90, {
            var a = ["use", try Argv.name(id, "account")]
            if let p = profile { a.append(try Argv.flag("profile", Argv.name(p, "profile"), "profile")) }
            return a + ["--json"]
        }, UseResult.init)
    }

    // MARK: adding an account

    /// Step 1 of the browser route: the URL to open. `--no-open`: the app
    /// opens it, in the browser chosen.
    /// `profile` is the pool a new account joins; an existing account
    /// keeps its own, and the answer's `pending.profile` says which.
    func loginStart(account: String, profile: String?, browser: String?) -> Result<LoginStart, CallError> {
        decode({
            var a = ["login", try Argv.name(account, "account"), "--direct", "--json", "--no-open"]
            if let p = profile { a.append(try Argv.flag("profile", Argv.name(p, "profile"), "profile")) }
            if let b = browser, !b.isEmpty { a.append(try Argv.flag("browser", b, "browser")) }
            return a
        }, LoginStart.init)
    }

    /// Step 2: the code the browser showed.
    func loginCode(account: String, code: String) -> Result<AccountAdded, CallError> {
        decode(timeout: 90, {
            let c = code.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !c.isEmpty else { throw CallError.refused("usage", "Paste the code the browser shows.") }
            return ["login", try Argv.name(account, "account"), try Argv.flag("code", c, "code"), "--json"]
        }, AccountAdded.init)
    }

    /// Saves the login Claude Code is signed into now in profile `from`
    /// (the source; nil: the default one), adding it to `profile` (the
    /// target pool). With no name the CLI refuses with name_required, the
    /// name suggested for that source's login as the whole hint.
    func add(account: String?, from: String?, profile: String?) -> Result<AccountAdded, CallError> {
        decode(timeout: 90, {
            var a = ["add"]
            if let id = account { a.append(try Argv.name(id, "account")) }
            if let f = from { a.append(try Argv.flag("from", Argv.name(f, "profile"), "profile")) }
            if let p = profile { a.append(try Argv.flag("profile", Argv.name(p, "profile"), "profile")) }
            return a + ["--json"]
        }, AccountAdded.init)
    }

    // MARK: daemon

    func daemon(_ verb: DaemonVerb) -> Result<DaemonStatus, CallError> {
        decode(timeout: 60, { ["daemon"] + verb.args + ["--json"] }, DaemonStatus.init)
    }

    // MARK: Claude in Chrome (lane 13)

    /// Never launches anything and never reads the keychain.
    func chromeList() -> Result<ChromeList, CallError> {
        decode({ ["chrome", "list", "--json"] }, ChromeList.init)
    }

    /// Opens Chrome on a new profile for the account, at the sign-in page and
    /// Claude in Chrome's store page, and records it.
    func chromeAdd(_ id: String) -> Result<ChromeAdded, CallError> {
        decode({ ["chrome", "add", try Argv.name(id, "account"), "--json"] }, ChromeAdded.init)
    }

    /// Opens the account's resolved Chrome profile (C2: its own, its
    /// profile's, or Chrome's last used). It never creates one.
    func chromeOpen(_ id: String) -> Result<ChromeOpened, CallError> {
        decode({ ["chrome", "open", try Argv.name(id, "account"), "--json"] }, ChromeOpened.init)
    }

    /// Whether the mapping was dropped (the Chrome profile itself stays).
    func chromeForget(_ id: String) -> Result<Bool, CallError> {
        decode({ ["chrome", "forget", try Argv.name(id, "account"), "--json"] }) { $0["forgotten"].bool }
    }

    // MARK: your own Chrome profiles (C2)

    /// Chrome's profiles, from its Local State (read by the CLI, read-only).
    func chromeProfiles() -> Result<ChromeProfiles, CallError> {
        decode({ ["chrome", "profiles", "--json"] }, ChromeProfiles.init)
    }

    /// Maps the account to one of the person's Chrome profiles; creates and
    /// opens nothing.
    func chromeAddExisting(_ id: String, _ folder: String) -> Result<ChromeAdded, CallError> {
        decode({ ["chrome", "add", try Argv.name(id, "account"), try Argv.flag("existing", folder, "Chrome profile"),
                  "--json"] }, ChromeAdded.init)
    }

    /// Opens the account's Chrome profile at the sign-in pages.
    func chromeSignin(_ id: String) -> Result<ChromeSignedIn, CallError> {
        decode({ ["chrome", "signin", try Argv.name(id, "account"), "--json"] }, ChromeSignedIn.init)
    }
}
