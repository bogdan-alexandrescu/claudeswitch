import AppKit
import ClaudeSwitchCore
import SwiftUI

/// The update checker (F10): one unauthenticated call to GitHub's latest
/// release a day, none when Advanced → Check for updates is off. When the
/// release is newer, the popover's foot offers it. Check for updates, and
/// the offer's Update…, open the update window (0.8.9): what was found, what
/// is new, and Install, which downloads, verifies and installs both the
/// binary and the app (ClaudeSwitchCore's Updater) under a progress bar,
/// restarts the daemon and relaunches; the reopened app says what it became.
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
    /// The version an install relaunched into, for the window that says so.
    static let installedKey = "updateJustInstalled"

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
    /// The update window's state; nil while it is closed.
    @Published private(set) var dialog: UpdateDialog?
    /// The binary to update beside the app: the path the app runs.
    var binary: () -> String? = { nil }
    /// The release the last check read.
    private var release: Release?
    /// Whether the open window asked for the check under way.
    private var asked = false
    private let cancelFlag = CancelFlag()

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

    /// --render: the window in a given state.
    func preview(dialog d: UpdateDialog?) { dialog = d }

    func start() {
        if let v = defaults.string(forKey: Self.installedKey) {
            defaults.removeObject(forKey: Self.installedKey)
            if v == current { show(.installed(version: v)) }
        }
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
            self.release = release
            available = UpdateOffer.available(current: current, latest: release)
            defaults.set(available, forKey: Self.availableKey)
        case .failure(let e):
            // Quietly unless the window asked: a failed daily check is not
            // worth an interruption; the next one is tomorrow.
            flowsLog.error("update check: \(e.message, privacy: .public)")
        }
        if asked, dialog == .checking {
            dialog = UpdateDialog.checked(r, current: current, arch: .current)
        }
        asked = false
    }

    /// Check for updates, from the ⋯ menu or Check now: the window opens and
    /// says what the check found, whatever it was.
    func checkNow() {
        if case .installing = dialog { show(dialog!); return }
        show(.checking)
        asked = true
        if phase == .checking { return } // the daily check under way answers it
        check()
    }

    /// The popover's Update…: the offer in the window, from the release the
    /// check read, or a fresh check when this launch has not read one.
    func showOffer() {
        if case .installing = dialog { show(dialog!); return }
        if let r = release, UpdateOffer.available(current: current, latest: r) != nil {
            show(UpdateDialog.checked(.success(r), current: current, arch: .current))
        } else {
            checkNow()
        }
    }

    /// Install, Cancel, Try Again and the window's other buttons.
    func cancelInstall() {
        if case .installing(_, let step) = dialog, step.cancellable { cancelFlag.set(true) }
    }

    func closeDialog() {
        if case .installing = dialog { return } // it goes on; the popover's foot shows it
        dialog = nil
        UpdateWindow.close()
    }

    /// The window was closed with its close button.
    func windowClosed() {
        if case .installing = dialog { return }
        dialog = nil
    }

    private func show(_ d: UpdateDialog) {
        dialog = d
        UpdateWindow.show(self)
    }

    /// Downloads, verifies and installs the latest release, then restarts
    /// the daemon and relaunches the app. Any failure leaves both as they
    /// were and says why.
    func update() {
        guard let version = available else { return }
        if case .updating = phase { return }
        let offer = dialog
        func refuse(_ message: String) {
            phase = .failed(message)
            show(.failed(version: version, message: message))
        }
        let app = Bundle.main.bundlePath
        guard app.hasSuffix(".app") else {
            refuse("This copy is not an app bundle (swift run), so it cannot update itself. Nothing was changed.")
            return
        }
        guard let bin = binary() else {
            refuse("claudeswitch was not found, so it cannot be updated beside the app. Nothing was changed.")
            return
        }
        var total: Int64 = 0
        if case .available(_, _, _, let bytes, _)? = offer { total = bytes }
        phase = .updating(version)
        cancelFlag.set(false)
        show(.installing(version: version, step: .downloading(received: 0, total: total)))
        let gate = UpdateProgressGate()
        let cancel = cancelFlag
        queue.async {
            let updater = Self.updater()
            let r = updater.latest().flatMap { release in
                updater.install(release, binary: bin, app: app, restartDaemon: Self.restartDaemon,
                                progress: { step in
                                    guard gate.pass(step) else { return }
                                    DispatchQueue.main.async {
                                        MainActor.assumeIsolated { self.progressed(version, step) }
                                    }
                                },
                                isCancelled: { cancel.get() })
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    switch r {
                    case .success(let v):
                        flowsLog.notice("updated to \(v, privacy: .public); relaunching")
                        self.defaults.removeObject(forKey: Self.availableKey)
                        self.defaults.set(v, forKey: Self.installedKey)
                        self.show(.installing(version: v, step: .relaunching))
                        Self.relaunch(app)
                    case .failure(.cancelled):
                        self.phase = .idle
                        if let o = offer { self.show(o) } else { self.dialog = nil }
                    case .failure(let e):
                        self.phase = .failed(e.message)
                        self.show(.failed(version: version, message: e.message))
                    }
                }
            }
        }
    }

    private func progressed(_ version: String, _ step: UpdateStep) {
        guard case .installing = dialog else { return }
        dialog = .installing(version: version, step: step)
    }

    /// The step under way, for the popover's foot.
    var installStep: UpdateStep? {
        if case .installing(_, let s) = dialog { return s }
        return nil
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

/// Cancel, set on the main thread and read by the install's queue.
final class CancelFlag: @unchecked Sendable {
    private var value = false
    private let lock = NSLock()
    func set(_ v: Bool) { lock.lock(); value = v; lock.unlock() }
    func get() -> Bool { lock.lock(); defer { lock.unlock() }; return value }
}

/// GETs through URLSession, synchronously (the updater runs off the main
/// thread). GitHub's API asks for a User-Agent.
struct URLSessionNetwork: UpdateNetwork {
    final class Box: @unchecked Sendable { var result: Result<Data, Error> = .failure(UpdateError.download("no answer")) }

    /// One download that says how far it has come, and stops when told.
    final class Progressing: NSObject, URLSessionDataDelegate, @unchecked Sendable {
        let url: URL
        let received: (Int64) -> Bool
        let done = DispatchSemaphore(value: 0)
        var data = Data()
        var result: Result<Data, Error> = .failure(UpdateError.download("no answer"))
        var cancelled = false

        init(url: URL, received: @escaping (Int64) -> Bool) {
            self.url = url
            self.received = received
        }

        func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive d: Data) {
            data.append(d)
            if !received(Int64(data.count)) {
                cancelled = true
                dataTask.cancel()
            }
        }

        func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
            defer { done.signal() }
            let name = url.lastPathComponent
            if cancelled { result = .failure(UpdateError.cancelled); return }
            if let error { result = .failure(UpdateError.download("\(name): \(error.localizedDescription)")); return }
            let code = (task.response as? HTTPURLResponse)?.statusCode ?? 0
            result = code == 200 ? .success(data) : .failure(UpdateError.download("\(name): HTTP \(code)"))
        }
    }

    func data(from url: URL, received: @escaping (Int64) -> Bool) throws -> Data {
        let req = Self.request(url)
        let p = Progressing(url: url, received: received)
        let queue = OperationQueue()
        queue.maxConcurrentOperationCount = 1
        let session = URLSession(configuration: .default, delegate: p, delegateQueue: queue)
        defer { session.finishTasksAndInvalidate() }
        session.dataTask(with: req).resume()
        p.done.wait()
        return try p.result.get()
    }

    static func request(_ url: URL) -> URLRequest {
        var req = URLRequest(url: url, timeoutInterval: 120)
        req.setValue("ClaudeSwitch", forHTTPHeaderField: "User-Agent")
        if url.host == "api.github.com" { req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept") }
        return req
    }

    func data(from url: URL) throws -> Data {
        let req = Self.request(url)
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

/// The popover's offer: "0.5.6 available · Update…", the progress, or why
/// it failed. Update… and the progress open the update window.
struct UpdateLine: View {
    @ObservedObject var updates: UpdateController

    var body: some View {
        switch updates.phase {
        case .updating(let v):
            Button { updates.showOffer() } label: {
                VStack(alignment: .leading, spacing: 4) {
                    Text(updates.installStep.map { "Updating to \(v): \($0.label)" } ?? "Updating to \(v)…")
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    ProgressView(value: updates.installStep?.fraction ?? 0)
                        .progressViewStyle(.linear)
                        .tint(Color.csAccent)
                        .controlSize(.small)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .font(.callout)
            .help("Shows the update window.")
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
                    Button("Update…") { updates.showOffer() }
                        .buttonStyle(.borderless)
                        .foregroundStyle(Color.csAccent)
                        .fontWeight(.semibold)
                        .help("Shows what is new in \(v), and Install, which downloads ClaudeSwitch and "
                              + "claudeswitch, checks them against the release's checksums, installs both, "
                              + "restarts the daemon and relaunches.")
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
                Button("Check now") { updates.checkNow() }
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
