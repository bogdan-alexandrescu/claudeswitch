import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The update checker (F10): one unauthenticated call to GitHub's latest
/// release a day, none when Advanced → Check for updates is off. When the
/// release is newer, the popover's foot offers it; Update downloads,
/// verifies and installs both the binary and the app (ClaudeSwitchCore's
/// Updater), restarts the daemon and relaunches.
@MainActor
final class UpdateController: ObservableObject {
    enum Phase: Equatable {
        case idle
        case checking
        case updating(String)
        case failed(String)
    }

    static let enabledKey = "checkForUpdates"
    static let lastCheckKey = "updateLastCheck"
    static let availableKey = "updateAvailable"

    @Published var enabled: Bool {
        didSet {
            defaults.set(enabled, forKey: Self.enabledKey)
            if enabled { checkIfDue() } else { available = nil }
        }
    }
    /// The newer version, when there is one.
    @Published private(set) var available: String?
    @Published private(set) var phase: Phase = .idle
    @Published private(set) var lastCheck: Date?
    /// The binary to update beside the app: the path the app runs.
    var binary: () -> String? = { nil }

    private let defaults = UserDefaults.standard
    private var timer: Timer?
    private let queue = DispatchQueue(label: "claudeswitch.bar.update")

    var current: String? { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String }

    init() {
        enabled = defaults.object(forKey: Self.enabledKey) as? Bool ?? true
        lastCheck = defaults.object(forKey: Self.lastCheckKey) as? Date
        // A version found by an earlier check, still newer than this app.
        if let v = defaults.string(forKey: Self.availableKey), let have = current.flatMap(SemVer.init),
           let new = SemVer(v), new > have, enabled {
            available = v
        }
    }

    /// --render: shows (or hides) an offer without any call.
    func preview(available v: String?, phase p: Phase = .idle) {
        available = v
        phase = p
    }

    func start() {
        checkIfDue()
        // Hourly, so a Mac that sleeps through the day still checks once.
        timer = Timer.scheduledTimer(withTimeInterval: 3600, repeats: true) { _ in
            Task { @MainActor in AppHub.updates.checkIfDue() }
        }
    }

    func checkIfDue() {
        guard UpdateSchedule.due(enabled: enabled, lastCheck: lastCheck, now: Date()) else { return }
        check()
    }

    /// One call to the releases API.
    func check() {
        guard enabled, phase == .idle || isFailed else { return }
        phase = .checking
        queue.async {
            let r = Self.updater().latest()
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self.checked(r) }
            }
        }
    }

    private var isFailed: Bool { if case .failed = phase { return true } else { return false } }

    private func checked(_ r: Result<Release, UpdateError>) {
        phase = .idle
        lastCheck = Date()
        defaults.set(lastCheck, forKey: Self.lastCheckKey)
        switch r {
        case .success(let release):
            available = UpdateOffer.available(current: current, latest: release)
            defaults.set(available, forKey: Self.availableKey)
        case .failure(let e):
            // Quietly: a failed check is not worth an interruption; the
            // next one is tomorrow, or Check now.
            flowsLog.error("update check: \(e.message, privacy: .public)")
        }
    }

    /// Downloads, verifies and installs the latest release, then restarts
    /// the daemon and relaunches the app. Any failure leaves both as they
    /// were and says why.
    func update() {
        guard let version = available else { return }
        if case .updating = phase { return }
        let app = Bundle.main.bundlePath
        guard app.hasSuffix(".app") else {
            phase = .failed("This copy is not an app bundle (swift run), so it cannot update itself.")
            return
        }
        guard let bin = binary() else {
            phase = .failed("claudeswitch was not found, so it cannot be updated beside the app. Nothing was changed.")
            return
        }
        phase = .updating(version)
        queue.async {
            let updater = Self.updater()
            let r = updater.latest().flatMap { release in
                updater.install(release, binary: bin, app: app, restartDaemon: Self.restartDaemon)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    switch r {
                    case .success(let v):
                        flowsLog.notice("updated to \(v, privacy: .public); relaunching")
                        self.defaults.removeObject(forKey: Self.availableKey)
                        Self.relaunch(app)
                    case .failure(let e):
                        self.phase = .failed(e.message)
                    }
                }
            }
        }
    }

    func dismissFailure() { if isFailed { phase = .idle } }

    nonisolated static func updater() -> Updater {
        Updater(network: URLSessionNetwork(), system: LocalUpdateSystem())
    }

    /// `daemon restart --json` on the new binary. No daemon installed is
    /// not a failure: there is nothing to restart.
    nonisolated static func restartDaemon(_ binary: String) -> Result<Void, UpdateError> {
        switch CLI(path: binary).daemon(.restart) {
        case .success: return .success(())
        case .failure(.app(let e)) where e.code == "not_installed" || e.code == "unsupported_platform":
            return .success(())
        case .failure(let e):
            return .failure(.daemon([e.code, e.message].compactMap { $0 }.joined(separator: ": ")))
        }
    }

    /// Opens the new bundle once this one has quit.
    static func relaunch(_ app: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "sleep 1; /usr/bin/open \"$0\"", app]
        try? p.run()
        NSApp.terminate(nil)
    }
}

/// GETs through URLSession, synchronously (the updater runs off the main
/// thread). GitHub's API asks for a User-Agent.
struct URLSessionNetwork: UpdateNetwork {
    final class Box: @unchecked Sendable { var result: Result<Data, Error> = .failure(UpdateError.download("no answer")) }

    func data(from url: URL) throws -> Data {
        var req = URLRequest(url: url, timeoutInterval: 120)
        req.setValue("ClaudeSwitch", forHTTPHeaderField: "User-Agent")
        if url.host == "api.github.com" { req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept") }
        let box = Box()
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { data, response, error in
            defer { done.signal() }
            if let error {
                box.result = .failure(UpdateError.download("\(url.lastPathComponent): \(error.localizedDescription)"))
                return
            }
            let code = (response as? HTTPURLResponse)?.statusCode ?? 0
            guard code == 200, let data else {
                box.result = .failure(UpdateError.download("\(url.lastPathComponent): HTTP \(code)"))
                return
            }
            box.result = .success(data)
        }.resume()
        done.wait()
        return try box.result.get()
    }
}

/// The real file system and commands.
struct LocalUpdateSystem: UpdateSystem {
    var fm: FileManager { .default }

    func makeTempDir() throws -> String {
        let dir = NSTemporaryDirectory() + "claudeswitch-update-" + UUID().uuidString
        try fm.createDirectory(atPath: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        return dir
    }

    func makeDir(_ path: String) throws {
        try fm.createDirectory(atPath: path, withIntermediateDirectories: true)
    }

    func write(_ data: Data, to path: String) throws {
        try data.write(to: URL(fileURLWithPath: path), options: .atomic)
    }

    func read(_ path: String) throws -> Data { try Data(contentsOf: URL(fileURLWithPath: path)) }

    func exists(_ path: String) -> Bool { fm.fileExists(atPath: path) }

    func resolve(_ path: String) -> String { URL(fileURLWithPath: path).resolvingSymlinksInPath().path }

    func remove(_ path: String) { try? fm.removeItem(atPath: path) }

    func move(_ from: String, _ to: String) throws {
        do { try fm.moveItem(atPath: from, toPath: to) } catch {
            throw UpdateError.install("could not move \((from as NSString).lastPathComponent) to \(to): "
                                      + error.localizedDescription)
        }
    }

    func run(_ argv: [String]) throws {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: argv[0])
        p.arguments = Array(argv.dropFirst())
        let err = Pipe()
        p.standardError = err
        p.standardOutput = FileHandle.nullDevice
        try p.run()
        p.waitUntilExit()
        let text = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard p.terminationStatus == 0 else {
            let tool = (argv[0] as NSString).lastPathComponent
            throw UpdateError.install(text.isEmpty ? "\(tool) failed (exit \(p.terminationStatus))" : "\(tool): \(text)")
        }
    }
}

/// The popover's offer: "0.5.6 available · Update", the progress, or why
/// it failed.
struct UpdateLine: View {
    @ObservedObject var updates: UpdateController

    var body: some View {
        switch updates.phase {
        case .updating(let v):
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Updating to \(v)…").foregroundStyle(.secondary)
                Spacer()
            }
            .font(.callout)
        case .failed(let message):
            InlineErrorView(title: "Could not update", message: message, hint: "", retryLine: nil) {
                updates.dismissFailure()
            }
        default:
            if let v = updates.available {
                HStack(spacing: 6) {
                    Image(systemName: "arrow.down.circle.fill").foregroundStyle(Color.csAccent)
                    Text(UpdateOffer.footer(v))
                    Text("·").foregroundStyle(.secondary)
                    Button("Update") { updates.update() }
                        .buttonStyle(.borderless)
                        .foregroundStyle(Color.csAccent)
                        .fontWeight(.semibold)
                        .help("Downloads ClaudeSwitch and claudeswitch \(v), checks them against the release's "
                              + "checksums, installs both, restarts the daemon and relaunches.")
                    Spacer()
                }
                .font(.callout)
                .accessibilityElement(children: .combine)
            }
        }
    }
}

/// Advanced → This app: the update check and notification rows.
struct AppFlowsRows: View {
    @ObservedObject var updates: UpdateController
    @ObservedObject var notifier: Notifier
    @ObservedObject var hotKey: HotKeyCenter

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Toggle(isOn: $updates.enabled) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Check for updates")
                    Text("Once a day, one call to GitHub's latest release. Off makes no call at all.")
                        .font(.caption).foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .toggleStyle(.switch)
            HStack(spacing: 8) {
                Text(checkedLine).font(.caption).foregroundStyle(.tertiary)
                if updates.phase == .checking { ProgressView().controlSize(.mini) }
                Spacer()
                Button("Check now") { updates.check() }
                    .controlSize(.small)
                    .disabled(!updates.enabled || updates.phase == .checking)
            }
        }
        Toggle(isOn: $notifier.enabled) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Notifications with actions")
                Text("Undo and Pin here when rotation switches; Sign in when an account needs it. "
                     + "The daemon's own notifications still appear too.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .toggleStyle(.switch)
        HotKeyRow(hotKey: hotKey)
    }

    var checkedLine: String {
        var s = updates.current.map { "This is \($0)" } ?? "Version unknown"
        if let v = updates.available { s += " · \(v) available" }
        if let d = updates.lastCheck { s += " · checked " + Format.age(d, now: Date()) }
        return s
    }
}
