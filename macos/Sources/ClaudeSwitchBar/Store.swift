import ClaudeSwitchCore
import Foundation
import SwiftUI

/// The app's model: reads state.json, asks the binary for its decision and
/// settings, and runs `claudeswitch use`. It never reads the keychain and never
/// calls the usage API; everything that touches either is the binary's job.
@MainActor
final class Store: ObservableObject {
    @Published private(set) var snapshot: Snapshot?
    @Published private(set) var problem: CLIError?
    @Published private(set) var binaryPath: String?
    @Published private(set) var binaryVersion: String?
    @Published private(set) var switching: String?
    @Published private(set) var lastSwitch: (id: String, outcome: UseOutcome)?
    @Published private(set) var refreshing = false

    @Published var compact: Bool {
        didSet { UserDefaults.standard.set(compact, forKey: Keys.compact) }
    }
    @Published var configuredBinary: String {
        didSet {
            UserDefaults.standard.set(configuredBinary, forKey: Keys.binary)
            refresh(full: true)
        }
    }

    private enum Keys {
        static let compact = "compactMenuBar"
        static let binary = "binaryPath"
    }

    let paths = Paths()
    private let work = DispatchQueue(label: "claudeswitch.bar.work")
    private var watcher: DirectoryWatcher?
    private var timer: Timer?
    private var debounce: DispatchWorkItem?
    /// Touched only on `work`.
    nonisolated(unsafe) private var versions = VersionCache()
    private var why: WhyReport?
    private var settings: ConfigSettings?
    private var settingsAt = Date.distantPast

    /// How often everything is re-read without a file change: countdowns move
    /// and the daemon can stop without touching state.json.
    static let tick: TimeInterval = 20

    init() {
        compact = UserDefaults.standard.bool(forKey: Keys.compact)
        configuredBinary = UserDefaults.standard.string(forKey: Keys.binary) ?? ""
        if CommandLine.arguments.contains("--render") { return }
        DispatchQueue.main.async { [weak self] in self?.start() }
    }

    /// A store that shows a fixed snapshot and runs nothing (--render).
    init(preview: Snapshot?, problem: CLIError? = nil, binaryPath: String? = nil) {
        compact = false
        configuredBinary = ""
        snapshot = preview
        self.problem = problem
        self.binaryPath = binaryPath
        binaryVersion = binaryPath == nil ? nil : "0.4.8"
        timer = Timer() // never scheduled; marks the store as started
    }

    func start() {
        guard timer == nil else { return }
        refresh(full: true)
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

    /// Re-reads state.json. With `full` (the timer, a switch, Refresh, a new
    /// binary path) it also locates the binary, checks its version (cached
    /// per path and modification time) and runs `why --json`, plus
    /// `config --json` once a minute.
    func refresh(full: Bool) {
        refreshing = true
        let configured = configuredBinary
        let paths = self.paths
        let needSettings = Date().timeIntervalSince(settingsAt) > 60 || settings == nil
        var path = binaryPath
        var problem = self.problem
        var version = binaryVersion
        var why = self.why
        var settings = self.settings
        work.async {
            var settingsFresh = false
            if full {
                path = BinaryLocator(configured: configured).locate()
                problem = nil
                if let p = path {
                    let cli = CLI(path: p)
                    switch self.versions.check(cli) {
                    case .success(let v): version = v
                    case .failure(let e): problem = e; version = nil
                    }
                    if problem == nil {
                        switch cli.why() {
                        case .success(let w): why = w
                        case .failure(let e): problem = e; why = nil
                        }
                    }
                    if problem == nil && needSettings, case .success(let c) = cli.settings() {
                        settings = c
                        settingsFresh = true
                    }
                } else {
                    problem = .missing
                    version = nil
                    why = nil
                }
            }
            let state = (try? Data(contentsOf: URL(fileURLWithPath: paths.stateFile))).flatMap(StateFile.init(data:))
            let snap = Snapshot.build(state: state, why: why, settings: settings, now: Date())
            DispatchQueue.main.async {
                self.binaryPath = path
                self.binaryVersion = version
                self.problem = problem
                self.why = why
                self.settings = settings
                if settingsFresh { self.settingsAt = Date() }
                self.snapshot = (state == nil && why == nil) ? nil : snap
                self.refreshing = false
            }
        }
    }

    /// Runs `claudeswitch use <id>` and re-reads everything afterwards.
    func switchTo(_ id: String) {
        guard switching == nil, let path = binaryPath else { return }
        switching = id
        lastSwitch = nil
        work.async {
            let outcome = CLI(path: path).use(id)
            DispatchQueue.main.async {
                self.switching = nil
                self.lastSwitch = (id, outcome)
                self.refresh(full: true)
            }
        }
    }

    func dismissSwitchResult() { lastSwitch = nil }
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
