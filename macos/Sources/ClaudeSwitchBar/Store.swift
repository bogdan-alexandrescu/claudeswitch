import AppKit
@preconcurrency import ClaudeSwitchCore
import Foundation
import SwiftUI

/// A CLI error or refusal, shown as a native alert with its hint.
struct AlertItem: Identifiable, Equatable {
    let id = UUID()
    var title: String
    var message: String
    var hint: String

    init(title: String, message: String, hint: String = "") {
        self.title = title
        self.message = message
        self.hint = hint
    }

    init(_ title: String, _ e: CallError) {
        self.init(title: title, message: e.message, hint: e.hint)
    }

    static func == (a: AlertItem, b: AlertItem) -> Bool { a.id == b.id }
}

/// What the add-account sheet opens with: a new account, or signing an
/// existing one in again.
struct AddAccountRequest: Identifiable, Equatable {
    let id = UUID()
    var account: String?
    var profile: String?
    var relogin: Bool { account != nil }
}

/// The app's model: reads state.json, and does everything else through the
/// claudeswitch binary's JSON CLI (docs/APP_CLI.md). It never reads the
/// keychain, never calls the usage API, never opens Chrome's files and never
/// touches the daemon's lock; all of that is the binary's job.
@MainActor
final class Store: ObservableObject {
    /// Everything read from state.json and the CLI, applied in order: a
    /// refresh carries only what it read, and a read older than an edit
    /// already applied is never applied over it (lane 16, B1).
    @Published private(set) var data = AppData()
    @Published private(set) var snapshot: Snapshot?
    @Published private(set) var refreshing = false

    var problem: CLIError? { data.problem }
    var binaryPath: String? { data.binaryPath }
    var binaryVersion: String? { data.binaryVersion }
    var state: StateFile? { data.state }
    var profiles: ProfileList? { data.profiles }
    /// A `profile list` failure that is the CLI's answer (B5): shown in
    /// the Profiles pane, the last good list kept.
    var profilesError: CallError? { data.profilesError }
    var chrome: ChromeList? { data.chrome }
    /// `account list --json`: plan and the rest per account, from config
    /// and state alone (lane 15).
    var accountList: AccountList? { data.accountList }

    @Published private(set) var schema: ConfigSchema?
    @Published private(set) var values: ConfigValues?
    @Published private(set) var daemon: DaemonStatus?
    @Published private(set) var recovery: [RecoveryItem]?

    /// Keys of the actions running now, for progress indicators.
    @Published private(set) var busy: Set<String> = []
    /// Alerts and one-line success notes, per surface: each shows in the
    /// window whose action raised it (the popover or Settings), not in both
    /// (the owner's live use: every alert appeared twice).
    @Published private(set) var alerts = Inbox<AlertItem>()
    @Published private(set) var notes = Inbox<String>()
    /// The surface the person is acting in: the key window's.
    private(set) var surface: Surface = .popover
    private var keyObserver: NSObjectProtocol?

    /// The acting surface's alert; setting it posts there only.
    var alert: AlertItem? {
        get { alerts.item(for: surface) }
        set { alerts.post(newValue, to: surface) }
    }

    /// The acting surface's note; setting it posts there only.
    var note: String? {
        get { notes.item(for: surface) }
        set { notes.post(newValue, to: surface) }
    }

    func alertBinding(_ s: Surface) -> Binding<AlertItem?> {
        Binding(get: { self.alerts.item(for: s) }, set: { self.alerts.post($0, to: s) })
    }

    func note(on s: Surface) -> String? { notes.item(for: s) }
    func clearNote(on s: Surface) { notes.post(nil, to: s) }

    /// A view appearing in a surface makes it the acting one.
    func acting(in s: Surface) { surface = s }
    @Published var addAccount: AddAccountRequest?

    @Published var compact: Bool {
        didSet { defaults.set(compact, forKey: Keys.compact) }
    }
    @Published var configuredBinary: String {
        didSet {
            defaults.set(configuredBinary, forKey: Keys.binary)
            refresh(full: true)
        }
    }
    /// The profile the menu-bar title follows (nil: default).
    @Published var menuProfile: String? {
        didSet { defaults.set(menuProfile, forKey: Keys.menuProfile) }
    }
    @Published var terminal: TerminalApp {
        didSet { defaults.set(terminal.rawValue, forKey: Keys.terminal) }
    }

    private enum Keys {
        static let compact = "compactMenuBar"
        static let binary = "binaryPath"
        static let menuProfile = "menuBarProfile"
        static let terminal = "terminalApp"
    }

    let paths = Paths()
    let preview: Bool
    private let defaults = UserDefaults.standard
    private let work = DispatchQueue(label: "claudeswitch.bar.work")
    /// Actions run here, apart from the reads: an account delete may wait
    /// ten seconds for the daemon, and the popover must keep updating.
    private let actions = DispatchQueue(label: "claudeswitch.bar.actions", attributes: .concurrent)
    private var watcher: DirectoryWatcher?
    private var timer: Timer?
    private var debounce: DispatchWorkItem?
    /// Touched only on `work`.
    nonisolated(unsafe) private var versions = VersionCache()
    private var settingsAt = Date.distantPast
    private var profilesAt = Date.distantPast
    private var inFlight = 0
    /// --render: the clock the fixtures were written at.
    private var fixedNow: Date?

    /// How often everything is re-read without a file change: countdowns move
    /// and the daemon can stop without touching state.json.
    static let tick: TimeInterval = 20
    /// `profile list` looks up each profile's sign-in, so it is read on
    /// demand (popover, Settings, after a change) and at most this often
    /// otherwise.
    static let profilesEvery: TimeInterval = 300

    init() {
        preview = false
        compact = defaults.bool(forKey: Keys.compact)
        configuredBinary = defaults.string(forKey: Keys.binary) ?? ""
        menuProfile = defaults.string(forKey: Keys.menuProfile)
        terminal = defaults.string(forKey: Keys.terminal).flatMap(TerminalApp.init(rawValue:)) ?? .default
        if CommandLine.arguments.contains("--render") { return }
        DispatchQueue.main.async { [weak self] in self?.start() }
    }

    /// A store that shows fixed data and runs nothing (--render).
    init(preview: Snapshot?, problem: CLIError? = nil, binaryPath: String? = nil, fixtures: PreviewData = PreviewData()) {
        self.preview = true
        compact = false
        configuredBinary = ""
        menuProfile = nil
        terminal = .default
        snapshot = preview
        fixedNow = preview?.now
        data = AppData(binaryPath: binaryPath, binaryVersion: binaryPath == nil ? nil : CLI.minimumVersion,
                       problem: problem, state: fixtures.state, why: fixtures.why, settings: nil,
                       profiles: fixtures.profiles, chrome: fixtures.chrome, accountList: fixtures.accountList)
        schema = fixtures.schema
        values = fixtures.values
        daemon = fixtures.daemon
        recovery = fixtures.recovery
        timer = Timer() // never scheduled; marks the store as started
    }

    func start() {
        guard timer == nil else { return }
        // Which window the person is acting in: Settings (or a sheet or
        // dialog on it), else the popover.
        keyObserver = NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification,
                                                             object: nil, queue: .main) { [weak self] n in
            guard let w = n.object as? NSWindow else { return }
            let root = w.sheetParent ?? w
            let settings = root.title == SettingsView.windowTitle
                || root.identifier?.rawValue.hasPrefix(SettingsView.windowID) == true
            MainActor.assumeIsolated { self?.surface = settings ? .settings : .popover }
        }
        refresh(full: true, profiles: true)
        watcher = DirectoryWatcher(path: paths.stateDir) { [weak self] in
            Task { @MainActor in self?.stateChanged() }
        }
        timer = Timer.scheduledTimer(withTimeInterval: Self.tick, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                if self.watcher?.isWatching != true {
                    self.watcher = DirectoryWatcher(path: self.paths.stateDir) { [weak self] in
                        Task { @MainActor in self?.stateChanged() }
                    }
                }
                self.refresh(full: true)
            }
        }
    }

    /// A save is a write to a temp file and a rename, so several events arrive
    /// together; act once they settle. Only state.json is re-read here: `why`
    /// can itself save state.json (when it discards a stale record), so
    /// running it on a file event would race the daemon's saves and echo.
    private func stateChanged() {
        debounce?.cancel()
        let item = DispatchWorkItem { [weak self] in
            Task { @MainActor in self?.refresh(full: false) }
        }
        debounce = item
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4, execute: item)
    }

    /// Re-reads state.json. With `full` (the timer, an action, Refresh, a new
    /// binary path) it also locates the binary, checks its version (cached
    /// per path and modification time) and runs `why --json` and
    /// `chrome list --json` and `account list --json` (neither touches the
    /// keychain), plus `config --json` once a minute and
    /// `profile list --json` when asked or every few minutes.
    func refresh(full: Bool, profiles wantProfiles: Bool = false) {
        guard !preview else { return }
        inFlight += 1
        refreshing = true
        let token = data.begin()
        let configured = configuredBinary
        let stateFile = paths.stateFile
        let request = ReadRequest(full: full,
                                  settings: Date().timeIntervalSince(settingsAt) > 60 || data.settings == nil,
                                  profiles: wantProfiles || Date().timeIntervalSince(profilesAt) > Self.profilesEvery)
        work.async {
            let path = full ? BinaryLocator(configured: configured).locate() : nil
            let reading = RefreshReader.read(path: path, request: request, token: token, stateFile: stateFile) {
                self.versions.check($0)
            }
            DispatchQueue.main.async {
                let applied = self.data.apply(reading)
                if applied.contains(.settings) { self.settingsAt = Date() }
                if applied.contains(.profiles) && reading.profilesError == nil { self.profilesAt = Date() }
                self.rebuild()
                self.inFlight -= 1
                self.refreshing = self.inFlight > 0
            }
        }
    }

    /// The snapshot from the data as it is now.
    private func rebuild() {
        snapshot = data.snapshot(now: fixedNow ?? Date())
    }

    // MARK: running actions

    func isBusy(_ key: String) -> Bool { busy.contains(key) }

    /// Runs one CLI call off the main thread, marking `key` busy meanwhile.
    func call<T>(_ key: String, _ op: @escaping (CLI) -> Result<T, CallError>) async -> Result<T, CallError> {
        if preview { return .failure(.cli(.failed("preview"))) }
        guard let path = binaryPath else { return .failure(.cli(.missing)) }
        busy.insert(key)
        defer { busy.remove(key) }
        let cli = CLI(path: path)
        let queue = actions
        return await withCheckedContinuation { cont in
            queue.async { cont.resume(returning: op(cli)) }
        }
    }

    /// Runs an action: on success `done`; on failure the CLI's error as an
    /// alert. Either way a full refresh follows (re-reading the profile
    /// list when `profiles`): a refusal can mean the app's picture was out
    /// of date (B5).
    func act<T>(_ key: String, title: String, profiles: Bool = false,
                _ op: @escaping (CLI) -> Result<T, CallError>, done: @escaping (T) -> Void = { _ in }) {
        let origin = surface // where the person acted, wherever they are when it ends
        Task {
            switch await call(key, op) {
            case .success(let v):
                let was = surface
                surface = origin
                done(v) // its note goes where the action started
                surface = was
            case .failure(let e):
                alerts.post(AlertItem(title, e), to: origin)
            }
            refresh(full: true, profiles: profiles)
        }
    }

    /// Puts an account into a profile (`profile pool`), applying the CLI's
    /// pools as soon as it answers and saying where the account is now.
    func movePool(_ step: PoolStep, key: String) {
        let run: (CLI) -> Result<PoolResult, CallError>
        let account: String
        switch step {
        case .already: return
        case .add(let a, let to):
            account = a
            run = { $0.profilePool(to, .add, account: a, to: nil) }
        case .move(let a, let from, let to):
            account = a
            run = { $0.profilePool(from, .remove, account: a, to: to) }
        }
        // Edits run concurrently: the answer is stamped like a read begun
        // now, so an older edit's answer never lands over a newer one.
        let token = data.begin()
        act(key, title: "Could not move \(account)", profiles: true, run) { [weak self] r in
            self?.applyPools(r, token: token)
        }
    }

    /// Takes an account out of a profile, back to default (D6).
    func removeFromPool(_ account: String, profile: String, key: String) {
        let token = data.begin()
        act(key, title: "Could not remove \(account) from \(profile)", profiles: true,
            { $0.profilePool(profile, .remove, account: account, to: nil) }) { [weak self] r in
            self?.applyPools(r, token: token)
        }
    }

    private func applyPools(_ r: PoolResult, token: Int) {
        data.applyPools(r, token: token)
        rebuild()
        note = Notes.pool(r)
    }

    // MARK: actions

    func use(_ account: String, profile: String) {
        act("use:\(profile)", title: "Could not switch \(profile) to \(account)", profiles: true,
            { $0.use(account, profile: profile) }) { [weak self] r in
            var text = "\(r.profile) now uses \(r.account)"
            if !r.verified { text += " (not yet confirmed by a usage read)" }
            self?.note = text
        }
    }

    func setPinned(_ pinned: Bool, profile: String, account: String?) {
        if pinned, let a = account {
            act("pin:\(profile)", title: "Could not pin \(profile)", profiles: true, { $0.pin(a) })
        } else {
            act("pin:\(profile)", title: "Could not unpin \(profile)", profiles: true, { $0.unpin(profile: profile) })
        }
    }

    /// Opens the user's terminal running `claudeswitch run <profile>`.
    func openClaudeCode(_ profile: String) {
        guard let bin = binaryPath else { alert = AlertItem("Could not open Claude Code", .cli(.missing)); return }
        guard let argv = TerminalLauncher.runArgv(binary: bin, profile: profile) else {
            alert = AlertItem(title: "Could not open Claude Code", message: "\"\(profile)\" is not a profile name.")
            return
        }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("claudeswitch-run", isDirectory: true)
        let script = dir.appendingPathComponent("run-\(profile).command").path
        let plan = TerminalLauncher.plan(terminal, argv: argv, scriptPath: script)
        let app = terminal.rawValue
        Task {
            let r: Result<Bool, CallError> = await call("run:\(profile)") { _ in
                Launcher.run(plan, scriptDir: dir.path, scriptPath: script)
            }
            if case .failure(let e) = r {
                alert = AlertItem(title: "Could not open \(app)", message: e.message,
                                  hint: "Choose another terminal in Settings → Advanced, or run: claudeswitch run \(profile)")
            }
        }
    }

    /// "Open Chrome for this account" (lane 13): its Chrome profile, set up
    /// first when it has none.
    func openChrome(_ account: String) {
        let known = chrome
        act("chrome:\(account)", title: "Could not open Chrome for \(account)",
            { $0.chromeOpenOrAdd(account, known: known) }) { [weak self] r in
            self?.note = r.opened ? "Opened Chrome for \(r.account)" : "Chrome for \(r.account) is set up"
        }
    }

    // MARK: Settings reads

    /// Everything the Settings window shows; run when it opens.
    func loadSettings() {
        guard !preview else { return }
        refresh(full: true, profiles: true)
        Task {
            async let s = call("load:schema") { $0.configSchema() }
            async let v = call("load:values") { $0.configValues() }
            async let d = call("load:daemon") { $0.daemon(.status) }
            if case .success(let x) = await s { schema = x }
            if case .success(let x) = await v { values = x }
            switch await d {
            case .success(let x): daemon = x
            case .failure(.app(let e)) where e.code == "not_installed" || e.code == "unsupported_platform": daemon = nil
            case .failure: break
            }
        }
    }

    func reloadValues() {
        Task { if case .success(let v) = await call("load:values", { $0.configValues() }) { values = v } }
    }

    func loadRecovery(identify: Bool) {
        Task {
            switch await call("recovery", { $0.recovery(identify: identify) }) {
            case .success(let r): recovery = r
            case .failure(let e): alert = AlertItem("Could not read the recovery copies", e)
            }
        }
    }

    func setDaemon(_ verb: DaemonVerb, title: String) {
        act("daemon", title: title, { $0.daemon(verb) }) { [weak self] s in self?.daemon = s }
    }

    func setPriority(_ ids: [String]) {
        data.reorder(ids)
        act("priority", title: "Could not change the rotation order", { $0.priority(ids) }) { [weak self] r in
            self?.data.applyPriority(r)
        }
    }

    /// The configured accounts, in rotation order as far as it is known.
    var accountIDs: [String] { data.accountIDs(snapshot: snapshot) }

    /// The profile whose pool holds an account.
    func pool(of account: String) -> String? { data.pool(of: account) }

    /// Switches a profile to its best account now (why's `best`).
    func switchToBest(_ card: ProfileCard) {
        guard let b = card.best else { return }
        use(b.id, profile: card.name)
    }
}

/// Fixture data for --render.
struct PreviewData {
    var state: StateFile?
    var why: WhyReport?
    var profiles: ProfileList?
    var chrome: ChromeList?
    var accountList: AccountList?
    var schema: ConfigSchema?
    var values: ConfigValues?
    var daemon: DaemonStatus?
    var recovery: [RecoveryItem]?
}

/// Watches a directory for entries being written, created or renamed — which
/// is how a state.json save appears — and calls back on every event.
final class DirectoryWatcher {
    private var source: DispatchSourceFileSystemObject?
    var isWatching: Bool { source != nil }

    init(path: String, onChange: @escaping () -> Void) {
        let fd = open(path, O_EVTONLY)
        guard fd >= 0 else { return } // not created yet: the timer covers it
        let src = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd, eventMask: [.write, .rename, .delete, .extend, .attrib], queue: .main)
        src.setEventHandler { [weak self] in
            onChange()
            // The directory itself went away: stop, and let the timer re-arm.
            if src.data.contains(.delete) || src.data.contains(.rename) { self?.stop() }
        }
        src.setCancelHandler { close(fd) }
        src.resume()
        source = src
    }

    func stop() {
        source?.cancel()
        source = nil
    }

    deinit { stop() }
}
